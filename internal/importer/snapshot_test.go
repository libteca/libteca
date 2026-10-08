package importer

import (
	"database/sql"
	"github.com/libteca/libteca/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestForeignDiscoveryUsesOneSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.db")
	writer, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, stmt := range []string{`PRAGMA journal_mode=WAL`, `CREATE TABLE fixture(value INTEGER)`, `INSERT INTO fixture VALUES(1)`} {
		if _, err := writer.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := openForeign(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := writer.Exec(`UPDATE fixture SET value=2`); err != nil {
		t.Fatal(err)
	}
	var got int
	if err := reader.QueryRow(`SELECT value FROM fixture`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatalf("mixed discovery snapshot: value=%d", got)
	}
	if _, err := reader.Exec(`UPDATE fixture SET value=3`); err == nil {
		t.Fatal("source handle allowed writes")
	}
}

func TestPlannedMediaReplacementRollsBack(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "media.mp3")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	files := []fileSpec{{Path: path, Duration: 1}}
	if err := snapshotFiles(files); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(root, "replacement")
	if err := os.WriteFile(replacement, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(root, "destination.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lib, _ := db.AddLibrary("audio", "audiobooks", root)
	work, _ := db.UpsertWork(&store.Work{LibraryID: lib, Title: "media"})
	edition, _ := db.UpsertEdition(&store.Edition{WorkID: work, Format: "mp3", Title: "media"})
	if _, err := applyFiles(db, edition, files); err == nil {
		t.Fatal("replaced planned input accepted")
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM files`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
