package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/api/core"
	"github.com/libteca/libteca/internal/store"
)

func newEnvAlias(t *testing.T, cfg Config, libPath string) (*Watcher, *store.DB, int64, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := os.MkdirAll(filepath.Join(dir, "data", "covers"), 0o755); err != nil {
		t.Fatal(err)
	}
	libID, err := db.AddLibrary("Books", "books", libPath)
	if err != nil {
		t.Fatal(err)
	}
	a := core.New(db, filepath.Join(dir, "data"))
	return New(a, db, cfg), db, libID, libPath
}

func TestSymlinkLibraryRootGetsWatched(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	alias := filepath.Join(dir, "alias")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	w, db, libID, _ := newEnvAlias(t, Config{Debounce: 100 * time.Millisecond, SweepEvery: 0}, alias)
	seedFreshJob(t, db, libID)
	start(t, w)
	waitWatched(t, w, alias)

	path := filepath.Join(alias, "Link - Book.cbz")
	writeCBZ(t, path)
	jobs := waitJobs(t, db, libID, 2)
	if jobs[0].FilesAdded != 1 {
		t.Fatalf("job ingested %d files, want 1 (job %+v)", jobs[0].FilesAdded, jobs[0])
	}
	var stored string
	if err := db.QueryRow(`SELECT path FROM files LIMIT 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(stored) {
		t.Fatalf("stored path %q must be absolute", stored)
	}
	realPath, err := filepath.EvalSymlinks(stored)
	if err != nil {
		t.Fatal(err)
	}
	wantReal, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if realPath != wantReal {
		t.Fatalf("stored path resolves to %q, want %q", realPath, wantReal)
	}
}

func TestSymlinkRootTargetReplacementRearms(t *testing.T) {
	dir := t.TempDir()
	targetA := filepath.Join(dir, "a")
	targetB := filepath.Join(dir, "b")
	for _, d := range []string{targetA, targetB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(targetA, alias); err != nil {
		t.Fatal(err)
	}
	w, db, libID, _ := newEnvAlias(t, Config{Debounce: 100 * time.Millisecond, SyncEvery: 50 * time.Millisecond, SweepEvery: 0}, alias)
	seedFreshJob(t, db, libID)
	start(t, w)
	waitWatched(t, w, alias)

	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetB, alias); err != nil {
		t.Fatal(err)
	}
	waitWatched(t, w, alias)

	path := filepath.Join(targetB, "Swapped - In.cbz")
	writeCBZ(t, path)
	jobs := waitJobs(t, db, libID, 2)
	if jobs[0].FilesAdded != 1 {
		t.Fatalf("job after target swap ingested %d files, want 1 (job %+v)", jobs[0].FilesAdded, jobs[0])
	}
}

func TestUnresolvableRootIsNotWatched(t *testing.T) {
	dir := t.TempDir()
	alias := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "missing"), alias); err != nil {
		t.Fatal(err)
	}
	w, db, libID, _ := newEnvAlias(t, Config{Debounce: 100 * time.Millisecond, SweepEvery: 0}, alias)
	seedFreshJob(t, db, libID)
	start(t, w)
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		w.mu.Lock()
		dirs := len(w.dirs)
		roots := len(w.roots)
		w.mu.Unlock()
		if dirs > 0 || roots > 0 {
			t.Fatalf("dangling root armed watches: dirs=%d roots=%d", dirs, roots)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
