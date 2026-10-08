package store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/pressly/goose/v3"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhysicalSourceIdentitySurvivesTitleAndGrouping(t *testing.T) {
	db := openLinkDB(t)
	lib, err := db.AddLibrary("physical", "books", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := "/physical/book.epub"
	var edition, work int64
	insert := func(title string) error {
		return db.Update(func(tx *Tx) error {
			w := &Work{LibraryID: lib, Title: title}
			var err error
			work, err = tx.UpsertSourceWork(w, []string{path}, lib)
			if err != nil {
				return err
			}
			e := &Edition{WorkID: work, Format: "epub", Title: title, SourceLibraryID: lib, SourceKey: "book.epub", SourcePaths: []string{path}}
			id, err := tx.UpsertEdition(e)
			if err != nil {
				return err
			}
			if edition != 0 && id != edition {
				t.Fatal("identity changed")
			}
			edition = id
			return tx.UpsertFile(&FileRec{EditionID: id, SourceLibraryID: lib, Path: path, SizeBytes: 1})
		})
	}
	if err := insert("original"); err != nil {
		t.Fatal(err)
	}
	target, err := db.UpsertWork(&Work{LibraryID: lib, Title: "manual grouping"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.MoveEditionToWork(edition, target); err != nil {
		t.Fatal(err)
	}
	if err := insert("changed embedded title"); err != nil {
		t.Fatal(err)
	}
	if work != target {
		t.Fatal("rescan undid manual grouping")
	}
	var title string
	db.QueryRow(`SELECT title FROM works WHERE id=?`, target).Scan(&title)
	if title != "manual grouping" {
		t.Fatal(title)
	}
	alternate := &Edition{WorkID: target, Format: "epub", Title: "original", SourceLibraryID: lib, SourceKey: "alternate.epub", SourcePaths: []string{"/physical/alternate.epub"}}
	id, err := db.UpsertEdition(alternate)
	if err != nil || id == edition {
		t.Fatalf("alternate identity %d %v", id, err)
	}
}

func TestPhysicalLibraryDeletionPreservesOtherSources(t *testing.T) {
	db := openLinkDB(t)
	first, _ := db.AddLibrary("first", "audiobooks", "/first")
	second, _ := db.AddLibrary("second", "audiobooks", "/second")
	work, _ := db.UpsertWork(&Work{LibraryID: first, Title: "shared"})
	retained, _ := db.UpsertEdition(&Edition{WorkID: work, Format: "m4b", Title: "retained", SourceLibraryID: second, SourceKey: "retained", SourcePaths: []string{"/second/a"}})
	if err := db.UpsertFile(&FileRec{EditionID: retained, SourceLibraryID: second, Path: "/second/a"}); err != nil {
		t.Fatal(err)
	}
	removed, _ := db.UpsertEdition(&Edition{WorkID: work, Format: "m4b", Title: "removed", SourceLibraryID: first, SourceKey: "removed", SourcePaths: []string{"/first/a"}})
	if err := db.UpsertFile(&FileRec{EditionID: removed, SourceLibraryID: first, Path: "/first/a"}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteLibrary(first); err != nil {
		t.Fatal(err)
	}
	root, err := db.LibraryRootForEdition(retained)
	if err != nil || root != "/second" {
		t.Fatal(root, err)
	}
	if _, err := db.EditionRow(removed); err != ErrNotFound {
		t.Fatal(err)
	}
	var moved int64
	db.QueryRow(`SELECT library_id FROM works WHERE id=?`, work).Scan(&moved)
	if moved != second {
		t.Fatal(moved)
	}
}

func TestReviewedSourceRepairRejectsStaleAndEscapingMaps(t *testing.T) {
	db := openLinkDB(t)
	root := t.TempDir()
	wrong, _ := db.AddLibrary("wrong", "books", t.TempDir())
	right, _ := db.AddLibrary("right", "books", root)
	path := filepath.Join(root, "a.epub")
	body := []byte("proof")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(path)
	work, _ := db.UpsertWork(&Work{LibraryID: wrong, Title: "repair"})
	edition, _ := db.UpsertEdition(&Edition{WorkID: work, Format: "epub", Title: "repair"})
	file := &FileRec{EditionID: edition, Path: path, SizeBytes: st.Size(), MtimeNS: st.ModTime().UnixNano()}
	if err := db.UpsertFile(file); err != nil {
		t.Fatal(err)
	}
	generation, _ := db.TimelineGeneration(edition)
	plan := SourceRepair{Version: 1, Reason: "reviewed fixture source ownership", Files: []SourceRepairFile{{FileID: file.ID, EditionID: edition, Generation: generation, OldSourceLibraryID: wrong, NewSourceLibraryID: right, Path: path, SizeBytes: st.Size(), MtimeNS: st.ModTime().UnixNano(), SHA256: fmt.Sprintf("%x", sha256.Sum256(body))}}}
	if err := db.RepairSources(plan, false); err != nil {
		t.Fatal(err)
	}
	rootBefore, _ := db.LibraryRootForFile(file.ID)
	if rootBefore == root {
		t.Fatal("dry run wrote")
	}
	if err := db.RepairSources(plan, true); err != nil {
		t.Fatal(err)
	}
	after, _ := db.LibraryRootForFile(file.ID)
	if after != root {
		t.Fatal(after)
	}
	if err := db.RepairSources(plan, true); err != ErrSourceConflict {
		t.Fatal("stale replay accepted", err)
	}
	plan.Files[0].NewSourceLibraryID = wrong
	if err := db.RepairSources(plan, true); err == nil {
		t.Fatal("escaping map accepted")
	}
}

func TestPhysicalSourceDowngradeRefusesLostOwnership(t *testing.T) {
	db := openLinkDB(t)
	first, _ := db.AddLibrary("first", "books", "/first")
	second, _ := db.AddLibrary("second", "books", "/second")
	work, _ := db.UpsertWork(&Work{LibraryID: first, Title: "a"})
	edition, _ := db.UpsertEdition(&Edition{WorkID: work, Format: "epub", Title: "a"})
	file := &FileRec{EditionID: edition, SourceLibraryID: first, Path: "/first/a"}
	if err := db.UpsertFile(file); err != nil {
		t.Fatal(err)
	}
	target, _ := db.UpsertWork(&Work{LibraryID: second, Title: "target"})
	if _, err := db.MoveEditionToWork(edition, target); err != nil {
		t.Fatal(err)
	}
	if err := goose.DownTo(db.DB, "migrations", 21); err != nil {
		t.Fatal(err)
	}
	if err := goose.Down(db.DB, "migrations"); err == nil {
		t.Fatal("unsafe ownership downgrade allowed")
	}
	root, err := db.LibraryRootForFile(file.ID)
	if err != nil || root != "/first" {
		t.Fatal(root, err)
	}
	if _, err := db.Exec(`UPDATE works SET library_id=? WHERE id=?`, first, target); err != nil {
		t.Fatal(err)
	}
	if err := goose.Down(db.DB, "migrations"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db.DB, "migrations"); err != nil {
		t.Fatal(err)
	}
	root, err = db.LibraryRootForFile(file.ID)
	if err != nil || root != "/first" {
		t.Fatal(root, err)
	}
}

func TestReviewedPhysicalSplitKeepsFileIDsAndResetsAffectedProgress(t *testing.T) {
	db := openLinkDB(t)
	root := t.TempDir()
	lib, _ := db.AddLibrary("physical", "audiobooks", root)
	work, _ := db.UpsertWork(&Work{LibraryID: lib, Title: "collapsed"})
	edition, _ := db.UpsertEdition(&Edition{WorkID: work, Format: "m4b", Title: "collapsed"})
	user := seedLinkUser(t, db)
	var files []*FileRec
	for i, name := range []string{"a.m4b", "b.m4b"} {
		path := filepath.Join(root, name)
		body := []byte(name)
		os.WriteFile(path, body, 0600)
		st, _ := os.Stat(path)
		file := &FileRec{EditionID: edition, SourceLibraryID: lib, Path: path, Seq: i + 1, SizeBytes: st.Size(), MtimeNS: st.ModTime().UnixNano(), DurationSecs: 10}
		if err := db.UpsertFile(file); err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	db.SetProgress(&Progress{UserID: user, EditionID: edition, EditionPositionSecs: 15, FileID: &files[1].ID, FileOffsetSecs: 5})
	before, _ := db.GetReadingProgress(user, edition)
	generation, _ := db.TimelineGeneration(edition)
	plan := SourceRepair{Version: 1, Reason: "reviewed separate encodings", ResetAffectedProgress: true, Files: []SourceRepairFile{{FileID: files[1].ID, EditionID: edition, Generation: generation, OldSourceLibraryID: lib, NewSourceLibraryID: lib, Path: files[1].Path, SizeBytes: files[1].SizeBytes, MtimeNS: files[1].MtimeNS, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("b.m4b"))), TargetSourceKey: "reviewed/b"}}}
	if err := db.RepairSources(plan, false); err != nil {
		t.Fatal(err)
	}
	if err := db.RepairSources(plan, true); err != nil {
		t.Fatal(err)
	}
	split, err := db.FileByID(files[1].ID)
	if err != nil || split.ID != files[1].ID || split.EditionID == edition {
		t.Fatal(split, err)
	}
	old, err := db.FileByID(files[0].ID)
	if err != nil || old.EditionID != edition {
		t.Fatal(old, err)
	}
	progress, err := db.GetReadingProgress(user, edition)
	if err != nil || progress.EditionPositionSecs != 0 || progress.ResetGeneration != before.ResetGeneration+1 || progress.Revision != before.Revision+1 {
		t.Fatal(progress, err)
	}
	newState, err := db.GetReadingProgress(user, split.EditionID)
	if err != nil || newState.ResetGeneration != 1 || !newState.Deleted {
		t.Fatal("split did not fence absent progress", newState, err)
	}
	_, _, applied, err := db.SetReadingProgressRevision(&ReadingProgress{Progress: Progress{UserID: user, EditionID: split.EditionID, EditionPositionSecs: 5}}, ProgressFields{Position: true}, 0, 0)
	if err != nil || applied {
		t.Fatal("old absent-row intent crossed repair", applied, err)
	}
	if err := db.RepairSources(plan, true); err != ErrSourceConflict {
		t.Fatal(err)
	}
}

func TestPlayedGamePhysicalDeletionDependencies(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		for _, rollback := range []bool{false, true} {
			t.Run(fmt.Sprintf("mixed=%v/rollback=%v", mixed, rollback), func(t *testing.T) {
				db := openLinkDB(t)
				uid := seedLinkUser(t, db)
				first, _ := db.AddLibrary("one", "games", "/one")
				second, _ := db.AddLibrary("two", "games", "/two")
				wid, _ := db.UpsertWork(&Work{LibraryID: first, Title: "played"})
				eid, err := db.UpsertEdition(&Edition{WorkID: wid, Format: "game-nes", Title: "played", SourceLibraryID: first, SourceKey: "game.nes"})
				if err != nil {
					t.Fatal(err)
				}
				file := &FileRec{EditionID: eid, SourceLibraryID: first, Path: "/one/game.nes"}
				if err := db.UpsertFile(file); err != nil {
					t.Fatal(err)
				}
				if mixed {
					if err := db.UpsertFile(&FileRec{EditionID: eid, SourceLibraryID: second, Path: "/two/game.nes"}); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := db.ReportGamePlaytime(uid, eid, 0, "session", 27.5); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`UPDATE progress SET file_id=?,file_offset_secs=9 WHERE user_id=? AND edition_id=?`, file.ID, uid, eid); err != nil {
					t.Fatal(err)
				}
				pl, err := db.CreatePlaylist(uid, "games")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.AddPlaylistItem(pl, eid); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`INSERT INTO playback_sessions(id,user_id,edition_id,file_id,started_at,updated_at) VALUES('s',?,?,?,?,?)`, uid, eid, file.ID, 0, 0); err != nil {
					t.Fatal(err)
				}
				if rollback {
					if _, err := db.Exec(`CREATE TRIGGER deny_source_delete BEFORE DELETE ON files BEGIN SELECT RAISE(ABORT,'hold'); END`); err != nil {
						t.Fatal(err)
					}
				}
				err = db.DeleteLibrary(first)
				if rollback {
					if err == nil {
						t.Fatal("delete should roll back")
					}
					var ref, total float64
					if err := db.QueryRow(`SELECT file_id,edition_position_secs FROM progress WHERE user_id=? AND edition_id=?`, uid, eid).Scan(&ref, &total); err != nil || int64(ref) != file.ID || total != 27.5 {
						t.Fatal(ref, total, err)
					}
				} else if err != nil {
					t.Fatal(err)
				} else if mixed {
					var ref *int64
					var total, watermark float64
					if err := db.QueryRow(`SELECT file_id,edition_position_secs FROM progress WHERE user_id=? AND edition_id=?`, uid, eid).Scan(&ref, &total); err != nil || ref != nil || total != 27.5 {
						t.Fatal(ref, total, err)
					}
					if err := db.QueryRow(`SELECT elapsed_secs FROM game_play_sessions WHERE user_id=? AND edition_id=?`, uid, eid).Scan(&watermark); err != nil || watermark != 27.5 {
						t.Fatal(watermark, err)
					}
					p, err := db.ReportGamePlaytime(uid, eid, 0, "session", 30)
					if err != nil || p.EditionPositionSecs != 30 {
						t.Fatal(p, err)
					}
				} else {
					for _, table := range []string{"editions", "progress", "playback_sessions", "playlist_items", "game_play_sessions"} {
						var n int
						if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
							t.Fatal(table, n, err)
						}
					}
				}
			})
		}
	}
}
func TestPhysicalProviderAliasAdoptionRequiresCompleteEvidence(t *testing.T) {
	for _, provider := range []string{"abs/book/foreign", "kavita/chapter/9"} {
		t.Run(provider, func(t *testing.T) {
			db := openLinkDB(t)
			lib, _ := db.AddLibrary("physical", "books", "/physical")
			wid, _ := db.UpsertWork(&Work{LibraryID: lib, Title: "curated"})
			proof := strings.Repeat("c", 64)
			original := &Edition{WorkID: wid, Format: "epub", Title: "import", SourceLibraryID: lib, SourceKey: provider, SourcePaths: []string{"/physical/a.epub"}, SourceDigests: []string{proof}}
			eid, err := db.UpsertEdition(original)
			if err != nil {
				t.Fatal(err)
			}
			file := &FileRec{EditionID: eid, SourceLibraryID: lib, Path: "/physical/a.epub", SHA256: &proof}
			if err := db.UpsertFile(file); err != nil {
				t.Fatal(err)
			}
			scanner := *original
			scanner.SourceKey = "a.epub"
			scanner.Title = "retagged"
			for i := 0; i < 2; i++ {
				got, err := db.UpsertEdition(&scanner)
				if err != nil || got != eid {
					t.Fatal(got, err)
				}
			}
			got, err := db.UpsertEdition(original)
			if err != nil || got != eid {
				t.Fatal(got, err)
			}
			var aliases int
			db.QueryRow(`SELECT count(*) FROM edition_source_aliases WHERE edition_id=?`, eid).Scan(&aliases)
			if aliases != 2 {
				t.Fatal(aliases)
			}
			ambiguous := scanner
			ambiguous.SourceKey = "unproven"
			ambiguous.SourceDigests = nil
			if _, err := db.UpsertEdition(&ambiguous); !errors.Is(err, ErrSourceConflict) {
				t.Fatal(err)
			}
			ambiguous.SourceDigests = []string{strings.Repeat("d", 64)}
			if _, err := db.UpsertEdition(&ambiguous); !errors.Is(err, ErrSourceConflict) {
				t.Fatal(err)
			}
		})
	}
}
