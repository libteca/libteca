package store_test

import (
	"errors"
	"fmt"
	"github.com/libteca/libteca/internal/store"
	"math"
	"strings"
	"testing"
)

func mediaFixture(t *testing.T) (*store.DB, store.MediaOperation) {
	t.Helper()
	db := openTestDB(t)
	eid := resetGenEdition(t, db, 1)
	if _, e := db.Exec(`UPDATE editions SET format='audio' WHERE id=?`, eid); e != nil {
		t.Fatal(e)
	}
	proof := strings.Repeat("b", 64)
	f := &store.FileRec{SHA256: &proof, EditionID: eid, Path: "/media-one", Seq: 1, DurationSecs: 100, Chapters: "[]"}
	if e := db.UpsertFile(f); e != nil {
		t.Fatal(e)
	}
	s, e := db.MediaSnapshot(1, "edition", eid)
	if e != nil {
		t.Fatal(e)
	}
	return db, store.MediaOperation{Version: 1, OperationID: "op-one", OwnerID: "1", Kind: "edition", TargetID: eid, InstallationID: "install", SessionID: "session", Sequence: 1, Generation: s.Generation, Intent: "seek", FileID: f.ID, Position: 70, FileOffset: 70, Duration: 100}
}
func TestMediaReceiptsExactlyOnceAndImmutable(t *testing.T) {
	db, o := mediaFixture(t)
	r, e := db.ApplyMediaOperation(1, o)
	if e != nil || r.Revision != 1 {
		t.Fatalf("first %+v %v", r, e)
	}
	for i := 0; i < 3; i++ {
		r, e = db.ApplyMediaOperation(1, o)
		if e != nil || r.Revision != 1 {
			t.Fatalf("replay %+v %v", r, e)
		}
	}
	p, e := db.GetReadingProgress(1, o.TargetID)
	if e != nil || p.Revision != 1 || p.EditionPositionSecs != 70 {
		t.Fatalf("progress %+v %v", p, e)
	}
	o.Position = 10
	o.FileOffset = 10
	if _, e = db.ApplyMediaOperation(1, o); !errors.Is(e, store.ErrMediaEnvelope) {
		t.Fatalf("changed envelope %v", e)
	}
	if _, e = db.MediaReceipt(2, o.OperationID); !errors.Is(e, store.ErrNotFound) {
		t.Fatalf("foreign receipt %v", e)
	}
}
func TestMediaOfflineChainRewindsWithoutChangingOriginalBase(t *testing.T) {
	db, o := mediaFixture(t)
	if _, e := db.ApplyMediaOperation(1, o); e != nil {
		t.Fatal(e)
	}
	next := o
	next.OperationID = "op-two"
	next.PredecessorID = o.OperationID
	next.Sequence = 2
	next.IntentEpoch = 1
	next.Position = 0
	next.FileOffset = 0
	next.Intent = "restart"
	r, e := db.ApplyMediaOperation(1, next)
	if e != nil || r.Revision != 2 {
		t.Fatalf("rewind %+v %v", r, e)
	}
	p, _ := db.GetReadingProgress(1, o.TargetID)
	if p.EditionPositionSecs != 0 {
		t.Fatal("seek max-merged")
	}
	next.OperationID = "op-three"
	next.PredecessorID = "op-two"
	next.Sequence = 3
	next.Position = 100
	next.FileOffset = 100
	next.Finished = true
	next.Intent = "finish"
	if _, e = db.ApplyMediaOperation(1, next); e != nil {
		t.Fatal(e)
	}
	p, _ = db.GetReadingProgress(1, o.TargetID)
	if !p.IsFinished || p.Revision != 3 {
		t.Fatalf("finish %+v", p)
	}
}
func TestMediaCASResetAndTimelineConflicts(t *testing.T) {
	for _, kind := range []string{"competing", "reset", "timeline", "missing"} {
		t.Run(kind, func(t *testing.T) {
			db, o := mediaFixture(t)
			switch kind {
			case "competing":
				if e := db.SetReadingProgress(&store.ReadingProgress{Progress: store.Progress{UserID: 1, EditionID: o.TargetID, EditionPositionSecs: 90}}); e != nil {
					t.Fatal(e)
				}
			case "reset":
				if e := db.DeleteProgress(1, o.TargetID); e != nil {
					t.Fatal(e)
				}
			case "timeline":
				if _, e := db.Exec(`UPDATE files SET seq=2 WHERE id=?`, o.FileID); e != nil {
					t.Fatal(e)
				}
			case "missing":
				if e := db.MarkFileMissing(o.FileID); e != nil {
					t.Fatal(e)
				}
			}
			if _, e := db.ApplyMediaOperation(1, o); !errors.Is(e, store.ErrMediaConflict) {
				t.Fatalf("conflict %v", e)
			}
			if _, e := db.MediaReceipt(1, o.OperationID); !errors.Is(e, store.ErrNotFound) {
				t.Fatal("failed operation recorded applied")
			}
		})
	}
}
func TestMediaReceiptAndProgressRollbackTogether(t *testing.T) {
	db, o := mediaFixture(t)
	if _, e := db.Exec(`CREATE TRIGGER fail_media_receipt BEFORE INSERT ON media_operation_receipts BEGIN SELECT RAISE(ABORT,'receipt failed'); END`); e != nil {
		t.Fatal(e)
	}
	if _, e := db.ApplyMediaOperation(1, o); e == nil {
		t.Fatal("receipt failure ignored")
	}
	if _, e := db.GetReadingProgress(1, o.TargetID); !errors.Is(e, store.ErrNotFound) {
		t.Fatalf("partial progress %v", e)
	}
	if _, e := db.MediaReceipt(1, o.OperationID); !errors.Is(e, store.ErrNotFound) {
		t.Fatal("partial receipt")
	}
}
func TestMediaRejectsOwnerBadNumbersAndForeignPredecessor(t *testing.T) {
	db, o := mediaFixture(t)
	for i, modify := range []func(*store.MediaOperation){func(v *store.MediaOperation) { v.OwnerID = "2" }, func(v *store.MediaOperation) { v.Position = math.NaN() }, func(v *store.MediaOperation) { v.FileOffset = 101 }, func(v *store.MediaOperation) { v.Finished = true }} {
		v := o
		v.OperationID = fmt.Sprint(i)
		modify(&v)
		if _, e := db.ApplyMediaOperation(1, v); !errors.Is(e, store.ErrMediaEnvelope) {
			t.Fatalf("invalid %d %v", i, e)
		}
	}
	if _, e := db.ApplyMediaOperation(1, o); e != nil {
		t.Fatal(e)
	}
	next := o
	next.OperationID = "foreign-chain"
	next.Sequence = 2
	next.SessionID = "other"
	next.PredecessorID = o.OperationID
	if _, e := db.ApplyMediaOperation(1, next); !errors.Is(e, store.ErrMediaConflict) {
		t.Fatalf("foreign chain %v", e)
	}
}
func TestPodcastMediaRevisionTombstoneAndContentFence(t *testing.T) {
	db := openTestDB(t)
	addUser(t, db)
	lib, e := db.AddLibrary("podcasts", "podcasts", t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	res, e := db.Exec(`INSERT INTO podcasts(library_id,feed_url,title,created_at) VALUES(?,'https://example.com/feed','show',0)`, lib)
	if e != nil {
		t.Fatal(e)
	}
	pid, _ := res.LastInsertId()
	res, e = db.Exec(`INSERT INTO podcast_episodes(podcast_id,guid,enclosure_url,created_at,duration_secs) VALUES(?,'ep','https://example.com/audio',0,100)`, pid)
	if e != nil {
		t.Fatal(e)
	}
	eid, _ := res.LastInsertId()
	fid, e := db.InsertPodcastFile("/podcast", 100, 1, "hash", 100, "mp3")
	if e != nil {
		t.Fatal(e)
	}
	if e = db.LinkEpisodeFile(eid, fid); e != nil {
		t.Fatal(e)
	}
	s, e := db.MediaSnapshot(1, "podcast-episode", eid)
	if e != nil {
		t.Fatal(e)
	}
	o := store.MediaOperation{Version: 1, OperationID: "podcast-one", OwnerID: "1", Kind: "podcast-episode", TargetID: eid, InstallationID: "install", SessionID: "session", Sequence: 1, Generation: s.Generation, Intent: "seek", FileID: fid, Position: 20, FileOffset: 20, Duration: 100}
	if _, e = db.ApplyMediaOperation(1, o); e != nil {
		t.Fatal(e)
	}
	if e = db.ResetEpisodeProgress(1, eid); e != nil {
		t.Fatal(e)
	}
	o.OperationID = "podcast-old"
	o.BaseRevision = 2
	if _, e = db.ApplyMediaOperation(1, o); !errors.Is(e, store.ErrMediaConflict) {
		t.Fatalf("reset revived %v", e)
	}
	s, _ = db.MediaSnapshot(1, "podcast-episode", eid)
	if !s.Deleted || s.ResetGeneration != 1 || s.Revision != 2 {
		t.Fatalf("tombstone %+v", s)
	}
	o.OperationID = "podcast-new"
	o.ResetGeneration = 1
	if _, e = db.ApplyMediaOperation(1, o); e != nil {
		t.Fatal(e)
	}
	if e = db.SetEpisodeProgress(&store.EpisodeProgress{UserID: 1, EpisodeID: eid, PositionSecs: 5}); e != nil {
		t.Fatal(e)
	}
	s, _ = db.MediaSnapshot(1, "podcast-episode", eid)
	if s.Revision != 4 || s.ResetGeneration != 1 {
		t.Fatalf("legacy revision %+v", s)
	}
	o.OperationID = "podcast-content"
	o.BaseRevision = 4
	if _, e = db.Exec(`UPDATE files SET hash='replacement' WHERE id=?`, fid); e != nil {
		t.Fatal(e)
	}
	if _, e = db.ApplyMediaOperation(1, o); !errors.Is(e, store.ErrMediaConflict) {
		t.Fatalf("changed content %v", e)
	}
}

func TestMediaResumeReorderAndContentReplacement(t *testing.T) {
	db, o := mediaFixture(t)
	proof := strings.Repeat("a", 64)
	f := &store.FileRec{EditionID: o.TargetID, Path: "/media-two", Seq: 2, DurationSecs: 20, Chapters: "[]", SHA256: &proof}
	if e := db.UpsertFile(f); e != nil {
		t.Fatal(e)
	}
	s, e := db.MediaSnapshot(1, "edition", o.TargetID)
	if e != nil {
		t.Fatal(e)
	}
	o.Generation = s.Generation
	o.FileID = f.ID
	o.FileOffset = 5
	o.Position = 105
	o.Duration = 120
	if _, e = db.ApplyMediaOperation(1, o); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`UPDATE files SET seq=CASE WHEN id=? THEN 1 ELSE 2 END WHERE edition_id=?`, f.ID, o.TargetID); e != nil {
		t.Fatal(e)
	}
	s, e = db.MediaSnapshot(1, "edition", o.TargetID)
	if e != nil {
		t.Fatal(e)
	}
	r, e := db.MediaResumeForTarget(1, "edition", o.TargetID, s.Generation)
	if e != nil || r.Conflict || r.FileID == nil || *r.FileID != f.ID || r.Position == nil || *r.Position != 5 || s.Position != 5 {
		t.Fatalf("reorder snapshot=%+v resume=%+v error=%v", s, r, e)
	}
	if _, e = db.Exec(`UPDATE files SET sha256='replacement' WHERE id=?`, f.ID); e != nil {
		t.Fatal(e)
	}
	s, e = db.MediaSnapshot(1, "edition", o.TargetID)
	if e != nil {
		t.Fatal(e)
	}
	r, e = db.MediaResumeForTarget(1, "edition", o.TargetID, s.Generation)
	if e != nil || !r.Conflict || r.Position != nil || !s.ResumeConflict || s.Position != 0 {
		t.Fatalf("replacement snapshot=%+v resume=%+v error=%v", s, r, e)
	}
}

