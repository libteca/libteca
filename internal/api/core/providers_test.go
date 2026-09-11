package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/meta"
	"github.com/libteca/libteca/internal/store"
)

var testAdminUID int64

type fakeProvider struct {
	name       string
	kind       string
	searchRes  []meta.Result
	searchErr  error
	searchHits int
	fetchRes   *meta.Result
	fetchErr   error
	fetchHits  int
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) Search(ctx context.Context, q meta.Query) ([]meta.Result, error) {
	if q.Kind != "" && q.Kind != p.kind {
		return nil, nil
	}
	p.searchHits++
	return p.searchRes, p.searchErr
}

func (p *fakeProvider) Fetch(ctx context.Context, id string) (*meta.Result, error) {
	p.fetchHits++
	return p.fetchRes, p.fetchErr
}

type queryCapture struct {
	fp  *fakeProvider
	out *meta.Query
}

func (q queryCapture) Name() string { return q.fp.Name() }

func (q queryCapture) Search(ctx context.Context, query meta.Query) ([]meta.Result, error) {
	*q.out = query
	return q.fp.Search(ctx, query)
}

func (q queryCapture) Fetch(ctx context.Context, id string) (*meta.Result, error) {
	return q.fp.Fetch(ctx, id)
}

func newMatchingAPI(t *testing.T, libType string, providers ...meta.Provider) (*API, *store.DB, int64) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	libID, err := db.AddLibrary("L", libType, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(db, t.TempDir())
	if len(providers) > 0 {
		set := providers
		a.SetMetaProviders(func() []meta.Provider { return set })
	}
	t.Cleanup(func() { a.SetMetaProviders(nil) })
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('admin','x',1,0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := res.LastInsertId()
	testAdminUID = uid
	return a, db, libID
}

func addMatchWork(t *testing.T, db *store.DB, libID int64, title, author, chapters string) int64 {
	t.Helper()
	w := &store.Work{LibraryID: libID, Title: title}
	if author != "" {
		w.Author = &author
	}
	workID, err := db.UpsertWork(w)
	if err != nil {
		t.Fatal(err)
	}
	e := &store.Edition{WorkID: workID, Format: "m4b", Title: title}
	edID, err := db.UpsertEdition(e)
	if err != nil {
		t.Fatal(err)
	}
	f := &store.FileRec{EditionID: edID, Path: title + ".m4b", Seq: 1, SizeBytes: 1, MtimeSecs: 1, DurationSecs: 500, Chapters: chapters}
	if err := db.UpsertFile(f); err != nil {
		t.Fatal(err)
	}
	return workID
}

func callHandler(t *testing.T, method, path, id, body string, h func(w http.ResponseWriter, r *http.Request)) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if id != "" {
		req.SetPathValue("id", id)
	}
	req = auth.WithUser(req, testAdminUID)
	rec := httptest.NewRecorder()
	h(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad response body %q: %v", rec.Body.String(), err)
	}
	return rec.Code, out
}

func waitRefresh(t *testing.T, a *API, libID int64) map[string]any {
	t.Helper()
	id := strconv.FormatInt(libID, 10)
	code, body := callHandler(t, "POST", "/libraries/1/refresh-meta", id, ``, a.refreshMeta)
	if code != 202 && code != 409 {
		t.Fatalf("refresh POST code = %d body = %v", code, body)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, snap := callHandler(t, "GET", "/libraries/1/refresh-meta", id, ``, a.refreshMetaStatus)
		if st, _ := snap["status"].(string); st != "running" {
			return snap
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("refresh-meta did not finish")
	return nil
}

func workCols(t *testing.T, db *store.DB, workID int64) (provider, description string, hasCover bool) {
	t.Helper()
	var p, desc *string
	var cover *string
	if err := db.QueryRow(`SELECT provider, description, cover_path FROM works WHERE id = ?`, workID).Scan(&p, &desc, &cover); err != nil {
		t.Fatal(err)
	}
	str := func(s *string) string {
		if s == nil {
			return ""
		}
		return *s
	}
	return str(p), str(desc), str(cover) != ""
}

func fileChaptersByPath(t *testing.T, db *store.DB, path string) string {
	t.Helper()
	var chapters string
	if err := db.QueryRow(`SELECT chapters FROM files WHERE path = ?`, path).Scan(&chapters); err != nil {
		t.Fatal(err)
	}
	return chapters
}

type multiFile struct {
	name     string
	duration float64
	chapters string
}

func addMultiFileWork(t *testing.T, db *store.DB, libID int64, format, title string, files []multiFile) int64 {
	t.Helper()
	w := &store.Work{LibraryID: libID, Title: title}
	workID, err := db.UpsertWork(w)
	if err != nil {
		t.Fatal(err)
	}
	e := &store.Edition{WorkID: workID, Format: format, Title: title}
	edID, err := db.UpsertEdition(e)
	if err != nil {
		t.Fatal(err)
	}
	for i, f := range files {
		fr := &store.FileRec{EditionID: edID, Path: f.name, Seq: i + 1, SizeBytes: 1, MtimeSecs: 1, DurationSecs: f.duration, Chapters: f.chapters}
		if err := db.UpsertFile(fr); err != nil {
			t.Fatal(err)
		}
	}
	return workID
}

func newImageServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("JPEGDATA"))
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/cover.jpg"
}

