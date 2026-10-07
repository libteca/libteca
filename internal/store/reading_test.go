package store_test

import (
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func TestMigration0004AudioFormatInsert(t *testing.T) {
	db := openTestDB(t)
	libID, err := db.AddLibrary("music", "music", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w := &store.Work{LibraryID: libID, Title: "Album"}
	workID, err := db.UpsertWork(w)
	if err != nil {
		t.Fatal(err)
	}
	// The exact insert the music scanner performs; 0001's CHECK rejected
	// format='audio' before 0004 widened it.
	dur := 180.5
	e := &store.Edition{WorkID: workID, Format: "audio", Title: "Track 1", DurationSecs: &dur}
	if _, err := db.UpsertEdition(e); err != nil {
		t.Fatalf("format='audio' insert failed: %v", err)
	}
}

func TestReadingProgressRoundtrip(t *testing.T) {
	db := openTestDB(t)
	user := addUser(t, db)
	libID, _ := db.AddLibrary("books", "books", t.TempDir())
	workID, err := db.UpsertWork(&store.Work{LibraryID: libID, Title: "The Trial"})
	if err != nil {
		t.Fatal(err)
	}
	pages := 320
	eid, err := db.UpsertEditionPages(&store.EditionPages{
		Edition:   store.Edition{WorkID: workID, Format: "epub", Title: "The Trial"},
		PageCount: &pages,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.GetReadingProgress(user, eid); err != store.ErrNotFound {
		t.Fatalf("get before set = %v, want ErrNotFound", err)
	}

	page, pct, loc := int64(42), 0.5, "chap3.xhtml"
	if err := db.SetReadingProgress(&store.ReadingProgress{
		Progress: store.Progress{UserID: user, EditionID: eid},
		Page:     &page, Percent: &pct, Locator: &loc,
	}); err != nil {
		t.Fatal(err)
	}
	p, err := db.GetReadingProgress(user, eid)
	if err != nil {
		t.Fatal(err)
	}
	if p.Page == nil || *p.Page != 42 || p.Percent == nil || *p.Percent != 0.5 || p.Locator == nil || *p.Locator != "chap3.xhtml" {
		t.Fatalf("roundtrip = page:%v percent:%v locator:%v", p.Page, p.Percent, p.Locator)
	}

	// Audio-style post without reading fields must not wipe page state.
	if err := db.SetReadingProgress(&store.ReadingProgress{
		Progress: store.Progress{UserID: user, EditionID: eid, EditionPositionSecs: 0, IsFinished: false},
	}); err != nil {
		t.Fatal(err)
	}
	p, err = db.GetReadingProgress(user, eid)
	if err != nil {
		t.Fatal(err)
	}
	if p.Page == nil || *p.Page != 42 || p.Percent == nil || p.Locator == nil {
		t.Fatalf("audio-style post wiped reading fields: %+v", p)
	}

	// Page-only update keeps percent/locator.
	next := int64(100)
	if err := db.SetReadingProgress(&store.ReadingProgress{
		Progress: store.Progress{UserID: user, EditionID: eid},
		Page:     &next,
	}); err != nil {
		t.Fatal(err)
	}
	p, _ = db.GetReadingProgress(user, eid)
	if *p.Page != 100 || *p.Percent != 0.5 || *p.Locator != "chap3.xhtml" {
		t.Fatalf("page-only update = %+v", p)
	}
}

func TestPageCountsByWork(t *testing.T) {
	db := openTestDB(t)
	libID, _ := db.AddLibrary("books", "books", t.TempDir())
	workID, _ := db.UpsertWork(&store.Work{LibraryID: libID, Title: "W"})
	epub, cbz := 312, 48
	e1, err := db.UpsertEditionPages(&store.EditionPages{Edition: store.Edition{WorkID: workID, Format: "epub", Title: "W"}, PageCount: &epub})
	if err != nil {
		t.Fatal(err)
	}
	e2, err := db.UpsertEditionPages(&store.EditionPages{Edition: store.Edition{WorkID: workID, Format: "cbz", Title: "W"}, PageCount: &cbz})
	if err != nil {
		t.Fatal(err)
	}
	dur := 0.0
	e3, err := db.UpsertEdition(&store.Edition{WorkID: workID, Format: "m4b", Title: "W", DurationSecs: &dur})
	if err != nil {
		t.Fatal(err)
	}
	pcs, err := db.PageCountsByWork(workID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pcs) != 2 || *pcs[e1] != 312 || *pcs[e2] != 48 {
		t.Fatalf("page counts = %v, want epub 312 cbz 48 (no m4b)", pcs)
	}
	if _, ok := pcs[e3]; ok {
		t.Fatal("m4b edition must not carry a page count")
	}
}

func revisionTestEdition(t *testing.T, db *store.DB) int64 {
	t.Helper()
	libID, err := db.AddLibrary("books", "books", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workID, err := db.UpsertWork(&store.Work{LibraryID: libID, Title: "W"})
	if err != nil {
		t.Fatal(err)
	}
	eid, err := db.UpsertEdition(&store.Edition{WorkID: workID, Format: "epub", Title: "W"})
	if err != nil {
		t.Fatal(err)
	}
	return eid
}

func TestSetReadingProgressRevisionRejectsStaleBase(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO users (id, name, password_hash, is_admin, created_at, updated_at) VALUES (1,'u','x',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	eid := revisionTestEdition(t, db)
	page := int64(10)
	rev, _, applied, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &page,
	}, store.ProgressFields{Position: false}, 0, -1)
	if err != nil || !applied {
		t.Fatalf("first revisioned write = rev %d applied %v err %v", rev, applied, err)
	}
	if rev != 1 {
		t.Fatalf("first write revision = %d, want 1", rev)
	}
	_, _, applied, err = db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &page,
	}, store.ProgressFields{Position: false}, 0, -1)
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("stale base revision accepted")
	}
	next := int64(11)
	rev, _, applied, err = db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &next,
	}, store.ProgressFields{Position: false}, 1, -1)
	if err != nil || !applied || rev != 2 {
		t.Fatalf("current-base write = rev %d applied %v err %v", rev, applied, err)
	}
	p, err := db.GetReadingProgress(1, eid)
	if err != nil || p.Revision != 2 || p.Page == nil || *p.Page != 11 {
		t.Fatalf("state after revisioned writes = %+v err %v", p, err)
	}
}

