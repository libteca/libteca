package core

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

func postProgress(t *testing.T, url, token string, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode %d: %v", resp.StatusCode, err)
	}
	return resp.StatusCode, out
}

func TestResetWinsAgainstRelabeledReplay(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	user, err := db.CreateUser("reader", "unused", false)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.IssueToken(db, user, "test")
	if err != nil {
		t.Fatal(err)
	}
	app := neutron.New()
	New(db, t.TempDir()).Mount(app.Router().Group("/api/core", auth.Middleware(db)))
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	lib, err := db.AddLibrary("Books", "books", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := seedWork(t, db, lib, "Reset fence", nil, nil, 1, 1)
	eid := seedEdition(t, db, work, nil)
	seedFile(t, db, eid, filepath.Join(t.TempDir(), "a.mp4"))
	base := srv.URL + "/api/core/progress/" + itoa(eid)

	status, out := postProgress(t, base, token, map[string]any{"page": 80, "percent": 0.8, "revision": 0, "resetGeneration": 0})
	if status != 200 || out["revision"] != float64(1) {
		t.Fatalf("first write = %d %v", status, out)
	}
	if err := db.DeleteProgress(user, eid); err != nil {
		t.Fatal(err)
	}
	resp := authedGet(t, base, token)
	defer resp.Body.Close()
	var view map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if view["deleted"] != true || view["resetGeneration"] != float64(1) || view["revision"] != float64(2) {
		t.Fatalf("post-reset read = %v, want deleted revision 2 generation 1", view)
	}

	status, out = postProgress(t, base, token, map[string]any{"page": 80, "percent": 0.8, "revision": 2, "resetGeneration": 0})
	if status != 409 {
		t.Fatalf("relabeled pre-reset replay = %d %v, want 409", status, out)
	}
	if cur, ok := out["current"].(map[string]any); !ok || cur["deleted"] != true || cur["resetGeneration"] != float64(1) {
		t.Fatalf("reset conflict current = %v", out["current"])
	}
	p, err := db.GetReadingProgress(user, eid)
	if err != nil || !p.Deleted || p.Page != nil {
		t.Fatalf("relabeled replay resurrected the reset row: %+v err %v", p, err)
	}

	status, out = postProgress(t, base, token, map[string]any{"page": 3, "percent": 0.03, "revision": 2, "resetGeneration": 1})
	if status != 200 || out["revision"] != float64(3) || out["resetGeneration"] != float64(1) {
		t.Fatalf("deliberate post-reset write = %d %v", status, out)
	}
	p, err = db.GetReadingProgress(user, eid)
	if err != nil || p.Deleted || p.Page == nil || *p.Page != 3 || p.ResetGeneration != 1 {
		t.Fatalf("post-reset state = %+v err %v, want live page 3 generation 1", p, err)
	}

	if err := db.DeleteProgress(user, eid); err != nil {
		t.Fatal(err)
	}
	p, err = db.GetReadingProgress(user, eid)
	if err != nil || p.ResetGeneration != 2 {
		t.Fatalf("second reset = %+v err %v, want generation 2", p, err)
	}
	status, _ = postProgress(t, base, token, map[string]any{"page": 5, "revision": 3, "resetGeneration": 1})
	if status != 409 {
		t.Fatalf("stale-generation replay after second reset = %d, want 409", status)
	}
}

func TestResetConflictResponseCarriesLineage(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	user, err := db.CreateUser("reader2", "unused", false)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.IssueToken(db, user, "test")
	if err != nil {
		t.Fatal(err)
	}
	app := neutron.New()
	New(db, t.TempDir()).Mount(app.Router().Group("/api/core", auth.Middleware(db)))
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	lib, err := db.AddLibrary("Books", "books", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := seedWork(t, db, lib, "Conflict shape", nil, nil, 1, 1)
	eid := seedEdition(t, db, work, nil)
	seedFile(t, db, eid, filepath.Join(t.TempDir(), "b.mp4"))
	base := srv.URL + "/api/core/progress/" + itoa(eid)

	status, _ := postProgress(t, base, token, map[string]any{"page": 10, "revision": 0})
	if status != 200 {
		t.Fatalf("first write = %d", status)
	}
	status, out := postProgress(t, base, token, map[string]any{"page": 4, "revision": 7})
	if status != 409 {
		t.Fatalf("ordinary stale write = %d %v, want 409", status, out)
	}
	if out["error"] != "stale progress revision" {
		t.Fatalf("ordinary conflict error = %v", out["error"])
	}
	cur, ok := out["current"].(map[string]any)
	if !ok || cur["resetGeneration"] != float64(0) || cur["deleted"] != false {
		t.Fatalf("ordinary conflict current = %v", out["current"])
	}
	if err := db.DeleteProgress(user, eid); err != nil {
		t.Fatal(err)
	}
	status, out = postProgress(t, base, token, map[string]any{"page": 4, "revision": 1, "resetGeneration": 0})
	if status != 409 || out["error"] != "progress was reset" {
		t.Fatalf("reset conflict = %d %v", status, out)
	}
	cur, ok = out["current"].(map[string]any)
	if !ok || cur["resetGeneration"] != float64(1) || cur["deleted"] != true {
		t.Fatalf("reset conflict current = %v", out["current"])
	}
}