func TestMatchWorkCandidates(t *testing.T) {
	fp := &fakeProvider{name: "audible", kind: "audiobook", searchRes: []meta.Result{
		{Provider: "audible", ID: "B1", Title: "Project Hail Mary", Author: "Andy Weir", Description: "desc"},
		{Provider: "audible", ID: "B2", Title: "Hail Mary Project"},
	}}
	a, db, libID := newMatchingAPI(t, "audiobooks", fp)
	workID := addMatchWork(t, db, libID, "Project Hail Mary", "Andy Weir", "[]")

	code, body := callHandler(t, "POST", "/works/1/match", strconv.FormatInt(workID, 10), `{}`, a.matchWork)
	if code != 200 {
		t.Fatalf("code = %d body = %v", code, body)
	}
	if fp.searchHits != 1 {
		t.Fatalf("searchHits = %d", fp.searchHits)
	}
	cands, _ := body["candidates"].([]any)
	if len(cands) != 2 {
		t.Fatalf("candidates = %v", cands)
	}
	first := cands[0].(map[string]any)
	if first["provider"] != "audible" || first["id"] != "B1" || first["title"] != "Project Hail Mary" {
		t.Fatalf("first = %v", first)
	}
}

func TestMatchWorkManualOverride(t *testing.T) {
	fp := &fakeProvider{name: "audible", kind: "audiobook"}
	var lastQuery meta.Query
	a, db, libID := newMatchingAPI(t, "audiobooks", queryCapture{fp: fp, out: &lastQuery})
	workID := addMatchWork(t, db, libID, "Scrambled Title", "", "[]")

	callHandler(t, "POST", "/works/1/match", strconv.FormatInt(workID, 10), `{"title":"Override Title","author":"Override Author"}`, a.matchWork)
	if lastQuery.Title != "Override Title" || lastQuery.Author != "Override Author" {
		t.Fatalf("query = %+v, want overrides applied", lastQuery)
	}
	if lastQuery.Kind != "audiobook" {
		t.Fatalf("kind = %q", lastQuery.Kind)
	}
}

func TestMatchWorkMissing(t *testing.T) {
	a, _, _ := newMatchingAPI(t, "audiobooks")
	code, body := callHandler(t, "POST", "/works/999/match", "999", `{}`, a.matchWork)
	if code != 404 {
		t.Fatalf("code = %d body = %v", code, body)
	}
}

func TestRefreshMetaAutoApply(t *testing.T) {
	coverURL := newImageServer(t)
	fp := &fakeProvider{
		name: "audible",
		kind: "audiobook",
		searchRes: []meta.Result{
			{Provider: "audible", ID: "B1", Title: "Project Hail Mary"},
		},
		fetchRes: &meta.Result{
			Provider: "audible", ID: "B1", Title: "Project Hail Mary",
			Description: "Ryland Grace is the sole survivor.",
			CoverURL:    coverURL,
			Chapters: []meta.Chapter{
				{Title: "Chapter 1", StartSec: 0, EndSec: 60},
				{Title: "Chapter 2", StartSec: 60, EndSec: 150},
			},
		},
	}
	a, db, libID := newMatchingAPI(t, "audiobooks", fp)
	workID := addMatchWork(t, db, libID, "Project Hail Mary", "Andy Weir", "[]")

	body := waitRefresh(t, a, libID)
	if body["matched"] != float64(1) || body["autoApplied"] != float64(1) {
		t.Fatalf("body = %v", body)
	}
	provider, desc, hasCover := workCols(t, db, workID)
	if provider != "audible" || desc != "Ryland Grace is the sole survivor." {
		t.Fatalf("work = %q %q", provider, desc)
	}
	if !hasCover {
		t.Fatal("cover_path not set")
	}
	coverFile := filepath.Join(a.DataDir, "covers", strconv.FormatInt(workID, 10)+".jpg")
	data, err := os.ReadFile(coverFile)
	if err != nil || string(data) != "JPEGDATA" {
		t.Fatalf("cover file = %v %q", err, data)
	}
	chapters := fileChaptersByPath(t, db, "Project Hail Mary.m4b")
	if !strings.Contains(chapters, `"title":"Chapter 1"`) || !strings.Contains(chapters, `"end":150`) {
		t.Fatalf("chapters = %q", chapters)
	}
}

