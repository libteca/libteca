package opds

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-dev/neutron-go/neutron"
)

type testEnv struct {
	db      *store.DB
	h       http.Handler
	userID  int64
	dataDir string
}

const testUser = "tyler"
const testPass = "pw"

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Now().UnixMilli()
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES (?,?,1,?,?)`,
		testUser, auth.Hash(testPass), now, now)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	uid, _ := res.LastInsertId()
	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "covers"), 0o755); err != nil {
		t.Fatalf("covers dir: %v", err)
	}
	r := neutron.New().Router()
	New(db, dataDir).Mount(r)
	return &testEnv{db: db, h: r, userID: uid, dataDir: dataDir}
}

func (e *testEnv) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	req.SetBasicAuth(testUser, testPass)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) getNoAuth(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", path, nil)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) addLibrary(t *testing.T, typ string) int64 {
	t.Helper()
	res, err := e.db.Exec(`INSERT INTO libraries (name, type, path, created_at) VALUES (?,?,?,?)`,
		typ+" lib", typ, t.TempDir(), time.Now().UnixMilli())
	if err != nil {
		t.Fatalf("seed library: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (e *testEnv) addWork(t *testing.T, libID int64, title, author string, createdAt int64) int64 {
	t.Helper()
	var authorPtr any
	if author != "" {
		authorPtr = author
	}
	res, err := e.db.Exec(`INSERT INTO works (library_id, title, author, created_at, updated_at) VALUES (?,?,?,?,?)`,
		libID, title, authorPtr, createdAt, createdAt)
	if err != nil {
		t.Fatalf("seed work: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func (e *testEnv) addEdition(t *testing.T, workID int64, format, path string, pageCount *int, createdAt int64) int64 {
	t.Helper()
	res, err := e.db.Exec(`INSERT INTO editions (work_id, format, title, page_count, created_at) VALUES (?,?,?,?,?)`,
		workID, format, "edition", pageCount, createdAt)
	if err != nil {
		t.Fatalf("seed edition: %v", err)
	}
	edID, _ := res.LastInsertId()
	size := int64(16)
	if fi, err := os.Stat(path); err == nil {
		size = fi.Size()
	}
	_, err = e.db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, chapters, embedded_meta, missing, probed_at)
		VALUES (?,?,?,?,?,0,'[]','{}',0,?)`,
		edID, path, 1, size, time.Now().Unix(), createdAt)
	if err != nil {
		t.Fatalf("seed file: %v", err)
	}
	return edID
}

func writeBookFile(t *testing.T, data []byte, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write book file: %v", err)
	}
	return path
}

func writeCBZ(t *testing.T, order []string, contents map[string][]byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "comic.cbz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create cbz: %v", err)
	}
	zw := zip.NewWriter(f)
	for _, name := range order {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatalf("zip header: %v", err)
		}
		if _, err := w.Write(contents[name]); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	zw.Close()
	f.Close()
	return path
}

// namespace-URL-tagged decode structs: plain tags match any namespace, the
// opensearch tags demand the xmlns:opensearch prefix resolve to the real URI.
type xFeed struct {
	XMLName xml.Name `xml:"http://www.w3.org/2005/Atom feed"`
	ID      string   `xml:"id"`
	Title   string   `xml:"title"`
	Updated string   `xml:"updated"`
	Links   []xLink  `xml:"link"`
	Total   int      `xml:"http://a9.com/-/spec/opensearch/1.1/ totalResults"`
	PerPage int      `xml:"http://a9.com/-/spec/opensearch/1.1/ itemsPerPage"`
	Entries []xEntry `xml:"entry"`
}

type xLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type xEntry struct {
	ID      string  `xml:"id"`
	Title   string  `xml:"title"`
	Summary string  `xml:"summary"`
	Links   []xLink `xml:"link"`
	Metas   []xMeta `xml:"meta"`
}

