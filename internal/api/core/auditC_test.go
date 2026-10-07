package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/meta"
	"github.com/libteca/libteca/internal/scan"
	"github.com/libteca/libteca/internal/store"
)

// C-03: an abandoned GET /works/{id} must stop paying for the multi-GB
// backfill hash: cancelling the request context aborts the read, the
// handler returns promptly, and later views still backfill fine.
func TestWorkBackfillAbortsWhenRequestCancelled(t *testing.T) {
	a, db, workID, uid := newGamesBackfillEnv(t)
	if _, err := db.Exec(`UPDATE files SET sha256 = NULL`); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	var once sync.Once
	orig := backfillHashFile
	backfillHashFile = func(ctx context.Context, root, path string) string {
		once.Do(func() { close(entered) })
		<-ctx.Done()
		return ""
	}
	defer func() { backfillHashFile = orig }()

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", "/works/1", nil)
	req.SetPathValue("id", strconv.FormatInt(workID, 10))
	req = req.WithContext(ctx)
	req = auth.WithUser(req, uid)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		rec := httptest.NewRecorder()
		a.work(rec, req)
		done <- rec
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("backfill hash never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("handler did not return after request cancellation")
	}

	backfillHashFile = orig
	rec := httptest.NewRecorder()
	req2 := httptest.NewRequest("GET", "/works/1", nil)
	req2.SetPathValue("id", strconv.FormatInt(workID, 10))
	req2 = auth.WithUser(req2, uid)
	a.work(rec, req2)
	if rec.Code != 200 {
		t.Fatalf("later view = %d %s, want 200", rec.Code, rec.Body.String())
	}
	want := fmt.Sprintf("%x", sha256.Sum256([]byte("mk")))
	files := backfillFiles(t, rec)
	if files[0]["sha256"] != want {
		t.Fatalf("later view sha256 = %v, want %s", files[0]["sha256"], want)
	}
}

