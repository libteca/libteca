package store_test

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
)

func assertForeignKeyConsistency(t *testing.T, db *sql.DB) {
	t.Helper()
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, parent string
		var rowID, constraint int64
		if err := rows.Scan(&table, &rowID, &parent, &constraint); err != nil {
			t.Fatal(err)
		}
		t.Errorf("broken foreign key: %s row %d -> %s constraint %d", table, rowID, parent, constraint)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationsFullRoundTrip(t *testing.T) {
	db := openTestDB(t)
	goose.SetBaseFS(nil)
	if err := goose.DownTo(db.DB, "migrations", 0); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db.DB, "migrations"); err != nil {
		t.Fatal(err)
	}
	assertForeignKeyConsistency(t, db.DB)
}

func TestGamesMigrationDownRemovesOnlyUnsupportedRecords(t *testing.T) {
	db := openTestDB(t)
	statements := []string{
		`INSERT INTO users (id, name, password_hash, created_at, updated_at) VALUES (1, 'reader', 'fixture', 1, 1)`,
		`INSERT INTO libraries (id, name, type, path, created_at) VALUES (1, 'Books', 'books', '/fixtures/books', 1), (2, 'Games', 'games', '/fixtures/games', 1)`,
		`INSERT INTO works (id, library_id, title, created_at, updated_at) VALUES (1, 1, 'Book', 1, 1), (2, 2, 'Game', 1, 1), (3, 2, 'Empty Game', 1, 1)`,
		`INSERT INTO editions (id, work_id, format, title, created_at) VALUES (1, 1, 'epub', 'Book', 1), (2, 2, 'game-nes', 'Game', 1)`,
		`INSERT INTO files (id, edition_id, path, seq, size_bytes, mtime_secs, duration_secs, probed_at) VALUES (1, 1, '/fixtures/books/a.epub', 1, 1, 1, 0, 1), (2, 2, '/fixtures/games/a.nes', 1, 1, 1, 0, 1)`,
		`INSERT INTO progress (user_id, edition_id, file_id, updated_at) VALUES (1, 1, 1, 1), (1, 2, 2, 1)`,
		`INSERT INTO playback_sessions (id, user_id, edition_id, file_id, started_at, updated_at) VALUES ('book', 1, 1, 1, 1, 1), ('game', 1, 2, 2, 1, 1)`,
		`INSERT INTO playlists (id, user_id, name, created_at, updated_at) VALUES (1, 1, 'Favorites', 1, 1)`,
		`INSERT INTO playlist_items (playlist_id, edition_id, position, added_at) VALUES (1, 1, 1, 1), (1, 2, 2, 1)`,
		`INSERT INTO scan_jobs (library_id, status, started_at, created_at) VALUES (1, 'done', 1, 1), (2, 'done', 1, 1)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	goose.SetBaseFS(nil)
	if err := goose.DownTo(db.DB, "migrations", 10); err != nil {
		t.Fatal(err)
	}
	assertForeignKeyConsistency(t, db.DB)
	for _, table := range []string{"libraries", "works", "editions", "files", "progress", "playback_sessions", "playlists", "playlist_items", "scan_jobs"} {
		var count int
		if err := db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Errorf("%s count = %d, want 1", table, count)
		}
	}
	if err := goose.Up(db.DB, "migrations"); err != nil {
		t.Fatal(err)
	}
	assertForeignKeyConsistency(t, db.DB)
}

func TestMigrationsUpgradePopulatedOriginalSchema(t *testing.T) {
	db := openTestDB(t)
	goose.SetBaseFS(nil)
	if err := goose.DownTo(db.DB, "migrations", 1); err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`INSERT INTO users (id, name, password_hash, created_at, updated_at) VALUES (7, 'reader', 'fixture', 1, 1)`,
		`INSERT INTO libraries (id, name, type, path, created_at) VALUES (8, 'Books', 'audiobooks', '/fixtures/books', 1)`,
		`INSERT INTO works (id, library_id, title, author, created_at, updated_at) VALUES (9, 8, 'Book', 'Author', 1, 1)`,
		`INSERT INTO editions (id, work_id, format, title, duration_secs, created_at) VALUES (10, 9, 'mp3', 'Book', 100, 1)`,
		`INSERT INTO files (id, edition_id, path, seq, size_bytes, mtime_secs, duration_secs, probed_at) VALUES (11, 10, '/fixtures/books/a.mp3', 1, 1, 1, 100, 1)`,
		`INSERT INTO progress (user_id, edition_id, file_id, file_offset_secs, edition_position_secs, duration_secs, updated_at) VALUES (7, 10, 11, 32, 32, 100, 1)`,
		`INSERT INTO playback_sessions (id, user_id, edition_id, file_id, started_at, updated_at) VALUES ('session', 7, 10, 11, 1, 1)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := goose.Up(db.DB, "migrations"); err != nil {
		t.Fatal(err)
	}
	assertForeignKeyConsistency(t, db.DB)
	work, err := db.WorkViewByID(9)
	if err != nil {
		t.Fatal(err)
	}
	if work.Title != "Book" || len(work.Editions) != 1 || work.Editions[0].ID != 10 || len(work.Editions[0].Files) != 1 || work.Editions[0].Files[0].ID != 11 {
		t.Fatalf("upgrade changed media identity: %+v", work)
	}
	progress, err := db.GetProgress(7, 10)
	if err != nil {
		t.Fatal(err)
	}
	if progress.EditionPositionSecs != 32 || progress.FileID == nil || *progress.FileID != 11 || progress.FileOffsetSecs != 32 {
		t.Fatalf("upgrade changed resume state: %+v", progress)
	}
	var integrity string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity = %q, %v", integrity, err)
	}
}