type xMeta struct {
	Name    string `xml:"name,attr"`
	Content string `xml:"content,attr"`
}

type xOSD struct {
	XMLName xml.Name `xml:"http://a9.com/-/spec/opensearch/1.1/ OpenSearchDescription"`
	Short   string   `xml:"ShortName"`
	Urls    []struct {
		Type     string `xml:"type,attr"`
		Template string `xml:"template,attr"`
	} `xml:"Url"`
}

func decodeFeed(t *testing.T, rec *httptest.ResponseRecorder) xFeed {
	t.Helper()
	var f xFeed
	if err := xml.Unmarshal(rec.Body.Bytes(), &f); err != nil {
		t.Fatalf("decode feed: %v\n%s", err, rec.Body.String())
	}
	return f
}

func findEntry(t *testing.T, f xFeed, id string) xEntry {
	t.Helper()
	for _, en := range f.Entries {
		if en.ID == id {
			return en
		}
	}
	t.Fatalf("entry %q not found in feed", id)
	return xEntry{}
}

func findLink(links []xLink, rel string) (xLink, bool) {
	for _, l := range links {
		if l.Rel == rel {
			return l, true
		}
	}
	return xLink{}, false
}

func TestBasicAuth(t *testing.T) {
	e := newEnv(t)
	rec := e.getNoAuth(t, "/opds")
	if rec.Code != 401 {
		t.Fatalf("no auth: status %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got != `Basic realm="libteca"` {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
	req := httptest.NewRequest("GET", "/opds/all", nil)
	req.SetBasicAuth(testUser, "wrong")
	rec = httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Fatalf("wrong password: status %d", rec.Code)
	}
	rec = e.getNoAuth(t, "/opds/all")
	if rec.Code != 401 {
		t.Fatalf("subfeed no auth: status %d", rec.Code)
	}
}

func TestRootCatalog(t *testing.T) {
	e := newEnv(t)
	books := e.addLibrary(t, "books")
	e.addLibrary(t, "comics")
	tv := e.addLibrary(t, "tv")
	e.addWork(t, books, "Some Book", "", time.Now().UnixMilli())

	rec := e.get(t, "/opds")
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "kind=navigation") {
		t.Fatalf("content type = %q", ct)
	}
	body := rec.Body.String()
	for _, ns := range []string{
		`xmlns="http://www.w3.org/2005/Atom"`,
		`xmlns:opds="http://opds-spec.org/2010/catalog"`,
		`xmlns:opensearch="http://a9.com/-/spec/opensearch/1.1/"`,
	} {
		if !strings.Contains(body, ns) {
			t.Fatalf("missing namespace declaration %q", ns)
		}
	}
	f := decodeFeed(t, rec)
	if f.ID != "urn:libteca:opds:root" || f.Title != "libteca" || f.Updated == "" {
		t.Fatalf("feed header = %+v", f)
	}
	if _, ok := findLink(f.Links, "self"); !ok || f.Links[0].Href != "/opds" {
		t.Fatalf("self link missing: %+v", f.Links)
	}
	if l, ok := findLink(f.Links, "start"); !ok || l.Href != "/opds" {
		t.Fatalf("start link: %+v", f.Links)
	}
	if l, ok := findLink(f.Links, "search"); !ok || l.Href != "/opds/search-description.xml" || l.Type != "application/opensearchdescription+xml" {
		t.Fatalf("search link: %+v", f.Links)
	}
	ids := map[string]bool{}
	for _, en := range f.Entries {
		ids[en.ID] = true
		if l, ok := findLink(en.Links, relSubsection); !ok || !strings.HasPrefix(l.Href, "/opds/") {
			t.Fatalf("entry %q subsection link: %+v", en.ID, en.Links)
		}
	}
	for _, want := range []string{
		"l-" + strconv.FormatInt(books, 10),
		"all", "in-progress", "newest",
	} {
		if !ids[want] {
			t.Fatalf("root missing entry %q, got %v", want, ids)
		}
	}
	if ids["l-"+strconv.FormatInt(tv, 10)] {
		t.Fatal("tv library leaked into OPDS root")
	}
}

