package importer

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestKavitaRejectsPathsOutsideLibraryFolders(t *testing.T) {
	path := buildKavitaFixture(t)
	root := filepath.Dir(path)
	outside := t.TempDir()
	outsideBook := filepath.Join(outside, "elsewhere.epub")
	secret := filepath.Join(outside, "secret.txt")
	insideText := filepath.Join(root, "notes.txt")
	link := filepath.Join(root, "linked.cbz")
	for _, p := range []string{outsideBook, secret, insideText} {
		if err := os.WriteFile(p, []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	sourceExec(t, path, func(db *sql.DB) {
		for i, p := range []string{outsideBook, insideText, link} {
			ch := 20 + i
			importExec(t, db, `INSERT INTO Chapter VALUES (?, 9, ?, '', '', 1)`, ch, ch)
			importExec(t, db, `INSERT INTO MangaFile VALUES (?, ?, ?, 3)`, ch, ch, p)
		}
	})
	db := openStore(t)
	plan, err := Kavita(path, db, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Files != 1 {
		t.Fatalf("files = %d, want only the in-folder comic", plan.Files)
	}
	var n int
	if err := db.DB.QueryRow(`SELECT count(*) FROM files WHERE path NOT LIKE '%Saga 001.cbz'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d out-of-policy files were imported", n)
	}
	for _, want := range []string{"outside the imported and configured library folders", "not a supported media file"} {
		if !warningsMention(plan.Warnings, want) {
			t.Fatalf("warning %q absent: %v", want, plan.Warnings)
		}
	}
}

func TestABSRejectsAudioFilesOutsideLibraryFolders(t *testing.T) {
	dir := buildABSReleasedFixture(t)
	outside := filepath.Join(t.TempDir(), "stray.mp3")
	if err := os.WriteFile(outside, make([]byte, 512), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceExec(t, filepath.Join(dir, "abs_database.db"), func(db *sql.DB) {
		importExec(t, db, `UPDATE books SET audioFiles = ? WHERE id = 'book-b'`, audioFilesJSON([]absAudioFile{{Path: outside, Index: 1, Duration: 100}}))
	})
	db := openStore(t)
	plan, err := ABS(dir, db, true)
	if err != nil {
		t.Fatal(err)
	}
	if !warningsMention(plan.Warnings, "outside the imported and configured library folders") {
		t.Fatalf("out-of-folder warning absent: %v", plan.Warnings)
	}
	for _, l := range plan.Libraries {
		if l.Type == "audiobooks" && l.Skipped == 0 {
			t.Fatalf("book with an out-of-folder file was not skipped: %+v", l)
		}
	}
}

func TestImportAcceptsFilesInsideExistingLibrary(t *testing.T) {
	path := buildKavitaFixture(t)
	sourceExec(t, path, func(db *sql.DB) {
		importExec(t, db, `DELETE FROM FolderPath`)
	})
	db := openStore(t)
	plan, err := Kavita(path, db, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Files != 0 {
		t.Fatalf("files = %d with no declared or configured roots, want 0", plan.Files)
	}
	if _, err := db.AddLibrary("Comics", "comics", filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	plan, err = Kavita(path, db, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Files != 1 {
		t.Fatalf("files = %d inside a configured library, want 1", plan.Files)
	}
}
