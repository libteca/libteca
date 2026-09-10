package importer

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

// buildKavitaFixture creates a synthetic Kavita app.db: one comic library
// with a series (author + summary), one volume/chapter with a real cbz on
// disk, one admin user with 10/24 pages read, plus a library of unsupported
// type (must warn-skip) and a chapter whose file is missing.
func buildKavitaFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	cbz := filepath.Join(root, "Saga 001.cbz")
	if err := os.WriteFile(cbz, make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "app.db")
	fdb, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer fdb.Close()
	stmts := []string{
		`CREATE TABLE Library (Id INTEGER PRIMARY KEY, Name TEXT, Type INTEGER)`,
		`CREATE TABLE Series (Id INTEGER PRIMARY KEY, LibraryId INTEGER, Name TEXT)`,
		`CREATE TABLE SeriesMetadata (Id INTEGER PRIMARY KEY, SeriesId INTEGER, Summary TEXT)`,
		`CREATE TABLE Person (Id INTEGER PRIMARY KEY, Name TEXT)`,
		`CREATE TABLE SeriesMetadataPeople (Id INTEGER PRIMARY KEY, SeriesMetadataId INTEGER, PersonId INTEGER, PersonRole INTEGER)`,
		`CREATE TABLE Volume (Id INTEGER PRIMARY KEY, SeriesId INTEGER, Number TEXT)`,
		`CREATE TABLE Chapter (Id INTEGER PRIMARY KEY, VolumeId INTEGER, Number REAL, Range TEXT, Title TEXT, Pages INTEGER)`,
		`CREATE TABLE MangaFile (Id INTEGER PRIMARY KEY, ChapterId INTEGER, FilePath TEXT, Format INTEGER)`,
		`CREATE TABLE AppUser (Id INTEGER PRIMARY KEY, Username TEXT, PasswordHash TEXT, Roles TEXT)`,
		`CREATE TABLE AppUserProgress (Id INTEGER PRIMARY KEY, AppUserId INTEGER, ChapterId INTEGER, PagesRead INTEGER)`,
		`INSERT INTO Library VALUES (1, 'Comics', 2), (2, 'Weird', 9)`,
		`INSERT INTO Series VALUES (5, 1, 'Saga'), (6, 2, 'Skipped Series')`,
		`INSERT INTO SeriesMetadata VALUES (1, 5, 'A space opera')`,
		`INSERT INTO Person VALUES (1, 'BKV')`,
		`INSERT INTO SeriesMetadataPeople VALUES (1, 1, 1, 3)`,
		`INSERT INTO Volume VALUES (9, 5, '1')`,
		`INSERT INTO Chapter VALUES (11, 9, 1, '', '', 24)`,
		`INSERT INTO MangaFile VALUES (1, 11, '` + cbz + `', 1)`,
		`INSERT INTO AppUser VALUES (1, 'ktan', 'aspnet-hash', '["Admin"]')`,
		`INSERT INTO AppUserProgress VALUES (1, 1, 11, 10)`,
	}
	for _, s := range stmts {
		if _, err := fdb.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return dbPath
}

func TestKavitaDryRun(t *testing.T) {
	db := openStore(t)
	plan, err := Kavita(buildKavitaFixture(t), db, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Works != 1 || plan.Editions != 1 || plan.Files != 1 {
		t.Fatalf("works/editions/files = %d/%d/%d, want 1/1/1", plan.Works, plan.Editions, plan.Files)
	}
	if plan.ProgressRows != 1 || len(plan.Users) != 1 || len(plan.Libraries) != 1 {
		t.Fatalf("progress/users/libraries = %d/%d/%d", plan.ProgressRows, len(plan.Users), len(plan.Libraries))
	}
	if plan.Libraries[0].Type != "comics" {
		t.Fatalf("library type = %s", plan.Libraries[0].Type)
	}
	if !warningsMention(plan.Warnings, "Weird") {
		t.Fatalf("unsupported library warning absent: %v", plan.Warnings)
	}
	if countRows(t, db, `SELECT count(*) FROM works`) != 0 {
		t.Fatal("dryRun wrote to the store")
	}
}

func TestKavitaCommit(t *testing.T) {
	db := openStore(t)
	dbPath := buildKavitaFixture(t)
	if _, err := Kavita(dbPath, db, false); err != nil {
		t.Fatal(err)
	}

	ktan, err := db.UserByName("ktan")
	if err != nil || !ktan.IsAdmin {
		t.Fatalf("user = %+v err %v", ktan, err)
	}
	var wid, eid int64
	var author, desc string
	if err := db.QueryRow(`SELECT w.id, coalesce(w.author,''), coalesce(w.description,'') FROM works w WHERE w.title = 'Saga'`).Scan(&wid, &author, &desc); err != nil {
		t.Fatal(err)
	}
	if author != "BKV" || desc != "A space opera" {
		t.Fatalf("work author/desc = %q/%q", author, desc)
	}
	var pages int
	var format, fpath string
	if err := db.QueryRow(`SELECT e.id, e.page_count, e.format, (SELECT f.path FROM files f WHERE f.edition_id = e.id ORDER BY f.seq LIMIT 1) FROM editions e WHERE e.work_id = ?`, wid).Scan(&eid, &pages, &format, &fpath); err != nil {
		t.Fatal(err)
	}
	if pages != 24 || format != "cbz" || filepath.Base(fpath) != "Saga 001.cbz" {
		t.Fatalf("edition = pages %d fmt %s file %s", pages, format, fpath)
	}
	rp, err := db.GetReadingProgress(ktan.ID, eid)
	if err != nil {
		t.Fatal(err)
	}
	if rp.Page == nil || *rp.Page != 10 || rp.Percent == nil {
		t.Fatalf("progress = %+v", rp)
	}
	if want := 10.0 / 24.0; *rp.Percent < want-1e-9 || *rp.Percent > want+1e-9 {
		t.Fatalf("percent = %v, want %v", *rp.Percent, want)
	}

	if _, err := Kavita(dbPath, db, false); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, db, `SELECT count(*) FROM works`); n != 1 {
		t.Fatalf("re-import works = %d, want 1", n)
	}
	if n := countRows(t, db, `SELECT count(*) FROM files`); n != 1 {
		t.Fatalf("re-import files = %d, want 1", n)
	}
}

func TestImportersRejectMissingPaths(t *testing.T) {
	db := openStore(t)
	if _, err := ABS(t.TempDir(), db, true); err == nil {
		t.Fatal("ABS without abs_database.db should fail")
	}
	if _, err := Kavita(filepath.Join(t.TempDir(), "missing.db"), db, true); err == nil {
		t.Fatal("Kavita with missing db should fail")
	}
}