func TestAcquisitionFeedPaging(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "books")
	now := time.Now().UnixMilli()
	for i := 0; i < 60; i++ {
		w := e.addWork(t, lib, fmt.Sprintf("B%02d", i), "", now)
		path := writeBookFile(t, []byte("epub-bytes"), fmt.Sprintf("b%02d.epub", i))
		e.addEdition(t, w, "epub", path, nil, now)
	}

	rec := e.get(t, "/opds/libraries/"+strconv.FormatInt(lib, 10))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "kind=acquisition") {
		t.Fatalf("content type = %q", ct)
	}
	f := decodeFeed(t, rec)
	if f.Total != 60 || f.PerPage != 50 {
		t.Fatalf("total=%d perPage=%d", f.Total, f.PerPage)
	}
	if len(f.Entries) != 50 {
		t.Fatalf("page 0 entries = %d", len(f.Entries))
	}
	if f.Entries[0].ID == "" || f.Entries[0].Title != "B00" {
		t.Fatalf("first entry = %+v", f.Entries[0])
	}
	next, ok := findLink(f.Links, "next")
	if !ok || !strings.Contains(next.Href, "page=1") {
		t.Fatalf("next link: %+v", f.Links)
	}
	if _, ok := findLink(f.Links, "prev"); ok {
		t.Fatal("page 0 must not have prev link")
	}

	f = decodeFeed(t, e.get(t, "/opds/libraries/"+strconv.FormatInt(lib, 10)+"?page=1"))
	if len(f.Entries) != 10 || f.Total != 60 {
		t.Fatalf("page 1: %d entries, total %d", len(f.Entries), f.Total)
	}
	if f.Entries[0].Title != "B50" {
		t.Fatalf("page 1 first = %q", f.Entries[0].Title)
	}
	if _, ok := findLink(f.Links, "next"); ok {
		t.Fatal("last page must not have next link")
	}
	if _, ok := findLink(f.Links, "prev"); !ok {
		t.Fatal("page 1 missing prev link")
	}

	f = decodeFeed(t, e.get(t, "/opds/all"))
	if f.Total != 60 {
		t.Fatalf("all feed total = %d", f.Total)
	}
}

func TestAcquisitionEntryLinks(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "books")
	now := time.Now().UnixMilli()
	w := e.addWork(t, lib, "Dune", "Frank Herbert", now)
	pages := 3
	cbzPath := writeCBZ(t, []string{"p001.jpg"}, map[string][]byte{"p001.jpg": {0xFF, 0xD8, 0xFF}})
	epubID := e.addEdition(t, w, "epub", writeBookFile(t, []byte("epub-bytes"), "dune.epub"), nil, now)
	cbzID := e.addEdition(t, w, "cbz", cbzPath, &pages, now)
	pdfID := e.addEdition(t, w, "pdf", writeBookFile(t, []byte("%PDF-fake"), "dune.pdf"), nil, now)
	e.db.Exec(`UPDATE works SET cover_path = ? WHERE id = ?`, strconv.FormatInt(w, 10)+".jpg", w)

	f := decodeFeed(t, e.get(t, "/opds/libraries/"+strconv.FormatInt(lib, 10)))

	epub := findEntry(t, f, "e-"+strconv.FormatInt(epubID, 10))
	l, ok := findLink(epub.Links, relAcquisition)
	if !ok || l.Href != "/opds/download/"+strconv.FormatInt(epubID, 10) || l.Type != "application/epub+zip" {
		t.Fatalf("epub acquisition link: %+v", epub.Links)
	}
	il, ok := findLink(epub.Links, relImage)
	if !ok || il.Href != "/opds/cover/"+strconv.FormatInt(w, 10) {
		t.Fatalf("epub image link: %+v", epub.Links)
	}
	if _, ok := findLink(epub.Links, relThumbnail); !ok {
		t.Fatal("epub thumbnail link missing")
	}

	cbz := findEntry(t, f, "e-"+strconv.FormatInt(cbzID, 10))
	if l, _ := findLink(cbz.Links, relAcquisition); l.Type != "application/zip" {
		t.Fatalf("cbz acquisition type = %q", l.Type)
	}
	pse, ok := findLink(cbz.Links, relPSE)
	if !ok || pse.Href != "/opds/pse/"+strconv.FormatInt(cbzID, 10)+"/{pageNumber}" || pse.Type != "image/jpeg" {
		t.Fatalf("pse link: %+v", cbz.Links)
	}
	if len(cbz.Metas) != 1 || cbz.Metas[0].Name != "pse,count" || cbz.Metas[0].Content != "3" {
		t.Fatalf("pse,count meta: %+v", cbz.Metas)
	}
	if cbz.Summary != "CBZ edition, 3 pages" {
		t.Fatalf("cbz summary = %q", cbz.Summary)
	}

	pdf := findEntry(t, f, "e-"+strconv.FormatInt(pdfID, 10))
	if l, _ := findLink(pdf.Links, relAcquisition); l.Type != "application/pdf" {
		t.Fatalf("pdf acquisition type = %q", l.Type)
	}
}

