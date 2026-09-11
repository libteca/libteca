package core

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/scan"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

type scanResp struct {
	Status string `json:"status"`
	JobID  int64  `json:"jobId"`
	Error  string `json:"error"`
}

func newTestAPI(t *testing.T, fn ScanFunc) (*API, *store.DB, int64, int64) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	libA, err := db.AddLibrary("A", "audiobooks", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	libB, err := db.AddLibrary("B", "audiobooks", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(db, t.TempDir())
	a.ScanFunc = fn
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('admin','x',1,0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	testAdminUID, _ = res.LastInsertId()
	return a, db, libA, libB
}

func blockingScan(started chan<- int64, release <-chan struct{}) ScanFunc {
	return func(db *store.DB, lib *store.Library, coversDir string, onProgress scan.ProgressFn) (int, error) {
		started <- lib.ID
		<-release
		onProgress(scan.Progress{FilesSeen: 5, FilesProbed: 4, FilesAdded: 3, FilesUpdated: 1, WorksChanged: 2, CurrentPath: "x/y.mp3"})
		return 3, nil
	}
}

func postScan(t *testing.T, a *API, libID int64) (int, scanResp) {
	t.Helper()
	req := httptest.NewRequest("POST", "/libraries/"+strconv.FormatInt(libID, 10)+"/scan", nil)
	req.SetPathValue("id", strconv.FormatInt(libID, 10))
	req = auth.WithUser(req, testAdminUID)
	rec := httptest.NewRecorder()
	a.scanLibrary(rec, req)
	var body scanResp
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("bad response body %q: %v", rec.Body.String(), err)
	}
	return rec.Code, body
}

func waitJobDone(t *testing.T, db *store.DB, libID int64) store.ScanJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := db.ListScanJobs(libID, 5)
		if err == nil && len(jobs) > 0 && jobs[0].Status != "running" {
			return jobs[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scan job did not finish in time")
	return store.ScanJob{}
}

func TestDifferentLibrariesScanConcurrently(t *testing.T) {
	started := make(chan int64, 4)
	release := make(chan struct{})
	a, db, libA, libB := newTestAPI(t, blockingScan(started, release))

	code, respA := postScan(t, a, libA)
	if code != 202 || respA.Status != "scanning" || respA.JobID == 0 {
		t.Fatalf("scan A = %d %+v, want 202 scanning with jobId", code, respA)
	}
	code, respB := postScan(t, a, libB)
	if code != 202 || respB.Status != "scanning" || respB.JobID == 0 {
		t.Fatalf("scan B = %d %+v, want 202 scanning with jobId", code, respB)
	}

	// Both stubs must be inside ScanFunc at once: neither can return until
	// release closes, so a global lock would deadlock this receive.
	got := map[int64]bool{}
	for i := 0; i < 2; i++ {
		select {
		case id := <-started:
			got[id] = true
		case <-time.After(2 * time.Second):
			t.Fatal("libraries did not scan concurrently")
		}
	}
	if !got[libA] || !got[libB] {
		t.Fatalf("started = %v, want both libraries", got)
	}
	close(release)

	for _, id := range []int64{libA, libB} {
		j := waitJobDone(t, db, id)
		if j.Status != "done" || j.FilesSeen != 5 || j.FilesAdded != 3 || j.FilesUpdated != 1 || j.WorksChanged != 2 || j.FinishedAt == nil {
			t.Fatalf("job for library %d = %+v", id, j)
		}
	}
}

func TestSameLibraryScanConflict(t *testing.T) {
	started := make(chan int64, 4)
	release := make(chan struct{})
	a, db, libA, _ := newTestAPI(t, blockingScan(started, release))

	code, first := postScan(t, a, libA)
	if code != 202 {
		t.Fatalf("first scan = %d, want 202", code)
	}
	code, second := postScan(t, a, libA)
	if code != 409 {
		t.Fatalf("second scan = %d, want 409", code)
	}
	if second.Status != "already_scanning" || second.JobID != first.JobID || second.Error == "" {
		t.Fatalf("conflict body = %+v, want already_scanning with first jobId", second)
	}

	close(release)
	waitJobDone(t, db, libA)
	// In-flight conflict resolved, but the finished job is still inside the
	// dedup window: the repeat POST 409s with the last job id.
	code, done := postScan(t, a, libA)
	if code != 409 || done.Status != "already_done" || done.JobID != first.JobID {
		t.Fatalf("scan after finish = %d %+v, want 409 already_done with first jobId", code, done)
	}
}

func TestScanDedupWindowExpires(t *testing.T) {
	old := scanDedupWindow
	scanDedupWindow = 30 * time.Millisecond
	t.Cleanup(func() { scanDedupWindow = old })
	a, db, libA, _ := newTestAPI(t, func(db *store.DB, lib *store.Library, coversDir string, onProgress scan.ProgressFn) (int, error) {
		return 0, nil
	})

	code, first := postScan(t, a, libA)
	if code != 202 || first.Status != "scanning" {
		t.Fatalf("first scan = %d %+v, want 202 scanning", code, first)
	}
	waitJobDone(t, db, libA)
	code, dup := postScan(t, a, libA)
	if code != 409 || dup.Status != "already_done" || dup.JobID != first.JobID {
		t.Fatalf("scan inside window = %d %+v, want 409 already_done", code, dup)
	}
	time.Sleep(60 * time.Millisecond)
	code, next := postScan(t, a, libA)
	if code != 202 || next.Status != "scanning" || next.JobID == first.JobID {
		t.Fatalf("scan after window = %d %+v, want 202 scanning with new jobId", code, next)
	}
	waitJobDone(t, db, libA)
}

func TestScanJobErrorRecorded(t *testing.T) {
	a, db, libA, _ := newTestAPI(t, func(db *store.DB, lib *store.Library, coversDir string, onProgress scan.ProgressFn) (int, error) {
		return 0, fmt.Errorf("disk fell over")
	})
	code, _ := postScan(t, a, libA)
	if code != 202 {
		t.Fatalf("scan = %d, want 202", code)
	}
	j := waitJobDone(t, db, libA)
	if j.Status != "error" || j.Error == nil || *j.Error != "disk fell over" {
		t.Fatalf("job = %+v, want error recorded", j)
	}
}

func mountTestServer(t *testing.T, a *API) *httptest.Server {
	t.Helper()
	app := neutron.New()
	r := app.Router()
	a.Mount(r.Group("/api/core"))
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return srv
}

type sseEvent struct {
	JobID  int64  `json:"jobId"`
	Status string `json:"status"`
	Error  string `json:"error"`
}

func readSSE(t *testing.T, body io.ReadCloser, events chan<- sseEvent) {
	t.Helper()
	defer body.Close()
	defer close(events)
	sc := bufio.NewScanner(body)
	var data string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event:") || strings.HasPrefix(line, ":") || line == "retry: 5000" {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			continue
		}
		if line == "" && data != "" {
			var ev sseEvent
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				t.Errorf("bad sse data %q: %v", data, err)
				return
			}
			events <- ev
			data = ""
		}
	}
}

