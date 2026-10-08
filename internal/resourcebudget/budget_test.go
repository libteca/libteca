package resourcebudget

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReservationBoundaries(t *testing.T) {
	for _, n := range []int64{9, 10, 11} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			b := New("bytes", 10)
			r, err := b.Reserve(n)
			if n > 10 {
				if !errors.Is(err, ErrLimit) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			r.Release()
			r.Release()
			if b.used != 0 {
				t.Fatal(b.used)
			}
		})
	}
}
func TestReservationConcurrency(t *testing.T) {
	b := New("bytes", 10)
	var admitted atomic.Int64
	var wg sync.WaitGroup
	release := make(chan struct{})
	ready := make(chan struct{}, 30)
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := b.Reserve(1)
			if err == nil {
				admitted.Add(1)
			}
			ready <- struct{}{}
			<-release
			r.Release()
		}()
	}
	for range 30 {
		<-ready
	}
	if admitted.Load() != 10 {
		t.Fatal(admitted.Load())
	}
	close(release)
	wg.Wait()
	if b.used != 0 {
		t.Fatal(b.used)
	}
}
func TestReservedReadLifetime(t *testing.T) {
	for _, n := range []int{9, 10, 11} {
		b := New("bytes", 11)
		data, r, err := ReadReserved(bytes.NewReader(make([]byte, n)), 10, b)
		if n > 10 {
			if !errors.Is(err, ErrLimit) || b.used != 0 {
				t.Fatalf("%v %d", err, b.used)
			}
			continue
		}
		if err != nil || len(data) != n {
			t.Fatalf("%v %d", err, len(data))
		}
		if _, err := b.Reserve(1); !errors.Is(err, ErrLimit) {
			t.Fatal(err)
		}
		r.Release()
		if b.used != 0 {
			t.Fatal(b.used)
		}
	}
}
func TestDiskAdmissionIncludesRestartAndActiveReservation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "old"), make([]byte, 4), 0600); err != nil {
		t.Fatal(err)
	}
	pool := NewDiskPool(root, 3, 10)
	a, err := pool.Acquire(filepath.Join(root, "a"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "a"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "a", "data"), make([]byte, 3), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := pool.Acquire(filepath.Join(root, "b"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Acquire(filepath.Join(root, "c")); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	b.Release()
	a.Release()
	restarted := NewDiskPool(root, 4, 10)
	if _, err := restarted.Acquire(filepath.Join(root, "new")); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
}
func TestMonitorStopsWriterAndChecksFinalBoundary(t *testing.T) {
	for _, n := range []int{9, 10, 11} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			finish := Monitor(ctx, dir, 10, cancel)
			if err := os.WriteFile(filepath.Join(dir, "out"), make([]byte, n), 0600); err != nil {
				t.Fatal(err)
			}
			err := finish()
			if (n > 10) != errors.Is(err, ErrLimit) {
				t.Fatal(err)
			}
		})
	}
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finish := Monitor(ctx, dir, 10, cancel)
	if err := os.WriteFile(filepath.Join(dir, "out"), make([]byte, 11), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("monitor did not cancel")
	}
	if !errors.Is(finish(), ErrLimit) {
		t.Fatal("missing limit error")
	}
}
func TestInvalidEnvironment(t *testing.T) {
	for _, v := range []string{"-1", "NaN", "9223372036854775808"} {
		t.Setenv("LIMIT_TEST", v)
		if _, err := Env("LIMIT_TEST"); err == nil {
			t.Fatal(v)
		}
	}
}
