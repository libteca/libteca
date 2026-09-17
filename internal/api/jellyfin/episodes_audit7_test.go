package jellyfin

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSpecialsSeasonFilterExcludesOrdinarySeasons(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "tv")
	work := e.addSeries(t, lib, "Show", 2, 2)
	now := time.Now().UnixMilli()
	eres, err := e.db.Exec(`INSERT INTO editions (work_id, format, title, duration_secs, position, season_num, episode_num, created_at)
		VALUES (?,?,?,?,0,0,1,?)`, work, "video", "Special", 600.0, now)
	if err != nil {
		t.Fatal(err)
	}
	edID, _ := eres.LastInsertId()
	if _, err := e.db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, chapters, embedded_meta, missing, probed_at)
		VALUES (?,?,?,?,?,?,?,?,0,?)`, edID, t.TempDir()+"/sp.mkv", 1, 1, now, 600.0, "[]", "{}", now); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("GET", fmt.Sprintf("/Shows/%d/Episodes?SeasonId=s%d-0", work, work), nil)
	req.Header.Set("X-Emby-Token", e.token)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("specials request = %d", rec.Code)
	}
	var out struct {
		Items []struct {
			Name string `json:"Name"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Items) != 1 || out.Items[0].Name != "Special" {
		t.Fatalf("specials filter returned %d items: %+v", len(out.Items), out.Items)
	}

	req2 := httptest.NewRequest("GET", fmt.Sprintf("/Shows/%d/Episodes?SeasonId=garbage", work), nil)
	req2.Header.Set("X-Emby-Token", e.token)
	rec2 := httptest.NewRecorder()
	e.h.ServeHTTP(rec2, req2)
	if rec2.Code != 400 {
		t.Fatalf("malformed SeasonId = %d, want 400", rec2.Code)
	}
}

func TestSaveFromSessionRejectsInvalidPosition(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "tv")
	work := e.addSeries(t, lib, "Show", 1, 1)
	ed := e.editionID(t, work, 1, 1)
	id := "e" + fmt.Sprint(ed)
	for _, body := range []string{
		`{"ItemId":"` + id + `","PositionTicks":-100}`,
		`{"ItemId":"` + id + `","PositionTicks":` + strings.Repeat("9", 19) + `}`,
	} {
		req := httptest.NewRequest("POST", "/Sessions/Playing/Progress", strings.NewReader(body))
		req.Header.Set("X-Emby-Token", e.token)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != 400 {
			t.Fatalf("body %s = %d (%s), want 400", body, rec.Code, rec.Body.String())
		}
	}
}
