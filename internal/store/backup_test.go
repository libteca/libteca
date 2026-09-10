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

	cop, err := store.Open(path)
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

	got, err := os.ReadFile(filepath.Join(backups, "covers", "7.jpg"))
	if err != nil || string(got) != "cover" {
		t.Fatalf("copied cover = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(backups, "covers", "thumb")); !os.IsNotExist(err) {
		t.Fatal("thumb cache must not be copied")
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
	if _, err := db.Snapshot(filepath.Join(t.TempDir(), "no-covers"), backups, 2); err != nil {
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
	if len(dbs) != 2 {
		t.Fatalf("backups left = %v, want the 2 newest", dbs)
	}
	for _, name := range dbs {
		if name == "libteca-20200101-000000.db" || name == "libteca-20200201-000000.db" {
			t.Fatalf("old backup %s not pruned", name)
		}
	}
	if _, err := os.Stat(filepath.Join(backups, "other.db")); err != nil {
		t.Fatal("pruning touched a non-libteca file")
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
	cop, err := store.Open(path2)
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
