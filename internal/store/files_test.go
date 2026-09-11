package store_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func seedEdition(t *testing.T, db *store.DB) int64 {
	t.Helper()
	lib, err := db.AddLibrary("L", "audiobooks", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := &store.Work{LibraryID: lib, Title: "W"}
	wid, err := db.UpsertWork(w)
	if err != nil {
		t.Fatal(err)
	}
	e := &store.Edition{WorkID: wid, Format: "m4b", Title: "W"}
	eid, err := db.UpsertEdition(e)
	if err != nil {
		t.Fatal(err)
	}
	return eid
}

func TestUpsertFileRelinkMissingHash(t *testing.T) {
	db := openTestDB(t)
	eid := seedEdition(t, db)
	hash := "abc-10"
	old := &store.FileRec{EditionID: eid, Path: "/old/a.m4b", Seq: 1, SizeBytes: 10, MtimeSecs: 1, Hash: &hash, DurationSecs: 5, Chapters: "[]"}
	if err := db.UpsertFile(old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE files SET missing = 1 WHERE id = ?`, old.ID); err != nil {
		t.Fatal(err)
	}

	moved := &store.FileRec{EditionID: eid, Path: "/new/a.m4b", Seq: 1, SizeBytes: 10, MtimeSecs: 2, Hash: &hash, DurationSecs: 5, Chapters: "[]"}
	if err := db.UpsertFile(moved); err != nil {
		t.Fatal(err)
	}
	if moved.Inserted || moved.ID != old.ID {
		t.Fatalf("inserted=%v id=%d want %d", moved.Inserted, moved.ID, old.ID)
	}
	var path string
	var missing int
	if err := db.QueryRow(`SELECT path, missing FROM files WHERE id = ?`, old.ID).Scan(&path, &missing); err != nil {
		t.Fatal(err)
	}
	if path != "/new/a.m4b" || missing != 0 {
		t.Fatalf("path=%s missing=%d", path, missing)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM files`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("count = %d %v", n, err)
	}
}

func TestUpsertFileGonePathRelink(t *testing.T) {
	db := openTestDB(t)
	eid := seedEdition(t, db)
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "gone.m4b")
	hash := "xyz-4"
	old := &store.FileRec{EditionID: eid, Path: oldPath, Seq: 1, SizeBytes: 4, MtimeSecs: 1, Hash: &hash, DurationSecs: 1, Chapters: "[]"}
	if err := db.UpsertFile(old); err != nil {
		t.Fatal(err)
	}

	moved := &store.FileRec{EditionID: eid, Path: filepath.Join(dir, "here.m4b"), Seq: 1, SizeBytes: 4, MtimeSecs: 2, Hash: &hash, DurationSecs: 1, Chapters: "[]"}
	if err := db.UpsertFile(moved); err != nil {
		t.Fatal(err)
	}
	if moved.ID != old.ID || moved.Inserted {
		t.Fatalf("id=%d inserted=%v want %d", moved.ID, moved.Inserted, old.ID)
	}
}

func TestUpsertFileLiveCopyInserts(t *testing.T) {
	db := openTestDB(t)
	eid := seedEdition(t, db)
	dir := t.TempDir()
	aPath := filepath.Join(dir, "a.m4b")
	bPath := filepath.Join(dir, "b.m4b")
	if err := os.WriteFile(aPath, []byte("xxxx"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte("xxxx"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash := "copy-4"
	a := &store.FileRec{EditionID: eid, Path: aPath, Seq: 1, SizeBytes: 4, MtimeSecs: 1, Hash: &hash, DurationSecs: 1, Chapters: "[]"}
	if err := db.UpsertFile(a); err != nil {
		t.Fatal(err)
	}
	b := &store.FileRec{EditionID: eid, Path: bPath, Seq: 2, SizeBytes: 4, MtimeSecs: 1, Hash: &hash, DurationSecs: 1, Chapters: "[]"}
	if err := db.UpsertFile(b); err != nil {
		t.Fatal(err)
	}
	if !b.Inserted || b.ID == a.ID {
		t.Fatalf("copy should insert: inserted=%v id=%d vs %d", b.Inserted, b.ID, a.ID)
	}
}
