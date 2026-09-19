package core

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

func seedBookEdition(t *testing.T, db *store.DB, workID int64, format string, pageCount *int) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO editions (work_id, format, title, page_count, created_at) VALUES (?,?,?,?,0)`,
		workID, format, "T", pageCount)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

type readingEnv struct {
	db    *store.DB
	base  string
	token string
	srv   *httptest.Server
}

func newReadingEnv(t *testing.T) *readingEnv {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('u','x',0,0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := res.LastInsertId()
	token, err := auth.IssueToken(db, uid, "test")
	if err != nil {
		t.Fatal(err)
	}
	a := New(db, t.TempDir())
	app := neutron.New()
	g := app.Router().Group("/api/core", auth.Middleware(db))
	a.Mount(g) // Mount ends with a.MountReading(r): download route included
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return &readingEnv{db: db, base: srv.URL + "/api/core", token: token, srv: srv}
}

// seedFileOnDisk writes content to a temp file and links it as the edition's
// single file row; the download endpoint must serve exactly these bytes.
func seedFileOnDisk(t *testing.T, db *store.DB, libID int64, editionID int64, name string, content []byte) string {
	t.Helper()
	var libDir string
	if err := db.QueryRow(`SELECT path FROM libraries WHERE id = ?`, libID).Scan(&libDir); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(libDir, name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, probed_at) VALUES (?,?,1,?,1,0,0)`,
		editionID, p, len(content)); err != nil {
		t.Fatal(err)
	}
	return p
}

func readingReq(t *testing.T, env *readingEnv, method, url, token, body string) (*http.Response, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	data, _ := io.ReadAll(resp.Body)
	return resp, string(data)
}

func TestEditionDownload(t *testing.T) {
	env := newReadingEnv(t)
	lib, _ := env.db.AddLibrary("Books", "books", t.TempDir())
	w := seedWork(t, env.db, lib, "The Trial", ptr("Franz Kafka"), nil, 1, 1)
	epub := seedBookEdition(t, env.db, w, "epub", ptr(3))
	content := []byte("PK\x03\x04 epub bytes")
	seedFileOnDisk(t, env.db, lib, epub, "trial.epub", content)

	resp, body := readingReq(t, env, "GET", env.base+fmt.Sprintf("/editions/%d/download", epub), env.token, "")
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/epub+zip" {
		t.Fatalf("content type = %q", ct)
	}
	if body != string(content) {
		t.Fatalf("body = %q", body)
	}

	// ?token= query auth (img/a-tag style) works without the header.
	resp, _ = readingReq(t, env, "GET", fmt.Sprintf("%s/editions/%d/download?token=%s", env.base, epub, env.token), "", "")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/epub+zip" {
		t.Fatalf("token query: status %d ct %q", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	// Client-supplied paths are ignored; only the DB path is ever served.
	resp, body = readingReq(t, env, "GET", fmt.Sprintf("%s/editions/%d/download?path=/etc/passwd&file=../../../../etc/passwd", env.base, epub), env.token, "")
	if resp.StatusCode != 200 || body != string(content) {
		t.Fatalf("path params must be ignored: status %d", resp.StatusCode)
	}

	// No token -> 401; unknown edition -> 404.
	resp, _ = readingReq(t, env, "GET", env.base+fmt.Sprintf("/editions/%d/download", epub), "", "")
	if resp.StatusCode != 401 {
		t.Fatalf("unauth status = %d, want 401", resp.StatusCode)
	}
	resp, _ = readingReq(t, env, "GET", env.base+"/editions/999999/download", env.token, "")
	if resp.StatusCode != 404 {
		t.Fatalf("unknown edition status = %d, want 404", resp.StatusCode)
	}
}

