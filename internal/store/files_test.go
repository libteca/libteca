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

	// A LIVE row whose path merely vanished does not relink yet: the
	// disappearance is unverified until reconciliation flags it missing.
	moved := &store.FileRec{EditionID: eid, Path: filepath.Join(dir, "here.m4b"), Seq: 1, SizeBytes: 4, MtimeSecs: 2, Hash: &hash, DurationSecs: 1, Chapters: "[]"}
	if err := db.UpsertFile(moved); err != nil {
		t.Fatal(err)
	}
	if !moved.Inserted || moved.ID == old.ID {
		t.Fatalf("unverified disappearance must insert: id=%d inserted=%v", moved.ID, moved.Inserted)
	}

	// After reconciliation marks the old row missing, a later move relinks.
	if _, err := db.Exec(`UPDATE files SET missing = 1 WHERE id = ?`, old.ID); err != nil {
		t.Fatal(err)
	}
	again := &store.FileRec{EditionID: eid, Path: filepath.Join(dir, "moved.m4b"), Seq: 1, SizeBytes: 4, MtimeSecs: 3, Hash: &hash, DurationSecs: 1, Chapters: "[]"}
	if err := db.UpsertFile(again); err != nil {
		t.Fatal(err)
	}
	if again.Inserted || again.ID != old.ID {
		t.Fatalf("verified disappearance should relink: id=%d inserted=%v want %d", again.ID, again.Inserted, old.ID)
	}
}

func TestUpsertFileRelinkScopesToLibrary(t *testing.T) {
	db := openTestDB(t)
	dir := t.TempDir()
	libA, err := db.AddLibrary("A", "audiobooks", dir)
	if err != nil {
		t.Fatal(err)
	}
	libB, err := db.AddLibrary("B", "audiobooks", dir)
	if err != nil {
		t.Fatal(err)
	}
	wA := &store.Work{LibraryID: libA, Title: "W"}
	widA, err := db.UpsertWork(wA)
	if err != nil {
		t.Fatal(err)
	}
	wB := &store.Work{LibraryID: libB, Title: "W"}
	widB, err := db.UpsertWork(wB)
	if err != nil {
		t.Fatal(err)
	}
	eA := &store.Edition{WorkID: widA, Format: "m4b", Title: "W"}
	eidA, err := db.UpsertEdition(eA)
	if err != nil {
		t.Fatal(err)
	}
	eB := &store.Edition{WorkID: widB, Format: "m4b", Title: "W"}
	eidB, err := db.UpsertEdition(eB)
	if err != nil {
		t.Fatal(err)
	}
	hash := "cross-4"
	old := &store.FileRec{EditionID: eidA, Path: filepath.Join(dir, "old.m4b"), Seq: 1, SizeBytes: 4, MtimeSecs: 1, Hash: &hash, DurationSecs: 1, Chapters: "[]"}
	if err := db.UpsertFile(old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE files SET missing = 1 WHERE id = ?`, old.ID); err != nil {
		t.Fatal(err)
	}
	other := &store.FileRec{EditionID: eidB, Path: filepath.Join(dir, "new.m4b"), Seq: 1, SizeBytes: 4, MtimeSecs: 2, Hash: &hash, DurationSecs: 1, Chapters: "[]"}
	if err := db.UpsertFile(other); err != nil {
		t.Fatal(err)
	}
	if !other.Inserted || other.ID == old.ID {
		t.Fatalf("cross-library relink must not happen: id=%d inserted=%v", other.ID, other.Inserted)
	}
}

func TestUpsertFileRelinkRequiresSizeMatch(t *testing.T) {
	db := openTestDB(t)
	eid := seedEdition(t, db)
	hash := "size-4"
	old := &store.FileRec{EditionID: eid, Path: "/old/a.m4b", Seq: 1, SizeBytes: 4, MtimeSecs: 1, Hash: &hash, DurationSecs: 1, Chapters: "[]"}
	if err := db.UpsertFile(old); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE files SET missing = 1 WHERE id = ?`, old.ID); err != nil {
		t.Fatal(err)
	}
	diffSize := &store.FileRec{EditionID: eid, Path: "/new/a.m4b", Seq: 1, SizeBytes: 5, MtimeSecs: 2, Hash: &hash, DurationSecs: 1, Chapters: "[]"}
	if err := db.UpsertFile(diffSize); err != nil {
		t.Fatal(err)
	}
	if !diffSize.Inserted || diffSize.ID == old.ID {
		t.Fatalf("same sampled hash with different size must insert: id=%d inserted=%v", diffSize.ID, diffSize.Inserted)
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
