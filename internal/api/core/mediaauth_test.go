package core

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

func TestIsMediaPath(t *testing.T) {
	media := []string{
		"/api/core/stream/12",
		"/api/core/covers/7.jpg",
		"/api/core/covers/7-fanart.jpg",
		"/api/core/subtitles/9",
		"/api/core/hls/web-3-abc/index.m3u8",
		"/api/core/hls/web-3-abc/seg00001.ts",
		"/api/core/progress/5",
		"/api/core/podcasts/export-opml",
		"/api/core/podcasts/episodes/31/stream",
		"/api/core/editions/4/download",
		"/api/core/editions/4/thumbs",
		"/api/core/editions/4/thumbs/000.jpg",
		"/api/core/libraries/2/scan/events",
		"/api/core/libraries/2/refresh-meta/events",
	}
	for _, p := range media {
		if !isMediaPath(p) {
			t.Fatalf("isMediaPath(%q) = false, want true", p)
		}
	}
	plain := []string{
		"/api/core/me",
		"/api/core/libraries",
		"/api/core/libraries/2/works",
		"/api/core/editions/4/playback",
		"/api/core/podcasts",
		"/api/core/podcasts/5",
		"/api/core/podcasts/episodes/31",
		"/api/core/scan-jobs/1",
		"/api/core/users",
		"/api/abs/me",
		"/",
	}
	for _, p := range plain {
		if isMediaPath(p) {
			t.Fatalf("isMediaPath(%q) = true, want false", p)
		}
	}
}

type mediaAuthEnv struct {
	db     *store.DB
	srv    *httptest.Server
	token  string
	fileID int64
	edID   int64
}

func newMediaAuthEnv(t *testing.T) *mediaAuthEnv {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Now().UnixMilli()
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('u','x',0,?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := res.LastInsertId()
	token, err := auth.IssueToken(db, uid, "test")
	if err != nil {
		t.Fatal(err)
	}
	lib, err := db.AddLibrary("Books", "books", dir)
	if err != nil {
		t.Fatal(err)
	}
	w := seedWork(t, db, lib, "Book", nil, nil, 1, 1)
	ed := seedBookEdition(t, db, w, "epub", nil)
	seedFileOnDisk(t, db, lib, ed, "book.epub", []byte("media bytes"))

	var fid int64
	if err := db.QueryRow(`SELECT id FROM files WHERE edition_id = ?`, ed).Scan(&fid); err != nil {
		t.Fatal(err)
	}
	a := New(db, dir)
	app := neutron.New()
	a.MountPublic(app.Router().Group("/api/core"))
	a.Mount(app.Router().Group("/api/core", auth.MiddlewareWithMediaCookie(db, MediaRequest, MediaMutationRequest)))
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return &mediaAuthEnv{db: db, srv: srv, token: token, fileID: fid, edID: ed}
}

func (e *mediaAuthEnv) do(t *testing.T, method, path, authz, cookie string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if authz != "" {
		req.Header.Set("Authorization", "Bearer "+authz)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.MediaCookieName, Value: cookie})
	}
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	data, _ := io.ReadAll(resp.Body)
	return resp, string(data)
}

func TestMediaRoutesRefuseQueryTokens(t *testing.T) {
	env := newMediaAuthEnv(t)
	path := "/api/core/stream/" + strconv.FormatInt(env.fileID, 10)
	resp, body := env.do(t, "GET", path+"?token="+env.token, "", "")
	if resp.StatusCode != 401 {
		t.Fatalf("stream query token = %d %s, want 401", resp.StatusCode, body)
	}
	resp, body = env.do(t, "GET", "/api/core/libraries?token="+env.token, "", "")
	if resp.StatusCode != 200 {
		t.Fatalf("non-media query token = %d %s, want 200 (legacy fallback)", resp.StatusCode, body)
	}
}

func TestMediaCookieAuthenticatesMediaRoutes(t *testing.T) {
	env := newMediaAuthEnv(t)
	path := "/api/core/stream/" + strconv.FormatInt(env.fileID, 10)

	resp, body := env.do(t, "GET", path, "", env.token)
	if resp.StatusCode != 200 || body != "media bytes" {
		t.Fatalf("cookie stream = %d %q, want 200 bytes", resp.StatusCode, body)
	}
	resp, body = env.do(t, "GET", path, env.token, "")
	if resp.StatusCode != 200 {
		t.Fatalf("header stream = %d %s, want 200", resp.StatusCode, body)
	}
	resp, _ = env.do(t, "GET", path, "", "not-a-token")
	if resp.StatusCode != 401 {
		t.Fatalf("garbage cookie = %d, want 401", resp.StatusCode)
	}

	resp, _ = env.do(t, "GET", "/api/core/libraries", "", env.token)
	if resp.StatusCode != 401 {
		t.Fatalf("cookie on non-media route = %d, want 401", resp.StatusCode)
	}
	resp, _ = env.do(t, "GET", "/api/core/libraries", env.token, "")
	if resp.StatusCode != 200 {
		t.Fatalf("header on non-media route = %d, want 200", resp.StatusCode)
	}
}

