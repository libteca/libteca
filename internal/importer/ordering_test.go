package importer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func TestAudioPathsInNaturalOrder(t *testing.T) {
	dir := t.TempDir()
	names := []string{
		"book/1.mp3", "book/2.mp3", "book/10.mp3",
		"book/CD1/01.mp3", "book/CD1/02.mp3", "book/CD2/01.mp3", "book/CD10/01.mp3",
	}
	for _, n := range names {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, n)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := audioPathsIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(dir, "book", "1.mp3"),
		filepath.Join(dir, "book", "2.mp3"),
		filepath.Join(dir, "book", "10.mp3"),
		filepath.Join(dir, "book", "CD1", "01.mp3"),
		filepath.Join(dir, "book", "CD1", "02.mp3"),
		filepath.Join(dir, "book", "CD2", "01.mp3"),
		filepath.Join(dir, "book", "CD10", "01.mp3"),
	}
	if len(got) != len(want) {
		t.Fatalf("got %d paths, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("path %d = %q, want %q (full order: %v)", i, got[i], want[i], got)
		}
	}
}

func TestOpenForeignEscapesPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "odd?name#with%chars and spaces.db")
	source, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	source.Close()
	foreign, err := openForeign(path)
	if err != nil {
		t.Fatalf("openForeign rejected a valid database behind URI-significant characters: %v", err)
	}
	defer foreign.Close()
	if _, err := foreign.Exec(`CREATE TABLE probe (x)`); err == nil {
		t.Fatal("read-only connection accepted a write")
	}
}

func TestOpenForeignMissing(t *testing.T) {
	if _, err := openForeign(filepath.Join(t.TempDir(), "absent.db")); err == nil {
		t.Fatal("expected error for missing database")
	} else if !strings.Contains(err.Error(), "cannot open") {
		t.Fatalf("unexpected error: %v", err)
	}
}
