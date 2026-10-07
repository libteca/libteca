package store_test

import (
	"strconv"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func resetGenEdition(t *testing.T, db *store.DB, userID int64) int64 {
	t.Helper()
	_, err := db.Exec(`INSERT INTO users (id, name, password_hash, is_admin, created_at, updated_at) VALUES (?,?, 'x',0,0,0)`, userID, strconv.FormatInt(userID, 10))
	if err != nil {
		t.Fatal(err)
	}
	return revisionTestEdition(t, db)
}

func TestDeleteProgressTombstonesAbsentRow(t *testing.T) {
	db := openTestDB(t)
	eid := resetGenEdition(t, db, 1)
	if err := db.DeleteProgress(1, eid); err != nil {
		t.Fatal(err)
	}
	p, err := db.GetReadingProgress(1, eid)
	if err != nil {
		t.Fatalf("absent-row reset left no tombstone: %v", err)
	}
	if !p.Deleted || p.Revision != 1 || p.ResetGeneration != 1 || p.Page != nil || p.IsFinished {
		t.Fatalf("absent-row tombstone = %+v, want deleted revision 1 generation 1 with cleared fields", p)
	}
	if _, _, applied, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid},
	}, store.ProgressFields{}, 0, 0); err != nil || applied {
		t.Fatalf("base-0 generation-0 write onto the fresh tombstone applied %v err %v", applied, err)
	}
	rev, _, applied, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid},
	}, store.ProgressFields{}, 1, 1)
	if err != nil || !applied || rev != 2 {
		t.Fatalf("deliberate restart on the tombstone = rev %d applied %v err %v", rev, applied, err)
	}
	p, err = db.GetReadingProgress(1, eid)
	if err != nil || p.Deleted || p.ResetGeneration != 1 || p.Revision != 2 {
		t.Fatalf("restart after absent-row reset = %+v err %v, want live revision 2 generation 1", p, err)
	}
}

func TestResetGenerationFenceRejectsPreResetOperations(t *testing.T) {
	db := openTestDB(t)
	eid := resetGenEdition(t, db, 1)
	page := int64(80)
	if _, _, _, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &page,
	}, store.ProgressFields{Position: false}, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteProgress(1, eid); err != nil {
		t.Fatal(err)
	}
	p, err := db.GetReadingProgress(1, eid)
	if err != nil || !p.Deleted || p.Revision != 2 || p.ResetGeneration != 1 {
		t.Fatalf("post-reset tombstone = %+v err %v, want deleted revision 2 generation 1", p, err)
	}
	if _, _, applied, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &page,
	}, store.ProgressFields{Position: false}, 2, 0); err != nil || applied {
		t.Fatalf("relabeled pre-reset operation accepted at the tombstone revision: applied %v err %v", applied, err)
	}
	fresh := int64(3)
	rev, gen, applied, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &fresh,
	}, store.ProgressFields{Position: false}, 2, 1)
	if err != nil || !applied || rev != 3 || gen != 1 {
		t.Fatalf("deliberate post-reset write = rev %d gen %d applied %v err %v", rev, gen, applied, err)
	}
	p, err = db.GetReadingProgress(1, eid)
	if err != nil || p.Deleted || p.Page == nil || *p.Page != 3 || p.ResetGeneration != 1 {
		t.Fatalf("post-reset state = %+v err %v, want live page 3 generation 1", p, err)
	}
	if _, _, applied, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &page,
	}, store.ProgressFields{Position: false}, 3, 0); err != nil || applied {
		t.Fatalf("pre-reset operation accepted after the tombstone was cleared: applied %v err %v", applied, err)
	}
	if _, _, applied, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &page,
	}, store.ProgressFields{Position: false}, 3, -1); err != nil || !applied {
		t.Fatalf("generation-unaware current-base write rejected: applied %v err %v", applied, err)
	}
	if err := db.DeleteProgress(1, eid); err != nil {
		t.Fatal(err)
	}
	p, err = db.GetReadingProgress(1, eid)
	if err != nil || p.ResetGeneration != 2 {
		t.Fatalf("second reset generation = %+v err %v, want generation 2", p, err)
	}
	if _, _, _, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &fresh,
	}, store.ProgressFields{Position: false}, 4, 1); err != nil {
		t.Fatal(err)
	}
	p, err = db.GetReadingProgress(1, eid)
	if err != nil || !p.Deleted {
		t.Fatalf("stale-generation write cleared the tombstone: %+v err %v", p, err)
	}
}
