package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func TestScanGamesUpdateKeepsSeq(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	first := filepath.Join(root, "Mario Kart (USA).gba")
	second := filepath.Join(root, "Mario Kart (Europe).gba")
	if err := os.WriteFile(first, []byte("mk"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte("zel"), 0o644); err != nil {
		t.Fatal(err)
	}
	covers := filepath.Join(t.TempDir(), "covers")
	os.MkdirAll(covers, 0o755)
	db.AddLibrary("Games", "games", root)
	lib := &store.Library{ID: 1, Type: "games", Path: root}
	if _, err := Library(context.Background(), db, lib, covers, nil); err != nil {
		t.Fatal(err)
	}
	seqs := map[string]int{}
	rows, err := db.Query(`SELECT path, seq FROM files`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p string
		var s int
		rows.Scan(&p, &s)
		seqs[p] = s
	}
	rows.Close()
	if seqs[first] == seqs[second] || seqs[first] < 1 || seqs[first] > 2 || seqs[second] < 1 || seqs[second] > 2 {
		t.Fatalf("initial seqs = %v, want distinct 1..2", seqs)
	}
	if err := os.WriteFile(first, []byte("mk2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Library(context.Background(), db, lib, covers, nil); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query(`SELECT path, seq FROM files`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p string
		var s int
		rows.Scan(&p, &s)
		if s != seqs[p] {
			t.Fatalf("updated %s moved from seq %d to %d", p, seqs[p], s)
		}
	}
	rows.Close()
}

func TestScanRejectsNonRegularEntries(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.nes")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link.nes")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := os.WriteFile(filepath.Join(root, "real.nes"), []byte("rom"), 0o644); err != nil {
		t.Fatal(err)
	}
	covers := filepath.Join(t.TempDir(), "covers")
	os.MkdirAll(covers, 0o755)
	db.AddLibrary("Games", "games", root)
	lib := &store.Library{ID: 1, Type: "games", Path: root}
	n, err := Library(context.Background(), db, lib, covers, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("scanned = %d, want only the regular file", n)
	}
	rows, err := db.Query(`SELECT count(*) FROM files WHERE path LIKE '%link.nes'`)
	if err != nil {
		t.Fatal(err)
	}
	var links int
	rows.Scan(&links)
	rows.Close()
	if links != 0 {
		t.Fatal("symlinked ROM was registered")
	}
}

func TestScanFailsOnUnavailableRoot(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lib := &store.Library{ID: 1, Type: "games", Path: filepath.Join(t.TempDir(), "does-not-exist")}
	covers := filepath.Join(t.TempDir(), "covers")
	if _, err := Library(context.Background(), db, lib, covers, nil); err == nil {
		t.Fatal("scan of an unavailable root must fail, not report an empty success")
	}
}

func TestScanWalkErrorFailsScan(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission-based walk failure is invisible to root")
	}
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "locked"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "locked", "a.nes"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	covers := filepath.Join(t.TempDir(), "covers")
	db.AddLibrary("Games", "games", root)
	lib := &store.Library{ID: 1, Type: "games", Path: root}
	if err := os.Chmod(filepath.Join(root, "locked"), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(root, "locked"), 0o755) })
	if _, err := Library(context.Background(), db, lib, covers, nil); err == nil {
		t.Fatal("traversal errors must fail the scan")
	}
}
