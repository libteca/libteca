package store_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func seedPatchEdition(t *testing.T, db *store.DB) (libID, workID, editionID int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('u','x',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	libID, err := db.AddLibrary("books", "audiobooks", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(`INSERT INTO works (library_id, title, created_at, updated_at) VALUES (?, 'W', 0, 0)`, libID)
	if err != nil {
		t.Fatal(err)
	}
	workID, _ = res.LastInsertId()
	eres, err := db.Exec(`INSERT INTO editions (work_id, format, title, duration_secs, created_at) VALUES (?, 'mp3', 'W', 3600, 0)`, workID)
	if err != nil {
		t.Fatal(err)
	}
	editionID, _ = eres.LastInsertId()
	fres, err := db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, chapters, embedded_meta, missing, probed_at) VALUES (?, '/lib/a.mp3', 1, 10, 1, 3600, '[]', '{}', 0, 0)`, editionID)
	if err != nil {
		t.Fatal(err)
	}
	fid, _ := fres.LastInsertId()
	if _, err := db.Exec(`INSERT INTO progress (user_id, edition_id, file_id, file_offset_secs, edition_position_secs, duration_secs, is_finished, device, updated_at)
		VALUES (1, ?, ?, 42, 642, 3600, 0, 'pixel', 1)`, editionID, fid); err != nil {
		t.Fatal(err)
	}
	return libID, workID, editionID
}

func TestProgressFieldsOmittedPositionPreservesPlaybackState(t *testing.T) {
	db := openTestDB(t)
	_, _, editionID := seedPatchEdition(t, db)

	finished := true
	p := &store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: editionID, IsFinished: finished},
	}
	if err := db.SetReadingProgressFields(p, store.ProgressFields{Finished: true}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetReadingProgress(1, editionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EditionPositionSecs != 642 || got.FileOffsetSecs != 42 {
		t.Fatalf("position reset by finished-only patch: %+v", got.Progress)
	}
	if got.DurationSecs == nil || *got.DurationSecs != 3600 {
		t.Fatalf("duration wiped by finished-only patch: %v", got.DurationSecs)
	}
	if got.Device == nil || *got.Device != "pixel" {
		t.Fatalf("device wiped by finished-only patch: %v", got.Device)
	}
	if !got.IsFinished {
		t.Fatal("finished flag not applied")
	}

	page := int64(13)
	pp := &store.ReadingProgress{Page: &page, Progress: store.Progress{UserID: 1, EditionID: editionID}}
	if err := db.SetReadingProgressFields(pp, store.ProgressFields{}); err != nil {
		t.Fatal(err)
	}
	got, err = db.GetReadingProgress(1, editionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EditionPositionSecs != 642 || got.Page == nil || *got.Page != 13 {
		t.Fatalf("page-only patch damaged playback state: %+v", got)
	}
}

func TestProgressFieldsExplicitPositionApplies(t *testing.T) {
	db := openTestDB(t)
	_, _, editionID := seedPatchEdition(t, db)

	pos := 0.0
	var fid int64
	if err := db.QueryRow(`SELECT id FROM files WHERE edition_id = ?`, editionID).Scan(&fid); err != nil {
		t.Fatal(err)
	}
	p := &store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: editionID, FileID: &fid, EditionPositionSecs: pos},
	}
	if err := db.SetReadingProgressFields(p, store.ProgressFields{Position: true}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetReadingProgress(1, editionID)
	if err != nil {
		t.Fatal(err)
	}
	if got.EditionPositionSecs != 0 {
		t.Fatalf("explicit zero position not applied: %+v", got.Progress)
	}
	if got.Device == nil || *got.Device != "pixel" {
		t.Fatalf("explicit zero position must not wipe device: %v", got.Device)
	}
}

func TestAddLibraryCheckedRejectsOverlap(t *testing.T) {
	db := openTestDB(t)
	root := t.TempDir()
	if _, err := db.AddLibraryChecked("a", "books", root, nil); err != nil {
		t.Fatal(err)
	}
	contains := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && (rel == "." || (rel != ".." && filepath.IsLocal(rel)))
	}
	checked := func(candidate string) func(string) bool {
		return func(existing string) bool {
			return contains(existing, candidate) || contains(candidate, existing)
		}
	}
	_, err := db.AddLibraryChecked("b", "books", filepath.Join(root, "sub"), checked(filepath.Join(root, "sub")))
	if !errors.Is(err, store.ErrLibraryOverlap) {
		t.Fatalf("err = %v, want ErrLibraryOverlap", err)
	}
	fresh := t.TempDir()
	id, err := db.AddLibraryChecked("c", "books", fresh, checked(fresh))
	if err != nil || id == 0 {
		t.Fatalf("non-overlapping insert failed: %v", err)
	}
}

func TestPurgeEpisodeSingleTransaction(t *testing.T) {
	db := openTestDB(t)
	libID, err := db.AddLibrary("casts", "podcasts", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pres, err := db.Exec(`INSERT INTO podcasts (library_id, feed_url, title, auto_download, max_episodes, created_at) VALUES (?, 'https://x/feed', 'X', 0, 1, 0)`, libID)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := pres.LastInsertId()
	fres, err := db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, chapters, embedded_meta, missing, probed_at) VALUES (NULL, '/data/podcasts/1/e.mp3', 1, 3, 0, 0, '[]', '{}', 0, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	fid, _ := fres.LastInsertId()
	eres, err := db.Exec(`INSERT INTO podcast_episodes (podcast_id, guid, enclosure_url, file_id, downloaded_at, created_at) VALUES (?, 'g', 'https://x/e.mp3', ?, 1, 0)`, pid, fid)
	if err != nil {
		t.Fatal(err)
	}
	epID, _ := eres.LastInsertId()
	if err := db.PurgeEpisode(epID, fid); err != nil {
		t.Fatal(err)
	}
	var missing int
	var link any
	if err := db.QueryRow(`SELECT missing FROM files WHERE id = ?`, fid).Scan(&missing); err != nil || missing != 1 {
		t.Fatalf("file missing = %d err = %v", missing, err)
	}
	if err := db.QueryRow(`SELECT file_id FROM podcast_episodes WHERE id = ?`, epID).Scan(&link); err != nil {
		t.Fatal(err)
	}
	if link != nil {
		t.Fatalf("episode file link = %v, want NULL", link)
	}
}
