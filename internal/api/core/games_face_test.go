package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/libteca/libteca/internal/api/abs"
	"github.com/libteca/libteca/internal/api/jellyfin"
	"github.com/libteca/libteca/internal/auth"
	"github.com/neutron-build/neutron/go/neutron"
	"github.com/libteca/libteca/internal/store"
)

// Games libraries are core-API-only: no client protocol face has a shape for
// them (omilator is the client), so the Jellyfin/ABS/OPDS listings must not
// expose them (PLAN-GAMES §2). OPDS and Subsonic are already type-scoped in
// their store queries (books/comics and music respectively).
func TestGamesLibraryExcludedFromFaces(t *testing.T) {
	db, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := auth.InitAdmin(db, "admin", "pw12345678"); err != nil {
		t.Fatal(err)
	}
	var uid int64
	db.QueryRow(`SELECT id FROM users`).Scan(&uid)
	tok, err := auth.IssueToken(db, uid, "t")
	if err != nil {
		t.Fatal(err)
	}

	audioDir, gamesDir := t.TempDir(), t.TempDir()
	if _, err := db.AddLibrary("Audio", "audiobooks", audioDir); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddLibrary("Roms", "games", gamesDir); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	jf := jellyfin.New(db, dir, nil)
	ab := abs.New(db, dir)
	app := neutron.New()
	r := app.Router()
	jf.Mount(r.Group("/jf"))
	ab.Mount(r.Group("/jf/abs")) // abs and jellyfin share route shapes; nest to avoid clashes
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()

	get := func(url string) *http.Response {
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("X-Emby-Token", tok)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// Jellyfin views for the admin user: the games library must be absent.
	resp := get(srv.URL + "/jf/Users/" + jsonInt(uid) + "/Views?api_key=" + tok)
	var views struct {
		Items []struct{ Name string }
	}
	json.NewDecoder(resp.Body).Decode(&views)
	resp.Body.Close()
	for _, v := range views.Items {
		if v.Name == "Roms" {
			t.Fatal("games library leaked into Jellyfin views")
		}
	}

	// ABS libraries: the games library must be absent.
	resp = get(srv.URL + "/jf/abs/libraries")
	var absOut []struct{ Name string }
	json.NewDecoder(resp.Body).Decode(&absOut)
	resp.Body.Close()
	for _, l := range absOut {
		if l.Name == "Roms" {
			t.Fatal("games library leaked into ABS libraries")
		}
	}
}

func jsonInt(i int64) string {
	b, _ := json.Marshal(i)
	return string(b)
}
