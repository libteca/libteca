package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-dev/neutron-go/neutron"
)

func newDiscoveryEnv(t *testing.T) (*store.DB, string, string) {
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
	a.Mount(app.Router().Group("/api/core", auth.Middleware(db)))
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return db, srv.URL + "/api/core", token
}

func authedGet(t *testing.T, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest("GET", url, nil)
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
	return resp
}

func bodyStr(t *testing.T, resp *http.Response) string {
	t.Helper()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}

func seedWork(t *testing.T, db *store.DB, libID int64, title string, author, cover *string, createdAt, updatedAt int64) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO works (library_id, title, author, cover_path, created_at, updated_at) VALUES (?,?,?,?,?,?)`,
		libID, title, author, cover, createdAt, updatedAt)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func seedEdition(t *testing.T, db *store.DB, workID int64, dur *float64) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO editions (work_id, format, title, duration_secs, created_at) VALUES (?,?,?,?,0)`,
		workID, "video", "E", dur)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func seedFile(t *testing.T, db *store.DB, editionID int64, path string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, probed_at) VALUES (?,?,1,0,0,1,0)`,
		editionID, path)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func seedProgress(t *testing.T, db *store.DB, user, editionID int64, pos, dur float64, fin bool, updatedAt int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO progress (user_id, edition_id, edition_position_secs, duration_secs, is_finished, updated_at) VALUES (?,?,?,?,?,?)`,
		user, editionID, pos, dur, fin, updatedAt); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestResumeEndpoint(t *testing.T) {
	db, base, token := newDiscoveryEnv(t)
	libTV, _ := db.AddLibrary("tv", "tv", t.TempDir())
	libAB, _ := db.AddLibrary("ab", "audiobooks", t.TempDir())

	w1 := seedWork(t, db, libTV, "Cosmos", ptr("Carl Sagan"), ptr("1.jpg"), 1, 1)
	e1 := seedEdition(t, db, w1, ptr(2000.0))
	seedProgress(t, db, 1, e1, 500, 0, false, 1694000000)

	w2 := seedWork(t, db, libAB, "Hitchhiker", nil, nil, 2, 2)
	e2 := seedEdition(t, db, w2, nil)
	seedProgress(t, db, 1, e2, 100, 1000, false, 1693000000)

	w3 := seedWork(t, db, libTV, "Finished", ptr("A"), nil, 3, 3)
	e3 := seedEdition(t, db, w3, ptr(1000.0))
	seedProgress(t, db, 1, e3, 1000, 1000, true, 1695000000)

	resp := authedGet(t, base+"/resume", token)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	want := `{"items":[` +
		`{"workId":1,"editionId":1,"libraryId":1,"libraryType":"tv","title":"Cosmos","author":"Carl Sagan","hasCover":true,"positionSecs":500,"durationSecs":2000,"percent":0.25,"updatedAt":1694000000},` +
		`{"workId":2,"editionId":2,"libraryId":2,"libraryType":"audiobooks","title":"Hitchhiker","author":"","hasCover":false,"positionSecs":100,"durationSecs":1000,"percent":0.1,"updatedAt":1693000000}` +
		`]}`
	if got := strings.TrimSpace(bodyStr(t, resp)); got != want {
		t.Fatalf("body =\n%s\nwant\n%s", got, want)
	}
}