// C-03: two simultaneous views of the same un-hashed work must perform
// exactly one file read (per-file singleflight) and both serve the hash.
func TestWorkBackfillSingleflight(t *testing.T) {
	a, db, workID, uid := newGamesBackfillEnv(t)
	if _, err := db.Exec(`UPDATE files SET sha256 = NULL`); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	var once sync.Once
	var reads atomic.Int32
	release := make(chan struct{})
	orig := backfillHashFile
	backfillHashFile = func(ctx context.Context, root, path string) string {
		reads.Add(1)
		once.Do(func() { close(entered) })
		<-release
		return orig(ctx, root, path)
	}
	defer func() { backfillHashFile = orig }()

	call := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/works/1", nil)
		req.SetPathValue("id", strconv.FormatInt(workID, 10))
		req = auth.WithUser(req, uid)
		rec := httptest.NewRecorder()
		a.work(rec, req)
		return rec
	}
	first := make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- call() }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("backfill hash never started")
	}
	second := make(chan *httptest.ResponseRecorder, 1)
	go func() { second <- call() }()
	time.Sleep(200 * time.Millisecond)
	if n := reads.Load(); n != 1 {
		t.Fatalf("file reads while first hash in flight = %d, want 1", n)
	}
	close(release)

	want := fmt.Sprintf("%x", sha256.Sum256([]byte("mk")))
	for i, ch := range []chan *httptest.ResponseRecorder{first, second} {
		select {
		case rec := <-ch:
			if rec.Code != 200 {
				t.Fatalf("view %d = %d %s, want 200", i, rec.Code, rec.Body.String())
			}
			files := backfillFiles(t, rec)
			if files[0]["sha256"] != want {
				t.Fatalf("view %d sha256 = %v, want %s", i, files[0]["sha256"], want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("view %d did not return", i)
		}
	}
	if n := reads.Load(); n != 1 {
		t.Fatalf("total file reads = %d, want exactly one shared hash", n)
	}
}

func newGamesBackfillEnv(t *testing.T) (*API, *store.DB, int64, int64) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	uid, _ := seedUser(t, db)
	roms := t.TempDir()
	if err := os.WriteFile(filepath.Join(roms, "Mario Kart (USA).gba"), []byte("mk"), 0o644); err != nil {
		t.Fatal(err)
	}
	libID, err := db.AddLibrary("Roms", "games", roms)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := db.Library(libID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scan.Library(context.Background(), db, lib, filepath.Join(dir, "covers"), nil); err != nil {
		t.Fatal(err)
	}
	works, err := db.WorksInLibrary(libID)
	if err != nil || len(works) != 1 {
		t.Fatalf("works = (%v, %v), want one", works, err)
	}
	return New(db, dir), db, works[0].ID, uid
}

func backfillFiles(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var payload struct {
		Editions []struct {
			Files []map[string]any `json:"files"`
		} `json:"editions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("bad body %q: %v", rec.Body.String(), err)
	}
	if len(payload.Editions) == 0 || len(payload.Editions[0].Files) != 1 {
		t.Fatalf("payload = %s, want one edition with one file", rec.Body.String())
	}
	return payload.Editions[0].Files
}

// C-04: a DB failure while composing the podcast detail body must be a 500,
// never a 2xx response carrying {"error"}.
func TestPodcastDetailDBFailuresAre500(t *testing.T) {
	a, db, token := newPodcastTestAPI(t)
	srv := mountPodcastServer(t, a, db)
	fs := newPodcastFeedServer(t, "Test Cast")
	code, body := doPodcastReq(t, srv, "POST", "/api/core/podcasts", token, map[string]any{
		"feedUrl": fs.URL + "/feed", "maxEpisodes": 2,
	})
	if code != 201 {
		t.Fatalf("subscribe = %d %v", code, body)
	}
	podID := strconv.FormatInt(int64(body["id"].(float64)), 10)

	origEps := podcastEpisodes
	origProg := episodeProgress
	defer func() {
		podcastEpisodes = origEps
		episodeProgress = origProg
	}()

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{"detail", "GET", "/api/core/podcasts/" + podID, nil},
		{"refresh", "POST", "/api/core/podcasts/" + podID + "/refresh", nil},
		{"patch", "PATCH", "/api/core/podcasts/" + podID, map[string]any{"autoDownload": false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			podcastEpisodes = origEps
			episodeProgress = origProg
			podcastEpisodes = func(_ *store.DB, _ int64) ([]store.PodcastEpisode, error) {
				return nil, errors.New("db down")
			}
			code, body := doPodcastReq(t, srv, tc.method, tc.path, token, tc.body)
			if code != 500 {
				t.Fatalf("%s = %d %v, want 500", tc.name, code, body)
			}
			if body["error"] != "internal error" {
				t.Fatalf("%s body = %v, want internal error", tc.name, body)
			}
		})
	}

	t.Run("progress failure", func(t *testing.T) {
		podcastEpisodes = origEps
		episodeProgress = func(_ *store.DB, _ int64, _ int64) (map[int64]*store.EpisodeProgress, error) {
			return nil, errors.New("db down")
		}
		code, body := doPodcastReq(t, srv, "GET", "/api/core/podcasts/"+podID, token, nil)
		if code != 500 {
			t.Fatalf("detail = %d %v, want 500", code, body)
		}
	})
}

type ctxBlockProvider struct {
	entered chan struct{}
	once    sync.Once
}

func (p *ctxBlockProvider) Name() string { return "ctxblock" }

func (p *ctxBlockProvider) Search(ctx context.Context, q meta.Query) ([]meta.Result, error) {
	p.once.Do(func() { close(p.entered) })
	<-ctx.Done()
	return nil, ctx.Err()
}

func (p *ctxBlockProvider) Fetch(ctx context.Context, id string) (*meta.Result, error) {
	return nil, ctx.Err()
}

// C-05: refresh-meta runs under the shutdown context; cancelling it must
// finish the run with the canceled error and release WaitJobs instead of
// grinding through every remaining provider search.
func TestRefreshMetaCancelsOnShutdown(t *testing.T) {
	prov := &ctxBlockProvider{entered: make(chan struct{})}
	a, db, libID := newMatchingAPI(t, "books", prov)
	addMatchWork(t, db, libID, "One", "A", "[]")
	addMatchWork(t, db, libID, "Two", "B", "[]")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.SetShutdownCtx(ctx)

	id := strconv.FormatInt(libID, 10)
	code, body := callHandler(t, "POST", "/libraries/1/refresh-meta", id, ``, a.refreshMeta)
	if code != 202 {
		t.Fatalf("refresh POST = %d %v", code, body)
	}
	select {
	case <-prov.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("provider search never started")
	}
	cancel()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, snap := callHandler(t, "GET", "/libraries/1/refresh-meta", id, ``, a.refreshMetaStatus)
		if st, _ := snap["status"].(string); st != "running" {
			if st != "error" || snap["error"] != "canceled" {
				t.Fatalf("terminal snapshot = %v, want error/canceled", snap)
			}
			waited := make(chan struct{})
			go func() {
				a.WaitJobs()
				close(waited)
			}()
			select {
			case <-waited:
				return
			case <-time.After(2 * time.Second):
				t.Fatal("WaitJobs blocked after shutdown cancellation")
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("refresh-meta run never reached a terminal state after cancellation")
}

// C-05: an OPML import handed a cancelled shutdown context must stop
// before subscribing anything and let WaitJobs return.
func TestOPMLImportCancelsOnShutdown(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a := New(db, dir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run := &opmlRun{snap: opmlImportStatus{Status: "running", Total: 3}}
	urls := []string{
		"https://example.invalid/one.xml",
		"https://example.invalid/two.xml",
		"https://example.invalid/three.xml",
	}
	if !a.launchJob(func() { a.runOPMLImport(ctx, run, urls) }) {
		t.Fatal("launchJob refused")
	}
	waited := make(chan struct{})
	go func() {
		a.WaitJobs()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		t.Fatal("WaitJobs blocked on a cancelled OPML import")
	}
	snap := run.snapshot()
	if snap.Status != "error" || snap.Added != 0 || snap.Failed != 0 {
		t.Fatalf("snapshot = %+v, want terminal error before any subscribe", snap)
	}
}

// C-06: ffmpeg stdout is capped at the subtitle cache budget and the child
// is killed the moment the cap is exceeded.
func TestFFmpegRunCapsOutput(t *testing.T) {
	script := filepath.Join(t.TempDir(), "spew")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nyes 0123456789abcdef | head -c 16777217\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	out, err := ffmpegRun(context.Background(), script, nil)
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want output-cap error", err)
	}
	if len(out) > subtitleCacheLimit {
		t.Fatalf("buffered %d bytes, want at most the cap", len(out))
	}
	if elapsed > 15*time.Second {
		t.Fatalf("cap enforcement took %s; child not killed on overflow", elapsed)
	}
}

// C-06, handler level: an extraction whose output blows past the cap
// answers 404 instead of buffering an unbounded stream.
func TestSubtitlesExtractionOverCapRejected(t *testing.T) {
	shim := t.TempDir()
	script := filepath.Join(shim, "ffmpeg")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nyes 0123456789abcdef | head -c 16777217\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	db, base, token, _ := newSubtitleEnv(t)
	dir := t.TempDir()
	lib, _ := db.AddLibrary("m", "movies", dir)
	w := seedWork(t, db, lib, "Film", nil, nil, 1, 1)
	e := seedEdition(t, db, w, ptr(100.0))
	media := filepath.Join(dir, "emb.mkv")
	if err := os.WriteFile(media, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fid := seedFile(t, db, e, media)
	start := time.Now()
	resp := authedGet(t, base+fmt.Sprintf("/subtitles/%d", fid), token)
	got := bodyStr(t, resp)
	if resp.StatusCode != 404 {
		t.Fatalf("status = %d body = %s, want 404 for over-cap extraction", resp.StatusCode, got)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("handler took %s; child not reaped on overflow", elapsed)
	}
}
