package store_test

import (
	"fmt"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func TestDiscoveryIgnoresResetProgress(t *testing.T) {
	db := openTestDB(t)
	user := addUser(t, db)
	other := addUser(t, db)
	lib, err := db.AddLibrary("Books", "books", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	reset := addWork(t, db, lib, "Novel reset", nil, 1, 1)
	mixed := addWork(t, db, lib, "Novel mixed", nil, 1, 1)
	finished := addWork(t, db, lib, "Novel finished", nil, 1, 1)
	resetEdition := addEdition(t, db, reset, "Reset", fltp(100))
	liveEdition := addEdition(t, db, mixed, "Live", fltp(100))
	shadowEdition := addEdition(t, db, mixed, "Reset sibling", fltp(100))
	finishedEdition := addEdition(t, db, finished, "Finished", fltp(100))
	addProgress(t, db, user, resetEdition, 90, fltp(100), true, 10)
	addProgress(t, db, user, liveEdition, 40, fltp(100), false, 20)
	addProgress(t, db, user, shadowEdition, 80, fltp(100), false, 30)
	addProgress(t, db, user, finishedEdition, 100, fltp(100), true, 40)
	addProgress(t, db, other, resetEdition, 75, fltp(100), false, 50)
	for _, id := range []int64{resetEdition, shadowEdition} {
		before, err := db.GetReadingProgress(user, id)
		if err != nil {
			t.Fatal(err)
		}
		if err := db.DeleteProgress(user, id); err != nil {
			t.Fatal(err)
		}
		after, err := db.GetReadingProgress(user, id)
		if err != nil || !after.Deleted || after.Revision != before.Revision+1 {
			t.Fatalf("reset baseline = %+v, %v", after, err)
		}
	}
	items, err := db.ResumeItems(user, 20)
	if err != nil || len(items) != 1 || items[0].EditionID != liveEdition || items[0].Percent != 0.4 {
		t.Fatalf("resume = %+v, %v", items, err)
	}
	for _, tc := range []struct {
		filter string
		want   int64
	}{
		{"in_progress", mixed},
		{"unplayed", reset},
		{"finished", finished},
	} {
		views, err := db.WorksInLibraryFiltered(lib, user, "title", "asc", tc.filter, 20, 0)
		if err != nil || len(views) != 1 || views[0].ID != tc.want {
			t.Fatalf("%s = %+v, %v", tc.filter, views, err)
		}
	}
	views, err := db.WorksInLibraryFiltered(lib, user, "title", "asc", "all", 20, 0)
	if err != nil || len(views) != 3 {
		t.Fatalf("works = %+v, %v", views, err)
	}
	for _, v := range views {
		if v.ID == mixed {
			if v.Percent == nil || *v.Percent != 0.4 {
				t.Fatalf("live sibling percent = %v", v.Percent)
			}
		} else if v.Percent != nil {
			t.Fatalf("work %d percent = %v, want nil", v.ID, v.Percent)
		}
	}
	hits, err := db.SearchWorks(user, "Novel", 20)
	if err != nil || len(hits) != 3 {
		t.Fatalf("search = %+v, %v", hits, err)
	}
	for _, hit := range hits {
		if hit.WorkID == reset && hit.Percent != nil {
			t.Fatalf("reset search percent = %v", hit.Percent)
		}
		if hit.WorkID == mixed && (hit.Percent == nil || *hit.Percent != 0.4) {
			t.Fatalf("sibling search percent = %v", hit.Percent)
		}
	}
	items, err = db.ResumeItems(other, 20)
	if err != nil || len(items) != 1 || items[0].EditionID != resetEdition || items[0].Percent != 0.75 {
		t.Fatalf("other user's resume changed = %+v, %v", items, err)
	}
}

func TestNextUpIgnoresResetProgress(t *testing.T) {
	db := openTestDB(t)
	user := addUser(t, db)
	lib, err := db.AddLibrary("TV", "tv", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	series := addWork(t, db, lib, "Series", nil, 0, 0)
	var editions []int64
	for episode := 1; episode <= 3; episode++ {
		id := addEdition(t, db, series, fmt.Sprintf("Episode %d", episode), fltp(100))
		if _, err := db.Exec(`UPDATE editions SET season_num = 1, episode_num = ? WHERE id = ?`, episode, id); err != nil {
			t.Fatal(err)
		}
		editions = append(editions, id)
	}
	addProgress(t, db, user, editions[0], 100, fltp(100), true, 10)
	addProgress(t, db, user, editions[2], 40, fltp(100), false, 20)
	if err := db.DeleteProgress(user, editions[2]); err != nil {
		t.Fatal(err)
	}
	items, err := db.NextUp(user, 0, false)
	if err != nil || len(items) != 1 || items[0].EditionID != editions[1] || items[0].LastUpdate != 10 {
		t.Fatalf("next after reset = %+v, %v", items, err)
	}
	if err := db.DeleteProgress(user, editions[0]); err != nil {
		t.Fatal(err)
	}
	for _, seriesID := range []int64{0, series} {
		items, err = db.NextUp(user, seriesID, false)
		if err != nil || len(items) != 0 {
			t.Fatalf("reset-only started series %d = %+v, %v", seriesID, items, err)
		}
		items, err = db.NextUp(user, seriesID, true)
		if err != nil || len(items) != 1 || items[0].EditionID != editions[0] || items[0].LastUpdate != 0 {
			t.Fatalf("reset-only unstarted series %d = %+v, %v", seriesID, items, err)
		}
	}
}

func TestMusicDiscoveryIgnoresResetProgress(t *testing.T) {
	db := openTestDB(t)
	user := addUser(t, db)
	lib, err := db.AddLibrary("Music", "music", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	album := addWork(t, db, lib, "Album", nil, 0, 0)
	track := addEdition(t, db, album, "Track", fltp(100))
	addProgress(t, db, user, track, 40, fltp(100), false, 10)
	for _, query := range []func(int64, int, int) ([]store.MusicAlbum, error){db.MusicAlbumsRecent, db.MusicAlbumsFrequent} {
		items, err := query(user, 20, 0)
		if err != nil || len(items) != 1 || items[0].ID != album {
			t.Fatalf("live music discovery = %+v, %v", items, err)
		}
	}
	if err := db.DeleteProgress(user, track); err != nil {
		t.Fatal(err)
	}
	for _, query := range []func(int64, int, int) ([]store.MusicAlbum, error){db.MusicAlbumsRecent, db.MusicAlbumsFrequent} {
		items, err := query(user, 20, 0)
		if err != nil || len(items) != 0 {
			t.Fatalf("reset music discovery = %+v, %v", items, err)
		}
	}
}

func TestOPDSDiscoveryIgnoresResetProgress(t *testing.T) {
	db := openTestDB(t)
	user := addUser(t, db)
	lib, err := db.AddLibrary("Books", "books", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	book := addWork(t, db, lib, "Book", nil, 0, 0)
	edition := addEdition(t, db, book, "EPUB", nil)
	if _, err := db.Exec(`UPDATE editions SET format = 'epub', page_count = 100 WHERE id = ?`, edition); err != nil {
		t.Fatal(err)
	}
	addFile(t, db, edition, "/books/book.epub", 0)
	page := int64(40)
	if err := db.SetReadingProgress(&store.ReadingProgress{
		Progress: store.Progress{UserID: user, EditionID: edition},
		Page:     &page,
	}); err != nil {
		t.Fatal(err)
	}
	items, total, err := db.OPDSEditionsInProgress(user, 20, 0)
	if err != nil || total != 1 || len(items) != 1 || items[0].EditionID != edition {
		t.Fatalf("live OPDS progress = %+v, %d, %v", items, total, err)
	}
	if err := db.DeleteProgress(user, edition); err != nil {
		t.Fatal(err)
	}
	items, total, err = db.OPDSEditionsInProgress(user, 20, 0)
	if err != nil || total != 0 || len(items) != 0 {
		t.Fatalf("reset OPDS progress = %+v, %d, %v", items, total, err)
	}
}