func TestMediaMatchingBaseIntentGuards(t *testing.T) {
	for _, scenario := range []string{"finish", "reset", "backward", "epoch"} {
		t.Run(scenario, func(t *testing.T) {
			db, o := mediaFixture(t)
			o.IntentEpoch = 2
			if scenario == "finish" {
				o.Intent = "finish"
				o.Finished = true
			}
			if scenario == "reset" {
				o.Intent = "reset"
				o.Position = 0
				o.FileOffset = 0
			}
			receipt, err := db.ApplyMediaOperation(1, o)
			if err != nil {
				t.Fatal(err)
			}
			next := o
			next.OperationID = "guarded-heartbeat"
			next.Sequence = 2
			next.Intent = "heartbeat"
			next.Finished = false
			next.BaseRevision = receipt.Revision
			next.ResetGeneration = receipt.ResetGeneration
			if scenario == "backward" {
				next.Position = 1
				next.FileOffset = 1
			}
			if scenario == "epoch" {
				next.PredecessorID = o.OperationID
				next.IntentEpoch = 1
			}
			if _, err := db.ApplyMediaOperation(1, next); !errors.Is(err, store.ErrMediaConflict) {
				t.Fatalf("admitted %s: %v", scenario, err)
			}
			if _, err := db.MediaReceipt(1, next.OperationID); !errors.Is(err, store.ErrNotFound) {
				t.Fatal("rejection published receipt", err)
			}
			next.OperationID = "explicit-restart"
			next.PredecessorID = ""
			next.Intent = "restart"
			next.IntentEpoch = 3
			next.Position = 0
			next.FileOffset = 0
			if _, err := db.ApplyMediaOperation(1, next); err != nil {
				t.Fatal("deliberate restart rejected", err)
			}
		})
	}
}
func TestMediaWeakContentEvidenceNeverRebases(t *testing.T) {
	for _, hash := range []string{"", "sampled-collision"} {
		t.Run(hash, func(t *testing.T) {
			db, o := mediaFixture(t)
			if _, err := db.Exec(`UPDATE files SET sha256=NULL,hash=?,size_bytes=10,mtime_secs=9,mtime_ns=9000000000 WHERE id=?`, hash, o.FileID); err != nil {
				t.Fatal(err)
			}
			s, err := db.MediaSnapshot(1, o.Kind, o.TargetID)
			if err != nil {
				t.Fatal(err)
			}
			o.Generation = s.Generation
			if _, err := db.ApplyMediaOperation(1, o); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE files SET seq=2 WHERE id=?`, o.FileID); err != nil {
				t.Fatal(err)
			}
			s, err = db.MediaSnapshot(1, o.Kind, o.TargetID)
			if err != nil || !s.ResumeConflict || s.Position != 0 {
				t.Fatalf("weak proof accepted %+v %v", s, err)
			}
			var proof string
			if err := db.QueryRow(`SELECT media_file_identity FROM progress WHERE user_id=1 AND edition_id=?`, o.TargetID).Scan(&proof); err != nil || proof != "" {
				t.Fatal(proof, err)
			}
		})
	}
}
