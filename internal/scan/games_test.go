package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func TestDetectGamePlatform(t *testing.T) {
	cases := []struct {
		ext  string
		want string
		ok   bool
	}{
		{"sfc", "snes", true},
		{"gba", "gba", true},
		{"wbfs", "wii", true},
		{"iso", "psx", true},   // shared: preference
		{"bin", "psx", true},   // shared: preference
		{"cue", "psx", true},   // shared: preference
		{"elf", "psp", true},   // shared: preference
		{"app", "n3ds", true}, // shared: preference
		{"ciso", "gamecube", true},
		{"mdf", "ps2", true},
		{"cdi", "dreamcast", true},
		{"txt", "", false},
		{"mp3", "", false},
	}
	for _, c := range cases {
		p, ok := detectGamePlatform(c.ext)
		if ok != c.ok {
			t.Fatalf("detectGamePlatform(%q) ok = %v, want %v", c.ext, ok, c.ok)
		}
		if ok && p.Tag != c.want {
			t.Fatalf("detectGamePlatform(%q) = %q, want %q", c.ext, p.Tag, c.want)
		}
	}
}

func TestCleanRomTitle(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Super Mario World (USA).sfc", "Super Mario World"},
		{"Super Mario World (USA) [!].sfc", "Super Mario World"},
		{"Zelda - A Link to the Past (Europe) (Rev A).sfc", "Zelda - A Link to the Past"},
		{"Sonic_the_Hedgehog_2_(W)_[!].gen", "Sonic the Hedgehog 2"},
		{"Super Mario World.sfc", "Super Mario World"},
		{"1686 - Castlevania (USA).nds", "1686 - Castlevania"},
	}
	for _, c := range cases {
		if got := cleanRomTitle(c.in); got != c.want {
			t.Fatalf("cleanRomTitle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestScanGamesLibrary(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "SNES"), 0o755)
	os.MkdirAll(filepath.Join(root, "GBA"), 0o755)

	// Same game on two platforms -> one work, two platform editions.
	// Two dumps on one platform -> one edition, two files.
	os.WriteFile(filepath.Join(root, "SNES", "Super Mario World (USA).sfc"), []byte("smw1"), 0o644)
	os.WriteFile(filepath.Join(root, "SNES", "Super Mario World (Europe).sfc"), []byte("smw2"), 0o644)
	os.WriteFile(filepath.Join(root, "GBA", "Super Mario World.smc"), []byte("nope"), 0o644) // .smc is SNES
	os.WriteFile(filepath.Join(root, "GBA", "Mario Kart (USA).gba"), []byte("mk"), 0o644)
	os.WriteFile(filepath.Join(root, "ignore.txt"), []byte("no"), 0o644)

	covers := filepath.Join(t.TempDir(), "covers")
	os.MkdirAll(covers, 0o755)
	lib := &store.Library{ID: 1, Type: "games", Path: root}
	db.AddLibrary("Games", "games", root)

	n, err := Library(context.Background(), db, lib, covers, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("scanned = %d, want 4 ROMs", n)
	}

	works, err := db.WorksInLibrary(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 2 {
		t.Fatalf("works = %d, want 2 (Super Mario World + Mario Kart)", len(works))
	}

	var smw *store.WorkView
	for i := range works {
		if works[i].Title == "Super Mario World" {
			smw = &works[i]
		}
	}
	if smw == nil {
		t.Fatalf("Super Mario World work missing; works = %+v", works)
	}
	if len(smw.Editions) != 1 || smw.Editions[0].Format != "game-snes" {
		// The .smc file landed on the SNES edition too: both dumps + the
		// mis-shelved file are the same platform edition of one work.
		t.Fatalf("smw editions = %+v, want 1 snes edition", smw.Editions)
	}
	if len(smw.Editions[0].Files) != 3 {
		t.Fatalf("smw snes files = %d, want 3 (two dumps + mis-shelved .smc)", len(smw.Editions[0].Files))
	}

	// Rescan with no changes is a full no-op.
	n, err = Library(context.Background(), db, lib, covers, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("rescan changed = %d, want 0", n)
	}
}
