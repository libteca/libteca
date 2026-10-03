package importer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func TestApplyFilesRollsBackFailedBatch(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "new"
		if existing {
			name = "existing"
		}
		t.Run(name, func(t *testing.T) {
			db := openStore(t)
			root := t.TempDir()
			path := filepath.Join(root, "1.mp3")
			if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
				t.Fatal(err)
			}
			libID, err := db.AddLibrary("Books", "audiobooks", root)
			if err != nil {
				t.Fatal(err)
			}
			wid, err := db.UpsertWork(&store.Work{LibraryID: libID, Title: "Book"})
			if err != nil {
				t.Fatal(err)
			}
			eid, err := db.UpsertEdition(&store.Edition{WorkID: wid, Format: "mp3", Title: "Book"})
			if err != nil {
				t.Fatal(err)
			}
			if existing {
				if _, err := applyFiles(db, eid, []fileSpec{{Path: path, Duration: 5}}); err != nil {
					t.Fatal(err)
				}
			}
			files := []fileSpec{{Path: path, Duration: 10}, {Path: filepath.Join(root, "gone.mp3"), Duration: 20}}
			if _, err := applyFiles(db, eid, files); err == nil {
				t.Fatal("vanished planned file must fail the batch")
			}
			var duration float64
			if err := db.QueryRow(`SELECT coalesce(sum(duration_secs), 0) FROM files WHERE edition_id = ?`, eid).Scan(&duration); err != nil {
				t.Fatal(err)
			}
			want := 0.0
			if existing {
				want = 5
			}
			if duration != want {
				t.Fatalf("failed import batch left duration %v, want %v", duration, want)
			}
		})
	}
}
