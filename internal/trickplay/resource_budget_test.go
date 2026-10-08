package trickplay

import (
	"context"
	"errors"
	"github.com/libteca/libteca/internal/resourcebudget"
	"os"
	"path/filepath"
	"testing"
)

func TestTrickplayDiskBoundaryAndRestart(t *testing.T) {
	for _, n := range []int{9, 10, 11} {
		g := newTestGen(t, func(_ context.Context, _ string, _ []*os.File, args ...string) ([]byte, error) {
			return nil, os.WriteFile(filepath.Join(filepath.Dir(args[len(args)-1]), "0.jpg"), make([]byte, n), 0600)
		})
		g.diskPool = resourcebudget.NewDiskPool(filepath.Join(g.dir, "trickplay"), 10, 10)
		_, err := g.Tile(context.Background(), "e1", tempOpener(t), 160, 0)
		if n > 10 {
			if !errors.Is(err, resourcebudget.ErrLimit) || g.complete("e1", 160) {
				t.Fatalf("%d %v", n, err)
			}
			if _, err := os.Stat(g.widthDir("e1", 160)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		restarted := New(g.dir)
		restarted.diskPool = resourcebudget.NewDiskPool(filepath.Join(g.dir, "trickplay"), 10, 10)
		if _, err := restarted.diskPool.Acquire(restarted.widthDir("e2", 160)); !errors.Is(err, resourcebudget.ErrLimit) {
			t.Fatal(err)
		}
		if _, err := g.Tile(context.Background(), "e1", tempOpener(t), 160, 0); err != nil {
			t.Fatal(err)
		}
	}
}
func TestTrickplayOngoingDiskCancellation(t *testing.T) {
	g := newTestGen(t, func(ctx context.Context, _ string, _ []*os.File, args ...string) ([]byte, error) {
		if err := os.WriteFile(filepath.Join(filepath.Dir(args[len(args)-1]), "0.jpg"), make([]byte, 11), 0600); err != nil {
			return nil, err
		}
		<-ctx.Done()
		return nil, ctx.Err()
	})
	g.diskPool = resourcebudget.NewDiskPool(filepath.Join(g.dir, "trickplay"), 10, 20)
	if _, err := g.Tile(context.Background(), "e1", tempOpener(t), 160, 0); !errors.Is(err, resourcebudget.ErrLimit) {
		t.Fatal(err)
	}
	g.run = fakeGen(t)
	if _, err := g.Tile(context.Background(), "e1", tempOpener(t), 160, 0); err != nil {
		t.Fatal(err)
	}
}

func TestTrickplayWriteFailureCleansOutputAndReleases(t *testing.T) {
	g := newTestGen(t, func(_ context.Context, _ string, _ []*os.File, args ...string) ([]byte, error) {
		if err := os.WriteFile(filepath.Join(filepath.Dir(args[len(args)-1]), "0.jpg"), []byte("partial"), 0600); err != nil {
			return nil, err
		}
		return nil, os.ErrPermission
	})
	g.diskPool = resourcebudget.NewDiskPool(filepath.Join(g.dir, "trickplay"), 10, 10)
	if _, err := g.Tile(context.Background(), "e1", tempOpener(t), 160, 0); !errors.Is(err, os.ErrPermission) {
		t.Fatal(err)
	}
	if _, err := os.Stat(g.widthDir("e1", 160)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	g.run = fakeGen(t)
	if _, err := g.Tile(context.Background(), "e1", tempOpener(t), 160, 0); err != nil {
		t.Fatal(err)
	}
}

func TestTrickplayCrashLeftOutputReclaimedBeforeFullReservation(t *testing.T) {
	for _, per := range []int64{0, 10} {
		g := newTestGen(t, func(_ context.Context, _ string, _ []*os.File, args ...string) ([]byte, error) {
			return nil, os.WriteFile(filepath.Join(filepath.Dir(args[len(args)-1]), "0.jpg"), make([]byte, 10), 0600)
		})
		g.diskPool = resourcebudget.NewDiskPool(filepath.Join(g.dir, "trickplay"), per, 10)
		dir := g.widthDir("e1", 160)
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "0.jpg"), []byte("partial"), 0600); err != nil {
			t.Fatal(err)
		}
		g.cleanup = func(string) error { return os.ErrPermission }
		if _, err := g.Tile(context.Background(), "e1", tempOpener(t), 160, 0); !errors.Is(err, os.ErrPermission) {
			t.Fatal("cleanup failure hidden", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "0.jpg")); err != nil {
			t.Fatal("partial removed on denied cleanup", err)
		}
		g.cleanup = nil
		if _, err := g.Tile(context.Background(), "e1", tempOpener(t), 160, 0); err != nil {
			t.Fatal("recovery admission", err)
		}
		if !g.complete("e1", 160) {
			t.Fatal("not published")
		}
	}
}
func TestTrickplayConcurrentGeneratorPreservesActiveAndForeignOutput(t *testing.T) {
	entered := make(chan struct{})
	finish := make(chan struct{})
	result := make(chan error, 1)
	g := newTestGen(t, func(_ context.Context, _ string, _ []*os.File, args ...string) ([]byte, error) {
		err := os.WriteFile(filepath.Join(filepath.Dir(args[len(args)-1]), "0.jpg"), []byte("active"), 0600)
		close(entered)
		<-finish
		return nil, err
	})
	go func() { _, err := g.Tile(context.Background(), "e1", tempOpener(t), 160, 0); result <- err }()
	<-entered
	other := New(g.dir)
	other.ffmpeg = "fake"
	other.fdArgs = g.fdArgs
	other.run = fakeGen(t)
	if _, err := other.Tile(context.Background(), "e1", tempOpener(t), 160, 0); !errors.Is(err, ErrBusy) {
		t.Fatal("concurrent admitted", err)
	}
	body, err := os.ReadFile(filepath.Join(g.widthDir("e1", 160), "0.jpg"))
	if err != nil || string(body) != "active" {
		t.Fatal(string(body), err)
	}
	close(finish)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	foreign := g.widthDir("e2", 160)
	os.MkdirAll(foreign, 0700)
	os.WriteFile(filepath.Join(foreign, "foreign.data"), []byte("foreign"), 0600)
	if _, err := other.Tile(context.Background(), "e2", tempOpener(t), 160, 0); !errors.Is(err, ErrBusy) {
		t.Fatal("foreign removed", err)
	}
	if _, err := os.Stat(filepath.Join(foreign, "foreign.data")); err != nil {
		t.Fatal(err)
	}
}
