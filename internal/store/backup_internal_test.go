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

func TestPruneCountsGenerationsAndLegacyTogether(t *testing.T) {
	dir := t.TempDir()
	writeBackupNames(t, dir, "libteca-20200101-000000.db")
	if err := os.MkdirAll(filepath.Join(dir, "covers"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "covers", "7.jpg"), []byte("legacy"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gen-20260101-000000.000000001-a", "gen-20260201-000000.000000001-b"} {
		if err := os.MkdirAll(filepath.Join(dir, name, "covers"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name, "snapshot.db"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	protect := filepath.Join(dir, "gen-20260201-000000.000000001-b")
	if err := pruneBackups(dir, 2, protect); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "libteca-20200101-000000.db")); !os.IsNotExist(err) {
		t.Fatal("oldest legacy backup not counted against the generation budget")
	}
	if _, err := os.Stat(filepath.Join(dir, "covers")); !os.IsNotExist(err) {
		t.Fatal("shared covers outlived the last legacy backup")
	}
	if _, err := os.Stat(filepath.Join(dir, "gen-20260101-000000.000000001-a", "snapshot.db")); err != nil {
		t.Fatalf("generation inside the keep budget damaged: %v", err)
	}
}

func TestPruneRemovesGenerationWhole(t *testing.T) {
	dir := t.TempDir()
	writeBackupNames(t, dir, "libteca-20200101-000000.db")
	gen := "gen-20260101-000000.000000001-a"
	if err := os.MkdirAll(filepath.Join(dir, gen, "covers"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, gen, "covers", "7.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	protect := filepath.Join(dir, "libteca-20200101-000000.db")
	if err := pruneBackups(dir, 1, protect); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, gen)); !os.IsNotExist(err) {
		t.Fatal("pruned generation directory left behind")
	}
}