func TestRefreshMetaAmbiguousGoesToInbox(t *testing.T) {
	fp := &fakeProvider{name: "audible", kind: "audiobook", searchRes: []meta.Result{
		{Provider: "audible", ID: "B1", Title: "Project Hail Mary"},
		{Provider: "audible", ID: "B2", Title: "Project Hail Mary: Bonus"},
	}}
	a, db, libID := newMatchingAPI(t, "audiobooks", fp)
	workID := addMatchWork(t, db, libID, "Project Hail Mary", "Andy Weir", "[]")

	body := waitRefresh(t, a, libID)
	if body["autoApplied"] != float64(0) || body["matched"] != float64(1) {
		t.Fatalf("body = %v", body)
	}
	provider, _, _ := workCols(t, db, workID)
	if provider != "" {
		t.Fatalf("provider = %q, want untouched", provider)
	}
	inbox, err := db.MatchingInbox(libID)
	if err != nil || len(inbox) != 1 || inbox[0].ID != workID {
		t.Fatalf("inbox = %+v err = %v", inbox, err)
	}
}

func TestRefreshMetaLowSimilarityStays(t *testing.T) {
	fp := &fakeProvider{name: "audible", kind: "audiobook", searchRes: []meta.Result{
		{Provider: "audible", ID: "B1", Title: "A Completely Different Book"},
	}}
	a, db, libID := newMatchingAPI(t, "audiobooks", fp)
	addMatchWork(t, db, libID, "Project Hail Mary", "Andy Weir", "[]")

	body := waitRefresh(t, a, libID)
	if body["autoApplied"] != float64(0) {
		t.Fatalf("body = %v", body)
	}
}

func TestRefreshMetaProviderErrorBlocksAutoApply(t *testing.T) {
	okay := &fakeProvider{name: "audible", kind: "audiobook", searchRes: []meta.Result{
		{Provider: "audible", ID: "B1", Title: "Project Hail Mary"},
	}}
	broken := &fakeProvider{name: "openlibrary", kind: "audiobook", searchErr: context.DeadlineExceeded}
	a, db, libID := newMatchingAPI(t, "audiobooks", okay, broken)
	workID := addMatchWork(t, db, libID, "Project Hail Mary", "Andy Weir", "[]")

	body := waitRefresh(t, a, libID)
	if body["autoApplied"] != float64(0) {
		t.Fatalf("body = %v — a failed sweep must not auto-apply", body)
	}
	provider, _, _ := workCols(t, db, workID)
	if provider != "" {
		t.Fatalf("provider = %q, want untouched", provider)
	}
}

func TestRefreshMetaSkipsMarkedWorks(t *testing.T) {
	fp := &fakeProvider{name: "audible", kind: "audiobook"}
	a, db, libID := newMatchingAPI(t, "audiobooks", fp)
	workID := addMatchWork(t, db, libID, "Project Hail Mary", "Andy Weir", "[]")
	if err := db.PutCached("match-skip", strconv.FormatInt(workID, 10), "{}"); err != nil {
		t.Fatal(err)
	}

	body := waitRefresh(t, a, libID)
	if body["matched"] != float64(0) || body["autoApplied"] != float64(0) {
		t.Fatalf("body = %v", body)
	}
	if fp.searchHits != 0 {
		t.Fatalf("searchHits = %d, skipped work must not be queried", fp.searchHits)
	}
}

