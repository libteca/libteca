package watch

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/scan"
	"github.com/libteca/libteca/internal/store"
)

type admissionFake struct {
	mu     sync.Mutex
	calls  int64
	jobIDs []int64
}

func (f *admissionFake) TriggerScan(ctx context.Context, libraryID int64) (int64, error) {
	n := atomic.AddInt64(&f.calls, 1)
	_ = n
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobIDs = append(f.jobIDs, libraryID)
	return 0, scan.ErrScanRunning
}

type failureFake struct{}

func (failureFake) TriggerScan(ctx context.Context, libraryID int64) (int64, error) {
	return 0, errors.New("library is gone")
}

func newAdmissionWatcher(t *testing.T, sc Scanner, db *store.DB) *Watcher {
	t.Helper()
	w := New(sc, db, Config{Debounce: 20 * time.Millisecond, SyncEvery: time.Hour, StaleAfter: time.Hour, SweepEvery: 0})
	w.ctx = context.Background()
	return w
}

func waitForCalls(t *testing.T, f *admissionFake, want int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&f.calls) >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("trigger calls = %d, want at least %d", atomic.LoadInt64(&f.calls), want)
}

func admissionEnv(t *testing.T) (*store.DB, int64) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	libID, err := db.AddLibrary("Books", "books", filepath.Join(dir, "lib"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateScanJob(libID); err != nil {
		t.Fatal(err)
	}
	jobs, err := db.ListScanJobs(libID, 1)
	if err != nil || len(jobs) == 0 {
		t.Fatalf("jobs = %v err = %v", jobs, err)
	}
	if err := db.FinishScanJob(jobs[0].ID, "done", nil); err != nil {
		t.Fatal(err)
	}
	return db, libID
}

func TestBusyAdmissionRearmsEvenWhenJobAlreadyDone(t *testing.T) {
	db, libID := admissionEnv(t)
	fake := &admissionFake{}
	w := newAdmissionWatcher(t, fake, db)
	defer w.stop()
	w.triggerAndWait(context.Background(), libID)
	waitForCalls(t, fake, 2)
}

func TestPermanentFailureDoesNotRearm(t *testing.T) {
	db, libID := admissionEnv(t)
	w := newAdmissionWatcher(t, failureFake{}, db)
	defer w.stop()
	w.triggerAndWait(context.Background(), libID)
	time.Sleep(80 * time.Millisecond)
	w.mu.Lock()
	timers := len(w.timers)
	w.mu.Unlock()
	if timers != 0 {
		t.Fatalf("timers armed after permanent failure = %d, want 0", timers)
	}
}
