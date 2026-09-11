package scan

import (
	"archive/zip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func writeCancelCBZ(t *testing.T, path string) {
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

func TestLibraryCancelledMidWalk(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 8; i++ {
		writeCancelCBZ(t, filepath.Join(root, "book"+strings.Repeat("x", i+1)+".cbz"))
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lib := &store.Library{ID: 1, Type: "books", Path: root}
	ctx, cancel := context.WithCancel(context.Background())
	var cancelledWalks int64
	onProgress := func(p Progress) {
		if p.FilesSeen >= 1 && atomic.CompareAndSwapInt64(&cancelledWalks, 0, 1) {
			cancel()
		}
	}
	_, err = Library(ctx, db, lib, filepath.Join(t.TempDir(), "covers"), onProgress)
	cancel()
	if err == nil {
		t.Fatal("Library returned nil error after cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "scan cancelled") {
		t.Fatalf("err = %q, want scan cancelled detail", err.Error())
	}
}

func TestAllCancelledUpfront(t *testing.T) {
	root := t.TempDir()
	writeCancelCBZ(t, filepath.Join(root, "book.cbz"))
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.AddLibrary("Books", "books", root); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var final Progress
	n, err := All(ctx, db, filepath.Join(t.TempDir(), "covers"), func(p Progress) { final = p })
	if err == nil {
		t.Fatal("All returned nil error after cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n != 0 || final.FilesSeen != 0 {
		t.Fatalf("n = %d, seen = %d, want zero progress", n, final.FilesSeen)
	}
}
