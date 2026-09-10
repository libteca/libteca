package store

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/pressly/goose/v3"
)

func openMetaTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func addMetaWork(t *testing.T, db *DB, libID int64, title, author string) int64 {
	t.Helper()
	w := &Work{LibraryID: libID, Title: title}
	if author != "" {
		w.Author = &author
	}
	id, err := db.UpsertWork(w)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func setMetaWorkDescription(t *testing.T, db *DB, workID int64, desc string) {
	t.Helper()
	if _, err := db.Exec(`UPDATE works SET description = ? WHERE id = ?`, desc, workID); err != nil {
		t.Fatal(err)
	}
}

func TestPutGetCached(t *testing.T) {
	db := openMetaTestDB(t)
	if _, _, ok := db.GetCached("tmdb", "search:x"); ok {
		t.Fatal("empty cache should miss")
	}
	if err := db.PutCached("tmdb", "search:x", `{"a":1}`); err != nil {
		t.Fatal(err)
	}
	resp, at, ok := db.GetCached("tmdb", "search:x")
	if !ok || resp != `{"a":1}` || at == 0 {
		t.Fatalf("GetCached = %q %d %v", resp, at, ok)
	}
	if err := db.PutCached("tmdb", "search:x", `{"a":2}`); err != nil {
		t.Fatal(err)
	}
	if resp, _, _ := db.GetCached("tmdb", "search:x"); resp != `{"a":2}` {
		t.Fatalf("upsert failed, response = %q", resp)
	}
}

func TestMatchingInboxPredicate(t *testing.T) {
	db := openMetaTestDB(t)
	libID, _ := db.AddLibrary("A", "audiobooks", t.TempDir())
	otherLib, _ := db.AddLibrary("B", "audiobooks", t.TempDir())

	noDescNoProvider := addMetaWork(t, db, libID, "W1", "Author") // in
	_ = noDescNoProvider
	descSet := addMetaWork(t, db, libID, "W2", "") // out: description present
	setMetaWorkDescription(t, db, descSet, "has description")
	providerSet := addMetaWork(t, db, libID, "W3", "Author") // out: author + provider
	if err := db.ApplyWorkMeta(providerSet, "d", "openlibrary", "OL1"); err != nil {
		t.Fatal(err)
	}
	noAuthorNoProvider := addMetaWork(t, db, libID, "W4", "") // in: author empty
	_ = noAuthorNoProvider
	otherLibrary := addMetaWork(t, db, otherLib, "W5", "Author") // in, but other lib
	_ = otherLibrary
	skipped := addMetaWork(t, db, libID, "W6", "Author") // out: match-skip
	if err := db.PutCached("match-skip", strconv.FormatInt(skipped, 10), "{}"); err != nil {
		t.Fatal(err)
	}

	all, err := db.MatchingInbox(0)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, w := range all {
		titles = append(titles, w.Title)
	}
	if len(titles) != 3 {
		t.Fatalf("inbox = %v, want W1 W4 W5", titles)
	}
	byTitle := map[string]InboxWork{}
	for _, w := range all {
		byTitle[w.Title] = w
	}
	if _, ok := byTitle["W1"]; !ok {
		t.Fatalf("W1 missing from %v", titles)
	}
	if _, ok := byTitle["W4"]; !ok {
		t.Fatalf("W4 missing from %v", titles)
	}
	if w, ok := byTitle["W5"]; !ok || w.LibraryID != otherLib || w.LibraryName != "B" || w.LibraryType != "audiobooks" {
		t.Fatalf("W5 = %+v ok=%v", w, ok)
	}

	one, err := db.MatchingInbox(libID)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 2 {
		t.Fatalf("lib-filtered inbox = %d, want 2", len(one))
	}
}

func workProviderCols(t *testing.T, db *DB, workID int64) (provider, providerID, description string) {
	t.Helper()
	var p, pid, desc *string
	if err := db.QueryRow(`SELECT provider, provider_id, description FROM works WHERE id = ?`, workID).Scan(&p, &pid, &desc); err != nil {
		t.Fatal(err)
	}
	str := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	return str(p), str(pid), str(desc)
}

func TestApplyWorkMeta(t *testing.T) {
	db := openMetaTestDB(t)
	libID, _ := db.AddLibrary("A", "audiobooks", t.TempDir())
	id := addMetaWork(t, db, libID, "W", "Author")

	if err := db.ApplyWorkMeta(id, "", "audible", "B07Z"); err != nil {
		t.Fatal(err)
	}
	p, pid, desc := workProviderCols(t, db, id)
	if p != "audible" || pid != "B07Z" {
		t.Fatalf("provider = %q/%q", p, pid)
	}
	if desc != "" {
		t.Fatalf("empty description must not clobber, got %q", desc)
	}

	if err := db.ApplyWorkMeta(id, "desc v1", "audible", "B07Z"); err != nil {
		t.Fatal(err)
	}
	if err := db.ApplyWorkMeta(id, "", "tmdb", "movie:1"); err != nil {
		t.Fatal(err)
	}
	p, pid, desc = workProviderCols(t, db, id)
	if desc != "desc v1" {
		t.Fatalf("description = %q, want preserved desc v1", desc)
	}
	if p != "tmdb" || pid != "movie:1" {
		t.Fatalf("provider = %q/%q", p, pid)
	}
}

func addMetaEditionFile(t *testing.T, db *DB, workID int64, format, title, chapters string, extraFile bool) int64 {
	t.Helper()
	e := &Edition{WorkID: workID, Format: format, Title: title}
	edID, err := db.UpsertEdition(e)
	if err != nil {
		t.Fatal(err)
	}
	path := title + ".m4b"
	f := &FileRec{EditionID: edID, Path: path, Seq: 1, SizeBytes: 1, MtimeSecs: 1, DurationSecs: 100, Chapters: chapters}
	if err := db.UpsertFile(f); err != nil {
		t.Fatal(err)
	}
	if extraFile {
		f2 := &FileRec{EditionID: edID, Path: title + "-2.m4b", Seq: 2, SizeBytes: 1, MtimeSecs: 1, DurationSecs: 100, Chapters: "[]"}
		if err := db.UpsertFile(f2); err != nil {
			t.Fatal(err)
		}
	}
	return edID
}

func fileChapters(t *testing.T, db *DB, path string) string {
	t.Helper()
	var chapters string
	if err := db.QueryRow(`SELECT chapters FROM files WHERE path = ?`, path).Scan(&chapters); err != nil {
		t.Fatal(err)
	}
	return chapters
}

func TestFillEmptyChapters(t *testing.T) {
	db := openMetaTestDB(t)
	libID, _ := db.AddLibrary("A", "audiobooks", t.TempDir())
	id := addMetaWork(t, db, libID, "W", "Author")

	addMetaEditionFile(t, db, id, "m4b", "single-empty", "[]", false)           // filled
	addMetaEditionFile(t, db, id, "m4b", "single-ffprobe", `[{"id":1}]`, false) // untouched
	addMetaEditionFile(t, db, id, "mp3", "mp3-edition", "[]", false)            // wrong format
	addMetaEditionFile(t, db, id, "m4b", "multi-file", "[]", true)              // multi-file: not single

	n, err := db.FillEmptyChapters(id, `[{"id":1,"start":0,"end":60,"title":"C1"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("filled = %d, want 1", n)
	}
	if got := fileChapters(t, db, "single-empty.m4b"); got != `[{"id":1,"start":0,"end":60,"title":"C1"}]` {
		t.Fatalf("empty chapters not written: %q", got)
	}
	if got := fileChapters(t, db, "single-ffprobe.m4b"); got != `[{"id":1}]` {
		t.Fatalf("ffprobe chapters clobbered: %q", got)
	}
	if got := fileChapters(t, db, "mp3-edition.m4b"); got != "[]" {
		t.Fatalf("mp3 edition touched: %q", got)
	}
	if got := fileChapters(t, db, "multi-file.m4b"); got != "[]" {
		t.Fatalf("multi-file edition touched: %q", got)
	}
}

func TestWorkGenres(t *testing.T) {
	db := openMetaTestDB(t)
	libID, _ := db.AddLibrary("A", "movies", t.TempDir())
	id := addMetaWork(t, db, libID, "W", "")
	if g := db.WorkGenres(id); len(g) != 0 {
		t.Fatalf("genres = %v, want empty", g)
	}
	if err := db.SetWorkGenres(id, []string{"Science Fiction", "Adventure"}); err != nil {
		t.Fatal(err)
	}
	g := db.WorkGenres(id)
	if len(g) != 2 || g[0] != "Science Fiction" {
		t.Fatalf("genres = %v", g)
	}
	var raw string
	if err := db.QueryRow(`SELECT genres FROM works WHERE id = ?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw != `["Science Fiction","Adventure"]` {
		t.Fatalf("column = %q", raw)
	}
}

func TestGenresMigrationFromKV(t *testing.T) {
	db := openMetaTestDB(t)
	libID, _ := db.AddLibrary("A", "movies", t.TempDir())
	withKV := addMetaWork(t, db, libID, "W1", "")
	noKV := addMetaWork(t, db, libID, "W2", "")

	// Roll back to the pre-genres schema, write KV rows the old way, then
	// re-run migrations so the real 0008 migration does the copy.
	if err := goose.DownTo(db.DB, "migrations", 7); err != nil {
		t.Fatalf("goose down: %v", err)
	}
	if err := db.SetSetting("genres:work/"+strconv.FormatInt(withKV, 10), `["Drama"]`); err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting("genres:work/999", `["Orphan"]`); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db.DB, "migrations"); err != nil {
		t.Fatalf("goose up: %v", err)
	}

	if g := db.WorkGenres(withKV); len(g) != 1 || g[0] != "Drama" {
		t.Fatalf("migrated genres = %v, want [Drama]", g)
	}
	if g := db.WorkGenres(noKV); len(g) != 0 {
		t.Fatalf("genres without KV = %v, want empty", g)
	}
	if _, ok := db.GetSetting("genres:work/" + strconv.FormatInt(withKV, 10)); !ok {
		t.Fatal("KV rows should be left in place")
	}
}

func TestWorkEpisodes(t *testing.T) {
	db := openMetaTestDB(t)
	libID, _ := db.AddLibrary("A", "tv", t.TempDir())
	id := addMetaWork(t, db, libID, "Show", "")

	s1, s2, e1, e2 := 1, 2, 1, 2
	if _, err := db.UpsertEdition(&Edition{WorkID: id, Format: "video", Title: "Special"}); err != nil {
		t.Fatal(err)
	}
	edS1E2, err := db.UpsertEdition(&Edition{WorkID: id, Format: "video", Title: "S01E02", SeasonNum: &s1, EpisodeNum: &e2})
	if err != nil {
		t.Fatal(err)
	}
	edS1E1, err := db.UpsertEdition(&Edition{WorkID: id, Format: "video", Title: "Show.S01E01.1080p", SeasonNum: &s1, EpisodeNum: &e1})
	if err != nil {
		t.Fatal(err)
	}
	edS2E1, err := db.UpsertEdition(&Edition{WorkID: id, Format: "video", Title: "Good Title", SeasonNum: &s2, EpisodeNum: &e1})
	if err != nil {
		t.Fatal(err)
	}

	eps, err := db.WorkEpisodes(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 3 {
		t.Fatalf("episodes = %+v, want 3 (no-season edition excluded)", eps)
	}
	if eps[0].ID != edS1E1 || eps[0].SeasonNum != 1 || eps[0].EpisodeNum != 1 || eps[0].Description != nil {
		t.Fatalf("eps[0] = %+v", eps[0])
	}
	if eps[1].ID != edS1E2 || eps[2].ID != edS2E1 {
		t.Fatalf("order = %+v, want (s1e1, s1e2, s2e1)", eps)
	}

	if err := db.SetEpisodeTitle(edS1E2, "The Beginning"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetEpisodeDescription(edS1E1, "After the incident."); err != nil {
		t.Fatal(err)
	}
	if err := db.SetEpisodeDescription(edS1E1, "clobber attempt"); err != nil {
		t.Fatal(err)
	}
	eps, err = db.WorkEpisodes(id)
	if err != nil {
		t.Fatal(err)
	}
	if eps[1].Title != "The Beginning" {
		t.Fatalf("title = %q", eps[1].Title)
	}
	if eps[0].Description == nil || *eps[0].Description != "After the incident." {
		t.Fatalf("description = %v, want preserved original", eps[0].Description)
	}
	if eps[2].Description != nil {
		t.Fatalf("description = %v, want untouched nil", eps[2].Description)
	}
}