func TestResumeRequiresAuth(t *testing.T) {
	_, base, _ := newDiscoveryEnv(t)
	resp := authedGet(t, base+"/resume", "")
	if resp.StatusCode != 401 {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

func TestSearchEndpoint(t *testing.T) {
	db, base, token := newDiscoveryEnv(t)
	libMovies, _ := db.AddLibrary("m", "movies", t.TempDir())
	libTV, _ := db.AddLibrary("t", "tv", t.TempDir())

	w1 := seedWork(t, db, libMovies, "Dune Messiah", ptr("Frank Herbert"), nil, 1, 1)
	e1 := seedEdition(t, db, w1, ptr(1000.0))
	seedProgress(t, db, 1, e1, 250, 1000, false, 500)
	seedWork(t, db, libTV, "Herbert West", ptr("Lovecraft"), nil, 2, 2)
	seedWork(t, db, libTV, "Foundation", ptr("Isaac Asimov"), nil, 3, 3)

	resp := authedGet(t, base+"/search?q=herBERT", token)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	want := `{"results":[` +
		`{"workId":2,"libraryId":2,"libraryType":"tv","title":"Herbert West","author":"Lovecraft","hasCover":false},` +
		`{"workId":1,"libraryId":1,"libraryType":"movies","title":"Dune Messiah","author":"Frank Herbert","hasCover":false,"percent":0.25}` +
		`]}`
	if got := strings.TrimSpace(bodyStr(t, resp)); got != want {
		t.Fatalf("body =\n%s\nwant\n%s", got, want)
	}

	for _, q := range []string{"q=h", "q=", "q=x"} {
		resp := authedGet(t, base+"/search?"+q, token)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: status = %d, want 200", q, resp.StatusCode)
		}
		if got := strings.TrimSpace(bodyStr(t, resp)); got != `{"results":[]}` {
			t.Fatalf("%s: body = %s, want empty results", q, got)
		}
	}
}

func TestRecentEndpoint(t *testing.T) {
	db, base, token := newDiscoveryEnv(t)
	lib, _ := db.AddLibrary("ab", "audiobooks", t.TempDir())
	seedWork(t, db, lib, "Old", nil, nil, 100, 100)
	seedWork(t, db, lib, "New", ptr("B"), ptr("2.jpg"), 300, 300)
	seedWork(t, db, lib, "Mid", nil, nil, 200, 200)

	resp := authedGet(t, base+"/recent?limit=2", token)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	want := `{"items":[` +
		`{"workId":2,"libraryId":1,"libraryType":"audiobooks","title":"New","author":"B","hasCover":true,"addedAt":300},` +
		`{"workId":3,"libraryId":1,"libraryType":"audiobooks","title":"Mid","author":"","hasCover":false,"addedAt":200}` +
		`]}`
	if got := strings.TrimSpace(bodyStr(t, resp)); got != want {
		t.Fatalf("body =\n%s\nwant\n%s", got, want)
	}

	// Default limit: all three, newest first.
	resp = authedGet(t, base+"/recent", token)
	got := bodyStr(t, resp)
	for _, title := range []string{`"title":"New"`, `"title":"Mid"`, `"title":"Old"`} {
		idx := strings.Index(got, title)
		if idx < 0 {
			t.Fatalf("missing %s in %s", title, got)
		}
		if idx != strings.LastIndex(got, title) {
			t.Fatalf("%s duplicated in %s", title, got)
		}
	}
	if !(strings.Index(got, `"New"`) < strings.Index(got, `"Mid"`) && strings.Index(got, `"Mid"`) < strings.Index(got, `"Old"`)) {
		t.Fatalf("default order wrong: %s", got)
	}
}

func TestWorksSortFilterEndpoint(t *testing.T) {
	db, base, token := newDiscoveryEnv(t)
	lib, _ := db.AddLibrary("ab", "audiobooks", t.TempDir())

	alpha := seedWork(t, db, lib, "Alpha", ptr("Zed"), nil, 200, 900)
	beta := seedWork(t, db, lib, "Beta", ptr("Alice"), nil, 300, 800)
	gamma := seedWork(t, db, lib, "Gamma", nil, nil, 100, 700)
	eA := seedEdition(t, db, alpha, ptr(100.0))
	eB := seedEdition(t, db, beta, ptr(100.0))
	seedFile(t, db, eA, filepath.Join(t.TempDir(), "a.m4b"))
	seedFile(t, db, eB, filepath.Join(t.TempDir(), "b.m4b"))
	seedProgress(t, db, 1, eA, 10, 100, false, 1000)
	seedProgress(t, db, 1, eB, 100, 100, true, 1001)

	fetchIDs := func(query string) []int64 {
		resp := authedGet(t, base+"/libraries/1/works"+query, token)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: status = %d, want 200", query, resp.StatusCode)
		}
		var items []map[string]any
		if err := json.Unmarshal([]byte(bodyStr(t, resp)), &items); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		ids := make([]int64, 0, len(items))
		for _, it := range items {
			ids = append(ids, int64(it["id"].(float64)))
		}
		return ids
	}
	equal := func(a []int64, b ...int64) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	cases := []struct {
		query string
		want  []int64
	}{
		{"", []int64{alpha, beta, gamma}},
		{"?sort=title&dir=desc", []int64{gamma, beta, alpha}},
		{"?sort=author", []int64{gamma, beta, alpha}},
		{"?sort=added", []int64{beta, alpha, gamma}},
		{"?sort=added&dir=asc", []int64{gamma, alpha, beta}},
		{"?sort=updated&dir=asc", []int64{gamma, beta, alpha}},
		{"?filter=in_progress", []int64{alpha}},
		{"?filter=unplayed", []int64{gamma}},
		{"?filter=finished", []int64{beta}},
		{"?filter=all&sort=title", []int64{alpha, beta, gamma}},
	}
	for _, tc := range cases {
		got := fetchIDs(tc.query)
		if !equal(got, tc.want...) {
			t.Fatalf("%s: got %v want %v", tc.query, got, tc.want)
		}
	}

	// Response shape is unchanged from the pre-sort/filter endpoint.
	resp := authedGet(t, base+"/libraries/1/works", token)
	var items []map[string]any
	if err := json.Unmarshal([]byte(bodyStr(t, resp)), &items); err != nil {
		t.Fatal(err)
	}
	first := items[0]
	for _, k := range []string{"id", "title", "author", "subtitle", "description", "hasCover", "editions"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("work item missing key %q: %v", k, first)
		}
	}
	ed := first["editions"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "format", "title", "duration", "files"} {
		if _, ok := ed[k]; !ok {
			t.Fatalf("edition item missing key %q: %v", k, ed)
		}
	}
	if ed["files"].(float64) != 1 {
		t.Fatalf("edition files = %v, want 1", ed["files"])
	}
}