func TestLegacyProgressWritesAdvanceRevision(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO users (id, name, password_hash, is_admin, created_at, updated_at) VALUES (1,'u','x',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	eid := revisionTestEdition(t, db)
	page := int64(10)
	if _, _, _, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &page,
	}, store.ProgressFields{Position: false}, 0, -1); err != nil {
		t.Fatal(err)
	}
	if err := db.SetReadingProgressFields(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid},
	}, store.ProgressFields{}); err != nil {
		t.Fatal(err)
	}
	p, err := db.GetReadingProgress(1, eid)
	if err != nil || p.Revision != 2 {
		t.Fatalf("legacy write did not advance revision: %+v err %v", p, err)
	}
	_, _, applied, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &page,
	}, store.ProgressFields{Position: false}, 1, -1)
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("revision base held across a legacy write")
	}
	if err := db.SetProgress(&store.Progress{UserID: 1, EditionID: eid, EditionPositionSecs: 5}); err != nil {
		t.Fatal(err)
	}
	p, err = db.GetReadingProgress(1, eid)
	if err != nil || p.Revision != 3 {
		t.Fatalf("audio-style write did not advance revision: %+v err %v", p, err)
	}
}

func TestDeleteProgressResetsRevisionLineage(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO users (id, name, password_hash, is_admin, created_at, updated_at) VALUES (1,'u','x',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	eid := revisionTestEdition(t, db)
	page := int64(10)
	if _, _, _, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &page,
	}, store.ProgressFields{Position: false}, 0, -1); err != nil {
		t.Fatal(err)
	}
	_, _, applied, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &page,
	}, store.ProgressFields{Position: false}, 99, -1)
	if err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("absent-row conditional write with a positive base must not apply")
	}
	next := int64(11)
	if _, _, _, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &next,
	}, store.ProgressFields{Position: false}, 1, -1); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteProgress(1, eid); err != nil {
		t.Fatal(err)
	}
	p, err := db.GetReadingProgress(1, eid)
	if err != nil || !p.Deleted || p.Revision != 3 || p.Page != nil || p.Percent != nil || p.Locator != nil || p.IsFinished {
		t.Fatalf("tombstone = %+v err %v, want deleted revision 3 with cleared fields", p, err)
	}
	if _, _, applied, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &page,
	}, store.ProgressFields{Position: false}, 1, -1); err != nil || applied {
		t.Fatalf("stale pre-delete base accepted: applied %v err %v", applied, err)
	}
	if _, _, applied, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &page,
	}, store.ProgressFields{Position: false}, 0, -1); err != nil || applied {
		t.Fatalf("base-0 insert onto a tombstone must conflict: applied %v err %v", applied, err)
	}
	if _, err := db.GetProgress(1, eid); err != store.ErrNotFound {
		t.Fatalf("compatibility reader must skip the tombstone: %v", err)
	}
	rev, _, applied, err := db.SetReadingProgressRevision(&store.ReadingProgress{
		Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &page,
	}, store.ProgressFields{Position: false}, 3, -1)
	if err != nil || !applied || rev != 4 {
		t.Fatalf("conditional restart on the tombstone revision = rev %d applied %v err %v, want rev 4", rev, applied, err)
	}
	p, err = db.GetReadingProgress(1, eid)
	if err != nil || p.Deleted || p.Revision != 4 || p.Page == nil || *p.Page != 10 {
		t.Fatalf("restarted progress = %+v err %v", p, err)
	}
}

