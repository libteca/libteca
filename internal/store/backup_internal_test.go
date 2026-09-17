package store

import (
	"os"
	"path/filepath"
	"testing"
)

func writeBackupNames(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func libtecaBackups(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && len(n) >= len("libteca-") && n[:8] == "libteca-" && n[len(n)-3:] == ".db" {
			out = append(out, n)
		}
	}
	return out
}

func TestPruneProtectCountsTowardBudget(t *testing.T) {
	dir := t.TempDir()
	writeBackupNames(t, dir,
		"libteca-20200101-000000.db",
		"libteca-20200201-000000.db",
		"libteca-20200301-000000.db",
	)
	protect := filepath.Join(dir, "libteca-20200101-000000.db")
	if err := pruneBackups(dir, 1, protect); err != nil {
		t.Fatal(err)
	}
	left := libtecaBackups(t, dir)
	if len(left) != 1 || left[0] != "libteca-20200101-000000.db" {
		t.Fatalf("protected-oldest keep=1 left %v, want exactly the protected file", left)
	}
}

func TestPruneProtectMiddleBudget(t *testing.T) {
	dir := t.TempDir()
	writeBackupNames(t, dir,
		"libteca-20200101-000000.db",
		"libteca-20200201-000000.db",
		"libteca-20200301-000000.db",
		"libteca-20200401-000000.db",
	)
	protect := filepath.Join(dir, "libteca-20200201-000000.db")
	if err := pruneBackups(dir, 2, protect); err != nil {
		t.Fatal(err)
	}
	left := libtecaBackups(t, dir)
	if len(left) != 2 {
		t.Fatalf("protected-middle keep=2 left %v, want exactly 2", left)
	}
	for _, n := range left {
		if n == "libteca-20200101-000000.db" {
			t.Fatalf("oldest candidate not removed: %v", left)
		}
	}
}
