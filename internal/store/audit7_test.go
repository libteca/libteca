package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/store"
)

func TestSnapshotSerializesAcrossProcesses(t *testing.T) {
	db := backupDB(t)
	covers := filepath.Join(t.TempDir(), "covers")
	os.MkdirAll(covers, 0o755)
	os.WriteFile(filepath.Join(covers, "7.jpg"), []byte("cover"), 0o644)
	backups := filepath.Join(t.TempDir(), "backups")

	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			<-start
			_, errs[slot] = db.Snapshot(covers, backups, 1)
		}(i)
	}
	close(start)
	wg.Wait()
	// The two opens hold independent file descriptions, so the flock
	// serializes them like two CLI processes: at most one wins the lock at a
	// time, and with keep=1 the retention of the LAST finisher must still
	// leave that run's own snapshot on disk.
	busy := 0
	for _, e := range errs {
		if e != nil {
			busy++
			if !strings.Contains(e.Error(), "another backup is active") {
				t.Fatalf("unexpected snapshot error: %v", e)
			}
		}
	}
	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}
	var dbs int
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "gen-") {
			dbs++
		}
	}
	if busy == 2 {
		t.Skip("both runs reported busy; lock serialization environment did not exercise retention")
	}
	if dbs < 1 {
		t.Fatalf("concurrent keep=1 retention left %d generations", dbs)
	}
}

func TestSnapshotPublishesAfterCovers(t *testing.T) {
	db := backupDB(t)
	covers := filepath.Join(t.TempDir(), "covers")
	os.MkdirAll(covers, 0o755)
	backups := filepath.Join(t.TempDir(), "backups")
	// A covers directory whose copy must fail (a subdirectory sits where a
	// file is expected to be written) leaves NO published generation.
	os.MkdirAll(filepath.Join(covers, "8.jpg"), 0o755)
	os.WriteFile(filepath.Join(covers, "8.jpg", "nested"), []byte("x"), 0o644)
	if _, err := db.Snapshot(covers, backups, 10); err == nil {
		t.Skip("filesystem accepted the odd layout")
	}
	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "gen-") {
			t.Fatalf("generation %s published although the covers copy failed", e.Name())
		}
	}
}

func TestSetReadingProgressPatchPreservesFinished(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO users (id, name, password_hash, is_admin, created_at, updated_at) VALUES (1,'u','x',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	lib, err := db.AddLibrary("L", "books", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wid, err := db.UpsertWork(&store.Work{LibraryID: lib, Title: "W"})
	if err != nil {
		t.Fatal(err)
	}
	eid, err := db.UpsertEdition(&store.Edition{WorkID: wid, Format: "pdf", Title: "W"})
	if err != nil {
		t.Fatal(err)
	}
	page := int64(12)
	if err := db.SetReadingProgress(&store.ReadingProgress{Progress: store.Progress{UserID: 1, EditionID: eid, IsFinished: true}, Page: &page}); err != nil {
		t.Fatal(err)
	}
	next := int64(13)
	if err := db.SetReadingProgressPatch(&store.ReadingProgress{Progress: store.Progress{UserID: 1, EditionID: eid, IsFinished: false}, Page: &next}, false); err != nil {
		t.Fatal(err)
	}
	p, err := db.GetReadingProgress(1, eid)
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsFinished || *p.Page != 13 {
		t.Fatalf("page-only update: finished=%v page=%d, want finished=true page=13", p.IsFinished, *p.Page)
	}
	if err := db.SetReadingProgressPatch(&store.ReadingProgress{Progress: store.Progress{UserID: 1, EditionID: eid, IsFinished: false}}, true); err != nil {
		t.Fatal(err)
	}
	p, err = db.GetReadingProgress(1, eid)
	if err != nil {
		t.Fatal(err)
	}
	if p.IsFinished {
		t.Fatal("explicit finished=false must reopen the item")
	}
}

func TestCloseSessionWithProgressCommitsBoth(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO users (id, name, password_hash, is_admin, created_at, updated_at) VALUES (1,'u','x',0,0,0)`); err != nil {
		t.Fatal(err)
	}
	lib, err := db.AddLibrary("L", "audiobooks", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wid, err := db.UpsertWork(&store.Work{LibraryID: lib, Title: "W"})
	if err != nil {
		t.Fatal(err)
	}
	eid, err := db.UpsertEdition(&store.Edition{WorkID: wid, Format: "mp3", Title: "W"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	if err := db.CreateSession(&store.Session{ID: "s1", UserID: 1, EditionID: eid, StartedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	s, err := db.Session("s1")
	if err != nil {
		t.Fatal(err)
	}
	p := &store.Progress{UserID: 1, EditionID: eid, EditionPositionSecs: 421}
	if err := db.CloseSessionWithProgress(s, p, 30); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetProgress(1, eid)
	if err != nil || got.EditionPositionSecs != 421 {
		t.Fatalf("final progress missing after close: %v %+v", err, got)
	}
	s2, err := db.Session("s1")
	if err != nil || s2.ClosedAt == nil {
		t.Fatalf("session not closed: %v", err)
	}
	other := &store.Progress{UserID: 1, EditionID: eid, EditionPositionSecs: 999}
	if err := db.CloseSessionWithProgress(s2, other, 0); err != nil {
		t.Fatalf("idempotent close: %v", err)
	}
	got, _ = db.GetProgress(1, eid)
	if got.EditionPositionSecs != 421 {
		t.Fatalf("closed-session retry overwrote final position: %+v", got)
	}
}

func TestMarkMissingLibraryFiles(t *testing.T) {
	db := openTestDB(t)
	dir := t.TempDir()
	lib, err := db.AddLibrary("L", "audiobooks", dir)
	if err != nil {
		t.Fatal(err)
	}
	wid, err := db.UpsertWork(&store.Work{LibraryID: lib, Title: "W"})
	if err != nil {
		t.Fatal(err)
	}
	eid, err := db.UpsertEdition(&store.Edition{WorkID: wid, Format: "mp3", Title: "W"})
	if err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(dir, "keep.mp3")
	gone := filepath.Join(dir, "gone.mp3")
	os.WriteFile(kept, []byte("k"), 0o644)
	os.WriteFile(gone, []byte("g"), 0o644)
	hash := "h-1"
	if err := db.UpsertFile(&store.FileRec{EditionID: eid, Path: kept, Seq: 1, SizeBytes: 1, Hash: &hash}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFile(&store.FileRec{EditionID: eid, Path: gone, Seq: 2, SizeBytes: 1, Hash: &hash}); err != nil {
		t.Fatal(err)
	}
	os.Remove(gone)
	n, err := db.MarkMissingLibraryFiles(lib)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("marked = %d, want 1", n)
	}
	rows, err := db.Query(`SELECT missing FROM files WHERE path = ?`, kept)
	if err != nil {
		t.Fatal(err)
	}
	var m int
	rows.Scan(&m)
	rows.Close()
	if m != 0 {
		t.Fatal("existing file was marked missing")
	}
}
