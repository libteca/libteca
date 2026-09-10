package jellyfin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron-go/neutron"
)

type testEnv struct {
	db    *store.DB
	h     http.Handler
	token string
	user  int64
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
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
	r := neutron.New().Router()
	New(db, t.TempDir(), nil).Mount(r)
	return &testEnv{db: db, h: r, token: token, user: uid}
}

func (e *testEnv) addLibrary(t *testing.T, typ string) int64 {
	t.Helper()
	res, err := e.db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES (?,?,?,?)`,
		"lib-"+typ, typ, t.TempDir(), time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("seed library: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// addSeries seeds work + season/episode editions + one file row per edition,
// mirroring how the scanner persists TV (season_num/episode_num on editions).
func (e *testEnv) addSeries(t *testing.T, libID int64, title string, seasons, eps int) int64 {
	t.Helper()
	now := time.Now().UnixMilli()
	res, err := e.db.Exec(`INSERT INTO works (library_id, title, created_at, updated_at) VALUES (?,?,?,?)`,
		libID, title, now, now)
	if err != nil {
		t.Fatalf("seed work: %v", err)
	}
	workID, _ := res.LastInsertId()
	for s := 1; s <= seasons; s++ {
		for ep := 1; ep <= eps; ep++ {
			dur := 600.0
			eres, err := e.db.Exec(`INSERT INTO editions (work_id, format, title, duration_secs, position, season_num, episode_num, created_at)
				VALUES (?,?,?,?,?,?,?,?)`,
				workID, "video", fmt.Sprintf("S%02dE%02d", s, ep), dur, ep, s, ep, now)
			if err != nil {
				t.Fatalf("seed edition: %v", err)
			}
			edID, _ := eres.LastInsertId()
			_, err = e.db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, chapters, embedded_meta, missing, probed_at)
				VALUES (?,?,?,?,?,?,?,?,0,?)`,
				edID, filepath.Join(t.TempDir(), title, fmt.Sprintf("s%02de%02d.mkv", s, ep)), 1, 1, now, dur, "[]", "{}", now)
			if err != nil {
				t.Fatalf("seed file: %v", err)
			}
		}
	}
	return workID
}

func (e *testEnv) editionID(t *testing.T, workID int64, season, ep int) int64 {
	t.Helper()
	var id int64
	err := e.db.QueryRow(`SELECT id FROM editions WHERE work_id = ? AND season_num = ? AND episode_num = ?`,
		workID, season, ep).Scan(&id)
	if err != nil {
		t.Fatalf("edition lookup s%de%d: %v", season, ep, err)
	}
	return id
}

func (e *testEnv) setProgress(t *testing.T, edID int64, pos float64, finished bool) {
	t.Helper()
	dur := 600.0
	if err := e.db.SetProgress(&store.Progress{
		UserID: e.user, EditionID: edID, EditionPositionSecs: pos,
		DurationSecs: &dur, IsFinished: finished,
	}); err != nil {
		t.Fatalf("SetProgress: %v", err)
	}
}

type nextUpItem struct {
	Id                string `json:"Id"`
	SeriesId          string `json:"SeriesId"`
	SeriesName        string `json:"SeriesName"`
	IndexNumber       int    `json:"IndexNumber"`
	ParentIndexNumber int    `json:"ParentIndexNumber"`
	UserData          struct {
		PlaybackPositionTicks int64 `json:"PlaybackPositionTicks"`
		IsPlayed              bool  `json:"IsPlayed"`
	} `json:"UserData"`
}

type nextUpDTO struct {
	Items            []nextUpItem `json:"Items"`
	TotalRecordCount int          `json:"TotalRecordCount"`
	StartIndex       int          `json:"StartIndex"`
}

func (e *testEnv) nextUp(t *testing.T, query string) nextUpDTO {
	t.Helper()
	req := httptest.NewRequest("GET", "/Shows/NextUp"+query, nil)
	req.Header.Set("X-Emby-Token", e.token)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("NextUp %s: status %d: %s", query, rec.Code, rec.Body.String())
	}
	var dto nextUpDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return dto
}

func (e *testEnv) requireEpisode(t *testing.T, it nextUpItem, season, ep int) {
	t.Helper()
	if it.ParentIndexNumber != season || it.IndexNumber != ep {
		t.Fatalf("item = S%02dE%02d, want S%02dE%02d", it.ParentIndexNumber, it.IndexNumber, season, ep)
	}
}

func TestNextUpNothingWatchedEmpty(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "tv")
	e.addSeries(t, lib, "Show", 2, 3)
	dto := e.nextUp(t, "")
	if len(dto.Items) != 0 || dto.TotalRecordCount != 0 {
		t.Fatalf("want empty, got %+v", dto)
	}
}

