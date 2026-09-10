package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-dev/neutron-go/neutron"
)

type podcastFeedItem struct {
	guid, title, date string
}

type podcastFeedServer struct {
	*httptest.Server
	mu    sync.Mutex
	title string
	etag  string
	items []podcastFeedItem
}

func newPodcastFeedServer(t *testing.T, title string) *podcastFeedServer {
	fs := &podcastFeedServer{title: title, etag: "v1", items: []podcastFeedItem{
		{"guid-1", "Episode One", "Tue, 10 Dec 2024 06:00:00 +0000"},
		{"guid-2", "Episode Two", "Tue, 17 Dec 2024 06:00:00 +0000"},
	}}
	mux := http.NewServeMux()
	mux.HandleFunc("/feed", func(w http.ResponseWriter, r *http.Request) {
		fs.mu.Lock()
		defer fs.mu.Unlock()
		w.Header().Set("ETag", fs.etag)
		if r.Header.Get("If-None-Match") == fs.etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		fmt.Fprint(w, fs.bodyLocked())
	})
	mux.HandleFunc("/enclosure/", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "fake-audio-"+strings.TrimSuffix(filepath.Base(r.URL.Path), ".mp3"))
	})
	fs.Server = httptest.NewServer(mux)
	t.Cleanup(fs.Close)
	return fs
}

func (fs *podcastFeedServer) setItems(items []podcastFeedItem, etag string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.items = items
	fs.etag = etag
}

func (fs *podcastFeedServer) bodyLocked() string {
	var b strings.Builder
	b.WriteString(`<rss version="2.0" xmlns:itunes="http://www.itunes.com/dtds/podcast-1.0.dtd"><channel>`)
	fmt.Fprintf(&b, `<title>%s</title><description>d</description><itunes:author>Auth</itunes:author>`, fs.title)
	for _, it := range fs.items {
		fmt.Fprintf(&b, `<item><title>%s</title><guid>%s</guid><pubDate>%s</pubDate><itunes:duration>60</itunes:duration><enclosure url="%s/enclosure/%s.mp3" length="10" type="audio/mpeg"/></item>`, it.title, it.guid, it.date, fs.URL, it.guid)
	}
	b.WriteString(`</channel></rss>`)
	return b.String()
}

func newPodcastTestAPI(t *testing.T) (*API, *store.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := auth.InitAdmin(db, "admin", "pw"); err != nil {
		t.Fatal(err)
	}
	var uid int64
	db.QueryRow(`SELECT id FROM users`).Scan(&uid)
	token, err := auth.IssueToken(db, uid, "test")
	if err != nil {
		t.Fatal(err)
	}
	return New(db, dir), db, token
}

func mountPodcastServer(t *testing.T, a *API, db *store.DB) *httptest.Server {
	t.Helper()
	app := neutron.New()
	r := app.Router()
	a.MountPodcasts(r.Group("/api/core", auth.Middleware(db)))
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return srv
}

func doPodcastReq(t *testing.T, srv *httptest.Server, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	code, raw := doPodcastReqBytes(t, srv, method, path, token, body)
	var out map[string]any
	json.Unmarshal(raw, &out)
	return code, out
}

func doPodcastReqBytes(t *testing.T, srv *httptest.Server, method, path, token string, body any) (int, []byte) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func TestPodcastEndpointsRequireAuth(t *testing.T) {
	a, db, _ := newPodcastTestAPI(t)
	srv := mountPodcastServer(t, a, db)
	for _, token := range []string{"", "bogus"} {
		code, body := doPodcastReq(t, srv, "GET", "/api/core/podcasts", token, nil)
		if code != 401 {
			t.Fatalf("GET podcasts with token %q = %d %v, want 401", token, code, body)
		}
	}
}

