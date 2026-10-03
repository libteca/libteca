package sourceinventory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func snapshotFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot & query?#.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	statements := []string{
		`INSERT INTO users VALUES (1,'synthetic','not-a-real-secret',0,1,1)`,
		`INSERT INTO libraries VALUES (1,'A','audiobooks','/recorded/a',1),(2,'B','books','/recorded/b',1),(3,'Nested','books','/recorded/a/nested',1)`,
		`INSERT INTO works (id,library_id,title,created_at,updated_at) VALUES (1,1,'Clean',1,1),(2,1,'Mixed',1,1),(3,1,'Ambiguous',1,1),(4,1,'Collapsed',1,1),(5,1,'Outside',1,1),(6,1,'Relative',1,1),(7,1,'Empty',1,1)`,
		`INSERT INTO editions (id,work_id,format,title,duration_secs,created_at) VALUES (10,1,'mp3','Clean',20,1),(20,2,'mp3','Mixed',20,1),(30,3,'epub','Ambiguous',0,1),(40,4,'pdf','Collapsed',0,1),(50,5,'video','Outside',10,1),(60,6,'mp3','Relative',10,1),(70,7,'epub','Empty',0,1)`,
		`INSERT INTO files (id,edition_id,path,seq,size_bytes,mtime_secs,duration_secs,probed_at,embedded_meta) VALUES (101,10,'/recorded/a/book/one.mp3',1,3,1,10,1,'{"album":"Synthetic"}'),(102,10,'/recorded/a/book/two.mp3',2,3,1,10,1,'{}'),(201,20,'/recorded/a/mixed/one.mp3',1,3,1,10,1,'{}'),(202,20,'/recorded/b/mixed/two.mp3',2,3,1,10,1,'{}'),(301,30,'/recorded/a/nested/a.epub',1,3,1,0,1,'{}'),(401,40,'/recorded/a/first/a.pdf',1,3,1,0,1,'{}'),(402,40,'/recorded/a/second/a.pdf',1,3,1,0,1,'{}'),(501,50,'/recorded/a-evil/film.mkv',1,3,1,10,1,'{}'),(601,60,'relative.mp3',1,3,1,10,1,'{}'),(701,NULL,'/podcast/download.mp3',1,3,1,10,1,'{}')`,
		`INSERT INTO progress (id,user_id,edition_id,file_id,file_offset_secs,edition_position_secs,duration_secs,is_finished,updated_at,revision,deleted,page,percent,locator) VALUES (1,1,10,102,2.5,12.5,20,0,1234,9,0,NULL,NULL,NULL),(2,1,40,401,0,0,0,0,1235,12,1,3,0.25,'synthetic-locator'),(3,1,20,101,1,1,20,0,1236,4,0,NULL,NULL,NULL)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(dir, "disposable-media")
	for _, name := range []string{"book/one.mp3", "book/two.mp3", "mixed/one.mp3", "nested/a.epub", "first/a.pdf", "second/a.pdf"} {
		target := filepath.Join(media, name)
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte("abc"), 0444); err != nil {
			t.Fatal(err)
		}
	}
	return path, media
}

func treeState(t *testing.T, root string) map[string]string {
	t.Helper()
	state := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		key, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		value := fmt.Sprintf("%v:%d:%d", info.Mode(), info.Size(), info.ModTime().UnixNano())
		if info.Mode().IsRegular() {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += fmt.Sprintf(":%x", sha256.Sum256(content))
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += ":" + target
		}
		state[key] = value
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func hasFinding(findings []string, expected string) bool {
	for _, finding := range findings {
		if finding == expected {
			return true
		}
	}
	return false
}

func editionByID(t *testing.T, report *Report, id int64) Edition {
	t.Helper()
	for _, e := range report.Editions {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("missing edition %d", id)
	return Edition{}
}

func TestReadOnlyInventoryPreservesBytesStateAndReferences(t *testing.T) {
	path, media := snapshotFixture(t)
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatal(err)
	}
	before := treeState(t, filepath.Dir(path))
	r, err := Read(context.Background(), path, []RootMapping{{1, media}})
	if err != nil {
		t.Fatal(err)
	}
	if got := treeState(t, filepath.Dir(path)); !reflect.DeepEqual(before, got) {
		t.Fatalf("inventory mutated snapshot/media tree\nbefore=%v\nafter=%v", before, got)
	}
	if !r.ReadOnly || r.OwnershipAssigned || r.Version != 1 {
		t.Fatalf("unsafe report contract: %+v", r)
	}
	clean := editionByID(t, r, 10)
	if len(clean.Findings) != 0 || len(clean.Files) != 2 || clean.Files[0].ID != 101 || clean.Files[1].ID != 102 {
		t.Fatalf("clean files/order changed: %+v", clean)
	}
	if clean.Files[0].Metadata != `{"album":"Synthetic"}` || clean.Files[1].Candidates[0].RelativePath != "book/two.mp3" || clean.Files[1].Candidates[0].Inspection != "regular_file" {
		t.Fatalf("lost metadata: %+v", clean.Files)
	}
	p := clean.Progress[0]
	if p.ID != 1 || p.UserID != 1 || p.Revision != 9 || p.UpdatedAt != 1234 || *p.FileID != 102 || p.FileOffset != 2.5 || p.Position != 12.5 {
		t.Fatalf("lost progress reference: %+v", p)
	}
	reset := editionByID(t, r, 40).Progress[0]
	if !reset.Deleted || reset.Revision != 12 || *reset.Page != 3 || *reset.Locator != "synthetic-locator" {
		t.Fatalf("lost tombstone: %+v", reset)
	}
	r2, err := Read(context.Background(), path, []RootMapping{{1, media}})
	if err != nil {
		t.Fatal(err)
	}
	first, _ := json.Marshal(r)
	second, _ := json.Marshal(r2)
	if string(first) != string(second) {
		t.Fatal("inventory not deterministic")
	}
	if strings.Contains(string(first), "not-a-real-secret") {
		t.Fatal("unrelated user authentication fields were read")
	}
}

func TestAmbiguousMixedCollapsedAndOutsideAreReportedNotRepaired(t *testing.T) {
	path, _ := snapshotFixture(t)
	before := treeState(t, filepath.Dir(path))
	r, err := Read(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		id      int64
		finding string
	}{{20, "mixed_lexical_roots"}, {20, "apparent_root_mismatch"}, {30, "ownership_review_required"}, {40, "possible_collapsed_edition"}, {40, "duplicate_file_sequence"}, {40, "multiple_parent_directories_review"}, {50, "apparent_root_mismatch"}, {60, "ownership_review_required"}, {70, "empty_edition"}}
	for _, tc := range cases {
		if !hasFinding(editionByID(t, r, tc.id).Findings, tc.finding) {
			t.Errorf("edition %d missing %s", tc.id, tc.finding)
		}
	}
	ambiguous := editionByID(t, r, 30).Files[0]
	if len(ambiguous.Candidates) != 2 || ambiguous.Candidates[0].LibraryID != 1 || ambiguous.Candidates[1].LibraryID != 3 {
		t.Fatalf("nested root ambiguity guessed away: %+v", ambiguous)
	}
	for _, candidate := range ambiguous.Candidates {
		if candidate.Inspection != "not_requested" {
			t.Fatal("stored media root was inspected")
		}
	}
	if len(editionByID(t, r, 50).Files[0].Candidates) != 0 {
		t.Fatal("raw path prefix treated as root membership")
	}
	if !hasFinding(editionByID(t, r, 20).Progress[0].Findings, "progress_file_not_in_edition") {
		t.Fatal("wrong edition progress reference not reported")
	}
	if len(r.UnattachedFiles) != 1 || r.UnattachedFiles[0].ID != 701 {
		t.Fatal("unattached/podcast file omitted")
	}
	if got := treeState(t, filepath.Dir(path)); !reflect.DeepEqual(before, got) {
		t.Fatal("classification repaired source state")
	}
}

func TestSQLiteConnectionRefusesWrites(t *testing.T) {
	path, _ := snapshotFixture(t)
	before := treeState(t, filepath.Dir(path))
	db, err := openReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE progress SET revision=100`); err == nil {
		t.Fatal("database allowed writes")
	}
	if _, err := db.Exec(`CREATE TABLE must_not_exist (id INTEGER)`); err == nil {
		t.Fatal("database allowed schema writes")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if got := treeState(t, filepath.Dir(path)); !reflect.DeepEqual(before, got) {
		t.Fatal("read-only connection modified snapshot")
	}
}