func TestApplyWritesAndNeverClobbersChapters(t *testing.T) {
	fp := &fakeProvider{
		name: "audible",
		kind: "audiobook",
		fetchRes: &meta.Result{
			Provider: "audible", ID: "B1", Title: "Project Hail Mary",
			Description: "applied desc",
			Chapters:    []meta.Chapter{{Title: "Chapter 1", StartSec: 0, EndSec: 60}},
		},
	}
	a, db, libID := newMatchingAPI(t, "audiobooks", fp)
	workID := addMatchWork(t, db, libID, "Project Hail Mary", "Andy Weir", "[]")

	// Second edition whose file already has ffprobe chapters.
	e := &store.Edition{WorkID: workID, Format: "m4b", Title: "Project Hail Mary (ffprobe)"}
	edID, err := db.UpsertEdition(e)
	if err != nil {
		t.Fatal(err)
	}
	f := &store.FileRec{EditionID: edID, Path: "probed.m4b", Seq: 1, SizeBytes: 1, MtimeSecs: 1, DurationSecs: 500, Chapters: `[{"id":1,"start":0,"end":500,"title":"ffprobe"}]`}
	if err := db.UpsertFile(f); err != nil {
		t.Fatal(err)
	}

	code, body := callHandler(t, "POST", "/works/1/apply", strconv.FormatInt(workID, 10),
		`{"provider":"audible","id":"B1"}`, a.applyMatch)
	if code != 200 {
		t.Fatalf("code = %d body = %v", code, body)
	}
	apply, _ := body["apply"].(map[string]any)
	if apply["chapters"] != float64(1) {
		t.Fatalf("apply summary = %v", apply)
	}
	provider, desc, _ := workCols(t, db, workID)
	if provider != "audible" || desc != "applied desc" {
		t.Fatalf("work = %q %q", provider, desc)
	}
	filled := fileChaptersByPath(t, db, "Project Hail Mary.m4b")
	if !strings.Contains(filled, "Chapter 1") {
		t.Fatalf("empty chapters not filled: %q", filled)
	}
	probed := fileChaptersByPath(t, db, "probed.m4b")
	if !strings.Contains(probed, "ffprobe") || strings.Contains(probed, "Chapter 1") {
		t.Fatalf("ffprobe chapters clobbered: %q", probed)
	}
}

func TestApplyMovieGenresNoChapters(t *testing.T) {
	fp := &fakeProvider{
		name: "tmdb",
		kind: "movie",
		fetchRes: &meta.Result{
			Provider: "tmdb", ID: "movie:1", Title: "Dune",
			Description: "desc", Genres: []string{"Science Fiction"},
			Chapters: []meta.Chapter{{Title: "should not apply", StartSec: 0, EndSec: 1}},
		},
	}
	a, db, libID := newMatchingAPI(t, "movies", fp)
	workID := addMatchWork(t, db, libID, "Dune", "", `[{"id":1,"start":0,"end":1,"title":"ffmpeg"}]`)

	_, body := callHandler(t, "POST", "/works/1/apply", strconv.FormatInt(workID, 10),
		`{"provider":"tmdb","id":"movie:1"}`, a.applyMatch)
	apply, _ := body["apply"].(map[string]any)
	if apply["genres"] != float64(1) || apply["chapters"] != float64(0) {
		t.Fatalf("apply summary = %v", apply)
	}
	if g := db.WorkGenres(workID); len(g) != 1 || g[0] != "Science Fiction" {
		t.Fatalf("genres = %v", g)
	}
	if got := fileChaptersByPath(t, db, "Dune.m4b"); got != `[{"id":1,"start":0,"end":1,"title":"ffmpeg"}]` {
		t.Fatalf("movie chapters touched: %q", got)
	}
}

func TestApplyUnknownProvider(t *testing.T) {
	a, db, libID := newMatchingAPI(t, "audiobooks")
	workID := addMatchWork(t, db, libID, "W", "", "[]")
	code, body := callHandler(t, "POST", "/works/1/apply", strconv.FormatInt(workID, 10),
		`{"provider":"nope","id":"x"}`, a.applyMatch)
	if code != 502 {
		t.Fatalf("code = %d body = %v", code, body)
	}
}

