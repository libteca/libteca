package core

import (
	"context"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/scan"
	"github.com/libteca/libteca/internal/store"
)

func TestScanCancelledMarksJobError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, db, libA, _ := newTestAPI(t, func(ctx context.Context, db *store.DB, lib *store.Library, coversDir string, onProgress scan.ProgressFn) (int, error) {
		cancel()
		<-ctx.Done()
		return 0, ctx.Err()
	})
	if _, err := a.TriggerScan(ctx, libA); err != nil {
		t.Fatal(err)
	}
	j := waitJobDone(t, db, libA)
	if j.Status != "error" || j.Error == nil || *j.Error != "cancelled" || j.FinishedAt == nil {
		t.Fatalf("job = %+v, want status error with detail cancelled", j)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		drained := len(a.runs) == 0
		a.mu.Unlock()
		if drained {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scan run goroutine did not exit after cancellation")
}

func TestScanCancelledViaShutdownCtx(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, db, libA, _ := newTestAPI(t, func(ctx context.Context, db *store.DB, lib *store.Library, coversDir string, onProgress scan.ProgressFn) (int, error) {
		<-ctx.Done()
		return 0, ctx.Err()
	})
	a.SetShutdownCtx(base)
	code, resp := postScan(t, a, libA)
	if code != 202 || resp.Status != "scanning" {
		t.Fatalf("scan = %d %+v, want 202 scanning", code, resp)
	}
	cancel()
	j := waitJobDone(t, db, libA)
	if j.Status != "error" || j.Error == nil || *j.Error != "cancelled" {
		t.Fatalf("job = %+v, want status error with detail cancelled", j)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		drained := len(a.runs) == 0
		a.mu.Unlock()
		if drained {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("scan run goroutine did not exit after cancellation")
}
