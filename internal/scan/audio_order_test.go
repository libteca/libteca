package scan

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/store"
)

func genTaggedAudio(t *testing.T, path, disc, track string) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skipf("ffmpeg unavailable: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"-y", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.1", "-c:a", "libmp3lame", "-metadata", "album=Tagged Book", "-metadata", "artist=Tagged Author", "-metadata", "disc=" + disc, "-metadata", "track=" + track, path}
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("generate tagged audio: %v: %s", err, out)
	}
}

func audiobookFixture(t *testing.T) (*store.DB, *store.Library, string) {
	t.Helper()
	db := openScanDB(t)
	root := t.TempDir()
	id, err := db.AddLibrary("Books", "audiobooks", root)
	if err != nil {
		t.Fatal(err)
	}
	return db, &store.Library{ID: id, Type: "audiobooks", Path: root}, t.TempDir()
}

func orderedBookPaths(t *testing.T, db *store.DB, root string) []string {
	t.Helper()
	rows, err := db.Query(`SELECT path FROM files WHERE missing = 0 ORDER BY seq, path`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			t.Fatal(err)
		}
		got = append(got, relPath(root, path))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAudioOrdersDiscTrackBeforeFilename(t *testing.T) {
	db, lib, covers := audiobookFixture(t)
	book := filepath.Join(lib.Path, "Folder Name")
	genTaggedAudio(t, filepath.Join(book, "a.mp3"), "2/2", "1/2")
	genTaggedAudio(t, filepath.Join(book, "b.mp3"), "1/2", "2/2")
	genTaggedAudio(t, filepath.Join(book, "z.mp3"), "1/2", "1/2")
	if p := scanOnce(t, db, lib, covers); p.FilesProbed != 3 {
		t.Fatalf("cold probes = %d, want 3", p.FilesProbed)
	}
	want := []string{"z.mp3", "b.mp3", "a.mp3"}
	if got := orderedBookPaths(t, db, book); !reflect.DeepEqual(got, want) {
		t.Fatalf("play order = %v, want %v", got, want)
	}
	if p := scanOnce(t, db, lib, covers); p.FilesProbed != 0 || p.FilesUpdated != 0 {
		t.Fatalf("warm scan = %+v", p)
	}
}

func TestAudioOrderUpgradePreservesMetadataAndIdentity(t *testing.T) {
	db, lib, covers := audiobookFixture(t)
	book := filepath.Join(lib.Path, "Folder Name")
	genTaggedAudio(t, filepath.Join(book, "a.mp3"), "1", "2")
	genTaggedAudio(t, filepath.Join(book, "z.mp3"), "1", "1")
	scanOnce(t, db, lib, covers)
	before := fileRows(t, db)
	if _, err := db.Exec(`UPDATE files SET embedded_meta = '{"custom":"keep"}', seq = CASE WHEN path LIKE '%a.mp3' THEN 1 ELSE 2 END`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE works SET title = 'Manual title', title_l = 'manual title'`); err != nil {
		t.Fatal(err)
	}
	p := scanOnce(t, db, lib, covers)
	if p.FilesProbed != 2 {
		t.Fatalf("legacy-order upgrade probes = %d, want 2", p.FilesProbed)
	}
	if got := orderedBookPaths(t, db, book); !reflect.DeepEqual(got, []string{"z.mp3", "a.mp3"}) {
		t.Fatalf("upgraded order = %v", got)
	}
	for path, file := range fileRows(t, db) {
		if file.editionID != before[path].editionID {
			t.Fatalf("legacy order repair reparented %s", path)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM works WHERE title = 'Manual title'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("manual title lost: count %d err %v", count, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM files WHERE json_extract(embedded_meta, '$.custom') = 'keep'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("stored metadata lost: count %d err %v", count, err)
	}
	if p := scanOnce(t, db, lib, covers); p.FilesProbed != 0 || p.FilesUpdated != 0 {
		t.Fatalf("upgraded warm scan = %+v", p)
	}
}

func TestAudioOrderUpgradeCancellationKeepsOldRows(t *testing.T) {
	db, lib, covers := audiobookFixture(t)
	book := filepath.Join(lib.Path, "Folder Name")
	genTaggedAudio(t, filepath.Join(book, "a.mp3"), "1", "2")
	genTaggedAudio(t, filepath.Join(book, "z.mp3"), "1", "1")
	scanOnce(t, db, lib, covers)
	if _, err := db.Exec(`UPDATE files SET embedded_meta = '{}', seq = CASE WHEN path LIKE '%a.mp3' THEN 1 ELSE 2 END`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr := newTracker(func(p Progress) {
		if p.FilesProbed > 0 {
			cancel()
		}
	})
	group := []bookFile{}
	for _, name := range []string{"a.mp3", "z.mp3"} {
		path := filepath.Join(book, name)
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		group = append(group, bookFile{path: path, name: name, dir: book, size: fi.Size(), mtime: fi.ModTime().Unix(), mtimeNs: fi.ModTime().UnixNano()})
	}
	tr.last = time.Time{}
	if err := scanBook(ctx, db, lib, lib.Path, book, group, covers, tr); !errors.Is(err, context.Canceled) {
		t.Fatalf("legacy ordering cancellation = %v", err)
	}
	if got := orderedBookPaths(t, db, book); !reflect.DeepEqual(got, []string{"a.mp3", "z.mp3"}) {
		t.Fatalf("cancelled repair changed order: %v", got)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM files WHERE embedded_meta != '{}'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("cancelled repair marked files complete: count %d err %v", count, err)
	}
}

func TestAudioDiscFoldersDoNotInterleaveWithoutDiscTags(t *testing.T) {
	db, lib, covers := audiobookFixture(t)
	book := filepath.Join(lib.Path, "Folder Name")
	for _, disc := range []string{"CD1", "CD2"} {
		genTaggedAudio(t, filepath.Join(book, disc, "z.mp3"), "", "1")
		genTaggedAudio(t, filepath.Join(book, disc, "a.mp3"), "", "2")
	}
	scanOnce(t, db, lib, covers)
	want := []string{"CD1/z.mp3", "CD1/a.mp3", "CD2/z.mp3", "CD2/a.mp3"}
	if got := orderedBookPaths(t, db, book); !reflect.DeepEqual(got, want) {
		t.Fatalf("disc folder order = %v, want %v", got, want)
	}
}

func TestAudioWarmScanFindsTaggedWorkCover(t *testing.T) {
	db, lib, covers := audiobookFixture(t)
	book := filepath.Join(lib.Path, "Folder Name")
	genTaggedAudio(t, filepath.Join(book, "a.mp3"), "", "")
	scanOnce(t, db, lib, covers)
	if err := os.WriteFile(filepath.Join(book, "cover.jpg"), []byte("fixture cover"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := scanOnce(t, db, lib, covers)
	if p.FilesProbed != 0 {
		t.Fatalf("cover-only warm scan probed %d files", p.FilesProbed)
	}
	var cover string
	if err := db.QueryRow(`SELECT cover_path FROM works WHERE title = 'Tagged Book'`).Scan(&cover); err != nil || cover == "" {
		t.Fatalf("tag-derived work cover missing: %q, err %v", cover, err)
	}
	if data, err := os.ReadFile(filepath.Join(covers, cover)); err != nil || string(data) != "fixture cover" {
		t.Fatalf("stored cover = %q, err %v", data, err)
	}
}

func TestAudioOrderUpgradeProbeFailureKeepsOldRows(t *testing.T) {
	db, lib, covers := audiobookFixture(t)
	book := filepath.Join(lib.Path, "Folder Name")
	genTaggedAudio(t, filepath.Join(book, "a.mp3"), "1", "2")
	path := filepath.Join(book, "z.mp3")
	genTaggedAudio(t, path, "1", "1")
	scanOnce(t, db, lib, covers)
	if _, err := db.Exec(`UPDATE files SET embedded_meta = '{}', seq = CASE WHEN path LIKE '%a.mp3' THEN 1 ELSE 2 END`); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, fi.Size()), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := Library(context.Background(), db, lib, covers, nil); err == nil {
		t.Fatal("legacy order repair succeeded despite an unreadable audio file")
	}
	if got := orderedBookPaths(t, db, book); !reflect.DeepEqual(got, []string{"a.mp3", "z.mp3"}) {
		t.Fatalf("failed repair changed order: %v", got)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM files WHERE embedded_meta != '{}'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed repair marked files complete: count %d err %v", count, err)
	}
}

func TestAudioOrderNaturalFallback(t *testing.T) {
	group := []bookFile{{path: "/book/Track 10.mp3"}, {path: "/book/CD10/1.mp3"}, {path: "/book/Track 2.mp3"}, {path: "/book/CD2/1.mp3"}}
	sortBookFiles("/book", group)
	var got []string
	for _, f := range group {
		got = append(got, f.path)
	}
	want := []string{"/book/CD2/1.mp3", "/book/CD10/1.mp3", "/book/Track 2.mp3", "/book/Track 10.mp3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("untagged order = %v, want %v", got, want)
	}
}

func TestAudioOrderUpgradeRebasesFileRelativeProgress(t *testing.T) {
	db, lib, covers := audiobookFixture(t)
	book := filepath.Join(lib.Path, "Folder Name")
	aPath, zPath := filepath.Join(book, "a.mp3"), filepath.Join(book, "z.mp3")
	genTaggedAudio(t, aPath, "1", "2")
	genTaggedAudio(t, zPath, "1", "1")
	scanOnce(t, db, lib, covers)
	var aID, zID, editionID int64
	if err := db.QueryRow(`SELECT id, edition_id FROM files WHERE path = ?`, aPath).Scan(&aID, &editionID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT id FROM files WHERE path = ?`, zPath).Scan(&zID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE files SET embedded_meta = '{}', seq = CASE WHEN id = ? THEN 1 ELSE 2 END, duration_secs = CASE WHEN id = ? THEN 10 ELSE 20 END`, aID, aID); err != nil {
		t.Fatal(err)
	}
	foreignWork, err := db.UpsertWork(&store.Work{LibraryID: lib.ID, Title: "Another Book"})
	if err != nil {
		t.Fatal(err)
	}
	foreignEdition, err := db.UpsertEdition(&store.Edition{WorkID: foreignWork, Format: "mp3", Title: "Another Book"})
	if err != nil {
		t.Fatal(err)
	}
	foreign := &store.FileRec{EditionID: foreignEdition, Path: filepath.Join(t.TempDir(), "foreign.mp3"), Seq: 1, DurationSecs: 50, Chapters: "[]"}
	if err := os.WriteFile(foreign.Path, []byte("fixture audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertFile(foreign); err != nil {
		t.Fatal(err)
	}
	type progressCase struct {
		name     string
		fileID   *int64
		offset   float64
		position float64
		want     float64
		deleted  bool
		finished bool
	}
	cases := []progressCase{
		{name: "moved earlier", fileID: &zID, offset: 1, position: 11, want: 1},
		{name: "moved later", fileID: &aID, offset: 3, position: 3, want: 23},
		{name: "completed", fileID: &zID, offset: 20, position: 30, want: 20, finished: true},
		{name: "already correct", fileID: &zID, offset: 1, position: 1, want: 1},
		{name: "reset", fileID: &zID, offset: 1, position: 11, want: 11, deleted: true},
		{name: "no file", offset: 0, position: 6, want: 6},
		{name: "foreign file", fileID: &foreign.ID, offset: 1, position: 6, want: 6},
	}
	userIDs := make([]int64, len(cases))
	for i, tc := range cases {
		uid, err := db.CreateUser(tc.name, "fixture", false)
		if err != nil {
			t.Fatal(err)
		}
		userIDs[i] = uid
		if err := db.SetProgress(&store.Progress{UserID: uid, EditionID: editionID, FileID: tc.fileID, FileOffsetSecs: tc.offset, EditionPositionSecs: tc.position, IsFinished: tc.finished, Device: strPtrForOrderTest("original device")}); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE progress SET updated_at = 42, revision = 7, deleted = ? WHERE user_id = ?`, tc.deleted, uid); err != nil {
			t.Fatal(err)
		}
	}
	scanOnce(t, db, lib, covers)
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := db.GetReadingProgress(userIDs[i], editionID)
			if err != nil {
				t.Fatal(err)
			}
			wantRevision := int64(7)
			if tc.want != tc.position {
				wantRevision++
			}
			if p.EditionPositionSecs != tc.want || !reflect.DeepEqual(p.FileID, tc.fileID) || p.FileOffsetSecs != tc.offset {
				t.Fatalf("repaired progress = %+v, want file %v offset %v position %v", p.Progress, tc.fileID, tc.offset, tc.want)
			}
			if p.Revision != wantRevision || p.UpdatedAt != 42 || p.IsFinished != tc.finished || p.Deleted != tc.deleted || p.Device == nil || *p.Device != "original device" {
				t.Fatalf("progress metadata changed incorrectly: %+v, want revision %d", p.Progress, wantRevision)
			}
		})
	}
	if p := scanOnce(t, db, lib, covers); p.FilesProbed != 0 {
		t.Fatalf("second scan probed %d files", p.FilesProbed)
	}
	p, err := db.GetProgress(userIDs[0], editionID)
	if err != nil || p.Revision != 8 {
		t.Fatalf("warm scan changed repaired progress: %+v, err %v", p, err)
	}
}

func strPtrForOrderTest(value string) *string { return &value }
