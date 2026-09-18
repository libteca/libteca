package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/libteca/libteca/internal/procfd"
)

type Chapter struct {
	ID    int64   `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

type Info struct {
	Duration     float64           `json:"duration"`
	Codec        string            `json:"codec"`
	Container    string            `json:"container"`
	Bitrate      int64             `json:"bitrate"`
	Channels     int               `json:"channels"`
	SampleRate   int               `json:"sample_rate"`
	Meta         map[string]string `json:"meta"`
	Chapters     []Chapter         `json:"chapters"`
	HasVideo     bool              `json:"has_video"`
	HasSubtitles bool              `json:"has_subtitles"`
}

type ffprobeOut struct {
	Streams []struct {
		CodecName   string            `json:"codec_name"`
		CodecType   string            `json:"codec_type"`
		Channels    int               `json:"channels"`
		SampleRate  string            `json:"sample_rate"`
		BitRate     string            `json:"bit_rate"`
		Duration    string            `json:"duration"`
		Tags        map[string]string `json:"tags"`
		Disposition map[string]int    `json:"disposition"`
	} `json:"streams"`
	Format struct {
		FormatName string            `json:"format_name"`
		Duration   string            `json:"duration"`
		BitRate    string            `json:"bit_rate"`
		Tags       map[string]string `json:"tags"`
	} `json:"format"`
	Chapters []struct {
		ID        int64             `json:"id"`
		StartTime string            `json:"start_time"`
		EndTime   string            `json:"end_time"`
		Start     float64           `json:"start"`
		End       float64           `json:"end"`
		Tags      map[string]string `json:"tags"`
	} `json:"chapters"`
}

const probeOutputLimit = 4 << 20

type boundedBuffer struct {
	bytes.Buffer
	limit  int
	cancel context.CancelFunc
	over   bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	room := b.limit - b.Buffer.Len()
	if room < 0 {
		room = 0
	}
	if len(p) > room {
		b.over = true
		if room > 0 {
			b.Buffer.Write(p[:room])
		}
		b.cancel()
	}
	if !b.over {
		b.Buffer.Write(p)
	}
	return len(p), nil
}

// ProbeFile runs ffprobe against an already opened, rooted media
// descriptor: the pathname never reaches the child and the protocol
// whitelist blocks demuxer-chased secondary resources (audit F03).
func ProbeFile(ctx context.Context, f *os.File) (*Info, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	inArgs, err := procfd.Args("ffprobe")
	if err != nil {
		return nil, err
	}
	stdout := &boundedBuffer{limit: probeOutputLimit, cancel: cancel}
	args := append([]string{"-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", "-show_chapters"}, inArgs...)
	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	cmd.ExtraFiles = []*os.File{f}
	cmd.Stdout = stdout
	if err := cmd.Run(); err != nil {
		if stdout.over {
			return nil, fmt.Errorf("ffprobe %s: output exceeds %d bytes", f.Name(), probeOutputLimit)
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("ffprobe %s: %w", f.Name(), ctx.Err())
		}
		return nil, fmt.Errorf("ffprobe %s: %w", f.Name(), err)
	}
	if stdout.over {
		return nil, fmt.Errorf("ffprobe %s: output exceeds %d bytes", f.Name(), probeOutputLimit)
	}
	return parseProbe(stdout.Bytes(), f.Name())
}

func parseProbe(out []byte, path string) (*Info, error) {
	var p ffprobeOut
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, fmt.Errorf("ffprobe json %s: %w", path, err)
	}
	info := &Info{Meta: map[string]string{}, Chapters: []Chapter{}}
	if d, err := strconv.ParseFloat(p.Format.Duration, 64); err == nil {
		info.Duration = d
	}
	if br, err := strconv.ParseInt(p.Format.BitRate, 10, 64); err == nil {
		info.Bitrate = br
	}
	info.Container = p.Format.FormatName
	for k, v := range p.Format.Tags {
		info.Meta[strings.ToLower(k)] = v
	}
	for _, s := range p.Streams {
		if s.CodecType == "audio" && info.Codec == "" {
			info.Codec = s.CodecName
			info.Channels = s.Channels
			if sr, err := strconv.Atoi(s.SampleRate); err == nil {
				info.SampleRate = sr
			}
			if info.Duration == 0 {
				if d, err := strconv.ParseFloat(s.Duration, 64); err == nil {
					info.Duration = d
				}
			}
			for k, v := range s.Tags {
				lk := strings.ToLower(k)
				if _, exists := info.Meta[lk]; !exists {
					info.Meta[lk] = v
				}
			}
		}
		if s.CodecType == "video" && s.Disposition["attached_pic"] == 1 {
			info.HasVideo = true
		}
		if isSubtitleStream(s.CodecType, s.CodecName) {
			info.HasSubtitles = true
		}
	}
	for i, c := range p.Chapters {
		var start, end float64
		if s, err := strconv.ParseFloat(c.StartTime, 64); err == nil {
			start = s
		} else {
			start = c.Start
		}
		if e, err := strconv.ParseFloat(c.EndTime, 64); err == nil {
			end = e
		} else {
			end = c.End
		}
		if end <= start && c.End > c.Start {
			start, end = c.Start, c.End
		}
		title := c.Tags["title"]
		if title == "" {
			title = fmt.Sprintf("Chapter %d", i+1)
		}
		info.Chapters = append(info.Chapters, Chapter{ID: c.ID, Start: start, End: end, Title: title})
	}
	return info, nil
}

func isSubtitleStream(codecType, codecName string) bool {
	if codecType == "subtitle" || codecType == "subtitles" {
		return true
	}
	switch strings.ToLower(codecName) {
	case "subrip", "ass", "webvtt", "mov_text":
		return true
	}
	return false
}

func MimeType(codec, container string) string {
	switch codec {
	case "aac":
		return "audio/mp4"
	case "mp3":
		return "audio/mpeg"
	case "flac":
		return "audio/flac"
	case "opus":
		return "audio/ogg"
	case "vorbis":
		return "audio/ogg"
	}
	if strings.Contains(container, "mp4") || strings.Contains(container, "ipod") {
		return "audio/mp4"
	}
	return "audio/mpeg"
}
