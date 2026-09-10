package watch

import (
	"archive/zip"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/api/core"
	"github.com/libteca/libteca/internal/store"
)

func newEnv(t *testing.T, cfg Config) (*Watcher, *store.DB, int64, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	libDir := filepath.Join(dir, "lib")
	if err := os.MkdirAll(filepath.Join(dir, "data", "covers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	libID, err := db.AddLibrary("Books", "books", libDir)
	if err != nil {
		t.Fatal(err)
	}
	a := core.New(db, filepath.Join(dir, "data"))
	return New(a, db, cfg), db, libID, libDir
}

func start(t *testing.T, w *Watcher) (context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	t.Cleanup(cancel)
	return cancel, done
}

func waitWatched(t *testing.T, w *Watcher, dir string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		_, ok := w.dirs[dir]
		w.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("watcher did not arm watches on library dir")
}

func seedFreshJob(t *testing.T, db *store.DB, libID int64) {
	t.Helper()
	id, err := db.CreateScanJob(libID)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishScanJob(id, "done", nil); err != nil {
		t.Fatal(err)
	}
}

func waitJobs(t *testing.T, db *store.DB, libID int64, min int) []store.ScanJob {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := db.ListScanJobs(libID, 50)
		if err == nil && len(jobs) >= min && jobs[0].Status != "running" {
			return jobs
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected at least %d finished scan jobs", min)
	return nil
}

func writeCBZ(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	w, _ := zw.Create("1.jpg")
	w.Write([]byte("\xff\xd8fakejpg"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCreateTriggersScanAndIngests(t *testing.T) {
	w, db, libID, libDir := newEnv(t, Config{Debounce: 300 * time.Millisecond, SweepEvery: 0})
	seedFreshJob(t, db, libID)
	start(t, w)
	waitWatched(t, w, libDir)

	path := filepath.Join(libDir, "Author - Title.cbz")
	writeCBZ(t, path)

	jobs := waitJobs(t, db, libID, 2)
	if jobs[0].FilesAdded != 1 {
		t.Fatalf("job ingested %d files, want 1 (job %+v)", jobs[0].FilesAdded, jobs[0])
	}
	var works int
	if err := db.QueryRow(`SELECT count(*) FROM works WHERE library_id = ?`, libID).Scan(&works); err != nil || works != 1 {
		t.Fatalf("works = %d err=%v, want 1", works, err)
	}
	var missing int
	if err := db.QueryRow(`SELECT missing FROM files WHERE path = ?`, path).Scan(&missing); err != nil || missing != 0 {
		t.Fatalf("file row missing=%d err=%v, want 0", missing, err)
	}
}

func TestChurnCollapsesToOneJob(t *testing.T) {
	debounce := 400 * time.Millisecond
	w, db, libID, libDir := newEnv(t, Config{Debounce: debounce, SweepEvery: 0})
	seedFreshJob(t, db, libID)
	start(t, w)
	waitWatched(t, w, libDir)

	for i := 0; i < 12; i++ {
		part := filepath.Join(libDir, fmt.Sprintf("churn-%d.part", i))
		if err := os.WriteFile(part, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(part, filepath.Join(libDir, fmt.Sprintf("churn-%d.cbz", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(libDir, ".hidden"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	jobs := waitJobs(t, db, libID, 2)
	time.Sleep(2*debounce + time.Second)
	after, err := db.ListScanJobs(libID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(jobs) {
		t.Fatalf("churn produced %d jobs, want 1", len(after)-1)
	}
}

func TestRemoveMarksMissing(t *testing.T) {
	w, db, libID, libDir := newEnv(t, Config{Debounce: 300 * time.Millisecond, SweepEvery: 0})
	seedFreshJob(t, db, libID)
	start(t, w)
	waitWatched(t, w, libDir)

	path := filepath.Join(libDir, "Gone Book.cbz")
	writeCBZ(t, path)
	waitJobs(t, db, libID, 2)

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	waitJobs(t, db, libID, 3)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var missing int
		if err := db.QueryRow(`SELECT missing FROM files WHERE path = ?`, path).Scan(&missing); err == nil {
			if missing == 1 {
				return
			}
		} else if err != sql.ErrNoRows {
			t.Fatalf("file row query: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("removed file row was never marked missing=1")
}

func TestStartupReconcileScansStaleLibraries(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := os.MkdirAll(filepath.Join(dir, "data", "covers"), 0o755); err != nil {
		t.Fatal(err)
	}
	staleDir := filepath.Join(dir, "stale")
	freshDir := filepath.Join(dir, "fresh")
	os.MkdirAll(staleDir, 0o755)
	os.MkdirAll(freshDir, 0o755)
	staleID, _ := db.AddLibrary("Stale", "books", staleDir)
	freshID, _ := db.AddLibrary("Fresh", "books", freshDir)

	seedFreshJob(t, db, staleID)
	seedFreshJob(t, db, freshID)
	if _, err := db.Exec(`UPDATE scan_jobs SET created_at = ? WHERE library_id = ?`,
		time.Now().Add(-25*time.Hour).UnixMilli(), staleID); err != nil {
		t.Fatal(err)
	}

	a := core.New(db, filepath.Join(dir, "data"))
	w := New(a, db, Config{Debounce: 300 * time.Millisecond, SweepEvery: 0})
	start(t, w)

	waitJobs(t, db, staleID, 2)
	time.Sleep(time.Second)
	freshJobs, err := db.ListScanJobs(freshID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(freshJobs) != 1 {
		t.Fatalf("fresh library got %d jobs, want 1 (no reconcile scan)", len(freshJobs))
	}
}

func TestLibraryAddedAtRuntimeGetsWatched(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := os.MkdirAll(filepath.Join(dir, "data", "covers"), 0o755); err != nil {
		t.Fatal(err)
	}
	libDir := filepath.Join(dir, "late")
	if err := os.MkdirAll(libDir, 0o755); err != nil {
		t.Fatal(err)
	}
	a := core.New(db, filepath.Join(dir, "data"))
	w := New(a, db, Config{Debounce: 300 * time.Millisecond, SweepEvery: 0, SyncEvery: 200 * time.Millisecond})
	start(t, w)

	libID, err := db.AddLibrary("Late", "books", libDir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(libDir, "Late Author - Late Book.cbz")
	writeCBZ(t, path)

	jobs := waitJobs(t, db, libID, 1)
	if jobs[0].FilesAdded != 1 {
		t.Fatalf("job ingested %d files, want 1 (job %+v)", jobs[0].FilesAdded, jobs[0])
	}
	var missing int
	if err := db.QueryRow(`SELECT missing FROM files WHERE path = ?`, path).Scan(&missing); err != nil || missing != 0 {
		t.Fatalf("file row missing=%d err=%v, want 0", missing, err)
	}
}

func TestShutdownStopsCleanly(t *testing.T) {
	w, db, libID, libDir := newEnv(t, Config{Debounce: 5 * time.Second, SweepEvery: 0})
	seedFreshJob(t, db, libID)
	cancel, done := start(t, w)
	waitWatched(t, w, libDir)

	writeCBZ(t, filepath.Join(libDir, "pending.cbz"))
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	time.Sleep(300 * time.Millisecond)
	jobs, err := db.ListScanJobs(libID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 {
		t.Fatalf("pending debounce fired after shutdown: %d jobs, want 1", len(jobs))
	}
}
