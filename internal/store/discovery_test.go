package store_test

import (
	"fmt"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

var userSeq int64

func addUser(t *testing.T, db *store.DB) int64 {
	t.Helper()
	userSeq++
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES (?, 'x', 0, 0, 0)`, fmt.Sprintf("u%d", userSeq))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func addWork(t *testing.T, db *store.DB, libID int64, title string, author *string, createdAt, updatedAt int64) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO works (library_id, title, author, created_at, updated_at) VALUES (?,?,?,?,?)`,
		libID, title, author, createdAt, updatedAt)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func addEdition(t *testing.T, db *store.DB, workID int64, title string, dur *float64) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO editions (work_id, format, title, duration_secs, created_at) VALUES (?,?,?,?,0)`,
		workID, "video", title, dur)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func addFile(t *testing.T, db *store.DB, editionID int64, path string, dur float64) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, probed_at) VALUES (?,?,1,0,0,?,0)`,
		editionID, path, dur)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func addProgress(t *testing.T, db *store.DB, userID, editionID int64, pos float64, dur *float64, fin bool, updatedAt int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO progress (user_id, edition_id, edition_position_secs, duration_secs, is_finished, updated_at) VALUES (?,?,?,?,?,?)`,
		userID, editionID, pos, dur, fin, updatedAt); err != nil {
		t.Fatal(err)
	}
}

func strp(s string) *string   { return &s }
func fltp(f float64) *float64 { return &f }

func TestResumeItems(t *testing.T) {
	db := openTestDB(t)
	user := addUser(t, db)
	other := addUser(t, db)
	libTV, _ := db.AddLibrary("tv", "tv", t.TempDir())
	libMovies, _ := db.AddLibrary("movies", "movies", t.TempDir())

	// One work, two editions: latest in-progress (E2) wins over E1.
	w1 := addWork(t, db, libTV, "Series", strp("Author One"), 1, 1)
	e1 := addEdition(t, db, w1, "S01E01", fltp(2000))
	e2 := addEdition(t, db, w1, "S01E02", fltp(2000))
	addProgress(t, db, user, e1, 100, fltp(2000), false, 1000)
	addProgress(t, db, user, e2, 500, fltp(2000), false, 2000)

	// Different library type, edition duration NULL -> falls back to progress duration.
	w2 := addWork(t, db, libMovies, "Film", nil, 2, 2)
	e3 := addEdition(t, db, w2, "Film", nil)
	addProgress(t, db, user, e3, 300, fltp(1500), false, 3000)

	// Finished work excluded even though most recent.
	w3 := addWork(t, db, libTV, "Done", strp("X"), 3, 3)
	e4 := addEdition(t, db, w3, "Done", fltp(1000))
	addProgress(t, db, user, e4, 1000, fltp(1000), true, 4000)

	// Finished NEWER than in-progress on same work: in-progress edition still resumes.
	w4 := addWork(t, db, libTV, "Mixed", strp("Y"), 4, 4)
	e5 := addEdition(t, db, w4, "Mixed A", fltp(1000))
	e6 := addEdition(t, db, w4, "Mixed B", fltp(1000))
	addProgress(t, db, user, e5, 100, fltp(1000), false, 5000)
	addProgress(t, db, user, e6, 1000, fltp(1000), true, 6000)

	// Other user's progress invisible.
	w5 := addWork(t, db, libTV, "Theirs", strp("Z"), 5, 5)
	e7 := addEdition(t, db, w5, "Theirs", fltp(1000))
	addProgress(t, db, other, e7, 100, fltp(1000), false, 7000)

	items, err := db.ResumeItems(user, 20)
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		workID, editionID int64
		percent           float64
		dur               float64
	}{
		{w4, e5, 0.1, 1000},
		{w2, e3, 0.2, 1500},
		{w1, e2, 0.25, 2000},
	}
	if len(items) != len(want) {
		t.Fatalf("got %d items, want %d: %+v", len(items), len(want), items)
	}
	for i, wv := range want {
		got := items[i]
		if got.WorkID != wv.workID || got.EditionID != wv.editionID {
			t.Fatalf("item %d = work %d edition %d, want work %d edition %d", i, got.WorkID, got.EditionID, wv.workID, wv.editionID)
		}
		if got.Percent != wv.percent || got.DurationSecs != wv.dur {
			t.Fatalf("item %d percent/dur = %v/%v, want %v/%v", i, got.Percent, got.DurationSecs, wv.percent, wv.dur)
		}
	}
	if items[0].LibraryType != "tv" || items[1].LibraryType != "movies" {
		t.Fatalf("library types = %v/%v, want tv/movies", items[0].LibraryType, items[1].LibraryType)
	}
	if items[0].Title != "Mixed" || items[0].Author == nil || *items[0].Author != "Y" {
		t.Fatalf("item 0 title/author = %q/%v", items[0].Title, items[0].Author)
	}
}

func TestResumeItemsReadingPercent(t *testing.T) {
	db := openTestDB(t)
	user := addUser(t, db)
	lib, _ := db.AddLibrary("books", "books", t.TempDir())
	w := addWork(t, db, lib, "Novel", strp("A"), 1, 1)
	res, err := db.Exec(`INSERT INTO editions (work_id, format, title, page_count, created_at) VALUES (?,?,?,?,0)`, w, "epub", "Novel", 200)
	if err != nil {
		t.Fatal(err)
	}
	eid, _ := res.LastInsertId()
	if _, err := db.Exec(`INSERT INTO progress (user_id, edition_id, edition_position_secs, is_finished, updated_at, percent, page) VALUES (?,?,0,0,10,0.42,84)`, user, eid); err != nil {
		t.Fatal(err)
	}
	items, err := db.ResumeItems(user, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Percent != 0.42 || items[0].LibraryType != "books" {
		t.Fatalf("got %+v", items)
	}
}

func TestResumeItemsLimit(t *testing.T) {
	db := openTestDB(t)
	user := addUser(t, db)
	lib, _ := db.AddLibrary("tv", "tv", t.TempDir())
	for i := 0; i < 25; i++ {
		w := addWork(t, db, lib, fmt.Sprintf("W%02d", i), strp("A"), int64(i), int64(i))
		e := addEdition(t, db, w, "E", fltp(100))
		addProgress(t, db, user, e, float64(i), fltp(100), false, int64(i+1))
	}
	items, err := db.ResumeItems(user, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 20 {
		t.Fatalf("got %d items, want 20", len(items))
	}
	if items[0].UpdatedAt != 25 || items[19].UpdatedAt != 6 {
		t.Fatalf("order wrong: first %d last %d, want 25..6", items[0].UpdatedAt, items[19].UpdatedAt)
	}
}

func TestSearchWorks(t *testing.T) {
	db := openTestDB(t)
	user := addUser(t, db)
	libMovies, _ := db.AddLibrary("movies", "movies", t.TempDir())
	libTV, _ := db.AddLibrary("tv", "tv", t.TempDir())

	authorMatch := addWork(t, db, libMovies, "Dune Messiah", strp("Frank Herbert"), 1, 1)
	titleMatch := addWork(t, db, libTV, "Herbert West", strp("Lovecraft"), 2, 2)
	addWork(t, db, libTV, "Foundation", strp("Isaac Asimov"), 3, 3)

	e := addEdition(t, db, authorMatch, "Dune", fltp(1000))
	addProgress(t, db, user, e, 250, fltp(1000), false, 100)

	for _, q := range []string{"herbert", "HERBERT", "erBeRt"} {
		hits, err := db.SearchWorks(user, q, 30)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 2 {
			t.Fatalf("q=%q: got %d hits, want 2: %+v", q, len(hits), hits)
		}
		if hits[0].WorkID != titleMatch || hits[1].WorkID != authorMatch {
			t.Fatalf("q=%q: order = %d,%d; want title match %d first, author match %d second",
				q, hits[0].WorkID, hits[1].WorkID, titleMatch, authorMatch)
		}
		if hits[0].Percent != nil {
			t.Fatalf("q=%q: unwatched hit percent = %v, want nil", q, *hits[0].Percent)
		}
		if hits[1].Percent == nil || *hits[1].Percent != 0.25 {
			t.Fatalf("q=%q: watched hit percent = %v, want 0.25", q, hits[1].Percent)
		}
	}

	// LIKE metacharacters must be literal, not wildcards.
	pct := addWork(t, db, libMovies, "100% Fresh", strp("Chef"), 4, 4)
	hits, err := db.SearchWorks(user, "100% fre", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].WorkID != pct {
		t.Fatalf("escaped search got %+v, want only work %d", hits, pct)
	}
	hits, err = db.SearchWorks(user, "0% fr__h", 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("wildcard chars must not match literally-absent pattern, got %+v", hits)
	}
}

func TestRecentWorks(t *testing.T) {
	db := openTestDB(t)
	lib, _ := db.AddLibrary("tv", "tv", t.TempDir())
	w1 := addWork(t, db, lib, "Newest", nil, 500, 500)
	w2 := addWork(t, db, lib, "Older B", nil, 300, 300)
	w3 := addWork(t, db, lib, "Older A", nil, 300, 300)
	addWork(t, db, lib, "Oldest", nil, 100, 100)

	works, err := db.RecentWorks(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(works) != 4 {
		t.Fatalf("got %d works, want 4", len(works))
	}
	gotOrder := []int64{works[0].WorkID, works[1].WorkID, works[2].WorkID, works[3].WorkID}
	if gotOrder[0] != w1 || gotOrder[1] != w3 || gotOrder[2] != w2 {
		t.Fatalf("order = %v, want [%d %d %d ...] (id desc on created_at ties)", gotOrder, w1, w3, w2)
	}
	if works[0].AddedAt != 500 || works[0].LibraryType != "tv" {
		t.Fatalf("first work = %+v", works[0])
	}

	limited, err := db.RecentWorks(2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 || limited[0].WorkID != w1 || limited[1].WorkID != w3 {
		t.Fatalf("limited = %+v, want 2 newest", limited)
	}
}

func TestWorksInLibraryFiltered(t *testing.T) {
	db := openTestDB(t)
	user := addUser(t, db)
	lib, _ := db.AddLibrary("books", "audiobooks", t.TempDir())

	alpha := addWork(t, db, lib, "Alpha", strp("Zed"), 200, 900)
	beta := addWork(t, db, lib, "Beta", strp("Alice"), 300, 800)
	gamma := addWork(t, db, lib, "Gamma", nil, 100, 700)

	eAlpha := addEdition(t, db, alpha, "A", fltp(100))
	eBeta := addEdition(t, db, beta, "B", fltp(100))
	eGamma := addEdition(t, db, gamma, "C", fltp(100))
	addFile(t, db, eAlpha, t.TempDir()+"/a.m4b", 100)
	addFile(t, db, eBeta, t.TempDir()+"/b.m4b", 100)
	addFile(t, db, eGamma, t.TempDir()+"/c.m4b", 100)
	addProgress(t, db, user, eAlpha, 10, fltp(100), false, 1000)
	addProgress(t, db, user, eBeta, 100, fltp(100), true, 1001)

	ids := func(views []store.WorkView) []int64 {
		out := make([]int64, 0, len(views))
		for _, v := range views {
			out = append(out, v.ID)
		}
		return out
	}
	equal := func(a []int64, b ...int64) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	cases := []struct {
		name         string
		sort, dir    string
		filter       string
		want         []int64
		checkEdition bool
	}{
		{"default is title asc", "", "", "all", []int64{alpha, beta, gamma}, false},
		{"explicit title asc", "title", "asc", "all", []int64{alpha, beta, gamma}, false},
		{"title desc", "title", "desc", "all", []int64{gamma, beta, alpha}, false},
		{"author asc null first", "author", "asc", "all", []int64{gamma, beta, alpha}, false},
		{"author desc", "author", "desc", "all", []int64{alpha, beta, gamma}, false},
		{"added desc default dir", "added", "", "all", []int64{beta, alpha, gamma}, false},
		{"added asc", "added", "asc", "all", []int64{gamma, alpha, beta}, false},
		{"updated asc", "updated", "asc", "all", []int64{gamma, beta, alpha}, false},
		{"in_progress filter", "title", "asc", "in_progress", []int64{alpha}, true},
		{"finished filter", "title", "asc", "finished", []int64{beta}, true},
		{"unplayed filter", "title", "asc", "unplayed", []int64{gamma}, true},
	}
	for _, tc := range cases {
		views, err := db.WorksInLibraryFiltered(lib, user, tc.sort, tc.dir, tc.filter)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		got := ids(views)
		if !equal(got, tc.want...) {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
		if tc.checkEdition {
			if len(views[0].Editions) != 1 || len(views[0].Editions[0].Files) != 1 {
				t.Fatalf("%s: editions/files not assembled: %+v", tc.name, views[0].Editions)
			}
		}
	}

	// Default path must equal the legacy WorksInLibrary exactly.
	legacy, err := db.WorksInLibrary(lib)
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := db.WorksInLibraryFiltered(lib, user, "title", "asc", "all")
	if err != nil {
		t.Fatal(err)
	}
	if !equal(ids(filtered), ids(legacy)...) {
		t.Fatalf("default sort drifted from WorksInLibrary: %v vs %v", ids(filtered), ids(legacy))
	}

	// Other user has no progress: filters see everything/none.
	other := addUser(t, db)
	views, err := db.WorksInLibraryFiltered(lib, other, "title", "asc", "unplayed")
	if err != nil {
		t.Fatal(err)
	}
	if !equal(ids(views), alpha, beta, gamma) {
		t.Fatalf("other user unplayed = %v, want all", ids(views))
	}
	views, err = db.WorksInLibraryFiltered(lib, other, "title", "asc", "in_progress")
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 0 {
		t.Fatalf("other user in_progress = %v, want none", ids(views))
	}
}

func TestWorksInLibraryFilteredPercent(t *testing.T) {
	db := openTestDB(t)
	user := addUser(t, db)
	lib, _ := db.AddLibrary("books", "books", t.TempDir())
	book := addWork(t, db, lib, "Novel", strp("A"), 1, 1)
	idle := addWork(t, db, lib, "Idle", strp("B"), 2, 2)
	res, err := db.Exec(`INSERT INTO editions (work_id, format, title, page_count, created_at) VALUES (?,?,?,?,0)`, book, "epub", "Novel", 200)
	if err != nil {
		t.Fatal(err)
	}
	eid, _ := res.LastInsertId()
	addEdition(t, db, idle, "Idle", nil)
	if _, err := db.Exec(`INSERT INTO progress (user_id, edition_id, edition_position_secs, is_finished, updated_at, percent, page) VALUES (?,?,0,0,10,0.4,80)`, user, eid); err != nil {
		t.Fatal(err)
	}
	views, err := db.WorksInLibraryFiltered(lib, user, "title", "asc", "all")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]store.WorkView{}
	for _, v := range views {
		byID[v.ID] = v
	}
	got := byID[book]
	if got.Percent == nil || *got.Percent != 0.4 {
		t.Fatalf("in-progress percent = %v, want 0.4", got.Percent)
	}
	if byID[idle].Percent != nil {
		t.Fatalf("unplayed percent = %v, want nil", *byID[idle].Percent)
	}
}
