package abs_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/api/abs"
	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-dev/neutron-go/neutron"
)

type podEnv struct {
	srv   *httptest.Server
	token string
	libID int64
	podID int64
	epID  int64
}

func podcastEnv(t *testing.T) *podEnv {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	now := time.Now().UnixMilli()
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('u','x',1,?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := res.LastInsertId()
	token, err := auth.IssueToken(db, uid, "test")
	if err != nil {
		t.Fatal(err)
	}

	res, err = db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('Podcasts','podcasts',?,?)`, filepath.Join(dir, "pods"), now)
	if err != nil {
		t.Fatal(err)
	}
	libID, _ := res.LastInsertId()

	res, err = db.Exec(`INSERT INTO podcasts (library_id, feed_url, title, author, description, auto_download, max_episodes, created_at)
		VALUES (?,?,?,?,?,1,3,?)`, libID, "https://example.com/feed.xml", "Test Show", "Host One", "A show about tests", now)
	if err != nil {
		t.Fatal(err)
	}
	podID, _ := res.LastInsertId()

	audioPath := filepath.Join(dir, "ep1.mp3")
	if err := os.WriteFile(audioPath, []byte("fake-episode-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, container, duration_secs, chapters, embedded_meta, missing, probed_at)
		VALUES (NULL,?,1,1,?, 'mp3', 1800.0, '[]','{}',0,?)`, audioPath, now, now)
	if err != nil {
		t.Fatal(err)
	}
	fileID, _ := res.LastInsertId()

	res, err = db.Exec(`INSERT INTO podcast_episodes (podcast_id, guid, title, description, pub_date, duration_secs, enclosure_url, enclosure_bytes, file_id, downloaded_at, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		podID, "guid-1", "Episode 1", "First episode", now, 1800.0, "https://example.com/ep1.mp3", 12345, fileID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	epID, _ := res.LastInsertId()

	// mirrors the ABS mounting in internal/server/server.go plus the reported
	// one-line MountPodcasts addition
	app := neutron.New(
		neutron.WithOpenAPIInfo("libteca", "0.1.0"),
		neutron.WithLogger(slog.Default()),
		neutron.WithMiddleware(neutron.Recover()),
	)
	r := app.Router()
	a := abs.New(db, dir)
	a.Mount(r.Group("/api", auth.Middleware(db)))
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return &podEnv{srv: srv, token: token, libID: libID, podID: podID, epID: epID}
}

func (e *podEnv) get(t *testing.T, path string, token string) (int, string) {
	t.Helper()
	return e.req(t, "GET", path, token, nil)
}

func (e *podEnv) req(t *testing.T, method, path, token string, body any) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(b)
}

func TestPodcastsLibraryMediaType(t *testing.T) {
	e := podcastEnv(t)
	status, body := e.get(t, "/api/libraries", e.token)
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	if !strings.Contains(body, `"mediaType":"podcast"`) || !strings.Contains(body, `"name":"Podcasts"`) {
		t.Fatalf("podcasts library not typed as podcast: %s", body)
	}
}

func TestLibraryPodcastsList(t *testing.T) {
	e := podcastEnv(t)
	status, body := e.get(t, fmt.Sprintf("/api/libraries/%d/podcasts", e.libID), e.token)
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	for _, want := range []string{`"mediaType":"podcast"`, `"title":"Test Show"`, `"numEpisodes":1`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
}

func TestPodcastDetailCarriesEpisodes(t *testing.T) {
	e := podcastEnv(t)
	status, body := e.get(t, fmt.Sprintf("/api/podcasts/%d", e.podID), e.token)
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	for _, want := range []string{
		`"episodes":[`, `"id":"pe-` + fmt.Sprint(e.epID) + `"`,
		`"contentUrl":"/api/podcasts/episodes/pe-` + fmt.Sprint(e.epID) + `/file"`,
		`"feedUrl":"https://example.com/feed.xml"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
}

func TestPodcastEpisodesPaged(t *testing.T) {
	e := podcastEnv(t)
	status, body := e.get(t, fmt.Sprintf("/api/podcasts/%d/episodes?limit=1&page=0", e.podID), e.token)
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	if !strings.Contains(body, `"total":1`) || !strings.Contains(body, `"limit":1`) {
		t.Fatalf("paging shape wrong: %s", body)
	}
}

func TestEpisodeFileStreamAndAuth(t *testing.T) {
	e := podcastEnv(t)
	path := fmt.Sprintf("/api/podcasts/episodes/pe-%d/file", e.epID)
	if status, _ := e.get(t, path, ""); status != 401 {
		t.Fatalf("unauthenticated stream: status %d, want 401", status)
	}
	status, body := e.get(t, path, e.token)
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	if body != "fake-episode-bytes" {
		t.Fatalf("stream bytes = %q", body)
	}
}

func TestEpisodeProgressZeroShape(t *testing.T) {
	e := podcastEnv(t)
	status, body := e.get(t, fmt.Sprintf("/api/podcasts/episodes/pe-%d/progress", e.epID), e.token)
	if status != 200 {
		t.Fatalf("status %d: %s", status, body)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"currentTime", "progress", "isFinished", "lastUpdate", "serverTime", "duration", "durationTimeSeconds", "libraryItemId"} {
		if _, ok := p[k]; !ok {
			t.Fatalf("progress payload missing %q: %s", k, body)
		}
	}
	if p["currentTime"].(float64) != 0 || p["isFinished"].(bool) || p["libraryItemId"] != fmt.Sprintf("pe-%d", e.epID) {
		t.Fatalf("zero progress wrong: %s", body)
	}
	if p["duration"].(float64) != 1800 {
		t.Fatalf("duration should fall back to episode duration: %s", body)
	}
}

func TestEpisodeProgressRoundTrip(t *testing.T) {
	e := podcastEnv(t)
	path := fmt.Sprintf("/api/podcasts/episodes/pe-%d/progress", e.epID)

	status, body := e.req(t, "POST", path, e.token, map[string]any{"currentTime": 900, "duration": 1800})
	if status != 200 {
		t.Fatalf("post: status %d: %s", status, body)
	}
	var p map[string]any
	json.Unmarshal([]byte(body), &p)
	if p["currentTime"].(float64) != 900 || p["progress"].(float64) != 0.5 || p["isFinished"].(bool) {
		t.Fatalf("post response wrong: %s", body)
	}

	status, body = e.get(t, path, e.token)
	if status != 200 {
		t.Fatalf("get: status %d: %s", status, body)
	}
	json.Unmarshal([]byte(body), &p)
	if p["currentTime"].(float64) != 900 || p["progress"].(float64) != 0.5 || p["isFinished"].(bool) {
		t.Fatalf("round trip lost state: %s", body)
	}

	// progress-only body derives position from progress*duration
	status, body = e.req(t, "POST", path, e.token, map[string]any{"progress": 0.25, "duration": 1800})
	if status != 200 {
		t.Fatalf("post: status %d: %s", status, body)
	}
	json.Unmarshal([]byte(body), &p)
	if p["currentTime"].(float64) != 450 {
		t.Fatalf("progress-derived position wrong: %s", body)
	}

	// within 5s of the end marks finished
	status, body = e.req(t, "POST", path, e.token, map[string]any{"currentTime": 1796, "duration": 1800})
	if status != 200 {
		t.Fatalf("post: status %d: %s", status, body)
	}
	json.Unmarshal([]byte(body), &p)
	if !p["isFinished"].(bool) {
		t.Fatalf("end-of-episode position should mark finished: %s", body)
	}

	// detail payloads carry inline progress
	status, body = e.get(t, fmt.Sprintf("/api/podcasts/%d", e.podID), e.token)
	if status != 200 || !strings.Contains(body, `"currentTime":1796`) {
		t.Fatalf("episode payload missing inline progress: %s", body)
	}
}

func TestEpisodeProgressAuthAndNotFound(t *testing.T) {
	e := podcastEnv(t)
	if status, _ := e.req(t, "GET", fmt.Sprintf("/api/podcasts/episodes/pe-%d/progress", e.epID), "", nil); status != 401 {
		t.Fatalf("unauthenticated progress: status %d, want 401", status)
	}
	if status, _ := e.req(t, "GET", "/api/podcasts/episodes/pe-999/progress", e.token, nil); status != 404 {
		t.Fatalf("unknown episode: status %d, want 404", status)
	}
}
