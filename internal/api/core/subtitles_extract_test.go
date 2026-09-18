package core

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

func swapFFmpeg(t *testing.T, look func(string) (string, error), run func(context.Context, string, []*os.File, ...string) ([]byte, error)) {
	t.Helper()
	origLook, origRun := ffmpegLookPath, ffmpegRun
	ffmpegLookPath, ffmpegRun = look, run
	t.Cleanup(func() {
		ffmpegLookPath, ffmpegRun = origLook, origRun
	})
}

func TestSubtitleCacheKeyChangesWithMtime(t *testing.T) {
	base := &store.FileRec{ID: 7, Path: "/lib/film.mkv", MtimeSecs: 100, MtimeNS: 200}
	changed := *base
	changed.MtimeNS = 201
	other := *base
	other.Path = "/lib/other.mkv"
	if subtitleCacheKey(base) == subtitleCacheKey(&changed) {
		t.Fatal("key must change with mtime")
	}
	if subtitleCacheKey(base) == subtitleCacheKey(&other) {
		t.Fatal("key must change with path")
	}
	if k := subtitleCacheKey(base); len(k) != 64 {
		t.Fatalf("key = %q, want 64 hex characters", k)
	}
}

func newSubtitleEnv(t *testing.T) (*store.DB, string, string, *API) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('u','x',0,0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := res.LastInsertId()
	token, err := auth.IssueToken(db, uid, "test")
	if err != nil {
		t.Fatal(err)
	}
	a := New(db, t.TempDir())
	app := neutron.New()
	a.Mount(app.Router().Group("/api/core", auth.Middleware(db)))
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return db, srv.URL + "/api/core", token, a
}

func TestSubtitlesSidecarPreferredOverExtract(t *testing.T) {
	called := false
	swapFFmpeg(t,
		func(string) (string, error) { return "/bin/ffmpeg", nil },
		func(context.Context, string, []*os.File, ...string) ([]byte, error) {
			called = true
			return []byte("WEBVTT\n\nEXTRACTED\n"), nil
		},
	)
	db, base, token := newDiscoveryEnv(t)
	dir := t.TempDir()
	lib, _ := db.AddLibrary("m", "movies", dir)
	w := seedWork(t, db, lib, "Film", ptr("Dir"), nil, 1, 1)
	e := seedEdition(t, db, w, ptr(100.0))
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
		func(_ context.Context, name string, files []*os.File, args ...string) ([]byte, error) {
			calls++
			if name != "ffmpeg" {
				t.Fatalf("name = %q", name)
			}
			if len(files) != 1 || files[0] == nil {
				t.Fatalf("extract must receive the confined descriptor, got %v", files)
			}
			gotArgs = append([]string(nil), args...)
			return vtt, nil
		},
	)
	db, base, token, a := newSubtitleEnv(t)
	dir := t.TempDir()
	lib, _ := db.AddLibrary("m", "movies", dir)
	w := seedWork(t, db, lib, "Film", ptr("Dir"), nil, 1, 1)
	e := seedEdition(t, db, w, ptr(100.0))
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
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"-protocol_whitelist", "fd,pipe", "-map", "0:s:0", "-f", "webvtt", "-"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args %q missing %q", joined, want)
		}
	}
	if !strings.Contains(joined, "-i fd:") && !strings.Contains(joined, "-i /dev/fd/3") {
		t.Fatalf("args %q must take the input from a descriptor", joined)
	}
	if strings.Contains(joined, media) {
		t.Fatalf("args %q leaked the source pathname to the child", joined)
	}
	if _, err := os.Stat(media + ".libteca.vtt"); !os.IsNotExist(err) {
		t.Fatalf("library-side cache must not exist: %v", err)
	}
	cacheDir := filepath.Join(a.DataDir, "subtitles")
	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache entries = %v err = %v, want exactly one", entries, err)
	}
	cached, err := os.ReadFile(filepath.Join(cacheDir, entries[0].Name()))
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
		func(context.Context, string, []*os.File, ...string) ([]byte, error) {
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
		func(context.Context, string, []*os.File, ...string) ([]byte, error) {
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

func TestSidecarSRTLanguageSuffix(t *testing.T) {
	dir := t.TempDir()
	media := filepath.Join(dir, "movie.mkv")
	os.WriteFile(media, []byte("x"), 0o644)
	os.WriteFile(filepath.Join(dir, "movie.eng.forced.srt"), []byte("s"), 0o644)
	got, ok := sidecarSRT(media)
	if !ok || !strings.HasSuffix(got, "movie.eng.forced.srt") {
		t.Fatalf("got %q ok=%v", got, ok)
	}
	os.WriteFile(filepath.Join(dir, "movie.srt"), []byte("s"), 0o644)
	got, ok = sidecarSRT(media)
	if !ok || !strings.HasSuffix(got, "movie.srt") {
		t.Fatalf("plain should win: %q ok=%v", got, ok)
	}
	os.WriteFile(filepath.Join(dir, "movie.notes.srt"), []byte("s"), 0o644)
	os.Remove(filepath.Join(dir, "movie.srt"))
	os.Remove(filepath.Join(dir, "movie.eng.forced.srt"))
	if _, ok := sidecarSRT(media); !ok {
		t.Fatal("letter-suffix sibling should match")
	}
	os.WriteFile(filepath.Join(dir, "movie.2010.srt"), []byte("s"), 0o644)
	os.Remove(filepath.Join(dir, "movie.notes.srt"))
	if _, ok := sidecarSRT(media); ok {
		t.Fatal("digit-containing suffix must not match")
	}
}

func TestSubtitlesExtractFromDescriptorIntegration(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	srt := filepath.Join(dir, "subs.srt")
	if err := os.WriteFile(srt, []byte("1\n00:00:01,000 --> 00:00:02,000\nEmbedded\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(dir, "emb.mkv")
	out, err := exec.Command("ffmpeg", "-y", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=duration=2:size=64x64:rate=10",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-i", srt,
		"-map", "0", "-map", "1", "-map", "2",
		"-c:v", "libx264", "-c:a", "aac", "-c:s", "srt", media).CombinedOutput()
	if err != nil {
		t.Skipf("could not generate embedded-subtitle source: %v: %s", err, out)
	}
	db, base, token, a := newSubtitleEnv(t)
	lib, _ := db.AddLibrary("m", "movies", dir)
	w := seedWork(t, db, lib, "Film", nil, nil, 1, 1)
	e := seedEdition(t, db, w, ptr(2.0))
	fid := seedFile(t, db, e, media)
	resp := authedGet(t, base+fmt.Sprintf("/subtitles/%d", fid), token)
	got := bodyStr(t, resp)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d body=%s", resp.StatusCode, got)
	}
	if !strings.Contains(got, "Embedded") {
		t.Fatalf("body = %q, want embedded cue extracted over the descriptor", got)
	}
	cacheDir := filepath.Join(a.DataDir, "subtitles")
	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("cache entries = %v err = %v, want exactly one", entries, err)
	}
}
