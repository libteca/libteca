package store

import (
	"path/filepath"
	"testing"
)

func openLinkDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedLinkUser(t *testing.T, db *DB) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO users (name, password_hash, created_at, updated_at) VALUES ('u','x',0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func nptr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func seedLinkWork(t *testing.T, db *DB, title, author, description string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO works (library_id, title, author, description, created_at, updated_at) VALUES (1,?,?,?,0,0)`,
		title, nptr(author), nptr(description))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func seedLinkEdition(t *testing.T, db *DB, workID int64, title string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO editions (work_id, format, title, created_at) VALUES (?, 'm4b', ?, 0)`, workID, title)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func seedLinkFile(t *testing.T, db *DB, editionID int64, path string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, probed_at) VALUES (?,?,1,1,1,1,0)`,
		editionID, path); err != nil {
		t.Fatal(err)
	}
}

func linkCount(t *testing.T, db *DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestMoveEditionKeepsIDFilesProgress(t *testing.T) {
	db := openLinkDB(t)
	if _, err := db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('L','audiobooks','/x',0)`); err != nil {
		t.Fatal(err)
	}
	uid := seedLinkUser(t, db)
	w1 := seedLinkWork(t, db, "One", "A", "desc one")
	w2 := seedLinkWork(t, db, "Two", "B", "desc two")
	e1 := seedLinkEdition(t, db, w1, "One")
	seedLinkEdition(t, db, w1, "One (mp3)")
	seedLinkFile(t, db, e1, "/x/one.m4b")
	if _, err := db.Exec(`INSERT INTO progress (user_id, edition_id, edition_position_secs, updated_at) VALUES (?,?,42,0)`, uid, e1); err != nil {
		t.Fatal(err)
	}

	res, err := db.MoveEditionToWork(e1, w2)
	if err != nil {
		t.Fatal(err)
	}
	if res.TargetWorkID != w2 || res.SourceWorkID != w1 || res.SourceDeleted {
		t.Fatalf("res = %+v", res)
	}
	var gotWork int64
	if err := db.QueryRow(`SELECT work_id FROM editions WHERE id = ?`, e1).Scan(&gotWork); err != nil || gotWork != w2 {
		t.Fatalf("edition work_id = %d err %v", gotWork, err)
	}
	if n := linkCount(t, db, `SELECT count(*) FROM files WHERE edition_id = ? AND path = '/x/one.m4b'`, e1); n != 1 {
		t.Fatalf("file linkage lost: %d", n)
	}
	if n := linkCount(t, db, `SELECT count(*) FROM progress WHERE edition_id = ? AND edition_position_secs = 42`, e1); n != 1 {
		t.Fatalf("progress did not follow edition: %d", n)
	}
}

func TestMoveLastEditionDeletesSourceWork(t *testing.T) {
	db := openLinkDB(t)
	if _, err := db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('L','audiobooks','/x',0)`); err != nil {
		t.Fatal(err)
	}
	w1 := seedLinkWork(t, db, "One", "A", "")
	w2 := seedLinkWork(t, db, "Two", "B", "")
	e1 := seedLinkEdition(t, db, w1, "One")

	res, err := db.MoveEditionToWork(e1, w2)
	if err != nil {
		t.Fatal(err)
	}
	if !res.SourceDeleted {
		t.Fatalf("res = %+v, want sourceDeleted", res)
	}
	if n := linkCount(t, db, `SELECT count(*) FROM works WHERE id = ?`, w1); n != 0 {
		t.Fatalf("empty source work not deleted")
	}
	if n := linkCount(t, db, `SELECT count(*) FROM editions WHERE work_id = ?`, w2); n != 1 {
		t.Fatalf("edition missing from target: %d", n)
	}
}

func TestEnsureWorkInLibraryMatchKeepsMetadata(t *testing.T) {
	db := openLinkDB(t)
	if _, err := db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('L','audiobooks','/x',0)`); err != nil {
		t.Fatal(err)
	}
	w1 := seedLinkWork(t, db, "Title", "Author", "precious")

	author := "author"
	got, err := db.EnsureWorkInLibrary(&Work{LibraryID: 1, Title: "TITLE", Author: &author})
	if err != nil {
		t.Fatal(err)
	}
	if got != w1 {
		t.Fatalf("match failed: got %d want %d", got, w1)
	}
	var desc string
	if err := db.QueryRow(`SELECT coalesce(description,'') FROM works WHERE id = ?`, w1).Scan(&desc); err != nil || desc != "precious" {
		t.Fatalf("existing work metadata clobbered: %q %v", desc, err)
	}

	fresh, err := db.EnsureWorkInLibrary(&Work{LibraryID: 1, Title: "Other", Author: &author})
	if err != nil {
		t.Fatal(err)
	}
	if fresh == w1 {
		t.Fatalf("new work not created")
	}
	if n := linkCount(t, db, `SELECT count(*) FROM works WHERE library_id = 1`); n != 2 {
		t.Fatalf("works = %d, want 2", n)
	}
}

