package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

type linkingEnv struct {
	db         *store.DB
	base       string
	adminToken string
	userToken  string
	libID      int64
}

func newLinkingEnv(t *testing.T) *linkingEnv {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	libID, err := db.AddLibrary("L", "audiobooks", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	adminID, err := db.CreateUser("admin", auth.Hash("password1"), true)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := db.CreateUser("user", auth.Hash("password1"), false)
	if err != nil {
		t.Fatal(err)
	}
	adminToken, err := auth.IssueToken(db, adminID, "t")
	if err != nil {
		t.Fatal(err)
	}
	userToken, err := auth.IssueToken(db, userID, "t")
	if err != nil {
		t.Fatal(err)
	}
	a := New(db, t.TempDir())
	app := neutron.New()
	g := app.Router().Group("/api/core", auth.Middleware(db))
	a.Mount(g)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return &linkingEnv{db: db, base: srv.URL + "/api/core", adminToken: adminToken, userToken: userToken, libID: libID}
}

func (e *linkingEnv) do(t *testing.T, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.base+path, rd)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func linkSeedWork(t *testing.T, db *store.DB, libID int64, title string) int64 {
	t.Helper()
	w := &store.Work{LibraryID: libID, Title: title}
	id, err := db.UpsertWork(w)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func linkSeedEdition(t *testing.T, db *store.DB, workID int64, title string) int64 {
	t.Helper()
	e := &store.Edition{WorkID: workID, Format: "m4b", Title: title}
	id, err := db.UpsertEdition(e)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestEditionMoveEndpoints(t *testing.T) {
	env := newLinkingEnv(t)
	w1 := linkSeedWork(t, env.db, env.libID, "Alpha")
	w2 := linkSeedWork(t, env.db, env.libID, "Beta")
	e1 := linkSeedEdition(t, env.db, w1, "Alpha")
	e2 := linkSeedEdition(t, env.db, w1, "Alpha mp3")

	code, _ := env.do(t, "POST", "/editions/999/move", env.userToken, `{"workId":`+itoa(w2)+`}`)
	if code != 403 {
		t.Fatalf("non-admin move = %d, want 403", code)
	}
	code, _ = env.do(t, "POST", "/editions/999/move", env.adminToken, `{"workId":`+itoa(w2)+`}`)
	if code != 404 {
		t.Fatalf("missing edition = %d, want 404", code)
	}
	code, _ = env.do(t, "POST", "/editions/"+itoa(e1)+"/move", env.adminToken, `{"workId":999}`)
	if code != 404 {
		t.Fatalf("missing work = %d, want 404", code)
	}
	code, _ = env.do(t, "POST", "/editions/"+itoa(e1)+"/move", env.adminToken, `{}`)
	if code != 400 {
		t.Fatalf("empty move body = %d, want 400", code)
	}

	code, body := env.do(t, "POST", "/editions/"+itoa(e1)+"/move", env.adminToken, `{"workId":`+itoa(w2)+`}`)
	if code != 200 || body["workId"] != float64(w2) || body["created"] != false || body["sourceDeleted"] != false {
		t.Fatalf("move to existing = %d %v", code, body)
	}

	// e2 is now w1's last edition: moving it to a new work must delete w1.
	code, body = env.do(t, "POST", "/editions/"+itoa(e2)+"/move", env.adminToken, `{"newTitle":"Gamma","newAuthor":"C"}`)
	if code != 200 || body["created"] != true || body["sourceDeleted"] != true || body["sourceWorkId"] != float64(w1) {
		t.Fatalf("move to new = %d %v", code, body)
	}
	gamma := int64(body["workId"].(float64))
	var author string
	if err := env.db.QueryRow(`SELECT coalesce(author,'') FROM works WHERE id = ?`, gamma).Scan(&author); err != nil || author != "C" {
		t.Fatalf("new work author = %q %v", author, err)
	}
	var n int
	env.db.QueryRow(`SELECT count(*) FROM works WHERE id = ?`, w1).Scan(&n)
	if n != 0 {
		t.Fatalf("emptied work not deleted")
	}
}

func TestEditionSplitEndpoint(t *testing.T) {
	env := newLinkingEnv(t)
	w1 := linkSeedWork(t, env.db, env.libID, "Collected")
	e1 := linkSeedEdition(t, env.db, w1, "Collected")
	e2 := linkSeedEdition(t, env.db, w1, "Side Story")

	code, body := env.do(t, "POST", "/editions/"+itoa(e2)+"/split", env.adminToken, `{"title":"Side Story"}`)
	if code != 200 || body["created"] != true || body["sourceDeleted"] != false {
		t.Fatalf("split = %d %v", code, body)
	}
	newWork := int64(body["workId"].(float64))
	var cnt int
	env.db.QueryRow(`SELECT count(*) FROM editions WHERE work_id = ?`, newWork).Scan(&cnt)
	if cnt != 1 {
		t.Fatalf("split edition count = %d", cnt)
	}

	// Splitting without a title derives "Collected" — which matches the
	// source work itself, so the move is a no-op. An explicit distinct title
	// creates the fresh work and empties (deletes) the source.
	code, body = env.do(t, "POST", "/editions/"+itoa(e1)+"/split", env.adminToken, `{"title":"Collected Solo"}`)
	if code != 200 || body["sourceDeleted"] != true {
		t.Fatalf("split last = %d %v", code, body)
	}
	env.db.QueryRow(`SELECT count(*) FROM works WHERE id = ?`, w1).Scan(&cnt)
	if cnt != 0 {
		t.Fatalf("split last left empty work behind")
	}
}

func TestWorkMergeEndpointGuards(t *testing.T) {
	env := newLinkingEnv(t)
	w1 := linkSeedWork(t, env.db, env.libID, "Alpha")
	w2 := linkSeedWork(t, env.db, env.libID, "Beta")
	linkSeedEdition(t, env.db, w1, "Alpha")

	code, _ := env.do(t, "POST", "/works/"+itoa(w1)+"/merge", env.userToken, `{"intoWorkId":`+itoa(w2)+`}`)
	if code != 403 {
		t.Fatalf("non-admin merge = %d, want 403", code)
	}
	code, _ = env.do(t, "POST", "/works/"+itoa(w1)+"/merge", env.adminToken, `{"intoWorkId":`+itoa(w1)+`}`)
	if code != 400 {
		t.Fatalf("self-merge = %d, want 400", code)
	}
	code, _ = env.do(t, "POST", "/works/"+itoa(w1)+"/merge", env.adminToken, `{}`)
	if code != 400 {
		t.Fatalf("missing intoWorkId = %d, want 400", code)
	}
	code, body := env.do(t, "POST", "/works/"+itoa(w1)+"/merge", env.adminToken, `{"intoWorkId":`+itoa(w2)+`}`)
	if code != 200 || body["ok"] != true {
		t.Fatalf("merge = %d %v", code, body)
	}
	var n int
	env.db.QueryRow(`SELECT count(*) FROM works WHERE id = ?`, w1).Scan(&n)
	if n != 0 {
		t.Fatalf("merged source not deleted")
	}
}