func TestPodcastFullFlow(t *testing.T) {
	a, db, token := newPodcastTestAPI(t)
	srv := mountPodcastServer(t, a, db)
	fs := newPodcastFeedServer(t, "Test Cast")

	code, body := doPodcastReq(t, srv, "POST", "/api/core/podcasts", token, map[string]any{
		"feedUrl": fs.URL + "/feed", "maxEpisodes": 2,
	})
	if code != 201 {
		t.Fatalf("subscribe = %d %v", code, body)
	}
	if body["title"] != "Test Cast" || body["episodeCount"] != float64(2) || body["downloadedCount"] != float64(2) {
		t.Fatalf("subscribe body = %v", body)
	}
	podID := int64(body["id"].(float64))
	eps := body["episodes"].([]any)
	if len(eps) != 2 {
		t.Fatalf("episodes = %d", len(eps))
	}
	ep0 := eps[0].(map[string]any)
	if ep0["guid"] != "guid-2" {
		t.Fatalf("first episode = %v, want newest", ep0["guid"])
	}
	if ep0["streamUrl"] == nil {
		t.Fatal("downloaded episode missing streamUrl")
	}
	epID := strconv.FormatInt(int64(ep0["id"].(float64)), 10)

	// list
	code, raw := doPodcastReqBytes(t, srv, "GET", "/api/core/podcasts", token, nil)
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatalf("list body %q: %v", raw, err)
	}
	if code != 200 || len(list) != 1 {
		t.Fatalf("list = %d %v", code, list)
	}

	// stream with Range
	req, _ := http.NewRequest("GET", srv.URL+"/api/core/podcasts/episodes/"+epID+"/stream?token="+token, nil)
	req.Header.Set("Range", "bytes=0-3")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || len(data) != 4 || string(data) != "fake" {
		t.Fatalf("stream = %d %q, want 206 first 4 bytes", resp.StatusCode, data)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "audio/mpeg" {
		t.Fatalf("stream content-type = %q", ct)
	}

	// refresh: new episode arrives, retention (max 1 after patch) keeps newest
	doPodcastReq(t, srv, "PATCH", "/api/core/podcasts/"+strconv.FormatInt(podID, 10), token, map[string]any{"maxEpisodes": 1})
	fs.setItems([]podcastFeedItem{
		{"guid-1", "Episode One", "Tue, 10 Dec 2024 06:00:00 +0000"},
		{"guid-2", "Episode Two", "Tue, 17 Dec 2024 06:00:00 +0000"},
		{"guid-3", "Episode Three", "Tue, 24 Dec 2024 06:00:00 +0000"},
	}, "v2")
	code, body = doPodcastReq(t, srv, "POST", "/api/core/podcasts/"+strconv.FormatInt(podID, 10)+"/refresh", token, nil)
	if code != 200 || body["changed"] != true {
		t.Fatalf("refresh = %d changed=%v", code, body["changed"])
	}
	if body["episodeCount"] != float64(3) || body["downloadedCount"] != float64(1) {
		t.Fatalf("after refresh = episodes %v downloaded %v, want 3/1 (retention)",
			body["episodeCount"], body["downloadedCount"])
	}
	for _, e := range body["episodes"].([]any) {
		em := e.(map[string]any)
		wantFile := em["guid"] == "guid-3"
		if hasFile, _ := em["hasFile"].(bool); hasFile != wantFile {
			t.Fatalf("episode %v hasFile=%v, want %v", em["guid"], em["hasFile"], wantFile)
		}
	}

	// patch reflects settings
	code, body = doPodcastReq(t, srv, "PATCH", "/api/core/podcasts/"+strconv.FormatInt(podID, 10), token, map[string]any{"autoDownload": false})
	if code != 200 || body["autoDownload"] != false || body["maxEpisodes"] != float64(1) {
		t.Fatalf("patch = %d %v", code, body)
	}

	// delete
	code, body = doPodcastReq(t, srv, "DELETE", "/api/core/podcasts/"+strconv.FormatInt(podID, 10), token, nil)
	if code != 200 || body["ok"] != true {
		t.Fatalf("delete = %d %v", code, body)
	}
	code, _ = doPodcastReq(t, srv, "GET", "/api/core/podcasts/"+strconv.FormatInt(podID, 10), token, nil)
	if code != 404 {
		t.Fatalf("detail after delete = %d, want 404", code)
	}
}

func TestPodcastSubscribeValidationAndDuplicate(t *testing.T) {
	a, db, token := newPodcastTestAPI(t)
	srv := mountPodcastServer(t, a, db)
	fs := newPodcastFeedServer(t, "Test Cast")

	code, body := doPodcastReq(t, srv, "POST", "/api/core/podcasts", token, map[string]any{"feedUrl": "not-a-url"})
	if code != 400 {
		t.Fatalf("bad url = %d %v, want 400", code, body)
	}
	code, body = doPodcastReq(t, srv, "POST", "/api/core/podcasts", token, map[string]any{"feedUrl": fs.URL + "/feed", "maxEpisodes": 0})
	if code != 400 {
		t.Fatalf("bad maxEpisodes = %d %v, want 400", code, body)
	}
	code, body = doPodcastReq(t, srv, "POST", "/api/core/podcasts", token, map[string]any{"feedUrl": fs.URL + "/feed"})
	if code != 201 {
		t.Fatalf("subscribe = %d %v", code, body)
	}
	code, body = doPodcastReq(t, srv, "POST", "/api/core/podcasts", token, map[string]any{"feedUrl": fs.URL + "/feed"})
	if code != 409 || body["podcastId"] == nil {
		t.Fatalf("duplicate = %d %v, want 409 with podcastId", code, body)
	}
}