func TestSkipWorkRemovesFromInbox(t *testing.T) {
	a, db, libID := newMatchingAPI(t, "audiobooks")
	workID := addMatchWork(t, db, libID, "W", "", "[]")

	inbox, err := db.MatchingInbox(libID)
	if err != nil || len(inbox) != 1 {
		t.Fatalf("inbox = %+v err = %v", inbox, err)
	}
	code, body := callHandler(t, "POST", "/works/1/skip", strconv.FormatInt(workID, 10), ``, a.skipWork)
	if code != 200 || body["ok"] != true {
		t.Fatalf("code = %d body = %v", code, body)
	}
	inbox, err = db.MatchingInbox(libID)
	if err != nil || len(inbox) != 0 {
		t.Fatalf("inbox after skip = %+v err = %v", inbox, err)
	}
}

func TestMatchingInboxEndpoint(t *testing.T) {
	a, db, libID := newMatchingAPI(t, "audiobooks")
	workID := addMatchWork(t, db, libID, "W", "Author", "[]")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/matching/inbox", nil)
	a.matchingInbox(rec, req)
	if rec.Code != 200 {
		t.Fatalf("code = %d", rec.Code)
	}
	var rows []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("inbox body %q: %v", rec.Body.String(), err)
	}
	if len(rows) != 1 || rows[0]["id"] != float64(workID) || rows[0]["libraryType"] != "audiobooks" || rows[0]["title"] != "W" {
		t.Fatalf("rows = %v", rows)
	}
}

func TestDistributeChapterMath(t *testing.T) {
	durations := []float64{100, 200, 300}
	chapters := []meta.Chapter{
		{Title: "C1", StartSec: 0, EndSec: 150},
		{Title: "C2", StartSec: 150, EndSec: 350},
		{Title: "C3", StartSec: 350, EndSec: 600},
		{Title: "C4", StartSec: 600, EndSec: 620},
	}
	got := distributeChapterMath(durations, chapters)
	want := [][]fileChapter{
		{{ID: 1, Start: 0, End: 100, Title: "C1"}},
		{{ID: 2, Start: 50, End: 200, Title: "C2"}},
		{{ID: 3, Start: 50, End: 300, Title: "C3"}},
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("file %d chapters = %+v, want %+v", i, got[i], want[i])
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Errorf("file %d chapter %d = %+v, want %+v", i, j, got[i][j], want[i][j])
			}
		}
	}
}

func TestApplyDistributesMultiFileChapters(t *testing.T) {
	fp := &fakeProvider{
		name: "audible",
		kind: "audiobook",
		fetchRes: &meta.Result{
			Provider: "audible", ID: "B1", Title: "Project Hail Mary",
			Description: "desc",
			Chapters: []meta.Chapter{
				{Title: "Chapter 1", StartSec: 0, EndSec: 150},
				{Title: "Chapter 2", StartSec: 150, EndSec: 350},
				{Title: "Chapter 3", StartSec: 350, EndSec: 600},
			},
		},
	}
	a, db, libID := newMatchingAPI(t, "audiobooks", fp)
	workID := addMultiFileWork(t, db, libID, "mp3", "Project Hail Mary", []multiFile{
		{name: "01.mp3", duration: 100, chapters: "[]"},
		{name: "02.mp3", duration: 200, chapters: `[{"id":1,"start":0,"end":200,"title":"02"}]`},
		{name: "03.mp3", duration: 300, chapters: `[{"id":1,"start":0,"end":300,"title":"ffprobe"}]`},
	})

	code, body := callHandler(t, "POST", "/works/1/apply", strconv.FormatInt(workID, 10),
		`{"provider":"audible","id":"B1"}`, a.applyMatch)
	if code != 200 {
		t.Fatalf("code = %d body = %v", code, body)
	}
	apply, _ := body["apply"].(map[string]any)
	if apply["chapters"] != float64(2) {
		t.Fatalf("apply summary = %v, want 2 files written", apply)
	}
	if got, want := fileChaptersByPath(t, db, "01.mp3"), `[{"id":1,"start":0,"end":100,"title":"Chapter 1"}]`; got != want {
		t.Fatalf("01.mp3 chapters = %q, want %q", got, want)
	}
	if got, want := fileChaptersByPath(t, db, "02.mp3"), `[{"id":2,"start":50,"end":200,"title":"Chapter 2"}]`; got != want {
		t.Fatalf("02.mp3 chapters = %q, want %q (filename-titled replaced)", got, want)
	}
	if got := fileChaptersByPath(t, db, "03.mp3"); !strings.Contains(got, "ffprobe") || strings.Contains(got, "Chapter 3") {
		t.Fatalf("03.mp3 chapters clobbered: %q", got)
	}
}