func TestSubtitlesEndpoint(t *testing.T) {
	db, base, token := newDiscoveryEnv(t)
	lib, _ := db.AddLibrary("m", "movies", t.TempDir())
	w := seedWork(t, db, lib, "Film", ptr("Dir"), nil, 1, 1)
	e := seedEdition(t, db, w, ptr(100.0))

	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("plain.mkv", "")
	write("plain.srt", "1\r\n00:00:01,000 --> 00:00:03,500\r\nHello <i>world</i>\r\n\r\n2\r\n00:00:04,000 --> 00:00:06,000\r\nSecond &cue\r\n\r\n")
	write("lang.mkv", "")
	write("lang.en.srt", "1\r\n00:00:00,500 --> 00:00:02,000\r\nLang track\r\n\r\n")
	write("both.mkv", "")
	write("both.srt", "1\r\n00:00:01,000 --> 00:00:02,000\r\nPlain wins\r\n\r\n")
	write("both.fr.srt", "1\r\n00:00:01,000 --> 00:00:02,000\r\nLang loses\r\n\r\n")
	write("none.mkv", "")

	fPlain := seedFile(t, db, e, filepath.Join(dir, "plain.mkv"))
	fLang := seedFile(t, db, e, filepath.Join(dir, "lang.mkv"))
	fBoth := seedFile(t, db, e, filepath.Join(dir, "both.mkv"))
	fNone := seedFile(t, db, e, filepath.Join(dir, "none.mkv"))

	// Plain sidecar: full conversion contract.
	resp := authedGet(t, base+fmt.Sprintf("/subtitles/%d", fPlain), token)
	if resp.StatusCode != 200 {
		t.Fatalf("plain status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/vtt" {
		t.Fatalf("content-type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "max-age=86400" {
		t.Fatalf("cache-control = %q", cc)
	}
	wantVTT := "WEBVTT\n\n00:00:01.000 --> 00:00:03.500\nHello <i>world</i>\n\n00:00:04.000 --> 00:00:06.000\nSecond &cue\n"
	if got := bodyStr(t, resp); got != wantVTT {
		t.Fatalf("plain body = %q, want %q", got, wantVTT)
	}

	// Lang sidecar served when no plain one exists.
	resp = authedGet(t, base+fmt.Sprintf("/subtitles/%d", fLang), token)
	if resp.StatusCode != 200 {
		t.Fatalf("lang status = %d, want 200", resp.StatusCode)
	}
	if got := bodyStr(t, resp); !strings.Contains(got, "00:00:00.500 --> 00:00:02.000\nLang track") {
		t.Fatalf("lang body = %q", got)
	}

	// Plain preferred over lang.
	resp = authedGet(t, base+fmt.Sprintf("/subtitles/%d", fBoth), token)
	if resp.StatusCode != 200 {
		t.Fatalf("both status = %d, want 200", resp.StatusCode)
	}
	if got := bodyStr(t, resp); !strings.Contains(got, "Plain wins") || strings.Contains(got, "Lang loses") {
		t.Fatalf("both body = %q, want plain sidecar", got)
	}

	// No sidecar, unknown file id, and garbage ids all 404.
	for _, path := range []string{fmt.Sprintf("/subtitles/%d", fNone), "/subtitles/999", "/subtitles/abc", "/subtitles/..%2Fetc%2Fpasswd"} {
		resp := authedGet(t, base+path, token)
		if resp.StatusCode != 404 {
			t.Fatalf("%s: status = %d, want 404", path, resp.StatusCode)
		}
	}

	// Auth required.
	resp = authedGet(t, base+fmt.Sprintf("/subtitles/%d", fPlain), "")
	if resp.StatusCode != 401 {
		t.Fatalf("no token status = %d, want 401", resp.StatusCode)
	}
}

func TestSRTToVTT(t *testing.T) {
	in := "\uFEFF1\r\n00:00:01,000 --> 00:00:03,000\r\nFirst cue\r\n\r\n2\r\n00:01:02,345 --> 01:00:03,678\r\nSecond\r\nmulti line\r\n\r\n42\r\n"
	want := "WEBVTT\n\n00:00:01.000 --> 00:00:03.000\nFirst cue\n\n00:01:02.345 --> 01:00:03.678\nSecond\nmulti line\n\n42\n"
	if got := srtToVTT(in); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}

	// Numeric-only text line not followed by a timestamp line is kept.
	in = "1\r\n00:00:01,000 --> 00:00:02,000\r\n42\r\n\r\n"
	want = "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\n42\n"
	if got := srtToVTT(in); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}

	// Bare LF input.
	in = "1\n00:00:00,000 --> 00:00:01,250\nLF cue\n\n"
	want = "WEBVTT\n\n00:00:00.000 --> 00:00:01.250\nLF cue\n"
	if got := srtToVTT(in); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}