func TestPodcastStreamNotDownloaded(t *testing.T) {
	a, db, token := newPodcastTestAPI(t)
	srv := mountPodcastServer(t, a, db)
	fs := newPodcastFeedServer(t, "Test Cast")

	_, body := doPodcastReq(t, srv, "POST", "/api/core/podcasts", token, map[string]any{
		"feedUrl": fs.URL + "/feed", "autoDownload": false,
	})
	podID := strconv.FormatInt(int64(body["id"].(float64)), 10)

	fs.setItems([]podcastFeedItem{
		{"guid-1", "Episode One", "Tue, 10 Dec 2024 06:00:00 +0000"},
		{"guid-2", "Episode Two", "Tue, 17 Dec 2024 06:00:00 +0000"},
		{"guid-3", "Episode Three", "Tue, 24 Dec 2024 06:00:00 +0000"},
	}, "v2")
	doPodcastReq(t, srv, "POST", "/api/core/podcasts/"+podID+"/refresh", token, nil)
	_, detail := doPodcastReq(t, srv, "GET", "/api/core/podcasts/"+podID, token, nil)
	for _, e := range detail["episodes"].([]any) {
		em := e.(map[string]any)
		if em["guid"] == "guid-3" {
			if em["streamUrl"] != nil {
				t.Fatal("undownloaded episode has streamUrl")
			}
			epID := strconv.FormatInt(int64(em["id"].(float64)), 10)
			code, sbody := doPodcastReq(t, srv, "GET", "/api/core/podcasts/episodes/"+epID+"/stream?token="+token, "", nil)
			if code != 404 {
				t.Fatalf("stream undownloaded = %d %v, want 404", code, sbody)
			}
			return
		}
	}
	t.Fatal("guid-3 not found after refresh")
}

func TestPodcastOPMLImportExportRoundtrip(t *testing.T) {
	a, db, token := newPodcastTestAPI(t)
	srv := mountPodcastServer(t, a, db)
	fsA := newPodcastFeedServer(t, "Cast A")
	fsB := newPodcastFeedServer(t, "Cast B")

	for _, feed := range []string{fsA.URL + "/feed", fsB.URL + "/feed"} {
		code, body := doPodcastReq(t, srv, "POST", "/api/core/podcasts", token, map[string]any{"feedUrl": feed})
		if code != 201 {
			t.Fatalf("subscribe %s = %d %v", feed, code, body)
		}
	}

	req, _ := http.NewRequest("GET", srv.URL+"/api/core/podcasts/export-opml", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	exported, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("export = %d", resp.StatusCode)
	}
	for _, want := range []string{fsA.URL + "/feed", fsB.URL + "/feed", "Cast A", "Cast B"} {
		if !strings.Contains(string(exported), want) {
			t.Fatalf("export missing %q:\n%s", want, exported)
		}
	}

	// import into a fresh server/db against the same feed servers
	a2, db2, token2 := newPodcastTestAPI(t)
	srv2 := mountPodcastServer(t, a2, db2)
	code, body := doPodcastReq(t, srv2, "POST", "/api/core/podcasts/import-opml", token2, map[string]any{"opml": string(exported)})
	if code != 200 {
		t.Fatalf("import = %d %v", code, body)
	}
	if body["subscribed"] != float64(2) || body["exists"] != float64(0) || body["failed"] != float64(0) {
		t.Fatalf("import counts = %v", body)
	}
	for _, r := range body["results"].([]any) {
		if r.(map[string]any)["status"] != "subscribed" {
			t.Fatalf("import result = %v", r)
		}
	}

	// re-import: same set, all reported as existing
	_, body = doPodcastReq(t, srv2, "POST", "/api/core/podcasts/import-opml", token2, map[string]any{"opml": string(exported)})
	if body["subscribed"] != float64(0) || body["exists"] != float64(2) {
		t.Fatalf("re-import counts = %v", body)
	}
	_, rawList := doPodcastReqBytes(t, srv2, "GET", "/api/core/podcasts", token2, nil)
	var items []map[string]any
	if err := json.Unmarshal(rawList, &items); err != nil {
		t.Fatalf("list body %q: %v", rawList, err)
	}
	got := map[string]bool{}
	for _, p := range items {
		got[p["feedUrl"].(string)] = true
	}
	if !got[fsA.URL+"/feed"] || !got[fsB.URL+"/feed"] || len(got) != 2 {
		t.Fatalf("imported set = %v", got)
	}

	// garbage OPML is a 400
	code, _ = doPodcastReq(t, srv2, "POST", "/api/core/podcasts/import-opml", token2, map[string]any{"opml": "<not-opml"})
	if code != 400 {
		t.Fatalf("garbage opml = %d, want 400", code)
	}
}
