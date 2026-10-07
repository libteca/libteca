package store

import (
	"path/filepath"
	"strconv"
	"testing"
)

func seedPodcastLibraryWithDependencies(t *testing.T, d *DB) (int64, int64, int64) {
	t.Helper()
	now := nowMilli()
	res, err := d.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('Pods', 'podcasts', '/tmp/pods', ?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	libID, _ := res.LastInsertId()
	ures, err := d.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('u', 'x', 0, ?, ?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := ures.LastInsertId()
	wres, err := d.Exec(`INSERT INTO works (library_id, title, created_at, updated_at) VALUES (?, 'Imported Cast', ?, ?)`, libID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	workID, _ := wres.LastInsertId()
	eres, err := d.Exec(`INSERT INTO editions (work_id, format, title, created_at) VALUES (?, 'mp3', 'Imported Cast', ?)`, workID, now)
	if err != nil {
		t.Fatal(err)
	}
	edID, _ := eres.LastInsertId()
	if _, err := d.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, chapters, embedded_meta, missing, probed_at)
		VALUES (?, '/tmp/pods/imported.mp3', 1, 10, 1, 60, '[]', '{}', 0, ?)`, edID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO progress (user_id, edition_id, edition_position_secs, is_finished, updated_at) VALUES (?, ?, 30, 0, ?)`, uid, edID, now); err != nil {
		t.Fatal(err)
	}
	plID, err := d.CreatePlaylist(uid, "Mix")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.AddPlaylistItem(plID, edID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`INSERT INTO playback_sessions (id, user_id, edition_id, started_at, updated_at) VALUES ('s1', ?, ?, 0, 0)`, uid, edID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreateScanJob(libID); err != nil {
		t.Fatal(err)
	}
	return libID, workID, edID
}

func countAuditD(t *testing.T, d *DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := d.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeletePodcastsLibraryRemovesEditionBackedWorks(t *testing.T) {
	d := openAuditDDB(t)
	libID, _, edID := seedPodcastLibraryWithDependencies(t, d)
	if _, err := d.DeletePodcastsLibrary(libID); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if _, err := d.Library(libID); err != ErrNotFound {
		t.Fatalf("library still present: %v", err)
	}
	for q, want := range map[string]int{
		`SELECT count(*) FROM works WHERE library_id = ?`:             0,
		`SELECT count(*) FROM editions WHERE id = ?`:                  0,
		`SELECT count(*) FROM files WHERE edition_id = ?`:             0,
		`SELECT count(*) FROM progress WHERE edition_id = ?`:          0,
		`SELECT count(*) FROM playlist_items WHERE edition_id = ?`:    0,
		`SELECT count(*) FROM playback_sessions WHERE edition_id = ?`: 0,
		`SELECT count(*) FROM scan_jobs WHERE library_id = ?`:         0,
	} {
		if got := countAuditD(t, d, q, edID); got != want && q != `SELECT count(*) FROM works WHERE library_id = ?` && q != `SELECT count(*) FROM scan_jobs WHERE library_id = ?` {
			t.Fatalf("%s = %d, want %d", q, got, want)
		}
	}
	if got := countAuditD(t, d, `SELECT count(*) FROM works WHERE library_id = ?`, libID); got != 0 {
		t.Fatalf("works remain: %d", got)
	}
	if got := countAuditD(t, d, `SELECT count(*) FROM scan_jobs WHERE library_id = ?`, libID); got != 0 {
		t.Fatalf("scan jobs remain: %d", got)
	}
}

func TestDeletePodcastsLibraryWithNativeSubscriptionsAndScanJobs(t *testing.T) {
	d := openAuditDDB(t)
	now := nowMilli()
	res, err := d.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('Native', 'podcasts', '/tmp/native', ?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	libID, _ := res.LastInsertId()
	pres, err := d.Exec(`INSERT INTO podcasts (library_id, title, feed_url, created_at) VALUES (?, 'Cast', 'https://example.com/feed', ?)`, libID, now)
	if err != nil {
		t.Fatal(err)
	}
	podID, _ := pres.LastInsertId()
	fres, err := d.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, chapters, embedded_meta, missing, probed_at)
		VALUES (NULL, '/tmp/native/ep1.mp3', 1, 10, 1, 60, '[]', '{}', 0, ?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	fid, _ := fres.LastInsertId()
	if _, err := d.Exec(`INSERT INTO podcast_episodes (podcast_id, guid, title, enclosure_url, file_id, created_at) VALUES (?, 'g1', 'Ep', 'https://example.com/e1', ?, ?)`, podID, fid, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreateScanJob(libID); err != nil {
		t.Fatal(err)
	}
	fileIDs, err := d.DeletePodcastsLibrary(libID)
	if err != nil {
		t.Fatalf("native library with scan job delete failed: %v", err)
	}
	if len(fileIDs) != 1 || fileIDs[0] != fid {
		t.Fatalf("captured episode file ids = %v, want [%d]", fileIDs, fid)
	}
	if _, err := d.Library(libID); err != ErrNotFound {
		t.Fatalf("library still present: %v", err)
	}
	if got := countAuditD(t, d, `SELECT count(*) FROM scan_jobs WHERE library_id = ?`, libID); got != 0 {
		t.Fatalf("scan jobs remain: %d", got)
	}
	if got := countAuditD(t, d, `SELECT count(*) FROM files WHERE id = ?`, fid); got != 0 {
		t.Fatalf("episode file remains: %d", got)
	}
}

func TestDeletePodcastsLibraryEmptyControlAndUnrelatedUntouched(t *testing.T) {
	d := openAuditDDB(t)
	now := nowMilli()
	res, err := d.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('Empty', 'podcasts', '/tmp/empty', ?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	emptyID, _ := res.LastInsertId()
	otherRes, err := d.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('Books', 'books', '/tmp/books', ?)`, now)
	if err != nil {
		t.Fatal(err)
	}
	otherID, _ := otherRes.LastInsertId()
	ores, err := d.Exec(`INSERT INTO works (library_id, title, created_at, updated_at) VALUES (?, 'Kept', ?, ?)`, otherID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	otherWork, _ := ores.LastInsertId()
	if _, err := d.DeletePodcastsLibrary(emptyID); err != nil {
		t.Fatalf("empty library delete failed: %v", err)
	}
	if _, err := d.Library(emptyID); err != ErrNotFound {
		t.Fatalf("empty library still present: %v", err)
	}
	if got := countAuditD(t, d, `SELECT count(*) FROM works WHERE id = ?`, otherWork); got != 1 {
		t.Fatalf("unrelated library work removed: %d", got)
	}
	if _, err := d.Library(otherID); err != nil {
		t.Fatalf("unrelated library removed: %v", err)
	}
}

func TestDeletePodcastsLibraryFailureIsAtomic(t *testing.T) {
	d := openAuditDDB(t)
	libID, _, edID := seedPodcastLibraryWithDependencies(t, d)
	now := nowMilli()
	if _, err := d.Exec(`INSERT INTO podcasts (library_id, title, feed_url, created_at) VALUES (?, 'Cast', 'https://example.com/feed', ?)`, libID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Exec(`CREATE TRIGGER block_podcast_delete BEFORE DELETE ON podcasts WHEN OLD.library_id = ` + strconv.FormatInt(libID, 10) + ` BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DeletePodcastsLibrary(libID); err == nil {
		t.Fatal("injected failure must surface")
	}
	if _, err := d.Library(libID); err != nil {
		t.Fatalf("library removed despite failed transaction: %v", err)
	}
	if got := countAuditD(t, d, `SELECT count(*) FROM works WHERE library_id = ?`, libID); got != 1 {
		t.Fatalf("works removed despite rollback: %d", got)
	}
	if got := countAuditD(t, d, `SELECT count(*) FROM files WHERE edition_id = ?`, edID); got != 1 {
		t.Fatalf("files removed despite rollback: %d", got)
	}
	if got := countAuditD(t, d, `SELECT count(*) FROM scan_jobs WHERE library_id = ?`, libID); got != 1 {
		t.Fatalf("scan jobs removed despite rollback: %d", got)
	}
}

func openAuditDDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
