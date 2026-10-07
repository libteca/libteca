package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/meta"
	"github.com/libteca/libteca/internal/podcast"
	"github.com/libteca/libteca/internal/scan"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

func newAuditDStack(t *testing.T) (*httptest.Server, *store.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	uid, tok := seedUser(t, db)
	_ = uid
	a := New(db, dir)
	app := neutron.New()
	g := app.Router().Group("/api/core", auth.MiddlewareWithMediaCookie(db, MediaRequest))
	a.Mount(g)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return srv, db, tok
}

func auditDPost(t *testing.T, srv *httptest.Server, tok, path, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest("POST", srv.URL+"/api/core"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func newScannedGamesEdition(t *testing.T, db *store.DB) int64 {
	t.Helper()
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
	if _, err := scan.Library(context.Background(), db, lib, filepath.Join(t.TempDir(), "covers"), nil); err != nil {
		t.Fatal(err)
	}
	works, err := db.WorksInLibrary(libID)
	if err != nil || len(works) != 1 || len(works[0].Editions) != 1 {
		t.Fatalf("works = %+v err = %v", works, err)
	}
	return works[0].Editions[0].ID
}

func TestGamePlaytimeAbove720HoursAccepted(t *testing.T) {
	srv, db, tok := newAuditDStack(t)
	edID := newScannedGamesEdition(t, db)
	h720 := 720 * 3600.0
	h721 := 721 * 3600.0
	for _, pos := range []float64{h720, h721} {
		code, body := auditDPost(t, srv, tok, fmt.Sprintf("/progress/%d", edID), fmt.Sprintf(`{"position":%g,"duration":0}`, pos))
		if code != 200 {
			t.Fatalf("game playtime %g = %d %s, want 200", pos, code, body)
		}
	}
	p, err := db.GetProgress(1, edID)
	if err != nil {
		t.Fatal(err)
	}
	if p.EditionPositionSecs != h721 {
		t.Fatalf("round trip = %v, want %v", p.EditionPositionSecs, h721)
	}
}

func TestGamePlaytimeBoundsRejected(t *testing.T) {
	srv, db, tok := newAuditDStack(t)
	edID := newScannedGamesEdition(t, db)
	cases := []string{
		`{"position":-1}`,
		`{"position":NaN}`,
		`{"position":9e999}`,
		fmt.Sprintf(`{"position":%g}`, float64(1<<53)),
	}
	for _, body := range cases {
		if code, b := auditDPost(t, srv, tok, fmt.Sprintf("/progress/%d", edID), body); code != 400 {
			t.Fatalf("game position %s = %d %s, want 400", body, code, b)
		}
	}
}

func TestAudioUnknownDurationCapUnchanged(t *testing.T) {
	srv, db, tok := newAuditDStack(t)
	lib, err := db.AddLibrary("Audio", "audiobooks", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := seedWork(t, db, lib, "Big Book", nil, nil, 1, 1)
	ed := seedBookEdition(t, db, w, "audio", nil)
	seedFileOnDisk(t, db, lib, ed, "big.m4b", []byte("audio"))
	over := 30*24*3600 + 1
	code, body := auditDPost(t, srv, tok, fmt.Sprintf("/progress/%d", ed), fmt.Sprintf(`{"position":%d}`, over))
	if code != 400 {
		t.Fatalf("audio unknown-duration over-cap = %d %s, want 400", code, body)
	}
}

func TestBackfillNoOpDoesNotPublishChecksum(t *testing.T) {
	a, db, workID, _ := newGamesBackfillEnv(t)
	_ = a
	if _, err := db.Exec(`UPDATE files SET sha256 = NULL`); err != nil {
		t.Fatal(err)
	}
	full, err := db.WorkViewByID(workID)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := db.Library(full.LibraryID)
	if err != nil {
		t.Fatal(err)
	}
	backfillSHA256(context.Background(), db, lib.Path, full.Editions)
	if full.Editions[0].Files[0].SHA256 == nil {
		t.Fatal("baseline backfill must publish the checksum")
	}
	var rowSHA string
	if err := db.QueryRow(`SELECT sha256 FROM files LIMIT 1`).Scan(&rowSHA); err != nil || rowSHA == "" {
		t.Fatalf("row sha = %q err %v", rowSHA, err)
	}
}

func TestBackfillConcurrentScanWinsNotPublished(t *testing.T) {
	a, db, workID, _ := newGamesBackfillEnv(t)
	_ = a
	full, err := db.WorkViewByID(workID)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := db.Library(full.LibraryID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE files SET sha256 = NULL`); err != nil {
		t.Fatal(err)
	}
	f := &full.Editions[0].Files[0]
	newer := "deadbeef"
	if _, err := db.Exec(`UPDATE files SET sha256 = ? WHERE id = ?`, newer, f.ID); err != nil {
		t.Fatal(err)
	}
	sum, ok := backfillFileSHA(context.Background(), db, lib.Path, f)
	if ok {
		t.Fatalf("stale observation published %q as persisted", sum)
	}
	var rowSHA string
	if err := db.QueryRow(`SELECT sha256 FROM files WHERE id = ?`, f.ID).Scan(&rowSHA); err != nil || rowSHA != newer {
		t.Fatalf("row sha = %q err %v, want concurrent scan value", rowSHA, err)
	}
}

func TestBackfillRelinkedRowNotPublished(t *testing.T) {
	a, db, workID, _ := newGamesBackfillEnv(t)
	_ = a
	full, err := db.WorkViewByID(workID)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := db.Library(full.LibraryID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE files SET sha256 = NULL`); err != nil {
		t.Fatal(err)
	}
	f := &full.Editions[0].Files[0]
	if _, err := db.Exec(`UPDATE files SET size_bytes = size_bytes + 1 WHERE id = ?`, f.ID); err != nil {
		t.Fatal(err)
	}
	if sum, ok := backfillFileSHA(context.Background(), db, lib.Path, f); ok {
		t.Fatalf("relinked/changed row published %q", sum)
	}
	var null int
	if err := db.QueryRow(`SELECT count(*) FROM files WHERE sha256 IS NULL`).Scan(&null); err != nil || null != 1 {
		t.Fatalf("null rows = %d err %v, want 1", null, err)
	}
}

type cancelAfterProvider struct {
	entered chan struct{}
	cancel  context.CancelFunc
	once    sync.Once
}

func (p *cancelAfterProvider) Name() string { return "cancelafter" }

func (p *cancelAfterProvider) Search(ctx context.Context, q meta.Query) ([]meta.Result, error) {
	p.once.Do(func() { close(p.entered) })
	p.cancel()
	return nil, errors.New("search failed")
}

func (p *cancelAfterProvider) Fetch(ctx context.Context, id string) (*meta.Result, error) {
	return nil, ctx.Err()
}

func TestRefreshMetaCanceledDuringFinalItemIsNotDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prov := &cancelAfterProvider{entered: make(chan struct{}), cancel: cancel}
	a, db, libID := newMatchingAPI(t, "books", prov)
	addMatchWork(t, db, libID, "One", "A", "[]")
	a.SetShutdownCtx(ctx)

	id := strconv.FormatInt(libID, 10)
	code, body := callHandler(t, "POST", "/libraries/1/refresh-meta", id, ``, a.refreshMeta)
	if code != 202 {
		t.Fatalf("refresh POST = %d %v", code, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, snap := callHandler(t, "GET", "/libraries/1/refresh-meta", id, ``, a.refreshMetaStatus)
		if st, _ := snap["status"].(string); st != "running" {
			if st != "error" || snap["error"] != "canceled" {
				t.Fatalf("terminal snapshot = %v, want error/canceled after final-item cancellation", snap)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("refresh-meta run never reached a terminal state")
}

type cancelingTransport struct {
	once   sync.Once
	cancel context.CancelFunc
}

func (tr *cancelingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.once.Do(func() { tr.cancel() })
	return nil, errors.New("transport failed")
}

func TestOPMLImportCanceledDuringFinalFeedIsNotDone(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a := New(db, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := &cancelingTransport{cancel: cancel}
	client := &http.Client{Transport: tr}
	a.Podcasts = podcast.NewWithClient(db, a.DataDir, client, client)
	run := &opmlRun{snap: opmlImportStatus{Status: "running", Total: 1}}
	if !a.launchJob(func() { a.runOPMLImport(ctx, run, []string{"https://example.com/feed.xml"}) }) {
		t.Fatal("launchJob refused")
	}
	waited := make(chan struct{})
	go func() {
		a.WaitJobs()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("WaitJobs blocked on the cancelled OPML import")
	}
	snap := run.snapshot()
	if snap.Status != "error" || snap.Error != "canceled" {
		t.Fatalf("snapshot = %+v, want error/canceled after final-feed cancellation", snap)
	}
	if snap.Added != 0 {
		t.Fatalf("added = %d, want 0", snap.Added)
	}
}
