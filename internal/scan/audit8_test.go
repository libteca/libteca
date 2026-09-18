package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func openScanDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestScanSymlinkLibraryRootWalksTarget(t *testing.T) {
	db := openScanDB(t)
	real := t.TempDir()
	genAudio(t, filepath.Join(real, "book.mp3"), "440", 1)
	alias := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, alias); err != nil {
		t.Skipf("symlink: %v", err)
	}
	lib := &store.Library{ID: 1, Name: "a", Type: "audiobooks", Path: alias}
	if _, err := db.AddLibraryChecked("a", "audiobooks", alias, nil); err != nil {
		t.Fatal(err)
	}
	lib.ID = 1
	scanOnce(t, db, lib, t.TempDir())

	rows := fileRows(t, db)
	if len(rows) != 1 {
		t.Fatalf("files = %d, want 1 (symlink root must not scan empty): %v", len(rows), rows)
	}
	var stored string
	if err := db.QueryRow(`SELECT path FROM files`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	rel, rerr := filepath.Rel(alias, stored)
	if rerr != nil || rel == "." || !filepath.IsLocal(rel) {
		t.Fatalf("stored path %q is not under the alias root %q", stored, alias)
	}
}

func TestScanRenameBetweenScansPreservesFileIdentity(t *testing.T) {
	db := openScanDB(t)
	dir := t.TempDir()
	genAudio(t, filepath.Join(dir, "book.mp3"), "440", 1)
	lib := &store.Library{ID: 1, Name: "a", Type: "audiobooks", Path: dir}
	if _, err := db.AddLibraryChecked("a", "audiobooks", dir, nil); err != nil {
		t.Fatal(err)
	}
	scanOnce(t, db, lib, t.TempDir())

	var fileID int64
	var stored string
	if err := db.QueryRow(`SELECT id, path FROM files`).Scan(&fileID, &stored); err != nil {
		t.Fatal(err)
	}

	renamed := filepath.Join(dir, "book2.mp3")
	if err := os.Rename(stored, renamed); err != nil {
		t.Fatal(err)
	}
	scanOnce(t, db, lib, t.TempDir())

	var afterID int64
	var afterPath string
	var missing int
	if err := db.QueryRow(`SELECT id, path, missing FROM files`).Scan(&afterID, &afterPath, &missing); err != nil {
		t.Fatal(err)
	}
	if afterID != fileID {
		t.Fatalf("rename lost file identity: %d -> %d", fileID, afterID)
	}
	if afterPath != renamed || missing != 0 {
		t.Fatalf("relinked row = %q missing=%d, want %q missing=0", afterPath, missing, renamed)
	}
}
