package importer

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/store"
	_ "modernc.org/sqlite"
)

func writeReleasedAudio(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func audioFilesJSON(entries []absAudioFile) string {
	raw, err := json.Marshal(entries)
	if err != nil {
		panic(err)
	}
	return string(raw)
}

// buildABSReleasedFixture creates a released-schema Audiobookshelf database:
// UUID ids everywhere, books.audioFiles JSON, bookAuthors/authors, two book
// libraries with distinct item/media ids, a deliberate cross-namespace
// collision (book B's libraryItem id equals book A's book media id), a
// multi-file audiobook whose audioFiles index order differs from natural
// path order, a podcast library whose episode id textually collides with a
// book media id, mediaProgresses and playlistMediaItems keyed by media ids.
func buildABSReleasedFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	bookDirA := filepath.Join(root, "media", "Novel A")
	bookDirB := filepath.Join(root, "media", "Novel B")
	for _, d := range []string{bookDirA, bookDirB} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	a1 := filepath.Join(bookDirA, "part2.m4b")
	a2 := filepath.Join(bookDirA, "part1.m4b")
	writeReleasedAudio(t, a1, 4096)
	writeReleasedAudio(t, a2, 4096)
	b1 := filepath.Join(bookDirB, "single.m4b")
	writeReleasedAudio(t, b1, 4096)
	epFile := filepath.Join(root, "media", "ep1.mp3")
	writeReleasedAudio(t, epFile, 2048)

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
		`CREATE TABLE libraries (id TEXT PRIMARY KEY, name TEXT, mediaType TEXT)`,
		`CREATE TABLE libraryFolders (id TEXT PRIMARY KEY, libraryId TEXT, path TEXT)`,
		`CREATE TABLE users (id TEXT PRIMARY KEY, username TEXT, pash TEXT, type TEXT)`,
		`CREATE TABLE libraryItems (id TEXT PRIMARY KEY, libraryId TEXT, mediaId TEXT, mediaType TEXT, title TEXT, path TEXT)`,
		`CREATE TABLE books (id TEXT PRIMARY KEY, title TEXT, description TEXT, duration REAL, audioFiles TEXT)`,
		`CREATE TABLE authors (id TEXT PRIMARY KEY, name TEXT)`,
		`CREATE TABLE bookAuthors (bookId TEXT, authorId TEXT)`,
		`CREATE TABLE podcasts (id TEXT PRIMARY KEY, title TEXT, itunesAuthor TEXT)`,
		`CREATE TABLE podcastEpisodes (id TEXT PRIMARY KEY, podcastId TEXT, season INTEGER, episode INTEGER, title TEXT, duration REAL, audioFile TEXT)`,
		`CREATE TABLE mediaProgresses (id TEXT PRIMARY KEY, userId TEXT, mediaItemId TEXT, mediaItemType TEXT, currentTime REAL, ebookProgress REAL, isFinished INTEGER)`,
		`CREATE TABLE playlists (id TEXT PRIMARY KEY, userId TEXT, name TEXT)`,
		`CREATE TABLE playlistMediaItems (id TEXT PRIMARY KEY, playlistId TEXT, mediaItemId TEXT, mediaItemType TEXT)`,
		`INSERT INTO libraries VALUES ('lib-1', 'Audiobooks', 'book'), ('lib-2', 'Pods', 'podcast')`,
		`INSERT INTO libraryFolders VALUES ('f-1', 'lib-1', '` + filepath.Join(root, "media") + `')`,
		`INSERT INTO users VALUES ('user-1', 'tyler', 'bcrypt-hash', 'admin'), ('user-2', 'guest', 'bcrypt-hash', 'user')`,
		`INSERT INTO libraryItems VALUES
			('item-a', 'lib-1', 'book-a', 'book', 'Novel A', '` + bookDirA + `'),
			('book-a', 'lib-1', 'book-b', 'book', 'Novel B', '` + bookDirB + `'),
			('item-p', 'lib-2', 'pod-1', 'podcast', 'The Cast', '')`,
		`INSERT INTO books VALUES
			('book-a', 'Novel A', 'A nice book', 7200, '` + audioFilesJSON([]absAudioFile{
			{Index: 2, Duration: 3000, Metadata: absAudioFileMetadata{Path: a1}},
			{Index: 1, Duration: 4200, Metadata: absAudioFileMetadata{Path: a2}},
		}) + `'),
			('book-b', 'Novel B', '', 2400, '` + audioFilesJSON([]absAudioFile{
			{Path: b1, Index: 1, Duration: 2400},
		}) + `')`,
		`INSERT INTO authors VALUES ('auth-1', 'Ann Auth')`,
		`INSERT INTO bookAuthors VALUES ('book-a', 'auth-1')`,
		`INSERT INTO podcasts VALUES ('pod-1', 'The Cast', 'Cast Author')`,
	}
	for _, s := range stmts {
		if _, err := fdb.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	audioFile := `{"duration":1800,"metadata":{"path":"` + epFile + `"}}`
	if _, err := fdb.Exec(`INSERT INTO podcastEpisodes VALUES ('book-a', 'pod-1', 1, 1, 'Ep One', 1800, ?)`, audioFile); err != nil {
		t.Fatal(err)
	}
	if _, err := fdb.Exec(`INSERT INTO mediaProgresses VALUES
		('mp-1', 'user-1', 'book-a', 'book', 1200, 0, 0),
		('mp-2', 'user-1', 'book-b', 'book', 800, 0, 0),
		('mp-3', 'user-1', 'book-a', 'podcastEpisode', 900, 0, 0),
		('mp-4', 'user-2', 'book-a', 'book', 0, 0.5, 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := fdb.Exec(`INSERT INTO playlists VALUES ('pl-1', 'user-1', 'Faves')`); err != nil {
		t.Fatal(err)
	}
	if _, err := fdb.Exec(`INSERT INTO playlistMediaItems VALUES ('pmi-1', 'pl-1', 'book-b', 'book'), ('pmi-2', 'pl-1', 'item-a', 'book')`); err != nil {
		t.Fatal(err)
	}
	return absDir
}

func editionIDByTitleAndFormat(t *testing.T, db *store.DB, title, format string) int64 {
	t.Helper()
	var id int64
	if err := db.QueryRow(`SELECT e.id FROM editions e WHERE e.title = ? AND e.format = ? ORDER BY e.id LIMIT 1`, title, format).Scan(&id); err != nil {
		t.Fatalf("edition %q/%s: %v", title, format, err)
	}
	return id
}

func TestABSReleasedSchemaImports(t *testing.T) {
	db := openStore(t)
	plan, err := ABS(buildABSReleasedFixture(t), db, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Works != 3 || plan.Editions != 3 || plan.Files != 4 {
		t.Fatalf("works/editions/files = %d/%d/%d, want 3/3/4", plan.Works, plan.Editions, plan.Files)
	}
	if len(plan.Users) != 2 || plan.ProgressRows != 4 || plan.Playlists != 1 {
		t.Fatalf("users/progress/playlists = %d/%d/%d, want 2/4/1", len(plan.Users), plan.ProgressRows, plan.Playlists)
	}
	tyler, err := db.UserByName("tyler")
	if err != nil || !tyler.IsAdmin {
		t.Fatalf("tyler = %+v err %v", tyler, err)
	}

	var workA int64
	if err := db.QueryRow(`SELECT id FROM works WHERE title = 'Novel A' AND author = 'Ann Auth'`).Scan(&workA); err != nil {
		t.Fatalf("Novel A work missing: %v", err)
	}
	var seqs []int
	var paths []string
	rows, qerr := db.Query(`SELECT f.seq, f.path FROM files f JOIN editions e ON e.id = f.edition_id WHERE e.work_id = ? ORDER BY f.seq`, workA)
	if qerr != nil {
		t.Fatal(qerr)
	}
	for rows.Next() {
		var seq int
		var path string
		if err := rows.Scan(&seq, &path); err != nil {
			t.Fatal(err)
		}
		seqs = append(seqs, seq)
		paths = append(paths, filepath.Base(path))
	}
	rows.Close()
	if len(paths) != 2 || paths[0] != "part1.m4b" || paths[1] != "part2.m4b" {
		t.Fatalf("audioFiles order not honored: %v", paths)
	}

	bookAEd := editionIDByTitleAndFormat(t, db, "Novel A", "m4b")
	bookBEd := editionIDByTitleAndFormat(t, db, "Novel B", "m4b")
	epEd := editionIDByTitleAndFormat(t, db, "Ep One", "mp3")

	pa, err := db.GetProgress(tyler.ID, bookAEd)
	if err != nil || pa.EditionPositionSecs != 1200 {
		t.Fatalf("book A progress = %+v err %v (cross-namespace collision must not misassign)", pa, err)
	}
	pb, err := db.GetProgress(tyler.ID, bookBEd)
	if err != nil || pb.EditionPositionSecs != 800 {
		t.Fatalf("book B progress = %+v err %v", pb, err)
	}
	pe, err := db.GetProgress(tyler.ID, epEd)
	if err != nil || pe.EditionPositionSecs != 900 {
		t.Fatalf("episode progress = %+v err %v (book/episode id namespaces must stay distinct)", pe, err)
	}
	guest, err := db.UserByName("guest")
	if err != nil {
		t.Fatal(err)
	}
	gp, err := db.GetReadingProgress(guest.ID, bookAEd)
	if err != nil || gp.Percent == nil || *gp.Percent != 0.5 {
		t.Fatalf("ebook progress = %+v err %v", gp, err)
	}

	pls, err := db.ListPlaylists(tyler.ID)
	if err != nil || len(pls) != 1 || pls[0].SongCount != 1 {
		t.Fatalf("playlists = %+v err %v (playlist must target the media-id book, not the colliding item id)", pls, err)
	}
	var plEdition int64
	if err := db.QueryRow(`SELECT edition_id FROM playlist_items WHERE playlist_id = ?`, pls[0].ID).Scan(&plEdition); err != nil {
		t.Fatal(err)
	}
	if plEdition != bookBEd {
		t.Fatalf("playlist edition = %d, want Novel B edition %d", plEdition, bookBEd)
	}
}

func TestABSReleasedDryRunWritesNothing(t *testing.T) {
	db := openStore(t)
	plan, err := ABS(buildABSReleasedFixture(t), db, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Works != 3 || plan.Users == nil || len(plan.Users) != 2 {
		t.Fatalf("dry plan = %+v", plan)
	}
	if n := countRows(t, db, `SELECT count(*) FROM works`); n != 0 {
		t.Fatalf("dryRun wrote %d works", n)
	}
}

func TestABSUnsupportedSchemaRefused(t *testing.T) {
	root := t.TempDir()
	absDir := filepath.Join(root, "abs")
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fdb, err := sql.Open("sqlite", filepath.Join(absDir, "abs_database.db"))
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`CREATE TABLE libraries (id TEXT PRIMARY KEY, name TEXT, mediaType TEXT)`,
		`CREATE TABLE books (id TEXT PRIMARY KEY, somethingElse REAL)`,
		`INSERT INTO libraries VALUES ('lib-1', 'Books', 'book')`,
		`INSERT INTO books VALUES ('b', 1)`,
	}
	for _, s := range stmts {
		if _, err := fdb.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	fdb.Close()
	db := openStore(t)
	plan, err := ABS(absDir, db, true)
	if err == nil {
		t.Fatalf("unsupported schema must be refused, got plan %+v", plan)
	}
	if !strings.Contains(err.Error(), "unsupported") && !strings.Contains(err.Error(), "not an Audiobookshelf") {
		t.Fatalf("refusal reason = %v", err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM works`); n != 0 {
		t.Fatalf("refused import wrote %d works", n)
	}
}