func TestRejectSnapshotSidecarsWithoutChangingAnything(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		t.Run(suffix, func(t *testing.T) {
			path, _ := snapshotFixture(t)
			if err := os.WriteFile(path+suffix, []byte("synthetic-sidecar"), 0444); err != nil {
				t.Fatal(err)
			}
			before := treeState(t, filepath.Dir(path))
			if r, err := Read(context.Background(), path, nil); err == nil || r != nil || !strings.Contains(err.Error(), "sidecar") {
				t.Fatalf("accepted sidecar: %v %+v", err, r)
			}
			if got := treeState(t, filepath.Dir(path)); !reflect.DeepEqual(before, got) {
				t.Fatal("sidecar rejection changed source")
			}
		})
	}
}

func TestSnapshotValidationNeverCreatesOrMigrates(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "missing.db")
		if _, err := Read(context.Background(), path, nil); err == nil {
			t.Fatal("accepted missing DB")
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 0 {
			t.Fatal("created DB")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		path, _ := snapshotFixture(t)
		link := filepath.Join(filepath.Dir(path), "link.db")
		if err := os.Symlink(path, link); err != nil {
			t.Fatal(err)
		}
		if _, err := Read(context.Background(), link, nil); err == nil {
			t.Fatal("accepted symlink DB")
		}
	})
	t.Run("old_schema", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "old.db")
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`CREATE TABLE libraries (id INTEGER PRIMARY KEY,name TEXT,type TEXT,path TEXT)`); err != nil {
			t.Fatal(err)
		}
		db.Close()
		before := treeState(t, filepath.Dir(path))
		if _, err := Read(context.Background(), path, nil); err == nil {
			t.Fatal("accepted incomplete old schema")
		}
		if got := treeState(t, filepath.Dir(path)); !reflect.DeepEqual(before, got) {
			t.Fatal("migrated old schema")
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		path, _ := snapshotFixture(t)
		before := treeState(t, filepath.Dir(path))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := Read(ctx, path, nil); err == nil {
			t.Fatal("ignored cancellation")
		}
		if got := treeState(t, filepath.Dir(path)); !reflect.DeepEqual(before, got) {
			t.Fatal("cancelled inventory changed source")
		}
	})
}

