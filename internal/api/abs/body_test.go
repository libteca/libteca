package abs

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

type bodyTestStack struct {
	db   *store.DB
	srv  *httptest.Server
	tok  string
	edID int64
}

func newBodyTestStack(t *testing.T) *bodyTestStack {
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
	tok, err := auth.IssueToken(db, uid, "test")
	if err != nil {
		t.Fatal(err)
	}
	res, _ = db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES ('Books','audiobooks',?,?)`, dir, now)
	libID, _ := res.LastInsertId()
	res, _ = db.Exec(`INSERT INTO works (library_id, title, created_at, updated_at) VALUES (?,?,?,?)`, libID, "Book", now, now)
	workID, _ := res.LastInsertId()
	res, _ = db.Exec(`INSERT INTO editions (work_id, format, title, duration_secs, position, created_at) VALUES (?,?,?,?,1,?)`, workID, "audio", "Book", 60, now)
	edID, _ := res.LastInsertId()
	if _, err := db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, chapters, embedded_meta, missing, probed_at)
		VALUES (?,?,1,1,?,60,'[]','{}',0,?)`, edID, filepath.Join(dir, "f.m4b"), now, now); err != nil {
		t.Fatal(err)
	}
	a := New(db, dir)
	app := neutron.New()
	a.Mount(app.Router().Group("/api", auth.Middleware(db)))
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return &bodyTestStack{db: db, srv: srv, tok: tok, edID: edID}
}

func (s *bodyTestStack) post(t *testing.T, path, contentType, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest("POST", s.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.tok)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := s.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data)
}

func (s *bodyTestStack) sessionCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM playback_sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPlayRejectsMalformedBody(t *testing.T) {
	s := newBodyTestStack(t)
	code, body := s.post(t, fmt.Sprintf("/api/items/%d/play", s.edID), "application/json", `{"deviceInfo":`)
	if code != 400 {
		t.Fatalf("malformed play = %d %s, want 400", code, body)
	}
	if n := s.sessionCount(t); n != 0 {
		t.Fatalf("sessions after malformed play = %d, want 0", n)
	}
}

func TestPlayRejectsOversizedBody(t *testing.T) {
	s := newBodyTestStack(t)
	big := `{"supportedMimeTypes":["` + strings.Repeat("a", 1<<20) + `"]}`
	code, body := s.post(t, fmt.Sprintf("/api/items/%d/play", s.edID), "application/json", big)
	if code != 413 {
		t.Fatalf("oversized play = %d %s, want 413", code, body)
	}
	if n := s.sessionCount(t); n != 0 {
		t.Fatalf("sessions after oversized play = %d, want 0", n)
	}
}

func TestPlayEntropyFailureIsServerError(t *testing.T) {
	s := newBodyTestStack(t)
	orig := newPlaySessionID
	newPlaySessionID = func() (string, error) { return "", fmt.Errorf("entropy exhausted") }
	defer func() { newPlaySessionID = orig }()
	code, body := s.post(t, fmt.Sprintf("/api/items/%d/play", s.edID), "application/json", `{}`)
	if code != 500 {
		t.Fatalf("entropy-failed play = %d %s, want 500", code, body)
	}
	if n := s.sessionCount(t); n != 0 {
		t.Fatalf("sessions after entropy failure = %d, want 0", n)
	}
}

func TestPlayEmptyBodyStillAccepted(t *testing.T) {
	s := newBodyTestStack(t)
	code, body := s.post(t, fmt.Sprintf("/api/items/%d/play", s.edID), "application/json", "")
	if code != 200 {
		t.Fatalf("empty play body = %d %s, want 200 (client compatibility)", code, body)
	}
	if n := s.sessionCount(t); n != 1 {
		t.Fatalf("sessions after empty play = %d, want 1", n)
	}
}

func TestSessionCloseMalformedBodyKeepsSessionOpen(t *testing.T) {
	s := newBodyTestStack(t)
	now := time.Now().UnixMilli()
	sess := &store.Session{ID: "sid-body-test", UserID: 1, EditionID: s.edID, StartedAt: now, UpdatedAt: now, DeviceInfo: "{}"}
	if err := s.db.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	code, body := s.post(t, "/api/session/sid-body-test/close", "application/json", `{"currentTime":12`)
	if code != 400 {
		t.Fatalf("malformed close = %d %s, want 400", code, body)
	}
	got, err := s.db.Session("sid-body-test")
	if err != nil {
		t.Fatal(err)
	}
	if got.ClosedAt != nil {
		t.Fatal("session must stay open after a rejected close")
	}
	var progress int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM progress WHERE user_id = 1 AND edition_id = ?`, s.edID).Scan(&progress); err != nil {
		t.Fatal(err)
	}
	if progress != 0 {
		t.Fatal("malformed close must not write progress")
	}

	code, body = s.post(t, "/api/session/sid-body-test/close", "application/json", `{"currentTime":30,"timeListened":10,"duration":60}`)
	if code != 200 {
		t.Fatalf("valid close = %d %s, want 200", code, body)
	}
	got, err = s.db.Session("sid-body-test")
	if err != nil {
		t.Fatal(err)
	}
	if got.ClosedAt == nil {
		t.Fatal("valid close must close the session")
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM progress WHERE user_id = 1 AND edition_id = ?`, s.edID).Scan(&progress); err != nil {
		t.Fatal(err)
	}
	if progress != 1 {
		t.Fatal("valid close must record progress")
	}
}