func TestApplyDistributesMultipleChaptersToOneFile(t *testing.T) {
	fp := &fakeProvider{
		name: "audible",
		kind: "audiobook",
		fetchRes: &meta.Result{
			Provider: "audible", ID: "B1", Title: "W",
			Chapters: []meta.Chapter{
				{Title: "C1", StartSec: 0, EndSec: 40},
				{Title: "C2", StartSec: 40, EndSec: 150},
			},
		},
	}
	a, db, libID := newMatchingAPI(t, "audiobooks", fp)
	workID := addMultiFileWork(t, db, libID, "mp3", "W", []multiFile{
		{name: "a.mp3", duration: 100, chapters: "[]"},
		{name: "b.mp3", duration: 50, chapters: "[]"},
	})

	_, body := callHandler(t, "POST", "/works/1/apply", strconv.FormatInt(workID, 10),
		`{"provider":"audible","id":"B1"}`, a.applyMatch)
	apply, _ := body["apply"].(map[string]any)
	if apply["chapters"] != float64(1) {
		t.Fatalf("apply summary = %v, want 1 (only the file holding chapter starts)", apply)
	}
	if got, want := fileChaptersByPath(t, db, "a.mp3"), `[{"id":1,"start":0,"end":40,"title":"C1"},{"id":2,"start":40,"end":100,"title":"C2"}]`; got != want {
		t.Fatalf("a.mp3 chapters = %q, want %q (end clamped to file duration)", got, want)
	}
	if got := fileChaptersByPath(t, db, "b.mp3"); got != "[]" {
		t.Fatalf("b.mp3 chapters = %q, want untouched []", got)
	}
}

func TestApplyDistributesSkipsTitledFiles(t *testing.T) {
	fp := &fakeProvider{
		name: "audible",
		kind: "audiobook",
		fetchRes: &meta.Result{
			Provider: "audible", ID: "B1", Title: "W",
			Chapters: []meta.Chapter{{Title: "Only", StartSec: 0, EndSec: 150}},
		},
	}
	a, db, libID := newMatchingAPI(t, "audiobooks", fp)
	workID := addMultiFileWork(t, db, libID, "mp3", "W", []multiFile{
		{name: "a.mp3", duration: 100, chapters: `[{"id":1,"start":0,"end":100,"title":"real"}]`},
		{name: "b.mp3", duration: 50, chapters: `[{"id":1,"start":0,"end":50,"title":"real too"}]`},
	})

	_, body := callHandler(t, "POST", "/works/1/apply", strconv.FormatInt(workID, 10),
		`{"provider":"audible","id":"B1"}`, a.applyMatch)
	apply, _ := body["apply"].(map[string]any)
	if apply["chapters"] != float64(0) {
		t.Fatalf("apply summary = %v, want 0 (titled files skipped)", apply)
	}
	if got := fileChaptersByPath(t, db, "a.mp3"); !strings.Contains(got, "real") {
		t.Fatalf("titled file clobbered: %q", got)
	}
	if got := fileChaptersByPath(t, db, "b.mp3"); !strings.Contains(got, "real too") {
		t.Fatalf("titled file clobbered: %q", got)
	}
}

func TestGenericChapters(t *testing.T) {
	cases := []struct {
		path, stored string
		want         bool
	}{
		{"x.mp3", "[]", true},
		{"x.mp3", "", true},
		{"x.mp3", "not json", true},
		{"x.mp3", `[{"id":1,"start":0,"end":10,"title":"x"}]`, true},
		{"x.mp3", `[{"id":1,"start":0,"end":10,"title":"ffprobe"}]`, false},
		{"x.mp3", `[{"id":1,"start":0,"end":10,"title":"x"},{"id":2,"start":10,"end":20,"title":"other"}]`, false},
		{"sub dir/y.m4a", `[{"id":1,"start":0,"end":10,"title":"y"}]`, true},
	}
	for _, tc := range cases {
		if got := genericChapters(tc.path, tc.stored); got != tc.want {
			t.Errorf("genericChapters(%q, %q) = %v, want %v", tc.path, tc.stored, got, tc.want)
		}
	}
}

