package jellyfin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

type podEnv struct {
	h      http.Handler
	token  string
	userID int64
	libID  int64
	podID  int64
	epID   int64
}

func newPodEnv(t *testing.T) *podEnv {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	now := time.Now().UnixMilli()
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('u','x',1,?,?)`, now, now)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	uid, _ := res.LastInsertId()
	token, err := auth.IssueToken(db, uid, "test")
	if err != nil {
		t.Fatalf("token: %v", err)
	}

	res, err = db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('Podcasts','podcasts',?,?)`, filepath.Join(dir, "pods"), now)
	if err != nil {
		t.Fatalf("seed library: %v", err)
	}
	libID, _ := res.LastInsertId()

	res, err = db.Exec(`INSERT INTO podcasts (library_id, feed_url, title, author, auto_download, max_episodes, created_at)
		VALUES (?,?,?,?,1,3,?)`, libID, "https://example.com/feed.xml", "Test Show", "Host One", now)
	if err != nil {
		t.Fatalf("seed podcast: %v", err)
	}
	podID, _ := res.LastInsertId()

	audioPath := filepath.Join(dir, "ep1.mp3")
	if err := os.WriteFile(audioPath, []byte("fake-podcast-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err = db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, container, duration_secs, chapters, embedded_meta, missing, probed_at)
		VALUES (NULL,?,1,1,?, 'mp3', 1800.0, '[]','{}',0,?)`, audioPath, now, now)
	if err != nil {
		t.Fatalf("seed file: %v", err)
	}
	fileID, _ := res.LastInsertId()

	res, err = db.Exec(`INSERT INTO podcast_episodes (podcast_id, guid, title, pub_date, duration_secs, enclosure_url, enclosure_bytes, file_id, downloaded_at, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		podID, "guid-1", "Episode 1", now, 1800.0, "https://example.com/ep1.mp3", 12345, fileID, now, now)
	if err != nil {
		t.Fatalf("seed episode: %v", err)
	}
	epID, _ := res.LastInsertId()

	// full Mount plus the reported one-line MountPodcasts addition, on one
	// router — proves the new routes coexist with the existing face
	r := neutron.New().Router()
	a := New(db, dir, nil)
	a.Mount(r)
	return &podEnv{h: r, token: token, userID: uid, libID: libID, podID: podID, epID: epID}
}

func (e *podEnv) get(t *testing.T, path string, token string) (*httptest.ResponseRecorder, string) {
	t.Helper()
	return e.req(t, "GET", path, token, nil)
}

func (e *podEnv) req(t *testing.T, method, path, token string, body any) (*httptest.ResponseRecorder, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rd = bytes.NewReader(data)
	}
	req := httptest.NewRequest(method, path, rd)
	if token != "" {
		req.Header.Set("X-Emby-Token", token)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec, rec.Body.String()
}

func TestPodcastViewsIncludePodcastsLibrary(t *testing.T) {
	e := newPodEnv(t)
	rec, body := e.get(t, fmt.Sprintf("/Users/%d/Views", e.userID), e.token)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, body)
	}
	var dto struct {
		Items []struct {
			Id   string `json:"Id"`
			Name string `json:"Name"`
			Type string `json:"Type"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := fmt.Sprintf("lib%d", e.libID)
	found := false
	for _, it := range dto.Items {
		if it.Id == want && it.Name == "Podcasts" && it.Type == "CollectionFolder" {
			found = true
		}
	}
	if !found {
		t.Fatalf("podcasts library missing from views: %s", body)
	}
}

func TestPodcastItemsShows(t *testing.T) {
	e := newPodEnv(t)
	rec, body := e.get(t, fmt.Sprintf("/Items?ParentId=lib%d", e.libID), e.token)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, body)
	}
	if !strings.Contains(body, `"Type":"AudioPodcast"`) || !strings.Contains(body, fmt.Sprintf(`"Id":"pod%d"`, e.podID)) {
		t.Fatalf("podcast show item wrong: %s", body)
	}
}

func TestPodcastItemsEpisodes(t *testing.T) {
	e := newPodEnv(t)
	rec, body := e.get(t, fmt.Sprintf("/Items?ParentId=pod%d", e.podID), e.token)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, body)
	}
	for _, want := range []string{
		fmt.Sprintf(`"Id":"pe%d"`, e.epID),
		`"Type":"Audio"`,
		fmt.Sprintf(`"SeriesId":"pod%d"`, e.podID),
		fmt.Sprintf(`/Audio/podcast/pe%d/stream`, e.epID),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in: %s", want, body)
		}
	}
}

func TestPodcastEpisodeStreamAndAuth(t *testing.T) {
	e := newPodEnv(t)
	path := fmt.Sprintf("/Audio/podcast/pe%d/stream", e.epID)
	if rec, _ := e.get(t, path, ""); rec.Code != 401 {
		t.Fatalf("unauthenticated stream: status %d, want 401", rec.Code)
	}
	rec, body := e.get(t, path, e.token)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, body)
	}
	if body != "fake-podcast-bytes" {
		t.Fatalf("stream bytes = %q", body)
	}
}

func TestPodcastEpisodeProgressUserData(t *testing.T) {
	e := newPodEnv(t)
	epPath := fmt.Sprintf("/Audio/podcast/pe%d/progress", e.epID)
	if rec, _ := e.req(t, "POST", epPath, "", map[string]any{"PositionTicks": 123450000}); rec.Code != 401 {
		t.Fatalf("unauthenticated progress post: status %d, want 401", rec.Code)
	}

	rec, body := e.req(t, "POST", epPath, e.token, map[string]any{"ItemId": fmt.Sprintf("pe%d", e.epID), "PositionTicks": 123450000})
	if rec.Code != 200 {
		t.Fatalf("progress post: status %d: %s", rec.Code, body)
	}

	rec, body = e.get(t, fmt.Sprintf("/Items?ParentId=pod%d", e.podID), e.token)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, body)
	}
	var dto struct {
		Items []struct {
			Id       string `json:"Id"`
			UserData struct {
				PlaybackPositionTicks int64   `json:"PlaybackPositionTicks"`
				PlayedPercentage      float64 `json:"PlayedPercentage"`
				IsPlayed              bool    `json:"IsPlayed"`
			} `json:"UserData"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Items) != 1 {
		t.Fatalf("want 1 episode item: %s", body)
	}
	ud := dto.Items[0].UserData
	if ud.PlaybackPositionTicks != 123450000 {
		t.Fatalf("PlaybackPositionTicks = %d, want 123450000", ud.PlaybackPositionTicks)
	}
	if ud.IsPlayed || ud.PlayedPercentage < 0.68 || ud.PlayedPercentage > 0.69 {
		t.Fatalf("UserData wrong: %+v", ud)
	}

	// show item carries the latest in-progress position for continue listening
	rec, body = e.get(t, fmt.Sprintf("/Items?ParentId=lib%d", e.libID), e.token)
	if rec.Code != 200 || !strings.Contains(body, `"PlaybackPositionTicks":123450000`) {
		t.Fatalf("show item missing resume position: %s", body)
	}

	// near the end marks played
	rec, _ = e.req(t, "POST", epPath, e.token, map[string]any{"PositionTicks": 17960000000})
	if rec.Code != 200 {
		t.Fatalf("finish post failed: %d", rec.Code)
	}
	rec, body = e.get(t, fmt.Sprintf("/Items?ParentId=pod%d", e.podID), e.token)
	var dto2 struct {
		Items []struct {
			UserData struct {
				PlaybackPositionTicks int64 `json:"PlaybackPositionTicks"`
				IsPlayed              bool  `json:"IsPlayed"`
			} `json:"UserData"`
		} `json:"Items"`
	}
	json.Unmarshal(rec.Body.Bytes(), &dto2)
	if !dto2.Items[0].UserData.IsPlayed {
		t.Fatalf("finished episode not IsPlayed: %s", body)
	}

	// finished show resets its resume position
	rec, body = e.get(t, fmt.Sprintf("/Items?ParentId=lib%d", e.libID), e.token)
	if !strings.Contains(body, `"PlaybackPositionTicks":0`) {
		t.Fatalf("finished show should reset resume position: %s", body)
	}
}
