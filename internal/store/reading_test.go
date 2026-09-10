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
