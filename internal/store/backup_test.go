package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func backupDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "src.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('u','x',0,0,0)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.AddLibrary("Books", "books", "/books"); err != nil {
		t.Fatalf("library: %v", err)
	}
	return db
}

func TestSnapshotOpensAsStore(t *testing.T) {
	db := backupDB(t)
	covers := filepath.Join(t.TempDir(), "covers")
	os.MkdirAll(filepath.Join(covers, "thumb"), 0o755)
	os.WriteFile(filepath.Join(covers, "7.jpg"), []byte("cover"), 0o644)
	os.WriteFile(filepath.Join(covers, "thumb", "7.jpg"), []byte("thumb"), 0o644)

	backups := filepath.Join(t.TempDir(), "backups")
	path, err := db.Snapshot(covers, backups, 10)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if strings.HasSuffix(path, ".db") || filepath.Base(path)[0] == '.' {
		t.Fatalf("published generation path = %s, want a gen-* directory", path)
	}

	cop, err := store.Open(filepath.Join(path, "snapshot.db"))
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer cop.Close()
	var users, libs int
	cop.QueryRow(`SELECT count(*) FROM users`).Scan(&users)
	cop.QueryRow(`SELECT count(*) FROM libraries`).Scan(&libs)
	if users != 1 || libs != 1 {
		t.Fatalf("backup contents: users=%d libs=%d", users, libs)
	}

	got, err := os.ReadFile(filepath.Join(path, "covers", "7.jpg"))
	if err != nil || string(got) != "cover" {
		t.Fatalf("copied cover = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(path, "covers", "thumb")); !os.IsNotExist(err) {
		t.Fatal("thumb cache must not be copied")
	}
}

func TestSnapshotGenerationsIsolateCovers(t *testing.T) {
	db := backupDB(t)
	covers := filepath.Join(t.TempDir(), "covers")
	os.MkdirAll(covers, 0o755)
	os.WriteFile(filepath.Join(covers, "7.jpg"), []byte("v1"), 0o644)
	backups := filepath.Join(t.TempDir(), "backups")
	first, err := db.Snapshot(covers, backups, 10)
	if err != nil {
		t.Fatalf("Snapshot 1: %v", err)
	}
	os.WriteFile(filepath.Join(covers, "7.jpg"), []byte("v2"), 0o644)
	os.WriteFile(filepath.Join(covers, "9.jpg"), []byte("new"), 0o644)
	second, err := db.Snapshot(covers, backups, 10)
	if err != nil {
		t.Fatalf("Snapshot 2: %v", err)
	}
	if first == second {
		t.Fatalf("two snapshots share one generation: %s", first)
	}
	got, err := os.ReadFile(filepath.Join(first, "covers", "7.jpg"))
	if err != nil || string(got) != "v1" {
		t.Fatalf("first generation cover = %q, %v (a later snapshot must never touch it)", got, err)
	}
	if _, err := os.Stat(filepath.Join(first, "covers", "9.jpg")); !os.IsNotExist(err) {
		t.Fatal("first generation sees a cover that did not exist when it was taken")
	}
	got, err = os.ReadFile(filepath.Join(second, "covers", "7.jpg"))
	if err != nil || string(got) != "v2" {
		t.Fatalf("second generation cover = %q, %v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(second, "covers", "9.jpg"))
	if err != nil || string(got) != "new" {
		t.Fatalf("second generation new cover = %q, %v", got, err)
	}
}

func TestSnapshotLegacyBackupsKeepWorking(t *testing.T) {
	db := backupDB(t)
	backups := t.TempDir()
	legacyCovers := filepath.Join(backups, "covers")
	os.MkdirAll(legacyCovers, 0o755)
	os.WriteFile(filepath.Join(legacyCovers, "7.jpg"), []byte("legacy"), 0o644)
	for _, name := range []string{"libteca-20200101-000000.db", "libteca-20200201-000000.db"} {
		if err := os.WriteFile(filepath.Join(backups, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Snapshot(filepath.Join(t.TempDir(), "no-covers"), backups, 10); err != nil {
		t.Fatalf("Snapshot over legacy layout: %v", err)
	}
	for _, name := range []string{"libteca-20200101-000000.db", "libteca-20200201-000000.db"} {
		if _, err := os.Stat(filepath.Join(backups, name)); err != nil {
			t.Fatalf("legacy backup %s disturbed by a new snapshot: %v", name, err)
		}
	}
	got, err := os.ReadFile(filepath.Join(legacyCovers, "7.jpg"))
	if err != nil || string(got) != "legacy" {
		t.Fatalf("legacy shared covers disturbed = %q, %v", got, err)
	}
}

func TestSnapshotPruneRemovesSharedCoversAfterLastLegacy(t *testing.T) {
	db := backupDB(t)
	backups := t.TempDir()
	legacyCovers := filepath.Join(backups, "covers")
	os.MkdirAll(legacyCovers, 0o755)
	os.WriteFile(filepath.Join(legacyCovers, "7.jpg"), []byte("legacy"), 0o644)
	if err := os.WriteFile(filepath.Join(backups, "libteca-20200101-000000.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Snapshot(filepath.Join(t.TempDir(), "no-covers"), backups, 2); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backups, "libteca-20200101-000000.db")); err != nil {
		t.Fatalf("legacy backup pruned inside the keep budget: %v", err)
	}
	if _, err := os.Stat(legacyCovers); err != nil {
		t.Fatalf("shared covers removed while a legacy backup remains: %v", err)
	}
	if _, err := db.Snapshot(filepath.Join(t.TempDir(), "no-covers"), backups, 1); err != nil {
		t.Fatalf("Snapshot 2: %v", err)
	}
	if _, err := os.Stat(filepath.Join(backups, "libteca-20200101-000000.db")); !os.IsNotExist(err) {
		t.Fatal("legacy backup outside the keep budget was kept")
	}
	if _, err := os.Stat(legacyCovers); !os.IsNotExist(err) {
		t.Fatal("shared covers outlived the last legacy backup")
	}
}

func TestSnapshotPrunesOldBackups(t *testing.T) {
	db := backupDB(t)
	backups := t.TempDir()
	for _, name := range []string{
		"libteca-20200101-000000.db", "libteca-20200201-000000.db",
		"libteca-20200301-000000.db", "other.db",
	} {
		if err := os.WriteFile(filepath.Join(backups, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path, err := db.Snapshot(filepath.Join(t.TempDir(), "no-covers"), backups, 2)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}
	var dbs []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, "libteca-") && strings.HasSuffix(n, ".db") {
			dbs = append(dbs, n)
		}
	}
	if len(dbs) != 1 || dbs[0] != "libteca-20200301-000000.db" {
		t.Fatalf("legacy backups left = %v, want only the newest alongside the new generation", dbs)
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		t.Fatalf("new generation missing after pruning %s: %v", path, err)
	}
	if _, err := os.Stat(filepath.Join(backups, "other.db")); err != nil {
		t.Fatal("pruning touched a non-libteca file")
	}
}

func TestSnapshotKeepBelowOnePrunesNothing(t *testing.T) {
	db := backupDB(t)
	backups := t.TempDir()
	for _, name := range []string{"libteca-20200101-000000.db", "libteca-20200201-000000.db"} {
		if err := os.WriteFile(filepath.Join(backups, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path, err := db.Snapshot(filepath.Join(t.TempDir(), "no-covers"), backups, 0)
	if err != nil {
		t.Fatalf("Snapshot keep=0: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "snapshot.db")); err != nil {
		t.Fatalf("fresh backup deleted at keep=0: %v", err)
	}
	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}
	var dbs int
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "libteca-") && strings.HasSuffix(e.Name(), ".db") {
			dbs++
		}
		if e.IsDir() && strings.HasPrefix(e.Name(), "gen-") {
			dbs++
		}
	}
	if dbs != 3 {
		t.Fatalf("keep=0 pruned backups: %d left, want all 3", dbs)
	}
}

func TestSnapshotNeverPrunesJustWritten(t *testing.T) {
	db := backupDB(t)
	backups := t.TempDir()
	// clock-skewed name sorts AFTER the fresh snapshot: with keep=1 the
	// just-written generation would land in the prune range without protection
	if err := os.WriteFile(filepath.Join(backups, "libteca-29991231-235959.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := db.Snapshot(filepath.Join(t.TempDir(), "no-covers"), backups, 1)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "snapshot.db")); err != nil {
		t.Fatalf("just-written backup pruned: %v", err)
	}
}

func TestSnapshotCapturesWalWrites(t *testing.T) {
	db := backupDB(t)
	dir := t.TempDir()
	if _, err := db.Snapshot(filepath.Join(t.TempDir(), "no-covers"), dir, 5); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('late','x',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	path2, err := db.Snapshot(filepath.Join(t.TempDir(), "no-covers"), dir, 5)
	if err != nil {
		t.Fatalf("Snapshot 2: %v", err)
	}
	cop, err := store.Open(filepath.Join(path2, "snapshot.db"))
	if err != nil {
		t.Fatalf("open second backup: %v", err)
	}
	defer cop.Close()
	var n int
	cop.QueryRow(`SELECT count(*) FROM users WHERE name='late'`).Scan(&n)
	if n != 1 {
		t.Fatalf("post-backup write missing from snapshot: %d", n)
	}
}

func TestSnapshotSameSecondNeverOverwrites(t *testing.T) {
	db := backupDB(t)
	backups := t.TempDir()
	first, err := db.Snapshot(filepath.Join(t.TempDir(), "no-covers"), backups, 10)
	if err != nil {
		t.Fatalf("Snapshot 1: %v", err)
	}
	firstBytes, err := os.ReadFile(filepath.Join(first, "snapshot.db"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.Snapshot(filepath.Join(t.TempDir(), "no-covers"), backups, 10)
	if err != nil {
		t.Fatalf("Snapshot 2 in the same second: %v", err)
	}
	if first == second {
		t.Fatalf("two snapshots in the same second share one name: %s", first)
	}
	again, err := os.ReadFile(filepath.Join(first, "snapshot.db"))
	if err != nil || len(again) == 0 || string(again) != string(firstBytes) {
		t.Fatalf("first snapshot destroyed by the second: read=%v err=%v", len(again), err)
	}
}

func TestBackupToRefusesExistingDestination(t *testing.T) {
	db := backupDB(t)
	dir := t.TempDir()
	dest := filepath.Join(dir, "existing.db")
	if err := os.WriteFile(dest, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.BackupTo(dest); err == nil {
		t.Fatal("BackupTo must refuse an existing destination")
	}
	got, err := os.ReadFile(dest)
	if err != nil || string(got) != "precious" {
		t.Fatalf("existing destination damaged: %q %v", got, err)
	}
}

func TestOpenEscapesReservedPathCharacters(t *testing.T) {
	for _, name := range []string{"q?mark.db", "f#rag.db", "p%ct.db", "sp ace.db"} {
		path := filepath.Join(t.TempDir(), name)
		db, err := store.Open(path)
		if err != nil {
			t.Fatalf("Open(%q): %v", name, err)
		}
		if _, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('u','x',0,0,0)`); err != nil {
			t.Fatalf("write(%q): %v", name, err)
		}
		var mode string
		if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
			t.Fatalf("journal_mode(%q) = %q err=%v, want wal (pragmas must survive the DSN encoding)", name, mode, err)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("database not created at the requested path %q: %v", path, err)
		}
		db.Close()
	}
}