func TestEditionDownloadContentTypes(t *testing.T) {
	env := newReadingEnv(t)
	lib, _ := env.db.AddLibrary("Comics", "comics", t.TempDir())
	w := seedWork(t, env.db, lib, "C", nil, nil, 1, 1)
	cbz := seedBookEdition(t, env.db, w, "cbz", ptr(24))
	pdf := seedBookEdition(t, env.db, w, "pdf", ptr(120))
	seedFileOnDisk(t, env.db, lib, cbz, "c.cbz", []byte("PK cbz"))
	seedFileOnDisk(t, env.db, lib, pdf, "c.pdf", []byte("%PDF-1.4"))

	for id, want := range map[int64]string{cbz: "application/zip", pdf: "application/pdf"} {
		resp, _ := readingReq(t, env, "GET", env.base+fmt.Sprintf("/editions/%d/download", id), env.token, "")
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != want {
			t.Fatalf("edition %d: status %d ct %q, want %q", id, resp.StatusCode, resp.Header.Get("Content-Type"), want)
		}
	}
}

func TestProgressReadingRoundtrip(t *testing.T) {
	env := newReadingEnv(t)
	lib, _ := env.db.AddLibrary("Books", "books", t.TempDir())
	w := seedWork(t, env.db, lib, "The Trial", nil, nil, 1, 1)
	epub := seedBookEdition(t, env.db, w, "epub", ptr(3))
	seedFileOnDisk(t, env.db, lib, epub, "trial.epub", []byte("PK"))

	post := func(body string) (int, string) {
		resp, b := readingReq(t, env, "POST", env.base+fmt.Sprintf("/progress/%d", epub), env.token, body)
		return resp.StatusCode, b
	}

	if code, _ := post(`{"page":12,"percent":0.34,"locator":"chap2.xhtml"}`); code != 200 {
		t.Fatalf("post = %d", code)
	}
	resp, body := readingReq(t, env, "GET", env.base+fmt.Sprintf("/progress/%d", epub), env.token, "")
	if resp.StatusCode != 200 {
		t.Fatalf("get = %d", resp.StatusCode)
	}
	var got struct {
		Page    *int64   `json:"page"`
		Percent *float64 `json:"percent"`
		Locator *string  `json:"locator"`
	}
	json.Unmarshal([]byte(body), &got)
	if got.Page == nil || *got.Page != 12 || got.Percent == nil || *got.Percent != 0.34 || got.Locator == nil || *got.Locator != "chap2.xhtml" {
		t.Fatalf("get body = %s", body)
	}

	if code, _ := post(`{"percent":1.5}`); code != 400 {
		t.Fatalf("percent 1.5 = %d, want 400", code)
	}
	if code, _ := post(`{"percent":-0.1}`); code != 400 {
		t.Fatalf("percent -0.1 = %d, want 400", code)
	}
	if code, _ := post(`{"percent":1}`); code != 200 {
		t.Fatalf("percent 1 = %d, want 200", code)
	}

	// Page-only update keeps percent/locator (coalesce semantics).
	if code, _ := post(`{"page":13}`); code != 200 {
		t.Fatalf("page-only post = %d", code)
	}
	_, body = readingReq(t, env, "GET", env.base+fmt.Sprintf("/progress/%d", epub), env.token, "")
	json.Unmarshal([]byte(body), &got)
	if *got.Page != 13 || *got.Locator != "chap2.xhtml" {
		t.Fatalf("page-only update body = %s", body)
	}

	// Audio-style post (no reading fields) never wipes reading state.
	if code, _ := post(`{"position":0,"duration":0}`); code != 200 {
		t.Fatalf("audio-style post = %d", code)
	}
	_, body = readingReq(t, env, "GET", env.base+fmt.Sprintf("/progress/%d", epub), env.token, "")
	json.Unmarshal([]byte(body), &got)
	if got.Page == nil || *got.Page != 13 {
		t.Fatalf("audio-style post wiped reading state: %s", body)
	}

	// Fresh edition: GET returns the classic default shape with no reading keys.
	other := seedBookEdition(t, env.db, w, "cbz", ptr(24))
	seedFileOnDisk(t, env.db, lib, other, "c.cbz", []byte("PK"))
	_, body = readingReq(t, env, "GET", env.base+fmt.Sprintf("/progress/%d", other), env.token, "")
	if strings.Contains(body, `"page"`) || strings.Contains(body, `"percent"`) || strings.Contains(body, `"locator"`) {
		t.Fatalf("default shape leaked reading keys: %s", body)
	}
}

