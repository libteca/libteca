package audio

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const fixtureM4B = `{
  "streams": [
    {
      "codec_name": "aac",
      "codec_type": "audio",
      "channels": 2,
      "sample_rate": "44100",
      "bit_rate": "128000",
      "duration": "123.45",
      "tags": {"title": "stream-title", "artist": "Stream Artist"}
    },
    {
      "codec_name": "mjpeg",
      "codec_type": "video",
      "disposition": {"attached_pic": 1}
    }
  ],
  "format": {
    "format_name": "ipod,mp4,m4a",
    "duration": "3600.5",
    "bit_rate": "131072",
    "tags": {"title": "Book Title", "ARTIST": "Format Artist", "album": "The Album"}
  },
  "chapters": [
    {
      "id": 0,
      "start_time": "0.000000",
      "end_time": "12.500000",
      "start": 0,
      "end": 275625,
      "tags": {"title": "Opening"}
    },
    {
      "id": 1,
      "start_time": "12.500000",
      "end_time": "12.500000",
      "start": 12.5,
      "end": 99.0,
      "tags": {}
    }
  ]
}`

const fixtureStreamDuration = `{
  "streams": [
    {
      "codec_name": "mp3",
      "codec_type": "audio",
      "channels": 1,
      "sample_rate": "48000",
      "duration": "77.25",
      "tags": {"genre": "Speech"}
    },
    {
      "codec_name": "h264",
      "codec_type": "video",
      "disposition": {"attached_pic": 0}
    }
  ],
  "format": {
    "format_name": "mp3",
    "duration": "",
    "bit_rate": "not-a-number",
    "tags": {}
  },
  "chapters": [
    {
      "id": 7,
      "start_time": "",
      "end_time": "bad",
      "start": 1.5,
      "end": 9.25,
      "tags": {"TITLE": "Ignored Case"}
    }
  ]
}`

func installFakeFFProbe(t *testing.T, stdout string, exit int) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ffprobe")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	if exit != 0 {
		b.WriteString("exit " + strconv.Itoa(exit) + "\n")
	} else {
		b.WriteString("cat <<'JSON'\n")
		b.WriteString(stdout)
		if !strings.HasSuffix(stdout, "\n") {
			b.WriteByte('\n')
		}
		b.WriteString("JSON\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestProbeM4BFixture(t *testing.T) {
	installFakeFFProbe(t, fixtureM4B, 0)
	info, err := Probe("book.m4b")
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration != 3600.5 {
		t.Fatalf("duration = %v, want format duration 3600.5", info.Duration)
	}
	if info.Codec != "aac" || info.Channels != 2 || info.SampleRate != 44100 {
		t.Fatalf("audio = %s ch=%d sr=%d", info.Codec, info.Channels, info.SampleRate)
	}
	if info.Bitrate != 131072 {
		t.Fatalf("bitrate = %d, want format bitrate", info.Bitrate)
	}
	if info.Container != "ipod,mp4,m4a" {
		t.Fatalf("container = %q", info.Container)
	}
	if !info.HasVideo {
		t.Fatal("HasVideo = false, want true for attached_pic")
	}
	if info.Meta["title"] != "Book Title" {
		t.Fatalf("title = %q", info.Meta["title"])
	}
	if info.Meta["artist"] != "Format Artist" {
		t.Fatalf("artist = %q, format tags must win over stream tags", info.Meta["artist"])
	}
	if info.Meta["album"] != "The Album" {
		t.Fatalf("album = %q", info.Meta["album"])
	}
	if len(info.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(info.Chapters))
	}
	c0 := info.Chapters[0]
	if c0.ID != 0 || c0.Start != 0 || c0.End != 12.5 || c0.Title != "Opening" {
		t.Fatalf("chapter 0 = %+v", c0)
	}
	c1 := info.Chapters[1]
	if c1.Title != "Chapter 2" {
		t.Fatalf("empty title = %q, want Chapter 2", c1.Title)
	}
	if c1.Start != 12.5 || c1.End != 99.0 {
		t.Fatalf("chapter 1 times = %v-%v, want start_time fallback to numeric end", c1.Start, c1.End)
	}
}

func TestProbeStreamDurationAndNoCover(t *testing.T) {
	installFakeFFProbe(t, fixtureStreamDuration, 0)
	info, err := Probe("talk.mp3")
	if err != nil {
		t.Fatal(err)
	}
	if info.Duration != 77.25 {
		t.Fatalf("duration = %v, want stream duration when format is empty", info.Duration)
	}
	if info.Bitrate != 0 {
		t.Fatalf("bitrate = %d, want 0 when format bit_rate is unparsable", info.Bitrate)
	}
	if info.HasVideo {
		t.Fatal("HasVideo = true, video without attached_pic must stay false")
	}
	if info.Codec != "mp3" || info.Channels != 1 || info.SampleRate != 48000 {
		t.Fatalf("audio = %s ch=%d sr=%d", info.Codec, info.Channels, info.SampleRate)
	}
	if info.Meta["genre"] != "Speech" {
		t.Fatalf("stream tag genre = %q", info.Meta["genre"])
	}
	if len(info.Chapters) != 1 {
		t.Fatalf("chapters = %d", len(info.Chapters))
	}
	c := info.Chapters[0]
	if c.ID != 7 || c.Start != 1.5 || c.End != 9.25 {
		t.Fatalf("numeric start/end fallback = %+v", c)
	}
	if c.Title != "Chapter 1" {
		t.Fatalf("title = %q, Tags[\"title\"] is case-sensitive so TITLE is ignored", c.Title)
	}
}

func TestProbeFFProbeFailure(t *testing.T) {
	installFakeFFProbe(t, "", 1)
	if _, err := Probe("missing.m4b"); err == nil {
		t.Fatal("want error when ffprobe exits non-zero")
	}
}

func TestProbeBadJSON(t *testing.T) {
	installFakeFFProbe(t, "not json", 0)
	if _, err := Probe("x.m4b"); err == nil {
		t.Fatal("want error on malformed ffprobe json")
	}
}

func TestMimeType(t *testing.T) {
	cases := []struct {
		codec, container, want string
	}{
		{"aac", "", "audio/mp4"},
		{"mp3", "", "audio/mpeg"},
		{"flac", "", "audio/flac"},
		{"opus", "", "audio/ogg"},
		{"vorbis", "", "audio/ogg"},
		{"", "mp4", "audio/mp4"},
		{"", "ipod,mp4,m4a", "audio/mp4"},
		{"", "matroska,webm", "audio/mpeg"},
	}
	for _, c := range cases {
		if got := MimeType(c.codec, c.container); got != c.want {
			t.Fatalf("MimeType(%q,%q) = %q, want %q", c.codec, c.container, got, c.want)
		}
	}
}