func TestExplicitMappedMediaIsMetadataOnlyAndConfined(t *testing.T) {
	path, media := snapshotFixture(t)
	if err := os.Remove(filepath.Join(media, "book/one.mp3")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/unmapped/outside.mp3", filepath.Join(media, "book/one.mp3")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(media, "book/two.mp3")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(media, "book/two.mp3"), []byte("size mismatch"), 0444); err != nil {
		t.Fatal(err)
	}
	before := treeState(t, filepath.Dir(path))
	r, err := Read(context.Background(), path, []RootMapping{{1, media}})
	if err != nil {
		t.Fatal(err)
	}
	files := editionByID(t, r, 10).Files
	if files[0].Candidates[0].Inspection != "symlink_not_followed" || files[1].Candidates[0].Inspection != "size_mismatch" {
		t.Fatalf("unexpected media evidence: %+v", files)
	}
	if got := treeState(t, filepath.Dir(path)); !reflect.DeepEqual(before, got) {
		t.Fatal("media inspection changed bytes/links/state")
	}
	for _, mappings := range [][]RootMapping{{{99, media}}, {{1, "relative"}}, {{1, media}, {1, media}}} {
		if _, err := Read(context.Background(), path, mappings); err == nil {
			t.Fatalf("accepted invalid mappings %+v", mappings)
		}
	}
}
