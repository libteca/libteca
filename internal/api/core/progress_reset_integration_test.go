package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/libteca/libteca/internal/api/abs"
	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

func TestABSResetUpdatesCoreDiscovery(t *testing.T) {
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
	dataDir := t.TempDir()
	app := neutron.New()
	New(db, dataDir).Mount(app.Router().Group("/api/core", auth.Middleware(db)))
	abs.New(db, dataDir).Mount(app.Router().Group("/api/abs", auth.Middleware(db)))
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	lib, err := db.AddLibrary("TV", "tv", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	work := seedWork(t, db, lib, "Reset series", nil, nil, 1, 1)
	first := seedEdition(t, db, work, ptr(100.0))
	second := seedEdition(t, db, work, ptr(100.0))
	for i, edition := range []int64{first, second} {
		if _, err := db.Exec(`UPDATE editions SET season_num = 1, episode_num = ? WHERE id = ?`, i+1, edition); err != nil {
			t.Fatal(err)
		}
		seedFile(t, db, edition, filepath.Join(dataDir, itoa(edition)+".mp4"))
	}
	seedProgress(t, db, user, second, 40, 100, false, 10)
	get := func(path string, out any) {
		t.Helper()
		resp := authedGet(t, srv.URL+"/api/core"+path, token)
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s = %d: %s", path, resp.StatusCode, bodyStr(t, resp))
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	var before map[string]any
	get("/progress/"+itoa(second), &before)
	req, err := http.NewRequest("DELETE", srv.URL+"/api/abs/me/progress/"+itoa(second), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("ABS reset = %d: %s", resp.StatusCode, bodyStr(t, resp))
	}
	var resumed struct {
		Items []resumeItem `json:"items"`
	}
	get("/resume", &resumed)
	if len(resumed.Items) != 0 {
		t.Fatalf("reset remained in resume: %+v", resumed.Items)
	}
	for _, filter := range []string{"all", "unplayed", "in_progress", "finished"} {
		var works []struct {
			ID      int64    `json:"id"`
			Percent *float64 `json:"percent"`
		}
		get("/libraries/"+itoa(lib)+"/works?filter="+filter, &works)
		want := 0
		if filter == "all" || filter == "unplayed" {
			want = 1
		}
		if len(works) != want {
			t.Fatalf("filter %s = %+v, want %d works", filter, works, want)
		}
		if len(works) > 0 && (works[0].ID != work || works[0].Percent != nil) {
			t.Fatalf("reset work percent = %+v", works[0])
		}
	}
	var search struct {
		Results []searchResult `json:"results"`
	}
	get("/search?q=Reset", &search)
	if len(search.Results) != 1 || search.Results[0].WorkID != work || search.Results[0].Percent != nil {
		t.Fatalf("reset search = %+v", search.Results)
	}
	var next struct {
		Items []struct {
			EditionID int64 `json:"editionId"`
		} `json:"items"`
	}
	get("/nextup", &next)
	if len(next.Items) != 1 || next.Items[0].EditionID != first {
		t.Fatalf("reset next up = %+v", next.Items)
	}
	var baseline map[string]any
	get("/progress/"+itoa(second), &baseline)
	if baseline["position"] != float64(0) || baseline["revision"] != before["revision"].(float64)+1 {
		t.Fatalf("reset revision baseline = %v; before = %v", baseline, before)
	}
}
