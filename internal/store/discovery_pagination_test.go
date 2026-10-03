package store_test

import (
	"fmt"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func TestWorksInLibraryFilteredStableTies(t *testing.T) {
	for _, sort := range []string{"title", "author", "added", "updated"} {
		for _, dir := range []string{"asc", "desc"} {
			t.Run(sort+"_"+dir, func(t *testing.T) {
				db := openTestDB(t)
				lib, err := db.AddLibrary("Books", "books", t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				var ids []int64
				for i := 0; i < 7; i++ {
					title, author := fmt.Sprintf("Title %d", 7-i), "Author"
					if sort == "title" {
						title, author = "Title", fmt.Sprintf("Author %d", 7-i)
					}
					ids = append(ids, addWork(t, db, lib, title, &author, 10, 20))
				}
				for offset := 0; offset < len(ids); offset += 3 {
					page, err := db.WorksInLibraryFiltered(lib, 0, sort, dir, "all", 3, offset)
					if err != nil {
						t.Fatal(err)
					}
					wantCount := min(3, len(ids)-offset)
					if len(page) != wantCount {
						t.Fatalf("offset %d returned %d, want %d", offset, len(page), wantCount)
					}
					for i, work := range page {
						want := ids[offset+i]
						if sort == "added" || sort == "updated" {
							want = ids[len(ids)-1-offset-i]
						}
						if work.ID != want {
							t.Fatalf("offset %d item %d = %d, want %d", offset, i, work.ID, want)
						}
					}
				}
			})
		}
	}
}

func TestWorksInLibraryFilteredLoadsOnlySelectedChildren(t *testing.T) {
	db := openTestDB(t)
	user := addUser(t, db)
	lib, err := db.AddLibrary("Large", "audiobooks", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Update(func(tx *store.Tx) error {
		if _, err := tx.Exec(`WITH RECURSIVE n(id) AS (VALUES(1) UNION ALL SELECT id + 1 FROM n WHERE id < 10000)
			INSERT INTO works (id, library_id, title, created_at, updated_at)
			SELECT id, ?, printf('Work %05d', id), id, id FROM n`, lib); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO editions (id, work_id, format, title, duration_secs, created_at)
			SELECT id, id, 'm4b', title, 100, 0 FROM works WHERE library_id = ?`, lib); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO files (id, edition_id, path, seq, size_bytes, mtime_secs, duration_secs, probed_at)
			SELECT id, id, '/library/' || id || '.m4b', 1, 1, 0, 100, 0 FROM editions`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO progress (user_id, edition_id, edition_position_secs, updated_at)
			SELECT ?, id, 50, id FROM editions`, user)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	checkPage := func(t *testing.T) {
		t.Helper()
		page, err := db.WorksInLibraryFiltered(lib, user, "title", "asc", "all", 200, 4800)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != 200 {
			t.Fatalf("page length = %d, want 200", len(page))
		}
		for i, work := range page {
			want := int64(4801 + i)
			if work.ID != want || len(work.Editions) != 1 || work.Editions[0].ID != want {
				t.Fatalf("page work %d = %+v", i, work)
			}
			ed := work.Editions[0]
			if len(ed.Files) != 1 || ed.Files[0].ID != want || ed.TotalDuration() != 100 {
				t.Fatalf("work %d files = %+v", want, ed)
			}
			if work.Percent == nil || *work.Percent != 0.5 {
				t.Fatalf("work %d percent = %v", want, work.Percent)
			}
		}
	}
	checkPage(t)
	for _, tc := range []struct {
		name, corrupt, restore string
	}{
		{"editions", `UPDATE editions SET duration_secs = 'invalid' WHERE id = 10000`, `UPDATE editions SET duration_secs = 100 WHERE id = 10000`},
		{"files", `UPDATE files SET duration_secs = 'invalid' WHERE id = 10000`, `UPDATE files SET duration_secs = 100 WHERE id = 10000`},
		{"progress", `UPDATE progress SET edition_position_secs = 'invalid' WHERE edition_id = 10000`, `UPDATE progress SET edition_position_secs = 50 WHERE edition_id = 10000`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec(tc.corrupt); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := db.Exec(tc.restore); err != nil {
					t.Error(err)
				}
			}()
			checkPage(t)
		})
	}
	all, err := db.WorksInLibraryFiltered(lib, user, "title", "asc", "all", 0, 0)
	if err != nil || len(all) != 10000 {
		t.Fatalf("unlimited query returned %d works, %v", len(all), err)
	}
}
