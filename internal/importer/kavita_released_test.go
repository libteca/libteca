package importer

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestKavitaLibraryTypeReleasedEnums(t *testing.T) {
	cases := map[int]struct {
		want      string
		supported bool
	}{
		0: {"comics", true},
		1: {"comics", true},
		2: {"books", true},
		3: {"", false},
		4: {"books", true},
		5: {"comics", true},
		9: {"", false},
	}
	for sourceType, want := range cases {
		got, ok := kavitaLibraryType(sourceType)
		if got != want.want || ok != want.supported {
			t.Fatalf("kavitaLibraryType(%d) = %q,%v want %q,%v", sourceType, got, ok, want.want, want.supported)
		}
	}
}

func TestKavitaFormatReleasedEnums(t *testing.T) {
	if got, ok := kavitaFormat(4, "/store/noext"); !ok || got != "pdf" {
		t.Fatalf("Pdf=4 extensionless = %q,%v want pdf", got, ok)
	}
	if got, ok := kavitaFormat(2, "/store/noext"); ok {
		t.Fatalf("Unknown=2 must be unsupported, got %q", got)
	}
	if got, ok := kavitaFormat(0, "/store/noext"); ok {
		t.Fatalf("Image=0 must be unsupported, got %q", got)
	}
	if got, ok := kavitaFormat(1, "/store/noext"); !ok || got != "cbz" {
		t.Fatalf("Archive=1 extensionless = %q,%v want cbz", got, ok)
	}
	if got, ok := kavitaFormat(9, "/store/book.epub"); !ok || got != "epub" {
		t.Fatalf("extension wins over unknown enum = %q,%v want epub", got, ok)
	}
}

func buildKavitaEnumFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dbPath := filepath.Join(root, "app.db")
	fdb, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer fdb.Close()
	stmts := []string{
		`CREATE TABLE Library (Id INTEGER PRIMARY KEY, Name TEXT, Type INTEGER)`,
		`CREATE TABLE Series (Id INTEGER PRIMARY KEY, LibraryId INTEGER, Name TEXT)`,
		`CREATE TABLE Volume (Id INTEGER PRIMARY KEY, SeriesId INTEGER, Number TEXT)`,
		`CREATE TABLE Chapter (Id INTEGER PRIMARY KEY, VolumeId INTEGER, Number REAL, Range TEXT, Title TEXT, Pages INTEGER)`,
		`CREATE TABLE MangaFile (Id INTEGER PRIMARY KEY, ChapterId INTEGER, FilePath TEXT, Format INTEGER)`,
		`CREATE TABLE AspNetUsers (Id TEXT PRIMARY KEY, UserName TEXT)`,
		`CREATE TABLE AspNetRoles (Id TEXT PRIMARY KEY, Name TEXT)`,
		`CREATE TABLE AspNetUserRoles (UserId TEXT, RoleId TEXT)`,
		`CREATE TABLE AppUserProgresses (Id INTEGER PRIMARY KEY, AppUserId TEXT, ChapterId INTEGER, PagesRead INTEGER)`,
		`INSERT INTO Library VALUES (1, 'Manga', 0), (2, 'Comics', 1), (3, 'Books', 2), (4, 'Images', 3), (5, 'Novels', 4), (6, 'ComicVine', 5)`,
		`INSERT INTO Series VALUES (10, 1, 'MM'), (11, 2, 'CC'), (12, 3, 'BB'), (13, 4, 'II'), (14, 5, 'NN'), (15, 6, 'VV')`,
		`INSERT INTO Volume VALUES (1, 10, '1'), (2, 11, '1'), (3, 12, '1'), (4, 13, '1'), (5, 14, '1'), (6, 15, '1')`,
		`INSERT INTO Chapter VALUES (10, 1, 1, '', '', 20), (11, 2, 1, '', '', 20), (12, 3, 1, '', '', 20), (13, 4, 1, '', '', 20), (14, 5, 1, '', '', 20), (15, 6, 1, '', '', 20)`,
	}
	for _, s := range stmts {
		if _, err := fdb.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	for _, ch := range []int{10, 11, 12, 14, 15} {
		path := filepath.Join(root, "file-"+string(rune('0'+ch))+".cbz")
		if err := os.WriteFile(path, make([]byte, 64), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := fdb.Exec(`INSERT INTO MangaFile VALUES (?, ?, ?, 1)`, ch, ch, path); err != nil {
			t.Fatal(err)
		}
	}
	imagePath := filepath.Join(root, "loose.png")
	if err := os.WriteFile(imagePath, make([]byte, 64), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fdb.Exec(`INSERT INTO MangaFile VALUES (16, 13, ?, 0)`, imagePath); err != nil {
		t.Fatal(err)
	}
	if _, err := fdb.Exec(`INSERT INTO AspNetUsers VALUES ('k-1', 'ktan')`); err != nil {
		t.Fatal(err)
	}
	if _, err := fdb.Exec(`INSERT INTO AspNetRoles VALUES ('r-1', 'Admin')`); err != nil {
		t.Fatal(err)
	}
	if _, err := fdb.Exec(`INSERT INTO AspNetUserRoles VALUES ('k-1', 'r-1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := fdb.Exec(`INSERT INTO AppUserProgresses VALUES (1, 'k-1', 10, 10)`); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

func TestKavitaReleasedLibraryClassification(t *testing.T) {
	db := openStore(t)
	plan, err := Kavita(buildKavitaEnumFixture(t), db, true)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Manga": "comics", "Comics": "comics", "Books": "books",
		"Novels": "books", "ComicVine": "comics",
	}
	if len(plan.Libraries) != len(want) {
		t.Fatalf("libraries = %+v, want %v", plan.Libraries, want)
	}
	for _, l := range plan.Libraries {
		if want[l.Name] != l.Type {
			t.Fatalf("library %q typed %q, want %q", l.Name, l.Type, want[l.Name])
		}
	}
	if !warningsMention(plan.Warnings, "Images") || !warningsMention(plan.Warnings, "unsupported type 3") {
		t.Fatalf("image library warning absent: %v", plan.Warnings)
	}
	if !warningsMention(plan.Warnings, "unsupported MangaFormat") {
		t.Fatalf("loose-image format warning absent: %v", plan.Warnings)
	}
	if plan.ProgressRows != 1 || len(plan.Users) != 1 {
		t.Fatalf("progress/users = %d/%d, want 1/1", plan.ProgressRows, len(plan.Users))
	}
	if !plan.Users[0].IsAdmin {
		t.Fatalf("AspNetUserRoles admin flag lost: %+v", plan.Users[0])
	}
}

func TestKavitaRequiredTableMissingIsFatal(t *testing.T) {
	path := buildKavitaEnumFixture(t)
	sourceExec(t, path, func(db *sql.DB) {
		importExec(t, db, `DROP TABLE MangaFile`)
	})
	store := openStore(t)
	if plan, err := Kavita(path, store, true); err == nil {
		t.Fatalf("missing required catalog table must be fatal, got plan %+v", plan)
	}
}

func TestKavitaMissingUserTablesWarn(t *testing.T) {
	path := buildKavitaEnumFixture(t)
	sourceExec(t, path, func(db *sql.DB) {
		importExec(t, db, `DROP TABLE AppUserProgresses`)
		importExec(t, db, `DROP TABLE AspNetUserRoles`)
		importExec(t, db, `DROP TABLE AspNetUsers`)
	})
	store := openStore(t)
	plan, err := Kavita(path, store, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Works != 5 {
		t.Fatalf("works = %d, want 5 (missing optional user tables degrade)", plan.Works)
	}
	if !warningsMention(plan.Warnings, "AspNetUsers unreadable") {
		t.Fatalf("users warning absent: %v", plan.Warnings)
	}
	if !warningsMention(plan.Warnings, "AppUserProgresses unreadable") {
		t.Fatalf("progress warning absent: %v", plan.Warnings)
	}
}
