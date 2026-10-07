package scan

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/store"
)

// C-01: the full-ROM sha256 read must happen BEFORE storeGame opens its
// BEGIN IMMEDIATE transaction. While a multi-GB hash is in flight, an
// unrelated writer (settings write, another library's scan transaction)
// must succeed; on the old in-transaction shape the second writer queued
// behind the hash and failed with SQLITE_BUSY after the 5s busy_timeout.
func TestScanGamesHashRunsOutsideWriteTx(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	bigRoot, smallRoot := t.TempDir(), t.TempDir()
	bigRom := filepath.Join(bigRoot, "Big Game (USA).iso")
	if err := os.WriteFile(bigRom, []byte("big"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"A.gba", "B.gba"} {
		if err := os.WriteFile(filepath.Join(smallRoot, name), []byte("small"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bigLibID, err := db.AddLibrary("Big", "games", bigRoot)
	if err != nil {
		t.Fatal(err)
	}
	smallLibID, err := db.AddLibrary("Small", "games", smallRoot)
	if err != nil {
		t.Fatal(err)
	}
	bigLib, err := db.Library(bigLibID)
	if err != nil {
		t.Fatal(err)
	}
	smallLib, err := db.Library(smallLibID)
	if err != nil {
		t.Fatal(err)
	}
	covers := filepath.Join(t.TempDir(), "covers")
	if err := os.MkdirAll(covers, 0o755); err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	orig := sha256File
	sha256File = func(path string) string {
		if path == bigRom {
			select {
			case entered <- struct{}{}:
			default:
			}
			<-release
			return ""
		}
		return orig(path)
	}
	defer func() { sha256File = orig }()

	bigDone := make(chan error, 1)
	go func() {
		_, err := Library(context.Background(), db, bigLib, covers, nil)
		bigDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("hash seam never entered")
	}

	settingDone := make(chan error, 1)
	go func() {
		settingDone <- db.SetSetting("lock.probe", "v")
	}()
	select {
	case err := <-settingDone:
		if err != nil {
			t.Fatalf("concurrent write blocked by games hash: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent write held up longer than 2s: write lock held during hashing")
	}

	smallDone := make(chan error, 1)
	go func() {
		_, err := Library(context.Background(), db, smallLib, covers, nil)
		smallDone <- err
	}()
	select {
	case err := <-smallDone:
		if err != nil {
			t.Fatalf("concurrent library scan failed: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second library scan blocked on games hash: write lock held during hashing")
	}

	close(release)
	if err := <-bigDone; err != nil {
		t.Fatal(err)
	}
}

// C-02: a changed file whose re-hash fails must lose its OLD sha256 (NULL,
// retryable), never keep the hash of bytes that no longer exist; the next
// scan must retry because warm-skip refuses NULL-sha256 rows, and the retry
// persists the hash of the new bytes.
func TestScanGamesFailedRehashClearsStaleSHA256(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	root := t.TempDir()
	rom := filepath.Join(root, "Mario Kart (USA).gba")
	if err := os.WriteFile(rom, []byte("mk"), 0o644); err != nil {
		t.Fatal(err)
	}
	covers := filepath.Join(t.TempDir(), "covers")
	if err := os.MkdirAll(covers, 0o755); err != nil {
		t.Fatal(err)
	}
	libID, err := db.AddLibrary("Games", "games", root)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := db.Library(libID)
	if err != nil {
		t.Fatal(err)
	}

	rowSHA := func() sql.NullString {
		var v sql.NullString
		if err := db.QueryRow(`SELECT sha256 FROM files WHERE path = ?`, rom).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}

	if _, err := Library(context.Background(), db, lib, covers, nil); err != nil {
		t.Fatal(err)
	}
	wantOld := fmt.Sprintf("%x", sha256.Sum256([]byte("mk")))
	if v := rowSHA(); !v.Valid || v.String != wantOld {
		t.Fatalf("initial sha256 = %v, want %s", v, wantOld)
	}

	newTime := time.Now().Add(2 * time.Second)
	if err := os.WriteFile(rom, []byte("mk2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(rom, newTime, newTime); err != nil {
		t.Fatal(err)
	}

	orig := sha256File
	sha256File = func(path string) string { return "" }
	if _, err := Library(context.Background(), db, lib, covers, nil); err != nil {
		t.Fatal(err)
	}
	if v := rowSHA(); v.Valid {
		t.Fatalf("failed re-hash kept stale sha256 %q, want NULL", v.String)
	}
	var newSize int64
	var newMtime int64
	if err := db.QueryRow(`SELECT size_bytes, mtime_secs FROM files WHERE path = ?`, rom).Scan(&newSize, &newMtime); err != nil {
		t.Fatal(err)
	}
	if newSize != int64(len("mk2")) || newMtime != newTime.Unix() {
		t.Fatalf("row stat = (%d, %d), want the rewritten file's (%d, %d)", newSize, newMtime, len("mk2"), newTime.Unix())
	}

	sha256File = orig
	if n, err := Library(context.Background(), db, lib, covers, nil); err != nil || n != 1 {
		t.Fatalf("retry scan = (%d, %v), want the NULL-sha256 row reprocessed", n, err)
	}
	wantNew := fmt.Sprintf("%x", sha256.Sum256([]byte("mk2")))
	if v := rowSHA(); !v.Valid || v.String != wantNew {
		t.Fatalf("retried sha256 = %v, want %s", v, wantNew)
	}

	if n, err := Library(context.Background(), db, lib, covers, nil); err != nil || n != 0 {
		t.Fatalf("warm rescan = (%d, %v), want 0", n, err)
	}
}
