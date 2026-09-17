package abs_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/api/abs"
	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

func TestPlayTrackOffsetsKeepFractionalDurations(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now().UnixMilli()
	res, _ := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('u','x',1,?,?)`, now, now)
	uid, _ := res.LastInsertId()
	token, _ := auth.IssueToken(db, uid, "test")
	res, _ = db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('Books','audiobooks',?,?)`, dir, now)
	libID, _ := res.LastInsertId()
	res, _ = db.Exec(`INSERT INTO works (library_id, title, created_at, updated_at) VALUES (?,?,?,?)`, libID, "Book", now, now)
	workID, _ := res.LastInsertId()
	res, _ = db.Exec(`INSERT INTO editions (work_id, format, title, duration_secs, position, created_at) VALUES (?,?,?,?,1,?)`, workID, "audio", "Book", 0, now)
	edID, _ := res.LastInsertId()
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, chapters, embedded_meta, missing, probed_at)
			VALUES (?,?,?,?,?,?,?,?,0,?)`, edID, filepath.Join(dir, "f"+strings.Repeat("x", i)+".m4b"), i+1, 1, now, 60.9, "[]", "{}", now); err != nil {
			t.Fatal(err)
		}
	}
	app := neutron.New()
	r := app.Router()
	abs.New(db, dir).Mount(r.Group("/api", auth.Middleware(db)))
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest("POST", srv.URL+"/api/items/"+strings.TrimSpace(strings.Repeat("", 0))+int64str(edID)+"/play", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("play = %d: %s", resp.StatusCode, body)
	}
	var out struct {
		AudioTracks []struct {
			StartOffset float64 `json:"startOffset"`
			Duration    float64 `json:"duration"`
		} `json:"audioTracks"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.AudioTracks) != 3 {
		t.Fatalf("tracks = %d", len(out.AudioTracks))
	}
	want := []float64{0, 60.9, 121.8}
	for i, tr := range out.AudioTracks {
		if diff := tr.StartOffset - want[i]; diff > 0.001 || diff < -0.001 {
			t.Fatalf("track %d startOffset = %v, want %v (no int truncation per track)", i, tr.StartOffset, want[i])
		}
	}
}

func int64str(v int64) string {
	return strings.TrimSpace(jsonInt(v))
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
