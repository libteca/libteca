package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func newAudioGroupLib(t *testing.T, files ...string) (*store.DB, *store.Library) {
	t.Helper()
	db := openScanDB(t)
	dir := t.TempDir()
	book := filepath.Join(dir, "MyBook")
	if err := os.MkdirAll(book, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		genAudio(t, filepath.Join(book, name), "440", 1)
	}
	libID, err := db.AddLibraryChecked("a", "audiobooks", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := db.Library(libID)
	if err != nil {
		t.Fatal(err)
	}
	return db, lib
}

func TestRolledBackGroupReportsNoCommittedCounts(t *testing.T) {
	db, lib := newAudioGroupLib(t, "book 01.mp3", "book 02.mp3")
	if _, err := db.Exec(`CREATE TRIGGER block_second_file BEFORE INSERT ON files
		WHEN NEW.path LIKE '%book 02%' BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	var final Progress
	_, err := Library(context.Background(), db, lib, t.TempDir(), func(p Progress) { final = p })
	if err == nil {
		t.Fatal("scan with injected file failure must fail")
	}
	if final.FilesAdded != 0 || final.FilesUpdated != 0 || final.WorksChanged != 0 {
		t.Fatalf("counts after rollback = added %d updated %d works %d, want all 0 (final %+v)",
			final.FilesAdded, final.FilesUpdated, final.WorksChanged, final)
	}
	var works, files int
	db.QueryRow(`SELECT count(*) FROM works`).Scan(&works)
	db.QueryRow(`SELECT count(*) FROM files`).Scan(&files)
	if works != 0 || files != 0 {
		t.Fatalf("rolled back rows persist: works %d files %d", works, files)
	}
}

func TestCommittedGroupStillCounts(t *testing.T) {
	db, lib := newAudioGroupLib(t, "book 01.mp3", "book 02.mp3")
	final := scanOnce(t, db, lib, t.TempDir())
	if final.FilesAdded != 2 || final.WorksChanged != 1 {
		t.Fatalf("counts after successful scan = added %d works %d, want 2/1 (final %+v)",
			final.FilesAdded, final.WorksChanged, final)
	}
	var files int
	db.QueryRow(`SELECT count(*) FROM files`).Scan(&files)
	if files != 2 {
		t.Fatalf("files = %d, want 2", files)
	}
	warm := scanOnce(t, db, lib, t.TempDir())
	if warm.FilesAdded != 0 || warm.WorksChanged != 0 {
		t.Fatalf("warm scan counts = added %d works %d, want 0/0 (final %+v)",
			warm.FilesAdded, warm.WorksChanged, warm)
	}
}
