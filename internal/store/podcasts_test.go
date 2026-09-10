package store_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/store"
	"github.com/pressly/goose/v3"
)

type EpisodeProgress = store.EpisodeProgress

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

// TestMigration0007PodcastEpisodeProgress rolls back to the pre-0007 schema,
// seeds podcasts/episodes, re-applies, and exercises the progress table:
// upsert on (user, episode), per-podcast lookup, latest-per-podcast, and both
// delete cascades.
func TestMigration0007PodcastEpisodeProgress(t *testing.T) {
	db := openTestDB(t)

	goose.SetBaseFS(nil)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownTo(db.DB, "migrations", 6); err != nil {
		t.Fatalf("down: %v", err)
	}
	var hasTable bool
	db.QueryRow(`SELECT COUNT(*) > 0 FROM sqlite_master WHERE name = 'podcast_episode_progress'`).Scan(&hasTable)
	if hasTable {
		t.Fatal("podcast_episode_progress survived DownTo(6)")
	}

	now := int64(1234)
	if _, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('u','h',1,?,?)`, now, now); err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('Podcasts','podcasts',?,?)`, t.TempDir(), now)
	if err != nil {
		t.Fatal(err)
	}
	libID, _ := res.LastInsertId()
	res, err = db.Exec(`INSERT INTO podcasts (library_id, feed_url, title, auto_download, max_episodes, created_at) VALUES (?,?,?,1,3,?)`, libID, "https://example.com/f.xml", "Show", now)
	if err != nil {
		t.Fatal(err)
	}
	podID, _ := res.LastInsertId()
	res, err = db.Exec(`INSERT INTO podcast_episodes (podcast_id, guid, title, duration_secs, enclosure_url, created_at) VALUES (?,?,?,?,?,?)`, podID, "g1", "Ep 1", 1800.0, "https://example.com/1.mp3", now)
	if err != nil {
		t.Fatal(err)
	}
	ep1, _ := res.LastInsertId()
	res, err = db.Exec(`INSERT INTO podcast_episodes (podcast_id, guid, title, duration_secs, enclosure_url, created_at) VALUES (?,?,?,?,?,?)`, podID, "g2", "Ep 2", 900.0, "https://example.com/2.mp3", now)
	if err != nil {
		t.Fatal(err)
	}
	ep2, _ := res.LastInsertId()

	if err := goose.Up(db.DB, "migrations"); err != nil {
		t.Fatalf("up: %v", err)
	}
	db.QueryRow(`SELECT COUNT(*) > 0 FROM sqlite_master WHERE name = 'podcast_episode_progress'`).Scan(&hasTable)
	if !hasTable {
		t.Fatal("podcast_episode_progress missing after Up")
	}

	if _, err := db.GetEpisodeProgress(1, ep1); err != store.ErrNotFound {
		t.Fatalf("empty lookup err = %v, want ErrNotFound", err)
	}
	dur := 1800.0
	if err := db.SetEpisodeProgress(&EpisodeProgress{UserID: 1, EpisodeID: ep1, PositionSecs: 300, DurationSecs: &dur}); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := db.SetEpisodeProgress(&EpisodeProgress{UserID: 1, EpisodeID: ep1, PositionSecs: 600, DurationSecs: &dur, IsFinished: true}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	p, err := db.GetEpisodeProgress(1, ep1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if p.PositionSecs != 600 || !p.IsFinished || p.DurationSecs == nil || *p.DurationSecs != 1800 {
		t.Fatalf("upsert did not take: %+v", p)
	}
	dur2 := 900.0
	if err := db.SetEpisodeProgress(&EpisodeProgress{UserID: 1, EpisodeID: ep2, PositionSecs: 100, DurationSecs: &dur2}); err != nil {
		t.Fatal(err)
	}
	// both writes can land in the same millisecond; pin the newest explicitly
	if _, err := db.Exec(`UPDATE podcast_episode_progress SET updated_at = updated_at + 60000 WHERE episode_id = ?`, ep2); err != nil {
		t.Fatal(err)
	}
	byPod, err := db.EpisodeProgressByPodcast(1, podID)
	if err != nil || len(byPod) != 2 {
		t.Fatalf("byPod = %v, %v; want 2 rows", byPod, err)
	}
	latest, err := db.LatestEpisodeProgressByPodcast(1)
	if err != nil || len(latest) != 1 {
		t.Fatalf("latest = %v, %v; want 1 podcast", latest, err)
	}
	if got := latest[podID]; got == nil || got.EpisodeID != ep2 {
		t.Fatalf("latest per podcast = %+v, want episode %d (newest update)", got, ep2)
	}

	// episode delete cascades: killing the podcast removes episodes, which
	// removes their progress rows
	if err := db.DeletePodcast(podID); err != nil {
		t.Fatal(err)
	}
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM podcast_episode_progress`).Scan(&n)
	if n != 0 {
		t.Fatalf("episode cascade left %d progress rows", n)
	}

	// user delete cascades too
	res, err = db.Exec(`INSERT INTO podcasts (library_id, feed_url, title, auto_download, max_episodes, created_at) VALUES (?,?,?,1,3,?)`, libID, "https://example.com/f2.xml", "Show 2", now)
	if err != nil {
		t.Fatal(err)
	}
	podID2, _ := res.LastInsertId()
	res, err = db.Exec(`INSERT INTO podcast_episodes (podcast_id, guid, title, enclosure_url, created_at) VALUES (?,?,?,?,?)`, podID2, "g3", "Ep 3", "https://example.com/3.mp3", now)
	if err != nil {
		t.Fatal(err)
	}
	ep3, _ := res.LastInsertId()
	if err := db.SetEpisodeProgress(&EpisodeProgress{UserID: 1, EpisodeID: ep3, PositionSecs: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM users WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	db.QueryRow(`SELECT COUNT(*) FROM podcast_episode_progress`).Scan(&n)
	if n != 0 {
		t.Fatalf("user cascade left %d progress rows", n)
	}
}