func TestCoverEndpoint(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "books")
	w := e.addWork(t, lib, "Dune", "", time.Now().UnixMilli())
	cover := []byte("JPEGCOVERBYTES")
	coverRel := strconv.FormatInt(w, 10) + ".jpg"
	if err := os.WriteFile(filepath.Join(e.dataDir, "covers", coverRel), cover, 0o644); err != nil {
		t.Fatal(err)
	}
	e.db.Exec(`UPDATE works SET cover_path = ? WHERE id = ?`, coverRel, w)
	rec := e.get(t, "/opds/cover/"+strconv.FormatInt(w, 10))
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if !bytes.Equal(rec.Body.Bytes(), cover) {
		t.Fatalf("cover bytes = %q", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("cover type = %q", ct)
	}
	if rec := e.get(t, "/opds/cover/999999"); rec.Code != 404 {
		t.Fatalf("unknown cover: %d", rec.Code)
	}
}

func TestDownload(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "books")
	w := e.addWork(t, lib, "Dune", "", time.Now().UnixMilli())
	data := []byte("PK\x03\x04 fake epub bytes")
	ed := e.addEdition(t, w, "epub", writeBookFile(t, data, "dune.epub"), nil, time.Now().UnixMilli())

	path := "/opds/download/" + strconv.FormatInt(ed, 10)
	rec := e.get(t, path)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/epub+zip" {
		t.Fatalf("type = %q", ct)
	}
	if !bytes.Equal(rec.Body.Bytes(), data) {
		t.Fatalf("body = %q", rec.Body.String())
	}

	req := httptest.NewRequest("GET", path, nil)
	req.Header.Set("Range", "bytes=0-3")
	req.SetBasicAuth(testUser, testPass)
	rr := httptest.NewRecorder()
	e.h.ServeHTTP(rr, req)
	if rr.Code != http.StatusPartialContent {
		t.Fatalf("range status %d", rr.Code)
	}
	if !bytes.Equal(rr.Body.Bytes(), data[:4]) {
		t.Fatalf("range body = %q", rr.Body.String())
	}
	if cr := rr.Header().Get("Content-Range"); !strings.HasPrefix(cr, "bytes 0-3/") {
		t.Fatalf("content-range = %q", cr)
	}

	if rec := e.get(t, "/opds/download/999999"); rec.Code != 404 {
		t.Fatalf("unknown edition: %d", rec.Code)
	}
	if rec := e.get(t, "/opds/download/abc"); rec.Code != 404 {
		t.Fatalf("bad id: %d", rec.Code)
	}
}

