package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func TestSelectedPlaybackHonorsFileAndGeneration(t *testing.T) {
	srv, db, tok := newAuditDStack(t)
	eid := newScannedGamesEdition(t, db)
	if _, err := db.Exec(`UPDATE editions SET format='video' WHERE id=?`, eid); err != nil {
		t.Fatal(err)
	}
	files, err := db.EditionByID(eid)
	if err != nil {
		t.Fatal(err)
	}
	codec, container, audio := "h264", "mp4", "aac"
	first := files.Files[0]
	first.DurationSecs = 10
	first.VideoCodec = &codec
	first.Container = &container
	first.Codec = &audio
	if err := db.UpsertFile(&first); err != nil {
		t.Fatal(err)
	}
	second := &store.FileRec{EditionID: eid, Path: "/selected-second", Seq: 2, DurationSecs: 20, VideoCodec: &codec, Container: &container, Codec: &audio, Chapters: "[]"}
	if err := db.UpsertFile(second); err != nil {
		t.Fatal(err)
	}
	timeline, err := db.EditionTimeline(eid)
	if err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"contractVersion":1,"requestId":"test","generation":%q,"fileId":%d,"fileOffsetSecs":4,"mode":"auto"}`, timeline.Generation, second.ID)
	status, response := auditDPost(t, srv, tok, fmt.Sprintf("/editions/%d/playback-sessions", eid), body)
	if status != http.StatusCreated {
		t.Fatalf("selected playback=%d %s", status, response)
	}
	var result struct {
		FileID   int64   `json:"fileId"`
		Position float64 `json:"editionPositionSecs"`
	}
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		t.Fatal(err)
	}
	if result.FileID != second.ID || result.Position != 14 {
		t.Fatalf("wrong part/offset: %+v", result)
	}
	if _, err := db.Exec(`UPDATE files SET seq=3 WHERE id=?`, first.ID); err != nil {
		t.Fatal(err)
	}
	status, response = auditDPost(t, srv, tok, fmt.Sprintf("/editions/%d/playback-sessions", eid), body)
	if status != 409 {
		t.Fatalf("stale playback=%d %s", status, response)
	}
	status, response = auditDPost(t, srv, tok, fmt.Sprintf("/progress/%d", eid), fmt.Sprintf(`{"position":14,"revision":0,"resetGeneration":0,"expectedGeneration":%q}`, timeline.Generation))
	if status != 409 {
		t.Fatalf("stale save=%d %s", status, response)
	}
}

func TestGamePlaytimeRouteFencesLegacyTotals(t *testing.T) {
	srv, db, tok := newAuditDStack(t)
	eid := newScannedGamesEdition(t, db)
	path := fmt.Sprintf("/progress/%d/playtime", eid)
	for _, body := range []string{`{"sessionId":"a","elapsed":10.25,"resetGeneration":0}`, `{"sessionId":"b","elapsed":20,"resetGeneration":0}`, `{"sessionId":"a","elapsed":5,"resetGeneration":0}`} {
		status, response := auditDPost(t, srv, tok, path, body)
		if status != 200 {
			t.Fatalf("%d %s", status, response)
		}
	}
	status, response := auditDPost(t, srv, tok, fmt.Sprintf("/progress/%d", eid), `{"position":5}`)
	if status != 409 {
		t.Fatalf("legacy overwrite=%d %s", status, response)
	}
	p, err := db.GetReadingProgress(1, eid)
	if err != nil || p.EditionPositionSecs != 30.25 {
		t.Fatalf("total=%+v %v", p, err)
	}
	if err := db.DeleteProgress(1, eid); err != nil {
		t.Fatal(err)
	}
	status, response = auditDPost(t, srv, tok, path, `{"sessionId":"a","elapsed":20,"resetGeneration":0}`)
	if status != 409 {
		t.Fatalf("old session restored reset=%d %s", status, response)
	}
}
