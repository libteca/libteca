package abs_test

import (
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
	req, err := http.NewRequest("GET", e.srv.URL+path, nil)
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
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, string(body)
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
