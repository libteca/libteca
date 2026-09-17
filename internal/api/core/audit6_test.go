package core

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

func TestWriteJSONSerializationFailureIs500(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, 200, map[string]any{"position": math.NaN()})
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type = %q", ct)
	}
	var out map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out["error"] != "internal error" {
		t.Fatalf("body = %s err=%v", rec.Body.String(), err)
	}
}

func TestWriteJSONProblemDetailsPreserved(t *testing.T) {
	rec := httptest.NewRecorder()
	writeJSON(rec, 404, map[string]string{"error": "work gone"})
	if rec.Code != 404 {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "work gone") {
		t.Fatalf("detail lost: %s", rec.Body.String())
	}
}

type progressEnv struct {
	a    *API
	db   *store.DB
	h    http.Handler
	eid  int64
	user int64
}

func newProgressEnv(t *testing.T) *progressEnv {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	uid, err := db.CreateUser("reader", "hash", false)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := db.AddLibrary("L", "audiobooks", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(`INSERT INTO works (library_id, title, created_at, updated_at) VALUES (?, 'W', 0, 0)`, lib)
	if err != nil {
		t.Fatal(err)
	}
	wid, _ := res.LastInsertId()
	eres, err := db.Exec(`INSERT INTO editions (work_id, format, title, duration_secs, created_at) VALUES (?, 'mp3', 'E', 100, 0)`, wid)
	if err != nil {
		t.Fatal(err)
	}
	eid, _ := eres.LastInsertId()
	media := filepath.Join(t.TempDir(), "a.mp3")
	if err := os.WriteFile(media, []byte("xx"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, codec, duration_secs, chapters, embedded_meta, missing, probed_at)
		VALUES (?,?,1,2,0,'mp3',100,'[]','{}',0,0)`, eid, media); err != nil {
		t.Fatal(err)
	}
	a := New(db, t.TempDir())
	app := neutron.New()
	a.Mount(app.Router())
	return &progressEnv{a: a, db: db, h: app.Handler(), eid: eid, user: uid}
}

func (e *progressEnv) post(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/progress/"+strconv.FormatInt(e.eid, 10), strings.NewReader(body))
	req = auth.WithUser(req, e.user)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *progressEnv) get(t *testing.T, eid int64) *httptest.ResponseRecorder {
	t.Helper()
	req := auth.WithUser(httptest.NewRequest("GET", "/progress/"+strconv.FormatInt(eid, 10), nil), e.user)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func TestSetProgressRejectsNonFiniteInput(t *testing.T) {
	e := newProgressEnv(t)
	for _, body := range []string{
		`{"position":NaN}`,
		`{"position":-1}`,
		`{"duration":-5}`,
		`{"page":-2}`,
		`{"device":"` + strings.Repeat("d", 257) + `"}`,
	} {
		if rec := e.post(t, body); rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s = %d %s, want 400", body, rec.Code, rec.Body.String())
		}
	}
}

func TestSetProgressRecordsDevice(t *testing.T) {
	e := newProgressEnv(t)
	if rec := e.post(t, `{"position":10,"device":"pixel-9"}`); rec.Code != http.StatusOK {
		t.Fatalf("setProgress = %d %s", rec.Code, rec.Body.String())
	}
	p, err := e.db.GetReadingProgress(e.user, e.eid)
	if err != nil {
		t.Fatal(err)
	}
	if p.Device == nil || *p.Device != "pixel-9" {
		t.Fatalf("device = %v, want pixel-9", p.Device)
	}
}

func TestGetProgressMissingEditionIs404(t *testing.T) {
	e := newProgressEnv(t)
	if rec := e.get(t, 999); rec.Code != http.StatusNotFound {
		t.Fatalf("missing edition = %d, want 404", rec.Code)
	}
	if rec := e.get(t, e.eid); rec.Code != http.StatusOK {
		t.Fatalf("no-progress edition = %d, want 200 default", rec.Code)
	}
}

func TestWorkDetailUsesScopedLoader(t *testing.T) {
	e := newProgressEnv(t)
	res, err := e.db.Exec(`INSERT INTO works (library_id, title, created_at, updated_at) VALUES (?, 'Other', 0, 0)`, int64(1))
	if err != nil {
		t.Fatal(err)
	}
	wid, _ := res.LastInsertId()
	wv, err := e.db.WorkViewByID(wid)
	if err != nil {
		t.Fatal(err)
	}
	if wv.ID != wid || wv.Title != "Other" || len(wv.Editions) != 0 {
		t.Fatalf("scoped loader = %+v", wv)
	}
	req := auth.WithUser(httptest.NewRequest("GET", "/works/"+strconv.FormatInt(wid, 10), nil), e.user)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("work detail for edition-less work = %d %s", rec.Code, rec.Body.String())
	}
}

func TestServeFileRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	serveFile(rec, req, dir)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("directory serve = %d, want 404", rec.Code)
	}
}

func TestAddLibraryValidatesType(t *testing.T) {
	e := newProgressEnv(t)
	body := func(s string) *strings.Reader {
		return strings.NewReader(`{"name":"x","type":"` + s + `","path":"` + t.TempDir() + `"}`)
	}
	admin, err := e.db.CreateUser("boss", "hash", true)
	if err != nil {
		t.Fatal(err)
	}
	req := auth.WithUser(httptest.NewRequest("POST", "/libraries", body("carrier-pigeon")), admin)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unsupported type = %d %s, want 400", rec.Code, rec.Body.String())
	}
	req = auth.WithUser(httptest.NewRequest("POST", "/libraries", body("  ")), admin)
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code == http.StatusBadRequest {
		var n int
		e.db.QueryRow(`SELECT COUNT(*) FROM libraries WHERE type = '  '`).Scan(&n)
		if n != 0 {
			t.Fatal("whitespace type must not be insertable")
		}
	}
}
