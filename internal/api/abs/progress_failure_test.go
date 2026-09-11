package abs_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/api/abs"
	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

func newSessionEnv(t *testing.T) (*store.DB, func(string, string) (int, string)) {
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
	res, err = db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('Books','audiobooks',?,?)`, t.TempDir(), now)
	if err != nil {
		t.Fatal(err)
	}
	libID, _ := res.LastInsertId()
	res, err = db.Exec(`INSERT INTO works (library_id, title, created_at, updated_at) VALUES (?,?,?,?)`, libID, "Book", now, now)
	if err != nil {
		t.Fatal(err)
	}
	workID, _ := res.LastInsertId()
	res, err = db.Exec(`INSERT INTO editions (work_id, format, title, duration_secs, position, created_at) VALUES (?,?,?,?,?,?)`,
		workID, "audio", "Book", 600, 1, now)
	if err != nil {
		t.Fatal(err)
	}
	edID, _ := res.LastInsertId()
	if _, err := db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, chapters, embedded_meta, missing, probed_at)
		VALUES (?,?,1,1,?,600,'[]','{}',0,?)`, edID, filepath.Join(dir, "b.m4b"), now, now); err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{"sess-open", "sess-close"} {
		if _, err := db.Exec(`INSERT INTO playback_sessions (id, user_id, edition_id, started_at, updated_at, position_secs, time_listened_secs, device_info)
			VALUES (?,?,?,?,?,0,0,'test')`, sid, uid, edID, now, now); err != nil {
			t.Fatal(err)
		}
	}
	app := neutron.New()
	r := app.Router()
	abs.New(db, dir).Mount(r.Group("/api", auth.Middleware(db)))
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	post := func(method, path string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(`{"currentTime":120,"timeListened":120,"duration":600}`))
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		buf := new(strings.Builder)
		if _, err := io.Copy(buf, res.Body); err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, buf.String()
	}
	return db, post
}

func TestSessionSyncProgressFailureIsError(t *testing.T) {
	db, post := newSessionEnv(t)
	if _, err := db.Exec(`DROP TABLE progress`); err != nil {
		t.Fatal(err)
	}
	if code, body := post("POST", "/api/session/sess-open/sync"); code != 500 {
		t.Fatalf("sync with progress-store failure = %d %s, want 500", code, body)
	}
}

func TestSessionSyncUpdateFailureIsError(t *testing.T) {
	db, post := newSessionEnv(t)
	if _, err := db.Exec(`CREATE TRIGGER fail_sync BEFORE UPDATE ON playback_sessions BEGIN SELECT RAISE(ABORT, 'boom'); END`); err != nil {
		t.Fatal(err)
	}
	if code, body := post("POST", "/api/session/sess-open/sync"); code != 500 {
		t.Fatalf("sync with session-update failure = %d %s, want 500", code, body)
	}
}

func TestSessionCloseProgressFailureIsError(t *testing.T) {
	db, post := newSessionEnv(t)
	if _, err := db.Exec(`DROP TABLE progress`); err != nil {
		t.Fatal(err)
	}
	if code, body := post("POST", "/api/session/sess-close/close"); code != 500 {
		t.Fatalf("close with progress-store failure = %d %s, want 500", code, body)
	}
}

func TestInternalErrorsDoNotLeakErrText(t *testing.T) {
	db, post := newSessionEnv(t)
	var edID int64
	if err := db.QueryRow(`SELECT id FROM editions LIMIT 1`).Scan(&edID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE progress`); err != nil {
		t.Fatal(err)
	}
	code, body := post("POST", "/api/me/progress/"+strconv.FormatInt(edID, 10))
	if code != 500 {
		t.Fatalf("progress post with store failure = %d %s, want 500", code, body)
	}
	if strings.Contains(body, "no such table") || strings.Contains(body, "sqlite") {
		t.Fatalf("internal error text leaked to client: %s", body)
	}
	if !strings.Contains(body, "Internal server error") {
		t.Fatalf("body = %s, want static message", body)
	}
}