func (e *mediaAuthEnv) beacon(t *testing.T, path, authz, cookie, body string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest("POST", e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if authz != "" {
		req.Header.Set("Authorization", "Bearer "+authz)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: auth.MediaCookieName, Value: cookie})
	}
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	data, _ := io.ReadAll(resp.Body)
	return resp, string(data)
}

func TestMediaCookieProgressBeaconOriginPolicy(t *testing.T) {
	env := newMediaAuthEnv(t)
	path := "/api/core/progress/" + strconv.FormatInt(env.edID, 10)
	origin := env.srv.URL

	resp, body := env.beacon(t, path, "", env.token, `{}`, map[string]string{"Sec-Fetch-Site": "same-origin"})
	if resp.StatusCode != 200 {
		t.Fatalf("same-origin beacon = %d %s, want 200", resp.StatusCode, body)
	}
	resp, body = env.beacon(t, path, "", env.token, `{}`, map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": origin})
	if resp.StatusCode != 200 {
		t.Fatalf("beacon with matching Origin = %d %s, want 200", resp.StatusCode, body)
	}
	resp, body = env.beacon(t, path, "", env.token, `{}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("beacon without origin markers (legacy same-origin UA) = %d %s, want 200", resp.StatusCode, body)
	}

	resp, _ = env.beacon(t, path, "", env.token, `{}`, map[string]string{"Sec-Fetch-Site": "cross-site"})
	if resp.StatusCode != 401 {
		t.Fatalf("cross-site beacon = %d, want 401", resp.StatusCode)
	}
	resp, _ = env.beacon(t, path, "", env.token, `{}`, map[string]string{"Sec-Fetch-Site": "same-site"})
	if resp.StatusCode != 401 {
		t.Fatalf("same-site sibling beacon = %d, want 401", resp.StatusCode)
	}
	resp, _ = env.beacon(t, path, "", env.token, `{}`, map[string]string{"Origin": "https://sibling.example"})
	if resp.StatusCode != 401 {
		t.Fatalf("mismatched Origin beacon = %d, want 401", resp.StatusCode)
	}
	resp, _ = env.beacon(t, path, "", env.token, `{}`, map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": "https://evil.example"})
	if resp.StatusCode != 401 {
		t.Fatalf("beacon with spoofed Sec-Fetch-Site + foreign Origin = %d, want 401", resp.StatusCode)
	}

	resp, body = env.beacon(t, path, env.token, "", `{}`, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"})
	if resp.StatusCode != 200 {
		t.Fatalf("bearer-header progress post = %d %s, want 200 (header auth is not cookie auth)", resp.StatusCode, body)
	}

	resp, _ = env.beacon(t, path+"?token="+env.token, "", "", `{}`, nil)
	if resp.StatusCode != 401 {
		t.Fatalf("query-token beacon = %d, want 401", resp.StatusCode)
	}

	resp, _ = env.do(t, "POST", "/api/core/libraries", "", env.token)
	if resp.StatusCode != 401 {
		t.Fatalf("cookie on an ordinary core mutation = %d, want 401", resp.StatusCode)
	}
}

func TestMediaCookieHlsStopOriginPolicy(t *testing.T) {
	env := newMediaAuthEnv(t)
	sameOrigin := func(t *testing.T, hdr map[string]string) int {
		t.Helper()
		req, err := http.NewRequest("DELETE", env.srv.URL+"/api/core/hls/web-1-none", nil)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		req.AddCookie(&http.Cookie{Name: auth.MediaCookieName, Value: env.token})
		resp, err := env.srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if code := sameOrigin(t, map[string]string{"Sec-Fetch-Site": "same-origin", "Origin": env.srv.URL}); code == 401 {
		t.Fatal("same-origin cookie HLS stop must pass authentication (handler outcome may be 404 for an unknown session)")
	}
	if code := sameOrigin(t, nil); code == 401 {
		t.Fatal("cookie HLS stop without origin markers (legacy same-origin UA) must pass authentication")
	}
	if code := sameOrigin(t, map[string]string{"Sec-Fetch-Site": "same-site"}); code != 401 {
		t.Fatalf("same-site sibling cookie HLS stop = %d, want 401", code)
	}
	if code := sameOrigin(t, map[string]string{"Origin": "https://evil.example"}); code != 401 {
		t.Fatalf("mismatched Origin cookie HLS stop = %d, want 401", code)
	}
}

func TestMediaMutationRequestClassification(t *testing.T) {
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		r := httptest.NewRequest(m, "/api/core/progress/5", nil)
		if MediaMutationRequest(r) {
			t.Fatalf("MediaMutationRequest(%s /progress/5) = true, want false", m)
		}
	}
	r := httptest.NewRequest(http.MethodPost, "/api/core/progress/5", nil)
	if !MediaMutationRequest(r) {
		t.Fatal("MediaMutationRequest(POST /progress/5) = false, want true")
	}
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut} {
		r := httptest.NewRequest(m, "/api/core/hls/web-1-abc", nil)
		if MediaMutationRequest(r) {
			t.Fatalf("MediaMutationRequest(%s /hls/...) = true, want false", m)
		}
	}
	r = httptest.NewRequest(http.MethodDelete, "/api/core/hls/web-1-abc", nil)
	if !MediaMutationRequest(r) {
		t.Fatal("MediaMutationRequest(DELETE /hls/{sid}) = false, want true")
	}
	for _, p := range []string{"/api/core/progress", "/api/core/progress/", "/api/core/progress/5/x", "/api/core/stream/5", "/api/core/libraries", "/api/core/hls", "/api/core/hls/a/b"} {
		r := httptest.NewRequest(http.MethodPost, p, nil)
		if MediaMutationRequest(r) {
			t.Fatalf("MediaMutationRequest(POST %s) = true, want false", p)
		}
	}
	r = httptest.NewRequest(http.MethodGet, "/api/core/progress/5", nil)
	if !MediaRequest(r) {
		t.Fatal("MediaRequest(GET /progress/5) = false, want true (read-only cookie read stays allowed)")
	}
	r = httptest.NewRequest(http.MethodPost, "/api/core/progress/5", nil)
	if MediaRequest(r) {
		t.Fatal("MediaRequest(POST /progress/5) = true, want false (cookie is read-only for media routes)")
	}
}

func TestMediaCookieLifecycle(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a := New(db, dir)
	app := neutron.New()
	a.MountPublic(app.Router().Group("/api/core"))
	a.Mount(app.Router().Group("/api/core", auth.MiddlewareWithMediaCookie(db, MediaRequest, MediaMutationRequest)))
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	hash, err := auth.Hash("password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('admin',?,1,0,0)`, hash); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srv.URL+"/api/core/login", "application/json", strings.NewReader(`{"username":"admin","password":"password123"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("login = %d %s", resp.StatusCode, body)
	}
	var cookies []*http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == auth.MediaCookieName {
			cookies = append(cookies, c)
		}
	}
	if len(cookies) != 1 || cookies[0].Value == "" {
		t.Fatalf("login must set the media cookie once, got %v", resp.Header.Values("Set-Cookie"))
	}
	c := cookies[0]
	if c.Path != "/api/core" || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("media cookie attributes = path %q httpOnly %v sameSite %v", c.Path, c.HttpOnly, c.SameSite)
	}
	if c.Secure {
		t.Fatal("media cookie must not be Secure in default direct-HTTP mode")
	}
	token := c.Value

	req, _ := http.NewRequest("GET", srv.URL+"/api/core/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	me, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	me.Body.Close()
	var reestablished bool
	for _, mc := range me.Cookies() {
		if mc.Name == auth.MediaCookieName && mc.Value == token {
			reestablished = true
		}
	}
	if !reestablished {
		t.Fatal("me must re-establish the media cookie for the active token")
	}

	req, _ = http.NewRequest("POST", srv.URL+"/api/core/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	out, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out.Body.Close()
	if out.StatusCode != 204 {
		t.Fatalf("logout = %d, want 204", out.StatusCode)
	}
	cleared := false
	for _, mc := range out.Cookies() {
		if mc.Name == auth.MediaCookieName && mc.Value == "" && mc.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("logout must clear the media cookie")
	}

	req, _ = http.NewRequest("GET", srv.URL+"/api/core/me", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	after, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	after.Body.Close()
	if after.StatusCode != 401 {
		t.Fatalf("revoked token = %d, want 401", after.StatusCode)
	}
}

func TestMediaCookieSecureMode(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	a := New(db, dir)
	a.SecureCookies = true
	app := neutron.New()
	a.MountPublic(app.Router().Group("/api/core"))
	a.Mount(app.Router().Group("/api/core", auth.MiddlewareWithMediaCookie(db, MediaRequest, MediaMutationRequest)))
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	hash, err := auth.Hash("password123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('admin',?,1,0,0)`, hash); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Post(srv.URL+"/api/core/login", "application/json", strings.NewReader(`{"username":"admin","password":"password123"}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("login = %d %s", resp.StatusCode, body)
	}
	var token string
	secureSet := false
	for _, c := range resp.Cookies() {
		if c.Name == auth.MediaCookieName {
			token = c.Value
			secureSet = c.Secure
		}
	}
	if token == "" || !secureSet {
		t.Fatalf("secure mode login cookie: token set %t secure %t", token != "", secureSet)
	}

	req, _ := http.NewRequest("POST", srv.URL+"/api/core/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	out, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out.Body.Close()
	if out.StatusCode != 204 {
		t.Fatalf("logout = %d, want 204", out.StatusCode)
	}
	clearedSecure := false
	for _, c := range out.Cookies() {
		if c.Name == auth.MediaCookieName && c.Value == "" && c.MaxAge < 0 && c.Secure {
			clearedSecure = true
		}
	}
	if !clearedSecure {
		t.Fatal("logout must clear the media cookie with the Secure attribute")
	}
}
