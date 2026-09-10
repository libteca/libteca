package store_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

type sqlNullInt = sql.NullInt64

// TestMigration0005FilesRebuildPreservesReferences exercises the real upgrade
// path: roll 0005 back, seed a library whose progress references files, then
// re-apply. The files table is rebuilt (edition_id becomes nullable) and must
// come through with rows, unique path and FK enforcement intact.
func TestMigration0005FilesRebuildPreservesReferences(t *testing.T) {
	db := openTestDB(t)

	seed := func() (fileID int64) {
		t.Helper()
		libID, err := db.AddLibrary("A", "audiobooks", t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		now := int64(1234)
		if _, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('u','h',1,?,?)`, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO works (library_id, title, created_at, updated_at) VALUES (?,?,?,?)`, libID, "W", now, now); err != nil {
			t.Fatal(err)
		}
		res, err := db.Exec(`INSERT INTO editions (work_id, format, title, created_at) VALUES (1,'mp3','E',?)`, now)
		if err != nil {
			t.Fatal(err)
		}
		edID, _ := res.LastInsertId()
		res, err = db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, probed_at) VALUES (?,?,1,1,1,1,?)`, edID, filepath.Join(t.TempDir(), "book.mp3"), now)
		if err != nil {
			t.Fatal(err)
		}
		fileID, _ = res.LastInsertId()
		if _, err := db.Exec(`INSERT INTO progress (user_id, edition_id, file_id, updated_at) VALUES (1,?, ?, ?)`, edID, fileID, now); err != nil {
			t.Fatal(err)
		}
		return fileID
	}

	// roll back to pre-podcasts schema, then seed and re-apply 0005
	goose.SetBaseFS(nil)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownTo(db.DB, "migrations", 4); err != nil {
		t.Fatalf("down: %v", err)
	}
	var hasPodcasts bool
	db.QueryRow(`SELECT COUNT(*) > 0 FROM sqlite_master WHERE name = 'podcasts'`).Scan(&hasPodcasts)
	if hasPodcasts {
		t.Fatal("podcasts table survived DownTo(4)")
	}
	fileID := seed()
	if err := goose.Up(db.DB, "migrations"); err != nil {
		t.Fatalf("up: %v", err)
	}

	var path string
	var missing int
	if err := db.QueryRow(`SELECT path, missing FROM files WHERE id = ?`, fileID).Scan(&path, &missing); err != nil {
		t.Fatalf("file row lost in rebuild: %v", err)
	}
	if missing != 0 || !strings.HasSuffix(path, "book.mp3") {
		t.Fatalf("file row mangled: %q missing=%d", path, missing)
	}
	var progressCount int
	db.QueryRow(`SELECT COUNT(*) FROM progress WHERE file_id = ?`, fileID).Scan(&progressCount)
	if progressCount != 1 {
		t.Fatal("progress lost in rebuild")
	}

	// unique path index survived
	if _, err := db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, probed_at) VALUES (NULL,?,1,1,1,1,1)`, path); err == nil {
		t.Fatal("duplicate path accepted; unique index lost in rebuild")
	}

	// podcast files land with NULL edition_id
	podFileID, err := db.InsertPodcastFile(filepath.Join(t.TempDir(), "ep.mp3"), 10, 1, "abc-10", 90.0, "mp3")
	if err != nil {
		t.Fatalf("insert podcast file: %v", err)
	}
	var editionID sqlNullInt
	if err := db.QueryRow(`SELECT edition_id FROM files WHERE id = ?`, podFileID).Scan(&editionID); err != nil {
		t.Fatal(err)
	}
	if editionID.Valid {
		t.Fatalf("podcast file edition_id = %v, want NULL", editionID.Int64)
	}

	// FK enforcement still on after the rebuild
	if _, err := db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, probed_at) VALUES (999999,?,1,1,1,1,1)`, filepath.Join(t.TempDir(), "x.mp3")); err == nil {
		t.Fatal("FK to editions not enforced after rebuild")
	}
}
