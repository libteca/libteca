package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func swapFFmpeg(t *testing.T, look func(string) (string, error), run func(context.Context, string, ...string) ([]byte, error)) {
	t.Helper()
	origLook, origRun := ffmpegLookPath, ffmpegRun
	ffmpegLookPath, ffmpegRun = look, run
	t.Cleanup(func() {
		ffmpegLookPath, ffmpegRun = origLook, origRun
	})
}

func TestLibtecaVTTPath(t *testing.T) {
	got := libtecaVTT("/movies/Foo.Bar.mkv")
	if got != "/movies/Foo.Bar.libteca.vtt" {
		t.Fatalf("got %q", got)
	}
}

func TestCachedVTTFreshAndStale(t *testing.T) {
	dir := t.TempDir()
	media := filepath.Join(dir, "film.mkv")
	cache := filepath.Join(dir, "film.libteca.vtt")
	if err := os.WriteFile(media, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache, []byte("WEBVTT\n\ncached\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, ok := cachedVTT(media)
	if !ok || !strings.Contains(string(data), "cached") {
		t.Fatalf("fresh cache miss: ok=%v data=%q", ok, data)
	}
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(media, later, later); err != nil {
		t.Fatal(err)
	}
	if _, ok := cachedVTT(media); ok {
		t.Fatal("stale cache hit")
	}
}

func TestSubtitlesSidecarPreferredOverExtract(t *testing.T) {
	called := false
	swapFFmpeg(t,
		func(string) (string, error) { return "/bin/ffmpeg", nil },
		func(context.Context, string, ...string) ([]byte, error) {
			called = true
			return []byte("WEBVTT\n\nEXTRACTED\n"), nil
		},
	)
	db, base, token := newDiscoveryEnv(t)
	lib, _ := db.AddLibrary("m", "movies", t.TempDir())
	w := seedWork(t, db, lib, "Film", ptr("Dir"), nil, 1, 1)
	e := seedEdition(t, db, w, ptr(100.0))
	dir := t.TempDir()
	media := filepath.Join(dir, "plain.mkv")
	if err := os.WriteFile(media, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plain.srt"), []byte("1\n00:00:01,000 --> 00:00:02,000\nSidecar\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fid := seedFile(t, db, e, media)
	resp := authedGet(t, base+fmt.Sprintf("/subtitles/%d", fid), token)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	got := bodyStr(t, resp)
	if !strings.Contains(got, "Sidecar") || strings.Contains(got, "EXTRACTED") {
		t.Fatalf("body = %q, want sidecar", got)
	}
	if called {
		t.Fatal("extract ran despite sidecar")
	}
}

func TestSubtitlesExtractWritesCache(t *testing.T) {
	var gotArgs []string
	var calls int
	vtt := []byte("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHi\n")
	swapFFmpeg(t,
		func(string) (string, error) { return "/bin/ffmpeg", nil },
		func(_ context.Context, name string, args ...string) ([]byte, error) {
			calls++
			if name != "ffmpeg" {
				t.Fatalf("name = %q", name)
			}
			gotArgs = append([]string(nil), args...)
			return vtt, nil
		},
	)
	db, base, token := newDiscoveryEnv(t)
	lib, _ := db.AddLibrary("m", "movies", t.TempDir())
	w := seedWork(t, db, lib, "Film", ptr("Dir"), nil, 1, 1)
	e := seedEdition(t, db, w, ptr(100.0))
	dir := t.TempDir()
	media := filepath.Join(dir, "emb.mkv")
	if err := os.WriteFile(media, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fid := seedFile(t, db, e, media)
	resp := authedGet(t, base+fmt.Sprintf("/subtitles/%d", fid), token)
	got := bodyStr(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d body=%s", resp.StatusCode, got)
	}
	if got != string(vtt) {
		t.Fatalf("body = %q", got)
	}
	wantArgs := []string{"-i", media, "-map", "0:s:0", "-f", "webvtt", "-"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("args = %#v want %#v", gotArgs, wantArgs)
	}
	cached, err := os.ReadFile(libtecaVTT(media))
	if err != nil {
		t.Fatal(err)
	}
	if string(cached) != string(vtt) {
		t.Fatalf("cache = %q", cached)
	}
	resp = authedGet(t, base+fmt.Sprintf("/subtitles/%d", fid), token)
	if resp.StatusCode != 200 {
		t.Fatalf("cached status = %d", resp.StatusCode)
	}
	if calls != 1 {
		t.Fatalf("extract calls = %d, want 1", calls)
	}
}

func TestSubtitlesExtractMissingFFmpeg(t *testing.T) {
	swapFFmpeg(t,
		func(string) (string, error) { return "", exec.ErrNotFound },
		func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("ffmpeg ran")
			return nil, nil
		},
	)
	db, base, token := newDiscoveryEnv(t)
	lib, _ := db.AddLibrary("m", "movies", t.TempDir())
	w := seedWork(t, db, lib, "Film", nil, nil, 1, 1)
	e := seedEdition(t, db, w, ptr(100.0))
	dir := t.TempDir()
	media := filepath.Join(dir, "none.mkv")
	if err := os.WriteFile(media, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fid := seedFile(t, db, e, media)
	resp := authedGet(t, base+fmt.Sprintf("/subtitles/%d", fid), token)
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestSubtitlesExtractNoStream(t *testing.T) {
	swapFFmpeg(t,
		func(string) (string, error) { return "/bin/ffmpeg", nil },
		func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("Stream map '0:s:0' matches no streams")
		},
	)
	db, base, token := newDiscoveryEnv(t)
	lib, _ := db.AddLibrary("m", "movies", t.TempDir())
	w := seedWork(t, db, lib, "Film", nil, nil, 1, 1)
	e := seedEdition(t, db, w, ptr(100.0))
	dir := t.TempDir()
	media := filepath.Join(dir, "none.mkv")
	if err := os.WriteFile(media, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fid := seedFile(t, db, e, media)
	resp := authedGet(t, base+fmt.Sprintf("/subtitles/%d", fid), token)
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}
