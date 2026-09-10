package audio

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
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

func Probe(path string) (*Info, error) {
	out, err := exec.Command("ffprobe", "-v", "quiet", "-print_format", "json", "-show_format", "-show_streams", "-show_chapters", path).Output()
	if err != nil {
		return nil, fmt.Errorf("ffprobe %s: %w", path, err)
	}
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
