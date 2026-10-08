package importer

import (
	"fmt"
	"github.com/libteca/libteca/internal/store"
	"path/filepath"
	"testing"
)

func TestReleasedImportScannerIdentityAdoptionAndRepeat(t *testing.T) {
	for _, provider := range []string{"abs", "kavita"} {
		t.Run(provider, func(t *testing.T) {
			db := openStore(t)
			source := ""
			var run func() error
			if provider == "abs" {
				source = buildABSReleasedFixture(t)
				run = func() error { _, err := ABS(source, db, false); return err }
			} else {
				source = buildKavitaEnumFixture(t)
				run = func() error { _, err := Kavita(source, db, false); return err }
			}
			if err := run(); err != nil {
				t.Fatal(err)
			}
			rows, err := db.Query(`SELECT e.id,e.work_id,e.source_library_id,e.format,l.path FROM editions e JOIN libraries l ON l.id=e.source_library_id ORDER BY e.id`)
			if err != nil {
				t.Fatal(err)
			}
			type identity struct {
				id, work, library int64
				format, root      string
			}
			var identities []identity
			for rows.Next() {
				var item identity
				if err := rows.Scan(&item.id, &item.work, &item.library, &item.format, &item.root); err != nil {
					t.Fatal(err)
				}
				identities = append(identities, item)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if len(identities) == 0 {
				t.Fatal("empty import")
			}
			for _, item := range identities {
				view, err := db.EditionByID(item.id)
				if err != nil {
					t.Fatal(err)
				}
				files := view.Files
				var paths, digests []string
				var ids []int64
				for _, file := range files {
					paths = append(paths, file.Path)
					if file.SHA256 == nil || len(*file.SHA256) != 64 {
						t.Fatal("missing proof")
					}
					digests = append(digests, *file.SHA256)
					ids = append(ids, file.ID)
				}
				keyPath := paths[0]
				if provider == "abs" {
					keyPath = filepath.Dir(keyPath)
				}
				key, err := store.SourceKey(item.root, keyPath)
				if err != nil {
					t.Fatal(err)
				}
				scanner := &store.Edition{WorkID: item.work, Format: item.format, Title: "retagged", SourceLibraryID: item.library, SourceKey: key, SourcePaths: paths, SourceDigests: digests}
				for i := 0; i < 2; i++ {
					eid, err := db.UpsertEdition(scanner)
					if err != nil || eid != item.id {
						t.Fatal("scanner identity", eid, err)
					}
				}
				if _, err := db.Exec(`UPDATE progress SET edition_position_secs=17.25,revision=revision+1 WHERE edition_id=?`, item.id); err != nil {
					t.Fatal(err)
				}
				var progressRows int
				if err := db.QueryRow(`SELECT count(*) FROM progress WHERE edition_id=?`, item.id).Scan(&progressRows); err != nil {
					t.Fatal(err)
				}
				revBefore := 0
				if progressRows > 0 {
					if err := db.QueryRow(`SELECT min(revision) FROM progress WHERE edition_id=?`, item.id).Scan(&revBefore); err != nil {
						t.Fatal(err)
					}
				}
				if err := run(); err != nil {
					t.Fatal(err)
				}
				afterView, err := db.EditionByID(item.id)
				if err != nil {
					t.Fatal(err)
				}
				after := afterView.Files
				if len(after) != len(files) {
					t.Fatal("membership changed")
				}
				for i := range after {
					if after[i].ID != ids[i] {
						t.Fatal("file identity changed")
					}
				}
				var aliases int
				if err := db.QueryRow(`SELECT count(*) FROM edition_source_aliases WHERE edition_id=?`, item.id).Scan(&aliases); err != nil || aliases != 2 {
					t.Fatal(fmt.Sprint(item.id), aliases, err)
				}
				var count int
				if progressRows > 0 {
					if err := db.QueryRow(`SELECT count(*) FROM progress WHERE edition_id=? AND (edition_position_secs=17.25 OR revision<>?+1)`, item.id, revBefore).Scan(&count); err != nil || count != 0 {
						t.Fatal("repeat import did not republish source progress", count, err)
					}
				}
			}
		})
	}
}
