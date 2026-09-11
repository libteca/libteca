package jellyfin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/libteca/libteca/internal/transcode"
	"github.com/neutron-build/neutron/go/neutron"
)

func (e *testEnv) get(t *testing.T, path string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if req.Header.Get("X-Emby-Token") == "" && req.Header.Get("X-Emby-Authorization") == "" && req.Header.Get("Authorization") == "" {
		req.Header.Set("X-Emby-Token", e.token)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) addWork(t *testing.T, libID int64, title string) int64 {
	t.Helper()
	now := time.Now().UnixMilli()
	res, err := e.db.Exec(`INSERT INTO works (library_id, title, created_at, updated_at) VALUES (?,?,?,?)`,
		libID, title, now, now)
	if err != nil {
		t.Fatalf("seed work: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestJFAuthXEmbyAuthorizationToken(t *testing.T) {
	e := newEnv(t)
	rec := e.get(t, "/System/Configuration", map[string]string{
		"X-Emby-Authorization": `MediaBrowser Client="JMP", Device="TV", DeviceId="d1", Version="10.10", Token="` + e.token + `"`,
	})
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestSessionsListLiveHub(t *testing.T) {
	e := newEnv(t)

	rec := e.get(t, "/Sessions", nil)
	if rec.Code != 200 {
		t.Fatalf("empty status %d: %s", rec.Code, rec.Body.String())
	}
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Fatalf("empty hub = %s", rec.Body.String())
	}

	e.jf.hub.update(&liveSession{
		DeviceID: "tv-dev", PlaySessionID: "ps-1", UserID: e.user,
		Client: "JMP", DeviceName: "Living Room", PosTicks: 42, Paused: true,
	})
	rec = e.get(t, "/Sessions", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var sessions []wsSessionDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %+v", sessions)
	}
	s := sessions[0]
	if s.Id != "tv-dev" || s.PlaySessionId != "ps-1" || s.Client != "JMP" ||
		s.DeviceName != "Living Room" || s.UserName != "u" ||
		s.PlayState == nil || s.PlayState.PositionTicks != 42 || !s.PlayState.IsPaused {
		t.Fatalf("session = %+v", s)
	}
}

func TestSessionStoppedWritesOnceAndClosesTranscode(t *testing.T) {
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
	tc := transcode.New(dir)
	sid := "ps-42"
	_, getErr := tc.Get(sid, 1, filepath.Join(dir, "missing.mkv"), 0)
	if getErr != nil {
		if _, err := os.Stat(filepath.Join(dir, "transcode", sid)); !os.IsNotExist(err) {
			t.Fatalf("failed session dir must be cleaned: %v", err)
		}
		return
	}
	sessDir := filepath.Join(dir, "transcode", sid)
	if _, err := os.Stat(sessDir); err != nil {
		t.Fatalf("session dir: %v", err)
	}
	r := neutron.New().Router()
	New(db, dir, tc).Mount(r)
	req := httptest.NewRequest("POST", "/Sessions/Playing/Stopped", strings.NewReader(`{"ItemId":"e1","PositionTicks":0,"PlaySessionId":"ps-42"}`))
	req.Header.Set("X-Emby-Token", token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	if err := dec.Decode(&v); err != io.EOF {
		t.Fatalf("second JSON value: %v body=%q", err, rec.Body.String())
	}
	if _, err := os.Stat(sessDir); !os.IsNotExist(err) {
		t.Fatalf("transcode session still on disk: %v", err)
	}
}

func TestSessionsPlayingProgressFailure(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "tv")
	work := e.addSeries(t, lib, "Show", 1, 1)
	edID := e.editionID(t, work, 1, 1)
	if _, err := e.db.Exec(`DROP TABLE progress`); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/Sessions/Playing",
		strings.NewReader(fmt.Sprintf(`{"ItemId":"e%d","PositionTicks":120000000}`, edID)))
	req.Header.Set("X-Emby-Token", e.token)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 500 {
		t.Fatalf("playing report with progress-store failure = %d %s, want 500", rec.Code, rec.Body.String())
	}
}

func TestMediaStreamsNoFakeSubtitle(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(video, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	streams := (&API{}).mediaStreams(&store.FileRec{Path: video})
	for _, s := range streams {
		if s["Type"] == "Subtitle" {
			t.Fatalf("unexpected subtitle: %+v", streams)
		}
	}
}

func TestMediaStreamsSidecarSRT(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(video, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "movie.srt"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	streams := (&API{}).mediaStreams(&store.FileRec{Path: video})
	n := 0
	for _, s := range streams {
		if s["Type"] == "Subtitle" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("subtitle streams = %d in %+v", n, streams)
	}
}

func TestTVLibraryDefaultBrowseReturnsSeries(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "tv")
	work := e.addSeries(t, lib, "Show", 1, 2)
	rec := e.get(t, fmt.Sprintf("/Users/%d/Items?ParentId=lib%d", e.user, lib), nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var dto struct {
		Items []struct {
			Id   string `json:"Id"`
			Type string `json:"Type"`
			Name string `json:"Name"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if len(dto.Items) != 1 || dto.Items[0].Type != "Series" || dto.Items[0].Name != "Show" ||
		dto.Items[0].Id != fmt.Sprintf("w%d", work) {
		t.Fatalf("items = %+v", dto.Items)
	}
}

func TestLibraryItemTypes(t *testing.T) {
	e := newEnv(t)
	cases := []struct {
		libType string
		want    string
	}{
		{"movies", "Movie"},
		{"audiobooks", "AudioBook"},
		{"books", "Book"},
		{"comics", "Book"},
	}
	for _, tc := range cases {
		lib := e.addLibrary(t, tc.libType)
		e.addWork(t, lib, tc.libType+"-title")
		rec := e.get(t, fmt.Sprintf("/Users/%d/Items?ParentId=lib%d", e.user, lib), nil)
		if rec.Code != 200 {
			t.Fatalf("%s status %d: %s", tc.libType, rec.Code, rec.Body.String())
		}
		var dto struct {
			Items []struct {
				Type string `json:"Type"`
			} `json:"Items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
			t.Fatal(err)
		}
		if len(dto.Items) != 1 || dto.Items[0].Type != tc.want {
			t.Fatalf("%s items = %+v, want Type %s", tc.libType, dto.Items, tc.want)
		}
	}
}

func TestUserItemsParentCasingAndSeasonBrowse(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "tv")
	work := e.addSeries(t, lib, "Show", 2, 2)

	rec := e.get(t, fmt.Sprintf("/Users/%d/Items?parentId=lib%d", e.user, lib), nil)
	if rec.Code != 200 {
		t.Fatalf("lowercase parentId status %d: %s", rec.Code, rec.Body.String())
	}
	var dto struct {
		Items []struct {
			Id   string `json:"Id"`
			Type string `json:"Type"`
		} `json:"Items"`
		Total int `json:"TotalRecordCount"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Total != 1 || len(dto.Items) != 1 || dto.Items[0].Type != "Series" {
		t.Fatalf("series browse via lowercase parentId = %+v", dto)
	}

	rec = e.get(t, fmt.Sprintf("/Users/%d/Items?PARENTID=w%d", e.user, work), nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Total != 2 || len(dto.Items) != 2 || dto.Items[0].Type != "Season" {
		t.Fatalf("series children = %+v", dto)
	}

	rec = e.get(t, fmt.Sprintf("/Users/%d/Items?ParentId=s%d-2&includeitemtypes=Episode", e.user, work), nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Total != 2 || len(dto.Items) != 2 || dto.Items[0].Type != "Episode" {
		t.Fatalf("season children = %+v", dto)
	}

	rec = e.get(t, fmt.Sprintf("/Users/%d/Items?ParentId=w%d&IncludeItemTypes=Episode", e.user, work), nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Total != 4 || len(dto.Items) != 4 {
		t.Fatalf("all episodes under series = %+v", dto)
	}
}

func TestPlayableWithNoFilesIs404(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "movies")
	work := e.addWork(t, lib, "Gone")
	now := time.Now().UnixMilli()
	res, err := e.db.Exec(`INSERT INTO editions (work_id, format, title, created_at) VALUES (?,?,?,?)`,
		work, "video", "Gone", now)
	if err != nil {
		t.Fatal(err)
	}
	edID, _ := res.LastInsertId()
	for _, path := range []string{
		fmt.Sprintf("/Videos/e%d/stream", edID),
		fmt.Sprintf("/Audio/e%d/universal", edID),
		fmt.Sprintf("/Videos/e%d/Trickplay/320/manifest.json", edID),
	} {
		rec := e.get(t, path, nil)
		if rec.Code != 404 {
			t.Fatalf("%s = %d, want 404 (no panic)", path, rec.Code)
		}
	}
	req := httptest.NewRequest("POST", fmt.Sprintf("/Items/e%d/PlaybackInfo", edID), nil)
	req.Header.Set("X-Emby-Token", e.token)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatalf("PlaybackInfo = %d, want 404 (no panic)", rec.Code)
	}
}

func TestSystemPingAndInfo(t *testing.T) {
	e := newEnv(t)
	rec := e.get(t, "/System/Ping", nil)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) == "" {
		t.Fatalf("ping = %d %q", rec.Code, rec.Body.String())
	}
	rec = e.get(t, "/System/Info", nil)
	if rec.Code != 200 {
		t.Fatalf("info status %d: %s", rec.Code, rec.Body.String())
	}
	var info map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info["Id"] != "libteca-server" || info["Version"] != "10.10.0" {
		t.Fatalf("info = %v", info)
	}
}

func TestRewriteHLSPlaylist(t *testing.T) {
	in := "#EXTM3U\n#EXT-X-VERSION:3\n#EXTINF:4.0,\nseg00000.ts\n#EXTINF:4.0,\nseg00001.ts\n"
	got := string(rewriteHLSPlaylist([]byte(in), "e3", "sess1", "tok"))
	if !strings.Contains(got, "/videos/e3/hls/sess1/seg00000.ts?api_key=tok") {
		t.Fatalf("seg0 missing: %s", got)
	}
	if !strings.Contains(got, "/videos/e3/hls/sess1/seg00001.ts?api_key=tok") {
		t.Fatalf("seg1 missing: %s", got)
	}
	if strings.Contains(got, "\nseg00000.ts\n") {
		t.Fatalf("relative uri left: %s", got)
	}
	if !strings.Contains(got, "#EXTM3U") {
		t.Fatal("header dropped")
	}
}

func TestSessionStoppedNilTranscodeWritesOnce(t *testing.T) {
	e := newEnv(t)
	req := httptest.NewRequest("POST", "/Sessions/Playing/Stopped", strings.NewReader(`{"ItemId":"e1","PlaySessionId":"ps-1"}`))
	req.Header.Set("X-Emby-Token", e.token)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	dec := json.NewDecoder(bytes.NewReader(rec.Body.Bytes()))
	var v map[string]any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	if err := dec.Decode(&v); err != io.EOF {
		t.Fatalf("second JSON value: %v body=%q", err, rec.Body.String())
	}
}