func TestMergeWorks(t *testing.T) {
	db := openLinkDB(t)
	if _, err := db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('L','audiobooks','/x',0)`); err != nil {
		t.Fatal(err)
	}
	uid := seedLinkUser(t, db)
	w1 := seedLinkWork(t, db, "One", "A", "")
	w2 := seedLinkWork(t, db, "Two", "B", "")
	e1 := seedLinkEdition(t, db, w1, "One")
	e2 := seedLinkEdition(t, db, w1, "One (mp3)")
	e3 := seedLinkEdition(t, db, w2, "Two")
	if _, err := db.Exec(`INSERT INTO progress (user_id, edition_id, edition_position_secs, updated_at) VALUES (?,?,7,0)`, uid, e1); err != nil {
		t.Fatal(err)
	}
	_ = e2
	_ = e3

	if err := db.MergeWorks(w1, w2); err != nil {
		t.Fatal(err)
	}
	if n := linkCount(t, db, `SELECT count(*) FROM editions WHERE work_id = ?`, w2); n != 3 {
		t.Fatalf("target editions = %d, want 3", n)
	}
	if n := linkCount(t, db, `SELECT count(*) FROM works WHERE id = ?`, w1); n != 0 {
		t.Fatalf("source work not deleted")
	}
	if n := linkCount(t, db, `SELECT count(*) FROM progress WHERE edition_id = ? AND edition_position_secs = 7`, e1); n != 1 {
		t.Fatalf("progress lost in merge: %d", n)
	}
	if err := db.MergeWorks(w2, w2); err != ErrSameWork {
		t.Fatalf("self-merge err = %v, want ErrSameWork", err)
	}
}

func TestSplitEditionToNewWork(t *testing.T) {
	db := openLinkDB(t)
	if _, err := db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('L','audiobooks','/x',0)`); err != nil {
		t.Fatal(err)
	}
	w1 := seedLinkWork(t, db, "Collected", "A", "")
	e1 := seedLinkEdition(t, db, w1, "First")
	e2 := seedLinkEdition(t, db, w1, "Second")
	seedLinkFile(t, db, e2, "/x/second.m4b")

	res, err := db.SplitEditionToNewWork(e2, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.TargetWorkID == w1 {
		t.Fatalf("res = %+v, want new work", res)
	}
	var title, author string
	if err := db.QueryRow(`SELECT w.title, coalesce(w.author,'') FROM works w WHERE w.id = ?`, res.TargetWorkID).Scan(&title, &author); err != nil {
		t.Fatal(err)
	}
	if title != "Second" || author != "A" {
		t.Fatalf("split work = %q/%q, want Second/A", title, author)
	}
	if n := linkCount(t, db, `SELECT count(*) FROM editions WHERE work_id = ?`, w1); n != 1 {
		t.Fatalf("source kept editions = %d, want 1", n)
	}

	// Splitting the last edition must delete the emptied source work.
	res, err = db.SplitEditionToNewWork(e1, "Solo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.SourceDeleted || res.SourceWorkID != w1 {
		t.Fatalf("res = %+v, want source deleted", res)
	}
}
func TestMergeWorksUnknownTargetRollsBack(t *testing.T) {
	db := openLinkDB(t)
	if _, err := db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('L','audiobooks','/x',0)`); err != nil {
		t.Fatal(err)
	}
	w1 := seedLinkWork(t, db, "One", "A", "")
	e1 := seedLinkEdition(t, db, w1, "One")

	if err := db.MergeWorks(w1, 999); err != ErrNotFound {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if n := linkCount(t, db, `SELECT count(*) FROM editions WHERE work_id = ?`, w1); n != 1 {
		t.Fatalf("editions moved despite rollback: %d", n)
	}
	if _, err := db.MoveEditionToWork(999, w1); err != ErrNotFound {
		t.Fatalf("move unknown edition err = %v, want ErrNotFound", err)
	}
	_ = e1
}