func TestTitleSimilarity(t *testing.T) {
	cases := []struct {
		a, b string
		want bool // >= 0.85
	}{
		{"Dune", "dune", true},
		{"Dune", "Dune (2021)", false},
		{"Project Hail Mary", "project hail mary", true},
		{"Project Hail Mary", "Project Hail Mary: A Novel", false},
		{"The Martian", "The Martian", true},
		{"The Martian", "Project Hail Mary", false},
		{"Dune", "", false},
	}
	for _, tc := range cases {
		got := titleSimilarity(tc.a, tc.b)
		if got >= 0.85 != tc.want {
			t.Errorf("similarity(%q, %q) = %.3f, want >=0.85: %v", tc.a, tc.b, got, tc.want)
		}
	}
	if s := titleSimilarity("Dune: Part Two", "Dune Part Two"); s < 0.85 {
		t.Errorf("punctuation-only difference = %.3f, want >= 0.85", s)
	}
}

type fakeSeasonProvider struct {
	*fakeProvider
	seasons map[int][]meta.EpisodeInfo
}

func (p *fakeSeasonProvider) FetchSeasonEpisodes(ctx context.Context, tvID string, season int) ([]meta.EpisodeInfo, error) {
	return p.seasons[season], nil
}

func addTVEpisodes(t *testing.T, db *store.DB, libID int64, title string, eps [][3]any) int64 {
	t.Helper()
	w := &store.Work{LibraryID: libID, Title: title}
	workID, err := db.UpsertWork(w)
	if err != nil {
		t.Fatal(err)
	}
	for _, ep := range eps {
		s, e := ep[0].(int), ep[1].(int)
		ed := &store.Edition{WorkID: workID, Format: "video", Title: ep[2].(string), SeasonNum: &s, EpisodeNum: &e}
		if _, err := db.UpsertEdition(ed); err != nil {
			t.Fatal(err)
		}
	}
	return workID
}

func TestApplyMatchFillsEpisodeTitles(t *testing.T) {
	fp := &fakeProvider{
		name: "tmdb",
		kind: "tv",
		fetchRes: &meta.Result{
			Provider: "tmdb", ID: "tv:9", Title: "Show",
			Description: "series desc", Genres: []string{"Drama"},
		},
	}
	sp := &fakeSeasonProvider{fakeProvider: fp, seasons: map[int][]meta.EpisodeInfo{
		1: {
			{Season: 1, Episode: 1, Title: "Pilot", Description: "The start"},
			{Season: 1, Episode: 2, Title: "Second Hour"},
		},
	}}
	a, db, libID := newMatchingAPI(t, "tv", sp)
	workID := addTVEpisodes(t, db, libID, "Show", [][3]any{{1, 1, "S01E01"}, {1, 2, "A Real Title"}})

	code, body := callHandler(t, "POST", "/works/1/apply", strconv.FormatInt(workID, 10),
		`{"provider":"tmdb","id":"tv:9"}`, a.applyMatch)
	if code != 200 {
		t.Fatalf("code = %d body = %v", code, body)
	}
	apply, _ := body["apply"].(map[string]any)
	if apply["episodes"] != float64(1) {
		t.Fatalf("apply summary = %v", apply)
	}
	eds, err := db.WorkEpisodes(workID)
	if err != nil || len(eds) != 2 {
		t.Fatalf("episodes = %v %v", eds, err)
	}
	if eds[0].Title != "Pilot" {
		t.Fatalf("S01E01 not filled: %q", eds[0].Title)
	}
	if eds[1].Title != "A Real Title" {
		t.Fatalf("good title clobbered: %q", eds[1].Title)
	}
}

func TestApplyEpisodesEndpoint(t *testing.T) {
	fp := &fakeProvider{name: "tmdb", kind: "tv", fetchRes: &meta.Result{Provider: "tmdb", ID: "tv:9"}}
	sp := &fakeSeasonProvider{fakeProvider: fp, seasons: map[int][]meta.EpisodeInfo{
		1: {{Season: 1, Episode: 1, Title: "Pilot"}},
	}}
	a, db, libID := newMatchingAPI(t, "tv", sp)
	workID := addTVEpisodes(t, db, libID, "Show", [][3]any{{1, 1, "S01E01"}})

	code, body := callHandler(t, "POST", "/works/1/apply-episodes", strconv.FormatInt(workID, 10),
		`{"provider":"tmdb","id":"tv:9"}`, a.applyEpisodes)
	if code != 200 {
		t.Fatalf("code = %d body = %v", code, body)
	}
	if body["updated"] != float64(1) {
		t.Fatalf("body = %v", body)
	}
}
