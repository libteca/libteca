package scan

import (
	"github.com/libteca/libteca/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestContentVerificationDetectsSameStampReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.mp3")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lib, _ := db.AddLibrary("audio", "audiobooks", root)
	work, _ := db.UpsertWork(&store.Work{LibraryID: lib, Title: "a"})
	edition, _ := db.UpsertEdition(&store.Edition{WorkID: work, Format: "mp3", Title: "a"})
	sha := SHA256File(path)
	f := &store.FileRec{EditionID: edition, SourceLibraryID: lib, Path: path, SizeBytes: st.Size(), MtimeSecs: st.ModTime().Unix(), MtimeNS: st.ModTime().UnixNano(), SHA256: &sha}
	if err := db.UpsertFile(f); err != nil {
		t.Fatal(err)
	}
	if !fileUnchanged(db, path, st.Size(), st.ModTime().Unix(), st.ModTime().UnixNano()) {
		t.Fatal("same bytes changed")
	}
	if err := os.WriteFile(path, []byte("replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, st.ModTime(), st.ModTime()); err != nil {
		t.Fatal(err)
	}
	if fileUnchanged(db, path, st.Size(), st.ModTime().Unix(), st.ModTime().UnixNano()) {
		t.Fatal("same stamp replacement was skipped")
	}
	generation, _ := db.TimelineGeneration(edition)
	newSHA := SHA256File(path)
	f.SHA256 = &newSHA
	if err := db.UpsertFile(f); err != nil {
		t.Fatal(err)
	}
	after, _ := db.TimelineGeneration(edition)
	if generation == after {
		t.Fatal("bytes did not invalidate generation")
	}
}

func TestPhysicalAudioEncodingGroups(t *testing.T) {
	file := func(path string) bookFile {
		return bookFile{path: path, top: "/books/title", name: filepath.Base(path)}
	}
	pairs := []struct {
		a, b string
		same bool
	}{
		{"/books/title/a.m4b", "/books/title/b.m4b", false},
		{"/books/title/part1.m4b", "/books/title/part2.m4b", true},
		{"/books/title/64kbps/01.mp3", "/books/title/128kbps/01.mp3", false},
		{"/books/title/disc1/01.mp3", "/books/title/disc2/01.mp3", true},
		{"/books/title/01.mp3", "/books/title/01.m4a", false},
	}
	for _, pair := range pairs {
		if same := audioSourceGroup(file(pair.a)) == audioSourceGroup(file(pair.b)); same != pair.same {
			t.Fatal(pair, same)
		}
	}
}

func TestLegacyStatRowsRequireContentEvidence(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.mp3")
	os.WriteFile(path, []byte("legacy"), 0600)
	st, _ := os.Stat(path)
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lib, _ := db.AddLibrary("audio", "audiobooks", root)
	work, _ := db.UpsertWork(&store.Work{LibraryID: lib, Title: "legacy"})
	edition, _ := db.UpsertEdition(&store.Edition{WorkID: work, Format: "mp3", Title: "legacy"})
	if err := db.UpsertFile(&store.FileRec{EditionID: edition, Path: path, SizeBytes: st.Size(), MtimeSecs: st.ModTime().Unix(), MtimeNS: st.ModTime().UnixNano()}); err != nil {
		t.Fatal(err)
	}
	if fileUnchanged(db, path, st.Size(), st.ModTime().Unix(), st.ModTime().UnixNano()) {
		t.Fatal("legacy row skipped without byte evidence")
	}
}

func TestIndependentMediaRescanKeepsManualGrouping(t *testing.T) {
	for _, kind := range []string{"music", "movies"} {
		t.Run(kind, func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			root := t.TempDir()
			folder := filepath.Join(root, "Album")
			os.Mkdir(folder, 0700)
			extension := ".mp3"
			if kind == "movies" {
				extension = ".mp4"
			}
			first := filepath.Join(folder, "a"+extension)
			second := filepath.Join(folder, "b"+extension)
			if kind == "music" {
				genAudio(t, first, "220", 1)
				genAudio(t, second, "440", 1)
			} else {
				genVideo(t, first, 1)
				genVideo(t, second, 1)
			}
			libID, err := db.AddLibrary("physical", kind, root)
			if err != nil {
				t.Fatal(err)
			}
			lib := &store.Library{ID: libID, Type: kind, Path: root}
			covers := t.TempDir()
			scanOnce(t, db, lib, covers)
			var fileID, edition, otherEdition int64
			if err := db.QueryRow(`SELECT id,edition_id FROM files WHERE path=?`, second).Scan(&fileID, &edition); err != nil {
				t.Fatal(err)
			}
			db.QueryRow(`SELECT edition_id FROM files WHERE path=?`, first).Scan(&otherEdition)
			if otherEdition == edition {
				t.Fatal("separate physical encodings collapsed")
			}
			logical, _ := db.AddLibrary("logical", kind, t.TempDir())
			target, _ := db.UpsertWork(&store.Work{LibraryID: logical, Title: "curated"})
			if _, err := db.MoveEditionToWork(edition, target); err != nil {
				t.Fatal(err)
			}
			if kind == "music" {
				genAudio(t, second, "880", 1.5)
			} else {
				genVideo(t, second, 1.5)
			}
			scanOnce(t, db, lib, covers)
			var gotFile, gotEdition, gotWork int64
			if err := db.QueryRow(`SELECT f.id,f.edition_id,e.work_id FROM files f JOIN editions e ON e.id=f.edition_id WHERE f.path=?`, second).Scan(&gotFile, &gotEdition, &gotWork); err != nil {
				t.Fatal(err)
			}
			if gotFile != fileID || gotEdition != edition || gotWork != target {
				t.Fatal("manual source identity changed", gotFile, gotEdition, gotWork)
			}
			openedRoot, err := db.LibraryRootForFile(fileID)
			if err != nil || openedRoot != root {
				t.Fatal(openedRoot, err)
			}
			var count int
			db.QueryRow(`SELECT count(*) FROM works`).Scan(&count)
			if count != 2 {
				t.Fatal("orphan work created", count)
			}
		})
	}
}