func TestPSEPages(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "comics")
	w := e.addWork(t, lib, "Comic", "", time.Now().UnixMilli())
	p1 := []byte{0xFF, 0xD8, 0xFF, 0xE0, 'p', '1'}
	p2 := []byte{0x89, 'P', 'N', 'G', 'p', '2'}
	p3 := []byte{0xFF, 0xD8, 0xFF, 0xE0, 'p', '3'}
	cbz := writeCBZ(t,
		[]string{"p10.jpg", "__MACOSX/p.jpg", ".hidden.jpg", "note.txt", "p2.png", "p001.jpg"},
		map[string][]byte{
			"p001.jpg": p1, "p2.png": p2, "p10.jpg": p3,
			"__MACOSX/p.jpg": {0xFF}, ".hidden.jpg": {0xFF}, "note.txt": {},
		})
	pages := 3
	ed := e.addEdition(t, w, "cbz", cbz, &pages, time.Now().UnixMilli())
	base := "/opds/pse/" + strconv.FormatInt(ed, 10) + "/"

	rec := e.get(t, base+"1")
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), p1) {
		t.Fatalf("page 1: %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("page 1 type = %q", ct)
	}
	rec = e.get(t, base+"2")
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), p2) {
		t.Fatalf("page 2: %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("page 2 type = %q", ct)
	}
	rec = e.get(t, base+"3")
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), p3) {
		t.Fatalf("page 3 (natural order): %d %q", rec.Code, rec.Body.String())
	}
	for _, bad := range []string{"0", "4", "99", "-1", "x"} {
		if rec := e.get(t, base+bad); rec.Code != 404 {
			t.Fatalf("page %q: status %d", bad, rec.Code)
		}
	}
	epubID := e.addEdition(t, w, "epub", writeBookFile(t, []byte("epub"), "c.epub"), nil, time.Now().UnixMilli())
	if rec := e.get(t, "/opds/pse/"+strconv.FormatInt(epubID, 10)+"/1"); rec.Code != 404 {
		t.Fatalf("pse on non-cbz: %d", rec.Code)
	}
	if rec := e.getNoAuth(t, base+"1"); rec.Code != 401 {
		t.Fatalf("pse without auth: %d", rec.Code)
	}
}

func TestSearch(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "books")
	now := time.Now().UnixMilli()
	dune := e.addWork(t, lib, "Dune", "Frank Herbert", now)
	e.addEdition(t, dune, "epub", writeBookFile(t, []byte("a"), "a.epub"), nil, now)
	dragon := e.addWork(t, lib, "The Dragon", "", now)
	e.addEdition(t, dragon, "pdf", writeBookFile(t, []byte("b"), "b.pdf"), nil, now)

	rec := e.get(t, "/opds/search-description.xml")
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/opensearchdescription+xml" {
		t.Fatalf("osd type = %q", ct)
	}
	var osd xOSD
	if err := xml.Unmarshal(rec.Body.Bytes(), &osd); err != nil {
		t.Fatalf("decode osd: %v", err)
	}
	if osd.Short != "libteca" || len(osd.Urls) != 1 {
		t.Fatalf("osd = %+v", osd)
	}
	if osd.Urls[0].Template != "/opds/search?q={searchTerms}" {
		t.Fatalf("osd template = %q", osd.Urls[0].Template)
	}

	f := decodeFeed(t, e.get(t, "/opds/search?q=dune"))
	if f.Total != 1 || len(f.Entries) != 1 || f.Entries[0].Title != "Dune" {
		t.Fatalf("title search: %+v", f.Entries)
	}
	f = decodeFeed(t, e.get(t, "/opds/search?q=herbert"))
	if f.Total != 1 || f.Entries[0].Title != "Dune" {
		t.Fatalf("author search: total %d", f.Total)
	}
	f = decodeFeed(t, e.get(t, "/opds/search?q=dragon"))
	if f.Total != 1 || f.Entries[0].Title != "The Dragon" {
		t.Fatalf("second title: total %d", f.Total)
	}
	f = decodeFeed(t, e.get(t, "/opds/search?q=zzz"))
	if f.Total != 0 || len(f.Entries) != 0 {
		t.Fatalf("no match: total %d entries %d", f.Total, len(f.Entries))
	}
}