func TestSetReadingProgressAppliesFullState(t *testing.T) {
	db := openTestDB(t)
	user := addUser(t, db)
	eid := revisionTestEdition(t, db)
	dur := 100.0
	dev := "tab-a"
	if err := db.SetReadingProgress(&store.ReadingProgress{
		Progress: store.Progress{UserID: user, EditionID: eid, FileOffsetSecs: 5, EditionPositionSecs: 5, DurationSecs: &dur, Device: &dev, IsFinished: false},
	}); err != nil {
		t.Fatal(err)
	}
	dev2 := "tab-b"
	if err := db.SetReadingProgress(&store.ReadingProgress{
		Progress: store.Progress{UserID: user, EditionID: eid, FileOffsetSecs: 42, EditionPositionSecs: 42, DurationSecs: &dur, Device: &dev2, IsFinished: true},
	}); err != nil {
		t.Fatal(err)
	}
	p, err := db.GetReadingProgress(user, eid)
	if err != nil {
		t.Fatal(err)
	}
	if p.FileOffsetSecs != 42 || p.EditionPositionSecs != 42 ||
		p.Device == nil || *p.Device != "tab-b" || !p.IsFinished {
		t.Fatalf("full-state update did not apply: %+v", p)
	}
}

func TestProgressReadersReportRevision(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO users (id, name, password_hash, is_admin, created_at, updated_at) VALUES (1,'u','x',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	eid := revisionTestEdition(t, db)
	page := int64(3)
	for i := 0; i < 3; i++ {
		if _, _, _, err := db.SetReadingProgressRevision(&store.ReadingProgress{
			Progress: store.Progress{UserID: 1, EditionID: eid}, Page: &page,
		}, store.ProgressFields{}, int64(i), -1); err != nil {
			t.Fatal(err)
		}
	}
	single, err := db.GetProgress(1, eid)
	if err != nil || single.Revision != 3 {
		t.Fatalf("GetProgress revision = %d err %v, want 3", single.Revision, err)
	}
	list, err := db.UserProgressList(1)
	if err != nil || len(list) != 1 || list[0].Revision != 3 {
		t.Fatalf("UserProgressList revision = %+v err %v", list, err)
	}
	reading, err := db.ReadingListByUser(1)
	if err != nil || len(reading) != 1 || reading[eid].Revision != 3 {
		t.Fatalf("ReadingListByUser revision = %+v err %v", reading, err)
	}
	if err := db.DeleteProgress(1, eid); err != nil {
		t.Fatal(err)
	}
	if list, err := db.UserProgressList(1); err != nil || len(list) != 0 {
		t.Fatalf("tombstone leaked into UserProgressList: %+v err %v", list, err)
	}
	if reading, err := db.ReadingListByUser(1); err != nil || len(reading) != 0 {
		t.Fatalf("tombstone leaked into ReadingListByUser: %+v err %v", reading, err)
	}
	if eds, err := db.EditionsInProgress(1); err != nil || len(eds) != 0 {
		t.Fatalf("tombstone leaked into EditionsInProgress: %v err %v", eds, err)
	}
}
