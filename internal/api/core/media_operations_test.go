package core

import (
	"encoding/json"
	"fmt"
	"github.com/libteca/libteca/internal/store"
	"net/http"
	"testing"
)

func TestMediaOperationHTTPReceiptAndConflict(t *testing.T) {
	srv, db, tok := newAuditDStack(t)
	eid := newScannedGamesEdition(t, db)
	if _, e := db.Exec(`UPDATE editions SET format='video' WHERE id=?`, eid); e != nil {
		t.Fatal(e)
	}
	ed, e := db.EditionByID(eid)
	if e != nil {
		t.Fatal(e)
	}
	f := ed.Files[0]
	f.DurationSecs = 100
	if e = db.UpsertFile(&f); e != nil {
		t.Fatal(e)
	}
	s, e := db.MediaSnapshot(1, "edition", eid)
	if e != nil {
		t.Fatal(e)
	}
	o := store.MediaOperation{Version: 1, OperationID: "http-one", OwnerID: s.OwnerID, Kind: "edition", TargetID: eid, InstallationID: "install", SessionID: "session", Sequence: 1, Generation: s.Generation, Intent: "seek", FileID: f.ID, FileOffset: 30, Position: 30, Duration: 100}
	raw, _ := json.Marshal(o)
	status, body := auditDPost(t, srv, tok, "/media-operations", string(raw))
	if status != 200 {
		t.Fatalf("apply %d %s", status, body)
	}
	status, body = auditDPost(t, srv, tok, "/media-operations", string(raw))
	if status != 200 {
		t.Fatalf("replay %d %s", status, body)
	}
	p, _ := db.GetReadingProgress(1, eid)
	if p.Revision != 1 {
		t.Fatal("double apply")
	}
	req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/core/media-operations/http-one", srv.URL), nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	res, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("receipt %d", res.StatusCode)
	}
	o.OperationID = "http-conflict"
	raw, _ = json.Marshal(o)
	status, body = auditDPost(t, srv, tok, "/media-operations", string(raw))
	if status != 409 {
		t.Fatalf("stale CAS %d %s", status, body)
	}
	o.OperationID = "http-foreign"
	o.OwnerID = "999"
	raw, _ = json.Marshal(o)
	status, _ = auditDPost(t, srv, tok, "/media-operations", string(raw))
	if status != 400 {
		t.Fatalf("owner %d", status)
	}
}

func TestMediaWorkDetailAtomicDurationsAndUnavailableEditions(t *testing.T) {
	srv, db, tok := newAuditDStack(t)
	eid := newScannedGamesEdition(t, db)
	if _, e := db.Exec(`UPDATE editions SET format='video',duration_secs=999 WHERE id=?`, eid); e != nil {
		t.Fatal(e)
	}
	ed, e := db.EditionByID(eid)
	if e != nil {
		t.Fatal(e)
	}
	f := ed.Files[0]
	f.DurationSecs = 10
	f.Chapters = `[{"title":"First","start":1,"end":2}]`
	if e = db.UpsertFile(&f); e != nil {
		t.Fatal(e)
	}
	second := &store.FileRec{EditionID: eid, Path: "/detail-part-two", Seq: 2, DurationSecs: 20, Chapters: `[{"title":"Second","start":1,"end":2}]`}
	if e = db.UpsertFile(second); e != nil {
		t.Fatal(e)
	}
	res, e := db.Exec(`INSERT INTO editions(work_id,format,title,created_at) VALUES (?,'video','Empty',0)`, ed.WorkID)
	if e != nil {
		t.Fatal(e)
	}
	empty, _ := res.LastInsertId()
	get := func() []map[string]any {
		t.Helper()
		req, _ := http.NewRequest("GET", fmt.Sprintf("%s/api/core/works/%d", srv.URL, ed.WorkID), nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		r, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		if r.StatusCode != 200 {
			t.Fatalf("detail status=%d", r.StatusCode)
		}
		var v struct {
			Editions []map[string]any `json:"editions"`
		}
		if e = json.NewDecoder(r.Body).Decode(&v); e != nil {
			t.Fatal(e)
		}
		return v.Editions
	}
	find := func(eds []map[string]any, id int64) map[string]any {
		for _, v := range eds {
			if v["id"] == float64(id) {
				return v
			}
		}
		t.Fatalf("missing edition %d", id)
		return nil
	}
	all := get()
	v := find(all, eid)
	if v["duration"] != float64(30) || v["durationKnown"] != true || v["available"] != true {
		t.Fatalf("atomic duration %+v", v)
	}
	unavailable := find(all, empty)
	if unavailable["available"] != false || len(unavailable["files"].([]any)) != 0 || unavailable["unavailableReason"] == nil {
		t.Fatalf("empty edition %+v", unavailable)
	}
	if _, e = db.Exec(`UPDATE files SET duration_secs=0 WHERE id=?`, f.ID); e != nil {
		t.Fatal(e)
	}
	v = find(get(), eid)
	if v["duration"] != float64(0) || v["durationKnown"] != false {
		t.Fatalf("unknown total %+v", v)
	}
	chapters := v["chapters"].([]any)
	if len(chapters) != 1 || chapters[0].(map[string]any)["fileId"] != float64(f.ID) {
		t.Fatalf("fabricated unknown chapter offsets %+v", chapters)
	}
	if _, e = db.Exec(`UPDATE files SET missing=1 WHERE id=?`, second.ID); e != nil {
		t.Fatal(e)
	}
	v = find(get(), eid)
	if v["available"] != false || len(v["files"].([]any)) != 0 {
		t.Fatalf("partial timeline exposed %+v", v)
	}
}

func TestMediaDirectRangeGenerationFence(t *testing.T) {
	srv, db, tok := newAuditDStack(t)
	eid := newScannedGamesEdition(t, db)
	ed, e := db.EditionByID(eid)
	if e != nil {
		t.Fatal(e)
	}
	f := ed.Files[0]
	generation, e := db.TimelineGeneration(eid)
	if e != nil {
		t.Fatal(e)
	}
	get := func(gen string) int {
		t.Helper()
		url := fmt.Sprintf("%s/api/core/stream/%d", srv.URL, f.ID)
		if gen != "" {
			url += "?generation=" + gen
		}
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Range", "bytes=0-0")
		r, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		return r.StatusCode
	}
	if code := get(generation); code != 206 {
		t.Fatalf("current range status=%d", code)
	}
	if _, e = db.Exec(`UPDATE files SET seq=seq+1 WHERE id=?`, f.ID); e != nil {
		t.Fatal(e)
	}
	if code := get(generation); code != 409 {
		t.Fatalf("stale range status=%d", code)
	}
	if code := get(""); code != 206 {
		t.Fatalf("legacy range status=%d", code)
	}
}
