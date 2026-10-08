package store_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func TestGamePlaySessionsAccumulateAndFenceReset(t *testing.T) {
	db := openTestDB(t)
	eid := resetGenEdition(t, db, 1)
	if _, err := db.Exec(`UPDATE editions SET format='game-nes' WHERE id=?`, eid); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, session := range []string{"device-a", "device-b"} {
		wg.Add(1)
		go func(session string) {
			defer wg.Done()
			if _, err := db.ReportGamePlaytime(1, eid, 0, session, 10.5); err != nil {
				t.Error(err)
			}
		}(session)
	}
	wg.Wait()
	for _, elapsed := range []float64{10.5, 5, 12.5, 11, 12.5} {
		if _, err := db.ReportGamePlaytime(1, eid, 0, "device-a", elapsed); err != nil {
			t.Fatal(err)
		}
	}
	p, err := db.GetReadingProgress(1, eid)
	if err != nil || p.EditionPositionSecs != 23 || p.Revision != 3 {
		t.Fatalf("position/revision=%+v, %v", p, err)
	}
	if err := db.DeleteProgress(1, eid); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReportGamePlaytime(1, eid, 0, "device-a", 15); !errors.Is(err, store.ErrPlaytimeConflict) {
		t.Fatalf("reset revived: %v", err)
	}
	p2, err := db.ReportGamePlaytime(1, eid, 1, "device-a", 2)
	if err != nil || p2.EditionPositionSecs != 2 || p2.ResetGeneration != 1 {
		t.Fatalf("new epoch: %+v %v", p2, err)
	}
	if _, err := db.ReportGamePlaytime(1, eid, 1, "long-session", 3e6); err != nil {
		t.Fatal(err)
	}
}