func TestABSReleasedMissingUsersWarns(t *testing.T) {
	absDir := buildABSReleasedFixture(t)
	source := filepath.Join(absDir, "abs_database.db")
	sourceExec(t, source, func(db *sql.DB) {
		importExec(t, db, `DROP TABLE mediaProgresses`)
		importExec(t, db, `DROP TABLE users`)
	})
	db := openStore(t)
	plan, err := ABS(absDir, db, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Works != 3 {
		t.Fatalf("works = %d, want 3 (missing optional users/progress degrade)", plan.Works)
	}
	if !warningsMention(plan.Warnings, "users table unreadable") {
		t.Fatalf("users warning absent: %v", plan.Warnings)
	}
}

func TestABSReleasedGoneAudioFileSkipsEntry(t *testing.T) {
	absDir := buildABSReleasedFixture(t)
	source := filepath.Join(absDir, "abs_database.db")
	sourceExec(t, source, func(db *sql.DB) {
		ghost := filepath.Join(t.TempDir(), "ghost.m4b")
		files := audioFilesJSON([]absAudioFile{
			{Path: ghost, Index: 1, Duration: 1000},
		})
		importExec(t, db, `UPDATE books SET audioFiles = ? WHERE id = 'book-b'`, files)
	})
	db := openStore(t)
	plan, err := ABS(absDir, db, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Works != 2 {
		t.Fatalf("works = %d, want 2 (book with only stale audioFiles entries skipped)", plan.Works)
	}
	if !warningsMention(plan.Warnings, "gone") {
		t.Fatalf("stale-entry warning absent: %v", plan.Warnings)
	}
}
