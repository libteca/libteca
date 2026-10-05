package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/api/abs"
	"github.com/libteca/libteca/internal/api/jellyfin"
	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/scan"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
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

// The omilator contract's optional per-file sha256: present for games
// editions (scan-time, or lazily backfilled for pre-0016 rows), absent for
// every other library type.
func TestGamesWorkPayloadSHA256(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, tok := seedUser(t, db)
	roms := t.TempDir()
	rom := filepath.Join(roms, "Mario Kart (USA).gba")
	if err := os.WriteFile(rom, []byte("mk"), 0o644); err != nil {
		t.Fatal(err)
	}
	libID, err := db.AddLibrary("Roms", "games", roms)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := db.Library(libID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scan.Library(context.Background(), db, lib, filepath.Join(dir, "covers"), nil); err != nil {
		t.Fatal(err)
	}
	works, err := db.WorksInLibrary(lib.ID)
	if err != nil || len(works) != 1 {
		t.Fatalf("works = %+v err = %v, want one work", works, err)
	}
	workID := works[0].ID
	want := fmt.Sprintf("%x", sha256.Sum256([]byte("mk")))

	books := t.TempDir()
	bookLib, err := db.AddLibrary("Books", "books", books)
	if err != nil {
		t.Fatal(err)
	}
	bw := seedWork(t, db, bookLib, "Book", nil, nil, 1, 1)
	be := seedBookEdition(t, db, bw, "epub", nil)
	seedFileOnDisk(t, db, bookLib, be, "book.epub", []byte("media bytes"))

	a := New(db, dir)
	app := neutron.New()
	a.MountPublic(app.Router().Group("/api/core"))
	a.Mount(app.Router().Group("/api/core", auth.MiddlewareWithMediaCookie(db, MediaRequest)))
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	fetchFiles := func(workID int64) []map[string]any {
		t.Helper()
		req, _ := http.NewRequest("GET", srv.URL+"/api/core/works/"+jsonInt(workID), nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("works/%d = %d", workID, resp.StatusCode)
		}
		var payload struct {
			Editions []struct {
				Files []map[string]any `json:"files"`
			} `json:"editions"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Editions) == 0 {
			t.Fatal("no editions in payload")
		}
		return payload.Editions[0].Files
	}

	files := fetchFiles(workID)
	if len(files) != 1 || files[0]["sha256"] != want {
		t.Fatalf("games file sha256 = %+v, want %s", files, want)
	}

	if _, err := db.Exec(`UPDATE files SET sha256 = NULL WHERE edition_id = ?`, works[0].Editions[0].ID); err != nil {
		t.Fatal(err)
	}
	files = fetchFiles(workID)
	if len(files) != 1 || files[0]["sha256"] != want {
		t.Fatalf("backfilled games file sha256 = %+v, want %s", files, want)
	}
	var stored sql.NullString
	if err := db.QueryRow(`SELECT sha256 FROM files WHERE edition_id = ?`, works[0].Editions[0].ID).Scan(&stored); err != nil || !stored.Valid || stored.String != want {
		t.Fatalf("backfill persisted = %q err = %v, want %s", stored.String, err, want)
	}

	bookFiles := fetchFiles(bw)
	if len(bookFiles) != 1 {
		t.Fatalf("book files = %+v, want one", bookFiles)
	}
	if _, ok := bookFiles[0]["sha256"]; ok {
		t.Fatalf("non-games file must not carry sha256: %+v", bookFiles[0])
	}
}

func seedUser(t *testing.T, db *store.DB) (int64, string) {
	t.Helper()
	now := time.Now().UnixMilli()
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('u','x',1,?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	uid, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.IssueToken(db, uid, "test")
	if err != nil {
		t.Fatal(err)
	}
	return uid, tok
}