func TestInProgressFeed(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "books")
	now := time.Now().UnixMilli()
	wDune := e.addWork(t, lib, "Dune", "", now)
	wDrag := e.addWork(t, lib, "The Dragon", "", now)
	wGone := e.addWork(t, lib, "Finished Book", "", now)
	wAudio := e.addWork(t, lib, "Audioish Book", "", now)
	dune := e.addEdition(t, wDune, "epub", writeBookFile(t, []byte("a"), "a.epub"), nil, now)
	drag := e.addEdition(t, wDrag, "epub", writeBookFile(t, []byte("b"), "b.epub"), nil, now)
	gone := e.addEdition(t, wGone, "epub", writeBookFile(t, []byte("c"), "c.epub"), nil, now)
	audio := e.addEdition(t, wAudio, "epub", writeBookFile(t, []byte("d"), "d.epub"), nil, now)

	page := int64(5)
	percent := 0.2
	if err := e.db.SetReadingProgress(&store.ReadingProgress{
		Progress: store.Progress{UserID: e.userID, EditionID: dune, IsFinished: false},
		Page:     &page, Percent: &percent,
	}); err != nil {
		t.Fatal(err)
	}
	percentOnly := 0.5
	if err := e.db.SetReadingProgress(&store.ReadingProgress{
		Progress: store.Progress{UserID: e.userID, EditionID: drag, IsFinished: false},
		Percent:  &percentOnly,
	}); err != nil {
		t.Fatal(err)
	}
	finPage := int64(9)
	if err := e.db.SetReadingProgress(&store.ReadingProgress{
		Progress: store.Progress{UserID: e.userID, EditionID: gone, IsFinished: true},
		Page:     &finPage,
	}); err != nil {
		t.Fatal(err)
	}
	dur := 600.0
	if err := e.db.SetProgress(&store.Progress{
		UserID: e.userID, EditionID: audio, EditionPositionSecs: 100, DurationSecs: &dur,
	}); err != nil {
		t.Fatal(err)
	}
	e.db.Exec(`UPDATE progress SET updated_at = 2000 WHERE edition_id = ?`, dune)
	e.db.Exec(`UPDATE progress SET updated_at = 1000 WHERE edition_id = ?`, drag)

	f := decodeFeed(t, e.get(t, "/opds/in-progress"))
	if f.Total != 2 {
		t.Fatalf("total = %d", f.Total)
	}
	if len(f.Entries) != 2 || f.Entries[0].Title != "Dune" || f.Entries[1].Title != "The Dragon" {
		t.Fatalf("entries = %+v", f.Entries)
	}
}

func TestNewestFeed(t *testing.T) {
	e := newEnv(t)
	lib := e.addLibrary(t, "books")
	oldest := e.addWork(t, lib, "Oldest", "", 1000)
	middle := e.addWork(t, lib, "Middle", "", 2000)
	newest := e.addWork(t, lib, "Newest", "", 3000)
	for _, w := range []int64{oldest, middle, newest} {
		e.addEdition(t, w, "epub", writeBookFile(t, []byte("x"), "n.epub"), nil, 3000)
	}
	f := decodeFeed(t, e.get(t, "/opds/newest"))
	if f.Total != 3 {
		t.Fatalf("total = %d", f.Total)
	}
	want := []string{"Newest", "Middle", "Oldest"}
	for i, title := range want {
		if f.Entries[i].Title != title {
			t.Fatalf("entry %d = %q, want %q", i, f.Entries[i].Title, title)
		}
	}
}