func TestWorkDetailPageCount(t *testing.T) {
	env := newReadingEnv(t)
	lib, _ := env.db.AddLibrary("Books", "books", t.TempDir())
	w := seedWork(t, env.db, lib, "The Trial", ptr("Franz Kafka"), nil, 1, 1)
	epub := seedBookEdition(t, env.db, w, "epub", ptr(312))
	audio := seedBookEdition(t, env.db, w, "audio", nil)
	seedFileOnDisk(t, env.db, lib, epub, "t.epub", []byte("PK"))
	seedFileOnDisk(t, env.db, lib, audio, "t.m4b", []byte("m4b"))

	_, body := readingReq(t, env, "GET", env.base+fmt.Sprintf("/works/%d", w), env.token, "")
	var resp struct {
		Editions []map[string]any `json:"editions"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	seen := map[string]any{}
	for _, e := range resp.Editions {
		seen[fmt.Sprint(e["format"])] = e["pageCount"]
	}
	if v, ok := seen["epub"].(float64); !ok || int(v) != 312 {
		t.Fatalf("epub pageCount = %v, want 312 (body: %s)", seen["epub"], body)
	}
	for _, e := range resp.Editions {
		if e["format"] == "audio" {
			if _, has := e["pageCount"]; has {
				t.Fatalf("audio edition carries pageCount: %s", body)
			}
		}
	}
}

func TestProgressRevisionProtocol(t *testing.T) {
	env := newReadingEnv(t)
	lib, _ := env.db.AddLibrary("Books", "books", t.TempDir())
	w := seedWork(t, env.db, lib, "W", nil, nil, 1, 1)
	epub := seedBookEdition(t, env.db, w, "epub", ptr(3))
	seedFileOnDisk(t, env.db, lib, epub, "b.epub", []byte("PK"))

	_, body := readingReq(t, env, "GET", env.base+fmt.Sprintf("/progress/%d", epub), env.token, "")
	if !strings.Contains(body, `"revision":0`) {
		t.Fatalf("empty progress must report revision 0: %s", body)
	}

	code, body := readingPost(t, env, epub, `{"page":4,"percent":0.8,"revision":0}`)
	if code != 200 {
		t.Fatalf("revisioned post = %d %s", code, body)
	}
	if !strings.Contains(body, `"revision":1`) {
		t.Fatalf("applied post must return the new revision: %s", body)
	}

	code, body = readingPost(t, env, epub, `{"page":2,"revision":0}`)
	if code != 409 {
		t.Fatalf("stale post = %d %s, want 409", code, body)
	}
	for _, want := range []string{`"revision":1`, `"page":4`, `"percent":0.8`, `"isFinished":false`} {
		if !strings.Contains(body, want) {
			t.Fatalf("409 body missing current state %s: %s", want, body)
		}
	}

	if code, body := readingPost(t, env, epub, `{"page":2,"revision":-1}`); code != 400 {
		t.Fatalf("negative revision = %d %s, want 400", code, body)
	}

	if code, body := readingPost(t, env, epub, `{"finished":true}`); code != 200 {
		t.Fatalf("legacy post = %d %s", code, body)
	}
	_, body = readingReq(t, env, "GET", env.base+fmt.Sprintf("/progress/%d", epub), env.token, "")
	if !strings.Contains(body, `"revision":2`) {
		t.Fatalf("legacy write must advance the revision: %s", body)
	}

	code, body = readingPost(t, env, epub, `{"page":5,"revision":1}`)
	if code != 409 {
		t.Fatalf("base held across a legacy write = %d %s, want 409", code, body)
	}
	code, body = readingPost(t, env, epub, `{"page":5,"revision":2}`)
	if code != 200 || !strings.Contains(body, `"revision":3`) {
		t.Fatalf("rebased post = %d %s", code, body)
	}
}