func TestNextUpUnstartedOnlyWithDisableFirstEpisode(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "tv")
	work := e.addSeries(t, lib, "Show", 2, 3)
	dto := e.nextUp(t, "?DisableFirstEpisode=true")
	if len(dto.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(dto.Items))
	}
	e.requireEpisode(t, dto.Items[0], 1, 1)
	if dto.Items[0].SeriesId != fmt.Sprintf("w%d", work) {
		t.Fatalf("SeriesId = %q", dto.Items[0].SeriesId)
	}
}

func TestNextUpPartialWins(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "tv")
	work := e.addSeries(t, lib, "Show", 2, 3)
	e.setProgress(t, e.editionID(t, work, 1, 2), 120, false)
	dto := e.nextUp(t, "")
	if len(dto.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(dto.Items))
	}
	e.requireEpisode(t, dto.Items[0], 1, 2)
	if dto.Items[0].UserData.PlaybackPositionTicks != 1200000000 {
		t.Fatalf("ticks = %d", dto.Items[0].UserData.PlaybackPositionTicks)
	}
	if dto.Items[0].UserData.IsPlayed {
		t.Fatal("IsPlayed should be false")
	}
}

func TestNextUpAfterFinishedEpisode(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "tv")
	work := e.addSeries(t, lib, "Show", 2, 3)
	e.setProgress(t, e.editionID(t, work, 1, 2), 600, true)
	dto := e.nextUp(t, "")
	if len(dto.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(dto.Items))
	}
	e.requireEpisode(t, dto.Items[0], 1, 3)
	if dto.Items[0].UserData.IsPlayed || dto.Items[0].UserData.PlaybackPositionTicks != 0 {
		t.Fatalf("next episode should be unwatched: %+v", dto.Items[0].UserData)
	}
}

func TestNextUpRollsToNextSeason(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "tv")
	work := e.addSeries(t, lib, "Show", 2, 3)
	for ep := 1; ep <= 3; ep++ {
		e.setProgress(t, e.editionID(t, work, 1, ep), 600, true)
	}
	dto := e.nextUp(t, "")
	if len(dto.Items) != 1 {
		t.Fatalf("want 1 item, got %d", len(dto.Items))
	}
	e.requireEpisode(t, dto.Items[0], 2, 1)
}

func TestNextUpAllFinishedExcluded(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "tv")
	work := e.addSeries(t, lib, "Show", 1, 2)
	for ep := 1; ep <= 2; ep++ {
		e.setProgress(t, e.editionID(t, work, 1, ep), 600, true)
	}
	if dto := e.nextUp(t, ""); len(dto.Items) != 0 {
		t.Fatalf("want empty, got %d", len(dto.Items))
	}
}

func TestNextUpSeriesFilter(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "tv")
	started := e.addSeries(t, lib, "Started", 1, 3)
	other := e.addSeries(t, lib, "Other", 1, 3)
	e.setProgress(t, e.editionID(t, started, 1, 1), 300, true)

	dto := e.nextUp(t, fmt.Sprintf("?SeriesId=w%d", started))
	if len(dto.Items) != 1 || dto.Items[0].SeriesName != "Started" {
		t.Fatalf("filter to started: %+v", dto.Items)
	}
	e.requireEpisode(t, dto.Items[0], 1, 2)

	if dto := e.nextUp(t, fmt.Sprintf("?SeriesId=w%d", other)); len(dto.Items) != 0 {
		t.Fatalf("unstarted series filtered without flag: %d items", len(dto.Items))
	}
	dto = e.nextUp(t, fmt.Sprintf("?SeriesId=w%d&DisableFirstEpisode=true", other))
	if len(dto.Items) != 1 {
		t.Fatalf("unstarted series with flag: %d items", len(dto.Items))
	}
	e.requireEpisode(t, dto.Items[0], 1, 1)
}

func TestNextUpLimitAndStartIndex(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "tv")
	a := e.addSeries(t, lib, "A Show", 1, 3)
	b := e.addSeries(t, lib, "B Show", 1, 3)
	e.setProgress(t, e.editionID(t, a, 1, 1), 600, true)
	e.setProgress(t, e.editionID(t, b, 1, 1), 600, true)
	e.db.Exec(`UPDATE progress SET updated_at = 1000 WHERE edition_id = ?`, e.editionID(t, a, 1, 1))
	e.db.Exec(`UPDATE progress SET updated_at = 2000 WHERE edition_id = ?`, e.editionID(t, b, 1, 1))

	dto := e.nextUp(t, "?Limit=1")
	if dto.TotalRecordCount != 2 || len(dto.Items) != 1 {
		t.Fatalf("limit: total %d, items %d", dto.TotalRecordCount, len(dto.Items))
	}
	if dto.Items[0].SeriesName != "B Show" {
		t.Fatalf("most recent first: %q", dto.Items[0].SeriesName)
	}
	dto = e.nextUp(t, "?StartIndex=1")
	if len(dto.Items) != 1 || dto.Items[0].SeriesName != "A Show" {
		t.Fatalf("start index: %+v", dto.Items)
	}
}
