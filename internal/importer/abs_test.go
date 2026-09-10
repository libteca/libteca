package importer

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/store"
	_ "modernc.org/sqlite"
)

func openStore(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// buildABSFixture creates a synthetic Audiobookshelf config dir: one book
// library with a real m4b on disk, one podcast library with one downloaded
// episode, two users, book + ebook + podcast progress, one playlist, plus a
// book item whose directory does not exist (must warn-skip).
func buildABSFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bookDir := filepath.Join(root, "media", "Book One")
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		t.Fatal(err)
	}
	m4b := filepath.Join(bookDir, "book.m4b")
	if err := os.WriteFile(m4b, make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	epFile := filepath.Join(root, "media", "ep1.mp3")
	if err := os.WriteFile(epFile, make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}

	absDir := filepath.Join(root, "abs")
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fdb, err := sql.Open("sqlite", filepath.Join(absDir, "abs_database.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fdb.Close()
	stmts := []string{
		`CREATE TABLE libraries (id INTEGER PRIMARY KEY, name TEXT, mediaType TEXT)`,
		`CREATE TABLE libraryFolders (id INTEGER PRIMARY KEY, libraryId INTEGER, path TEXT)`,
		`CREATE TABLE users (id INTEGER PRIMARY KEY, username TEXT, pash TEXT, type TEXT)`,
		`CREATE TABLE libraryItems (id INTEGER PRIMARY KEY, libraryId INTEGER, mediaId INTEGER, mediaType TEXT, title TEXT, path TEXT)`,
		`CREATE TABLE books (id INTEGER PRIMARY KEY, title TEXT, author TEXT, description TEXT, durationSec REAL)`,
		`CREATE TABLE podcasts (id INTEGER PRIMARY KEY, title TEXT, itunesAuthor TEXT)`,
		`CREATE TABLE podcastEpisodes (id INTEGER PRIMARY KEY, podcastId INTEGER, season INTEGER, episode INTEGER, title TEXT, duration REAL, audioFile TEXT)`,
		`CREATE TABLE mediaProgress (id INTEGER PRIMARY KEY, userId INTEGER, mediaItemId INTEGER, mediaItemType TEXT, currentTime REAL, progress REAL, isFinished INTEGER, ebookProgress REAL)`,
		`CREATE TABLE playlists (id INTEGER PRIMARY KEY, userId INTEGER, name TEXT)`,
		`CREATE TABLE playlistMediaItems (id INTEGER PRIMARY KEY, playlistId INTEGER, mediaItemId INTEGER, mediaItemType TEXT)`,
		`CREATE TABLE audioTracks (id INTEGER PRIMARY KEY, bookId INTEGER, "index" INTEGER, duration REAL)`,
		`INSERT INTO libraries VALUES (1, 'Audiobooks', 'book'), (2, 'Pods', 'podcast')`,
		`INSERT INTO libraryFolders VALUES (1, 1, '` + root + `/media')`,
		`INSERT INTO users VALUES (1, 'tyler', 'bcrypt-hash', 'admin'), (2, 'guest', 'bcrypt-hash', 'user')`,
		`INSERT INTO libraryItems VALUES (1, 1, 1, 'book', 'Book One', '` + bookDir + `'), (2, 2, 2, 'podcast', 'The Cast', ''), (3, 1, 3, 'book', 'Ghost Book', '/nonexistent/ghost')`,
		`INSERT INTO books VALUES (1, 'Book One', '["Ann Auth"]', 'A nice book', 3600), (3, 'Ghost Book', 'X', '', 600)`,
		`INSERT INTO podcasts VALUES (2, 'The Cast', 'Cast Author')`,
	}
	for _, s := range stmts {
		if _, err := fdb.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	audioFile := `{"duration":1800,"metadata":{"path":"` + epFile + `"}}`
	if _, err := fdb.Exec(`INSERT INTO podcastEpisodes VALUES (7, 2, 1, 1, 'Ep One', 1800, ?)`, audioFile); err != nil {
		t.Fatal(err)
	}
	if _, err := fdb.Exec(`INSERT INTO mediaProgress VALUES (1, 1, 1, 'book', 1200, 0.33, 0, 0), (2, 2, 1, 'book', 0, 0, 0, 0.5), (3, 1, 7, 'podcastEpisode', 900, 0.5, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := fdb.Exec(`INSERT INTO playlists VALUES (1, 1, 'Faves')`); err != nil {
		t.Fatal(err)
	}
	if _, err := fdb.Exec(`INSERT INTO playlistMediaItems VALUES (1, 1, 1, 'book')`); err != nil {
		t.Fatal(err)
	}
	if _, err := fdb.Exec(`INSERT INTO audioTracks VALUES (1, 1, 1, 3600)`); err != nil {
		t.Fatal(err)
	}
	return absDir
}

func countRows(t *testing.T, db *store.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestABSDryRun(t *testing.T) {
	db := openStore(t)
	plan, err := ABS(buildABSFixture(t), db, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Works != 2 || plan.Editions != 2 || plan.Files != 2 {
		t.Fatalf("works/editions/files = %d/%d/%d, want 2/2/2", plan.Works, plan.Editions, plan.Files)
	}
	if len(plan.Users) != 2 || len(plan.Libraries) != 2 {
		t.Fatalf("users = %d libraries = %d, want 2/2", len(plan.Users), len(plan.Libraries))
	}
	if plan.ProgressRows != 3 || plan.Playlists != 1 {
		t.Fatalf("progress = %d playlists = %d, want 3/1", plan.ProgressRows, plan.Playlists)
	}
	if countRows(t, db, `SELECT count(*) FROM works`) != 0 || countRows(t, db, `SELECT count(*) FROM users`) != 0 {
		t.Fatal("dryRun wrote to the store")
	}
	if !warningsMention(plan.Warnings, "Ghost Book") {
		t.Fatalf("missing-book warning absent: %v", plan.Warnings)
	}
	if !warningsMention(plan.Warnings, "password hashes cannot be migrated") {
		t.Fatalf("password warning absent: %v", plan.Warnings)
	}
}

func warningsMention(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

func TestABSCommit(t *testing.T) {
	db := openStore(t)
	absDir := buildABSFixture(t)
	plan, err := ABS(absDir, db, false)
	if err != nil {
		t.Fatal(err)
	}

	tyler, err := db.UserByName("tyler")
	if err != nil {
		t.Fatal("tyler not created")
	}
	if !tyler.IsAdmin {
		t.Fatal("admin flag lost")
	}
	for _, u := range plan.Users {
		if u.Name == "guest" && (u.Exists || u.TempPassword == "") {
			t.Fatalf("guest user plan = %+v, want temp password", u)
		}
	}

	if n := countRows(t, db, `SELECT count(*) FROM works`); n != 2 {
		t.Fatalf("works = %d, want 2", n)
	}
	var bookWork int64
	if err := db.QueryRow(`SELECT id FROM works WHERE title = 'Book One' AND author = 'Ann Auth'`).Scan(&bookWork); err != nil {
		t.Fatalf("book work missing: %v", err)
	}
	var eid int64
	var format string
	var dur float64
	if err := db.QueryRow(`SELECT id, format, duration_secs FROM editions WHERE work_id = ?`, bookWork).Scan(&eid, &format, &dur); err != nil {
		t.Fatal(err)
	}
	if format != "m4b" || dur != 3600 {
		t.Fatalf("edition = %s/%v", format, dur)
	}
	var fpath string
	if err := db.QueryRow(`SELECT path FROM files WHERE edition_id = ?`, eid).Scan(&fpath); err != nil {
		t.Fatalf("file not linked to edition: %v", err)
	}
	if !strings.HasSuffix(fpath, "book.m4b") {
		t.Fatalf("file path = %q", fpath)
	}

	p, err := db.GetProgress(tyler.ID, eid)
	if err != nil {
		t.Fatal(err)
	}
	if p.EditionPositionSecs != 1200 || p.IsFinished {
		t.Fatalf("book progress = %+v", p)
	}
	guest, err := db.UserByName("guest")
	if err != nil {
		t.Fatal(err)
	}
	gp, err := db.GetReadingProgress(guest.ID, eid)
	if err != nil || gp.Percent == nil || *gp.Percent != 0.5 {
		t.Fatalf("ebook progress = %+v err %v", gp, err)
	}

	var castWork int64
	if err := db.QueryRow(`SELECT w.id FROM works w JOIN libraries l ON l.id = w.library_id WHERE w.title = 'The Cast' AND l.type = 'podcasts'`).Scan(&castWork); err != nil {
		t.Fatalf("podcast work missing: %v", err)
	}
	var season, episode int
	var epEid int64
	if err := db.QueryRow(`SELECT id, season_num, episode_num FROM editions WHERE work_id = ?`, castWork).Scan(&epEid, &season, &episode); err != nil {
		t.Fatal(err)
	}
	if season != 1 || episode != 1 {
		t.Fatalf("episode numbering = S%dE%d", season, episode)
	}
	ep, err := db.GetProgress(tyler.ID, epEid)
	if err != nil || ep.EditionPositionSecs != 900 {
		t.Fatalf("podcast progress = %+v err %v", ep, err)
	}

	pls, err := db.ListPlaylists(tyler.ID)
	if err != nil || len(pls) != 1 || pls[0].Name != "Faves" || pls[0].SongCount != 1 {
		t.Fatalf("playlists = %+v err %v", pls, err)
	}

	// Re-running against the same instance must not duplicate anything.
	if _, err := ABS(absDir, db, false); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM works`); n != 2 {
		t.Fatalf("re-import works = %d, want 2", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM editions`); n != 2 {
		t.Fatalf("re-import editions = %d, want 2", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM files`); n != 2 {
		t.Fatalf("re-import files = %d, want 2", n)
	}
	pls, err = db.ListPlaylists(tyler.ID)
	if err != nil || len(pls) != 1 {
		t.Fatalf("re-import playlists = %+v err %v", pls, err)
	}
}
