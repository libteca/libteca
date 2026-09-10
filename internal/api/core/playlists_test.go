package core

import (
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron-go/neutron"
)

type playlistsEnv struct {
	db         *store.DB
	base       string
	adminToken string
}

func newPlaylistsEnv(t *testing.T) *playlistsEnv {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := auth.InitAdmin(db, "admin", "password123"); err != nil {
		t.Fatal(err)
	}
	a := New(db, t.TempDir())
	app := neutron.New()
	r := app.Router()
	a.MountPublic(r.Group("/api/core"))
	g := r.Group("/api/core", auth.Middleware(db))
	a.Mount(g)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return &playlistsEnv{db: db, base: srv.URL + "/api/core", adminToken: loginRequest(t, srv.URL+"/api/core", "admin", "password123")}
}

func (e *playlistsEnv) edition(t *testing.T, title string) int64 {
	t.Helper()
	res, err := e.db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('L','music','/x',0)`)
	if err != nil {
		t.Fatal(err)
	}
	libID, _ := res.LastInsertId()
	res, err = e.db.Exec(`INSERT INTO works (library_id, title, author, cover_path, created_at, updated_at) VALUES (?,?,?,?,0,0)`, libID, "Album "+title, "Artist", title+".jpg")
	if err != nil {
		t.Fatal(err)
	}
	workID, _ := res.LastInsertId()
	res, err = e.db.Exec(`INSERT INTO editions (work_id, format, title, duration_secs, position, created_at) VALUES (?,?,?,?,1,0)`, workID, "mp3", title, 100)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (e *playlistsEnv) userToken(t *testing.T, name string) string {
	t.Helper()
	code, _ := callJSON(t, "POST", e.base+"/users", e.adminToken, map[string]any{"name": name, "password": "password123"})
	if code != 201 {
		t.Fatalf("create user %s = %d", name, code)
	}
	return loginRequest(t, e.base, name, "password123")
}

func itemEditions(t *testing.T, v any) []int64 {
	t.Helper()
	row, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("detail not an object: %v", v)
	}
	items, ok := row["items"].([]any)
	if !ok {
		t.Fatalf("no items array: %v", row)
	}
	out := make([]int64, 0, len(items))
	for _, it := range items {
		m := it.(map[string]any)
		out = append(out, int64(m["editionId"].(float64)))
		if pos := m["position"].(float64); pos != float64(len(out)) {
			t.Fatalf("positions not compact 1..n: %v", items)
		}
	}
	return out
}

func eqIDs(t *testing.T, got, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestCorePlaylistLifecycle(t *testing.T) {
	e := newPlaylistsEnv(t)
	e1 := e.edition(t, "One")
	e2 := e.edition(t, "Two")
	e3 := e.edition(t, "Three")

	code, body := callJSON(t, "POST", e.base+"/playlists", e.adminToken, map[string]any{"name": "Mix"})
	if code != 201 {
		t.Fatalf("create = %d %v", code, body)
	}
	plID := int64(body.(map[string]any)["id"].(float64))

	code, _ = callJSON(t, "POST", e.base+"/playlists", e.adminToken, map[string]any{"name": ""})
	if code != 400 {
		t.Fatalf("empty name = %d, want 400", code)
	}

	// create with editionIds seeds ordered items
	code, body = callJSON(t, "POST", e.base+"/playlists", e.adminToken, map[string]any{"name": "Seeded", "editionIds": []int64{e3, e1}})
	if code != 201 {
		t.Fatalf("create seeded = %d %v", code, body)
	}
	seededID := int64(body.(map[string]any)["id"].(float64))
	code, body = callJSON(t, "GET", fmt.Sprintf("%s/playlists/%d", e.base, seededID), e.adminToken, nil)
	if code != 200 {
		t.Fatalf("get seeded = %d", code)
	}
	eqIDs(t, itemEditions(t, body), []int64{e3, e1})
	code, _ = callJSON(t, "POST", e.base+"/playlists", e.adminToken, map[string]any{"name": "Bad", "editionIds": []int64{999999}})
	if code != 404 {
		t.Fatalf("unknown edition in create = %d, want 404", code)
	}

	// items add/remove/reorder with compaction
	code, _ = callJSON(t, "POST", fmt.Sprintf("%s/playlists/%d/items", e.base, plID), e.adminToken, map[string]any{"editionId": e1})
	if code != 201 {
		t.Fatalf("add e1 = %d", code)
	}
	callJSON(t, "POST", fmt.Sprintf("%s/playlists/%d/items", e.base, plID), e.adminToken, map[string]any{"editionId": e2})
	callJSON(t, "POST", fmt.Sprintf("%s/playlists/%d/items", e.base, plID), e.adminToken, map[string]any{"editionId": e3})
	code, _ = callJSON(t, "POST", fmt.Sprintf("%s/playlists/%d/items", e.base, plID), e.adminToken, map[string]any{"editionId": 999999})
	if code != 404 {
		t.Fatalf("unknown edition add = %d, want 404", code)
	}
	code, _ = callJSON(t, "DELETE", fmt.Sprintf("%s/playlists/%d/items/%d", e.base, plID, e1), e.adminToken, nil)
	if code != 200 {
		t.Fatalf("remove e1 = %d", code)
	}
	code, _ = callJSON(t, "DELETE", fmt.Sprintf("%s/playlists/%d/items/%d", e.base, plID, e1), e.adminToken, nil)
	if code != 404 {
		t.Fatalf("remove absent item = %d, want 404", code)
	}

	code, body = callJSON(t, "GET", fmt.Sprintf("%s/playlists/%d", e.base, plID), e.adminToken, nil)
	if code != 200 {
		t.Fatalf("detail = %d", code)
	}
	detail := body.(map[string]any)
	if detail["songCount"].(float64) != 2 || detail["durationSecs"].(float64) != 200 || detail["owner"] != "admin" {
		t.Fatalf("detail stats = %v", detail)
	}
	first := detail["items"].([]any)[0].(map[string]any)
	if first["title"] != "Two" || first["format"] != "mp3" || first["workTitle"] != "Album Two" || first["hasCover"] != true {
		t.Fatalf("item join fields = %v", first)
	}

	code, _ = callJSON(t, "POST", fmt.Sprintf("%s/playlists/%d/reorder", e.base, plID), e.adminToken, map[string]any{"editionId": e3, "position": 1})
	if code != 200 {
		t.Fatalf("reorder = %d", code)
	}
	code, body = callJSON(t, "GET", fmt.Sprintf("%s/playlists/%d", e.base, plID), e.adminToken, nil)
	eqIDs(t, itemEditions(t, body), []int64{e3, e2})
	code, _ = callJSON(t, "POST", fmt.Sprintf("%s/playlists/%d/reorder", e.base, plID), e.adminToken, map[string]any{"editionId": e3, "position": 0})
	if code != 400 {
		t.Fatalf("position 0 = %d, want 400", code)
	}
	code, _ = callJSON(t, "POST", fmt.Sprintf("%s/playlists/%d/reorder", e.base, plID), e.adminToken, map[string]any{"editionId": 999999, "position": 1})
	if code != 404 {
		t.Fatalf("reorder absent item = %d, want 404", code)
	}

	// rename + delete
	code, _ = callJSON(t, "PATCH", fmt.Sprintf("%s/playlists/%d", e.base, plID), e.adminToken, map[string]any{"name": "Renamed"})
	if code != 200 {
		t.Fatalf("patch = %d", code)
	}
	code, body = callJSON(t, "GET", fmt.Sprintf("%s/playlists/%d", e.base, plID), e.adminToken, nil)
	if code != 200 || body.(map[string]any)["name"] != "Renamed" {
		t.Fatalf("after rename = %d %v", code, body)
	}
	code, _ = callJSON(t, "DELETE", fmt.Sprintf("%s/playlists/%d", e.base, plID), e.adminToken, nil)
	if code != 200 {
		t.Fatalf("delete = %d", code)
	}
	code, _ = callJSON(t, "GET", fmt.Sprintf("%s/playlists/%d", e.base, plID), e.adminToken, nil)
	if code != 404 {
		t.Fatalf("get deleted = %d, want 404", code)
	}
	code, _ = callJSON(t, "GET", e.base+"/playlists/999999", e.adminToken, nil)
	if code != 404 {
		t.Fatalf("unknown playlist = %d, want 404", code)
	}
}

func TestCorePlaylistAuthAndOwnership(t *testing.T) {
	e := newPlaylistsEnv(t)
	alice := e.userToken(t, "alice")
	bob := e.userToken(t, "bob")
	e1 := e.edition(t, "Shared")

	// no token: middleware rejects
	code, _ := callJSON(t, "GET", e.base+"/playlists", "", nil)
	if code != 401 {
		t.Fatalf("no token = %d, want 401", code)
	}

	code, body := callJSON(t, "POST", e.base+"/playlists", alice, map[string]any{"name": "Alices", "editionIds": []int64{e1}})
	if code != 201 {
		t.Fatalf("alice create = %d %v", code, body)
	}
	plID := int64(body.(map[string]any)["id"].(float64))

	// bob: every accessor answers 404 (no existence leak)
	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", fmt.Sprintf("%s/playlists/%d", e.base, plID), nil},
		{"PATCH", fmt.Sprintf("%s/playlists/%d", e.base, plID), map[string]any{"name": "Stolen"}},
		{"DELETE", fmt.Sprintf("%s/playlists/%d", e.base, plID), nil},
		{"POST", fmt.Sprintf("%s/playlists/%d/items", e.base, plID), map[string]any{"editionId": e1}},
		{"DELETE", fmt.Sprintf("%s/playlists/%d/items/%d", e.base, plID, e1), nil},
		{"POST", fmt.Sprintf("%s/playlists/%d/reorder", e.base, plID), map[string]any{"editionId": e1, "position": 1}},
	} {
		code, _ = callJSON(t, tc.method, tc.path, bob, tc.body)
		if code != 404 {
			t.Fatalf("bob %s %s = %d, want 404", tc.method, tc.path, code)
		}
	}

	// list scoping: bob sees none, alice sees hers, admin sees all
	code, body = callJSON(t, "GET", e.base+"/playlists", bob, nil)
	if code != 200 || len(jsonRows(t, body)) != 0 {
		t.Fatalf("bob list = %d %v", code, body)
	}
	code, body = callJSON(t, "GET", e.base+"/playlists", alice, nil)
	rows := jsonRows(t, body)
	if code != 200 || len(rows) != 1 || rows[0]["owner"] != "alice" {
		t.Fatalf("alice list = %d %v", code, body)
	}
	if _, leaked := rows[0]["passwordHash"]; leaked {
		t.Fatal("playlist row leaked passwordHash")
	}
	code, body = callJSON(t, "GET", e.base+"/playlists", e.adminToken, nil)
	rows = jsonRows(t, body)
	if code != 200 || len(rows) != 1 || rows[0]["ownerId"].(float64) != float64(userIDByName(t, e.db, "alice")) {
		t.Fatalf("admin list must include alice's: %v", rows)
	}

	// admin can read (and manage) another user's playlist
	code, _ = callJSON(t, "GET", fmt.Sprintf("%s/playlists/%d", e.base, plID), e.adminToken, nil)
	if code != 200 {
		t.Fatalf("admin get alice's = %d, want 200", code)
	}

	// playlist survives its items' reorder clamp at the boundary
	code, _ = callJSON(t, "POST", fmt.Sprintf("%s/playlists/%d/reorder", e.base, plID), alice, map[string]any{"editionId": e1, "position": 50})
	if code != 200 {
		t.Fatalf("reorder clamp = %d", code)
	}
	code, body = callJSON(t, "GET", fmt.Sprintf("%s/playlists/%d", e.base, plID), alice, nil)
	eqIDs(t, itemEditions(t, body), []int64{e1})
}
