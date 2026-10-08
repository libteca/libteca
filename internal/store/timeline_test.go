package store_test

import (
	"errors"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func TestTimelineMutationAndFailedTransaction(t *testing.T) {
	db := openTestDB(t)
	eid := resetGenEdition(t, db, 1)
	file := &store.FileRec{EditionID: eid, Path: "/timeline-one", Seq: 1, DurationSecs: 10, Chapters: "[]"}
	if err := db.UpsertFile(file); err != nil {
		t.Fatal(err)
	}
	first, err := db.EditionTimeline(eid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE editions SET title='Renamed' WHERE id=?`, eid); err != nil {
		t.Fatal(err)
	}
	renamed, err := db.EditionTimeline(eid)
	if err != nil || renamed.Generation != first.Generation {
		t.Fatalf("title changed timeline: %v %v", renamed, err)
	}
	if err := db.Update(func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE files SET duration_secs=20 WHERE id=?`, file.ID)
		if err != nil {
			return err
		}
		return errors.New("rollback")
	}); err == nil {
		t.Fatal("expected rollback")
	}
	rolled, err := db.EditionTimeline(eid)
	if err != nil || rolled.Generation != first.Generation {
		t.Fatalf("rollback changed generation: %+v %v", rolled, err)
	}
	if _, err := db.Exec(`UPDATE files SET mtime_ns=99 WHERE id=?`, file.ID); err != nil {
		t.Fatal(err)
	}
	changed, err := db.EditionTimeline(eid)
	if err != nil || changed.Generation == first.Generation {
		t.Fatalf("replacement reused generation: %+v %v", changed, err)
	}
	if _, err := db.Exec(`UPDATE files SET missing=1 WHERE id=?`, file.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.EditionTimeline(eid); !errors.Is(err, store.ErrTimelineUnavailable) {
		t.Fatalf("missing membership silently removed: %v", err)
	}
}

func TestTimelineProgressRejectsConcurrentRescan(t *testing.T) {
	db := openTestDB(t)
	eid := resetGenEdition(t, db, 1)
	var generation int64
	if err := db.QueryRow(`SELECT timeline_generation FROM editions WHERE id=?`, eid).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE editions SET duration_secs=50 WHERE id=?`, eid); err != nil {
		t.Fatal(err)
	}
	p := &store.ReadingProgress{Progress: store.Progress{UserID: 1, EditionID: eid, EditionPositionSecs: 5}}
	if _, _, applied, err := db.SetReadingProgressTimelineRevision(p, store.ProgressFields{Position: true}, 0, 0, generation); err != nil || applied {
		t.Fatalf("stale timeline write: applied=%v err=%v", applied, err)
	}
}