func TestScanEventsStream(t *testing.T) {
	started := make(chan int64, 4)
	release := make(chan struct{})
	a, _, libA, _ := newTestAPI(t, blockingScan(started, release))
	srv := mountTestServer(t, a)

	code, _ := postScan(t, a, libA)
	if code != 202 {
		t.Fatalf("scan = %d, want 202", code)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("stub never started")
	}

	resp, err := srv.Client().Get(srv.URL + "/api/core/libraries/" + strconv.FormatInt(libA, 10) + "/scan/events")
	if err != nil {
		t.Fatal(err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	events := make(chan sseEvent, 8)
	go readSSE(t, resp.Body, events)

	var snapshot sseEvent
	select {
	case snapshot = <-events:
	case <-time.After(2 * time.Second):
		t.Fatal("no snapshot event on connect")
	}
	if snapshot.Status != "running" || snapshot.JobID == 0 {
		t.Fatalf("snapshot = %+v, want running job", snapshot)
	}

	close(release)
	var terminal sseEvent
	select {
	case terminal = <-events:
	case <-time.After(2 * time.Second):
		t.Fatal("no terminal event after scan end")
	}
	if terminal.Status != "done" {
		t.Fatalf("terminal = %+v, want done", terminal)
	}
	select {
	case ev, ok := <-events:
		if ok {
			t.Fatalf("stream sent extra event %+v after terminal", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not close after terminal event")
	}
}

func TestScanEventsIdleSendsBaselineAndCloses(t *testing.T) {
	a, _, libA, _ := newTestAPI(t, blockingScan(make(chan int64, 4), make(chan struct{})))
	srv := mountTestServer(t, a)

	resp, err := srv.Client().Get(srv.URL + "/api/core/libraries/" + strconv.FormatInt(libA, 10) + "/scan/events")
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan sseEvent, 8)
	go readSSE(t, resp.Body, events)

	select {
	case ev := <-events:
		if ev.Status != "idle" {
			t.Fatalf("idle event = %+v, want status idle", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no baseline event")
	}
	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("stream should close after baseline")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not close")
	}
}
func TestScanPrunesProviderCache(t *testing.T) {
	a, db, libA, libB := newTestAPI(t, func(db *store.DB, lib *store.Library, coversDir string, onProgress scan.ProgressFn) (int, error) {
		return 0, nil
	})
	now := time.Now().UnixMilli()
	old := now - 22*24*3600*1000
	seedCache := func(provider, key string, at int64) {
		t.Helper()
		if err := db.PutCached(provider, key, "{}"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE provider_cache SET fetched_at = ? WHERE provider = ? AND key = ?`, at, provider, key); err != nil {
			t.Fatal(err)
		}
	}
	seedCache("tmdb", "old", old)
	seedCache("tmdb", "fresh", now)
	seedCache("match-skip", "7", old)

	code, _ := postScan(t, a, libA)
	if code != 202 {
		t.Fatalf("scan = %d, want 202", code)
	}
	waitJobDone(t, db, libA)
	if _, _, ok := db.GetCached("tmdb", "old"); ok {
		t.Fatal("stale cache row survived scan")
	}
	if _, _, ok := db.GetCached("tmdb", "fresh"); !ok {
		t.Fatal("fresh cache row pruned")
	}
	if _, _, ok := db.GetCached("match-skip", "7"); !ok {
		t.Fatal("match-skip row pruned")
	}

	seedCache("audible", "old2", old)
	code, _ = postScan(t, a, libB)
	if code != 202 {
		t.Fatalf("second scan = %d, want 202", code)
	}
	waitJobDone(t, db, libB)
	if _, _, ok := db.GetCached("audible", "old2"); !ok {
		t.Fatal("24h guard failed: second scan pruned again")
	}
}
