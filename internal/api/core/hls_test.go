package core

import (
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func hlsStr(s string) *string { return &s }

func TestHLSBrowserPlayable(t *testing.T) {
	cases := []struct {
		name, vcodec, acodec, container string
		want                            bool
	}{
		{"h264 mp4 aac", "h264", "aac", "mp4", true},
		{"h264 mov aac", "h264", "aac", "mov", true},
		{"h264 m4v aac", "h264", "aac", "m4v", true},
		{"h264 mp4 mp3", "h264", "mp3", "mp4", true},
		{"h264 mp4 flac", "h264", "flac", "mp4", true},
		{"h264 mp4 opus", "h264", "opus", "mp4", true},
		{"hevc mp4 aac", "hevc", "aac", "mp4", false},
		{"vp9 webm opus", "vp9", "opus", "webm", true},
		{"av1 webm opus", "av1", "opus", "webm", true},
		{"vp9 mp4 aac", "vp9", "aac", "mp4", true},
		{"mp4 comma aac", "h264", "aac", "mp4,mov", true},
		{"audio only aac", "", "aac", "mp4", true},
		{"audio only mp3", "", "mp3", "", true},
		{"audio only vorbis", "", "vorbis", "", true},
		{"audio only ac3", "", "ac3", "", false},
		{"h264 mkv aac", "h264", "aac", "mkv", false},
		{"h264 mp4 ac3", "h264", "ac3", "mp4", false},
		{"mpeg2 mp4 aac", "mpeg2video", "aac", "mp4", false},
		{"vp8 webm vorbis", "vp8", "vorbis", "webm", false},
		{"h264 webm aac", "h264", "aac", "webm", false},
		{"video only h264 mp4", "h264", "", "mp4", true},
		{"video only h264 mkv", "h264", "", "mkv", false},
		{"empty codecs", "", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := store.FileRec{}
			if tc.vcodec != "" {
				f.VideoCodec = hlsStr(tc.vcodec)
			}
			if tc.acodec != "" {
				f.Codec = hlsStr(tc.acodec)
			}
			if tc.container != "" {
				f.Container = hlsStr(tc.container)
			}
			ed := &store.EditionView{Files: []store.FileRec{f}}
			if got := browserPlayable(ed); got != tc.want {
				t.Fatalf("browserPlayable = %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("no files", func(t *testing.T) {
		if browserPlayable(&store.EditionView{}) {
			t.Fatal("empty files should not be playable")
		}
	})
}

func TestRewriteHLSPlaylist(t *testing.T) {
	in := "#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:4.0,\nseg00000.ts\n#EXTINF:4.0,\nseg00001.ts\n#EXT-X-ENDLIST\n"
	got := string(rewriteHLSPlaylist([]byte(in), "web-3", ""))
	if !strings.Contains(got, "/api/core/hls/web-3/seg00000.ts") {
		t.Fatalf("seg0 missing: %s", got)
	}
	if !strings.Contains(got, "/api/core/hls/web-3/seg00001.ts") {
		t.Fatalf("seg1 missing: %s", got)
	}
	if strings.Contains(got, "\nseg00000.ts\n") {
		t.Fatalf("relative uri left: %s", got)
	}
	if !strings.Contains(got, "#EXTM3U") {
		t.Fatal("header dropped")
	}
	tok := string(rewriteHLSPlaylist([]byte(in), "web-3", "tok"))
	if !strings.Contains(tok, "/api/core/hls/web-3/seg00000.ts?token=tok") {
		t.Fatalf("token rewrite: %s", tok)
	}
	master := string(rewriteHLSPlaylist([]byte("index.m3u8\n"), "web-1", "ab"))
	if master != "/api/core/hls/web-1/index.m3u8?token=ab\n" {
		t.Fatalf("index rewrite: %q", master)
	}
}
