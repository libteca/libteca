package meta

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestOL(t *testing.T, base string, timeout time.Duration) *OpenLibrary {
	t.Helper()
	if timeout == 0 {
		timeout = olTimeout
	}
	return &OpenLibrary{
		base:  base,
		cover: "https://covers.openlibrary.org/b/id",
		http:  &http.Client{Timeout: timeout},
	}
}

const olSearchFixture = `{
 "numFound": 2,
 "start": 0,
 "docs": [
  {
   "key": "/works/OL12345W",
   "title": "Dune",
   "author_name": ["Frank Herbert"],
   "first_sentence": ["Set on the desert planet Arrakis, Dune is the story of the boy Paul Atreides."],
   "first_publish_year": 1965,
   "cover_i": 8232841,
   "edition_count": 156,
   "language": ["eng"],
   "subject": ["Science Fiction", "Dystopian", "Planets", "Politics", "Imaginary wars"]
  },
  {
   "key": "/works/OL67890W",
   "title": "The Left Hand of Darkness",
   "author_name": ["Ursula K. Le Guin", "Another Contributor"],
   "first_publish_year": 1969,
   "edition_count": 98,
   "language": ["eng"],
   "subject": ["Winter"]
  }
 ]
}`

const olWorkStringDesc = `{
 "key": "/works/OL12345W",
 "title": "Dune",
 "description": "A plain string description.",
 "covers": [-1, 8232841],
 "first_publish_date": "1965-08-01",
 "subjects": ["Science Fiction", "American Science fiction"],
 "authors": [{"author": {"key": "/authors/OL41197A"}}]
}`

const olWorkObjectDesc = `{
 "key": "/works/OL12345W",
 "title": "Dune",
 "description": {"type": "/type/text", "value": "An object-shaped description."},
 "covers": [8232841],
 "first_publish_date": "1965"
}`

const olEditionsFixture = `{
 "entries": [
  {"key": "/books/OL24202367M", "page_count": 412, "publish_date": "1965", "physical_format": "Hardcover"},
  {"key": "/books/OL24202368M", "number_of_pages": 300}
 ]
}`

const olEditionsPagesFallback = `{
 "entries": [
  {"key": "/books/OL1M", "number_of_pages": 300}
 ]
}`

const olAuthorHerbert = `{
 "key": "/authors/OL41197A",
 "name": "Frank Herbert",
 "personal_name": "Frank Herbert"
}`

const olAuthorPenName = `{
 "key": "/authors/OL1A",
 "name": "Pen Name",
 "personal_name": "Real Name"
}`

const olAuthorPlain = `{"key": "/authors/OL2A", "name": "Second Author"}`

const olWorkTwoAuthors = `{
 "key": "/works/OL12345W",
 "title": "Dune",
 "description": "Two-author work.",
 "covers": [8232841],
 "first_publish_date": "1965",
 "authors": [
  {"author": {"key": "/authors/OL1A"}},
  {"author": {"key": "/authors/OL2A"}}
 ]
}`

func TestOpenLibrarySearch(t *testing.T) {
	var gotTitle, gotAuthor, gotLimit, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search.json" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q := r.URL.Query()
		gotTitle, gotAuthor, gotLimit = q.Get("title"), q.Get("author"), q.Get("limit")
		gotUA = r.Header.Get("User-Agent")
		fmt.Fprint(w, olSearchFixture)
	}))
	defer srv.Close()
	ol := newTestOL(t, srv.URL, 0)

	res, err := ol.Search(context.Background(), Query{Kind: "book", Title: "dune", Author: "frank herbert"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotTitle != "dune" || gotAuthor != "frank herbert" || gotLimit != "5" {
		t.Errorf("params title=%q author=%q limit=%q", gotTitle, gotAuthor, gotLimit)
	}
	if gotUA == "" {
		t.Error("User-Agent not set")
	}
	if len(res) != 2 {
		t.Fatalf("results = %d, want 2", len(res))
	}
	first := res[0]
	if first.Provider != "openlibrary" || first.ID != "OL12345W" || first.Title != "Dune" {
		t.Errorf("first = %+v", first)
	}
	if first.Author != "Frank Herbert" {
		t.Errorf("Author = %q", first.Author)
	}
	if first.Year == nil || *first.Year != 1965 {
		t.Errorf("Year = %v", first.Year)
	}
	if want := "https://covers.openlibrary.org/b/id/8232841-L.jpg"; first.CoverURL != want {
		t.Errorf("CoverURL = %q", first.CoverURL)
	}
	if !strings.HasPrefix(first.Description, "Set on the desert planet") {
		t.Errorf("Description = %q", first.Description)
	}
	if len(first.Genres) != 4 {
		t.Errorf("Genres = %v, want capped at 4", first.Genres)
	}
	if first.Extra["editionCount"] != "156" || first.Extra["language"] != "eng" {
		t.Errorf("Extra = %v", first.Extra)
	}
	if res[1].CoverURL != "" || res[1].Author != "Ursula K. Le Guin, Another Contributor" {
		t.Errorf("second = %+v", res[1])
	}
}

func TestOpenLibrarySearchISBN(t *testing.T) {
	var gotQ, gotTitle string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		gotQ, gotTitle = q.Get("q"), q.Get("title")
		fmt.Fprint(w, olSearchFixture)
	}))
	defer srv.Close()
	ol := newTestOL(t, srv.URL, 0)

	if _, err := ol.Search(context.Background(), Query{Kind: "book", ISBN: "9780441013593"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotQ != "9780441013593" || gotTitle != "" {
		t.Errorf("q=%q title=%q, want ISBN in q and no title", gotQ, gotTitle)
	}
}

func TestOpenLibrarySearchKindFilter(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		fmt.Fprint(w, olSearchFixture)
	}))
	defer srv.Close()
	ol := newTestOL(t, srv.URL, 0)
	for _, kind := range []string{"movie", "music", "comic"} {
		res, err := ol.Search(context.Background(), Query{Kind: kind, Title: "x"})
		if res != nil || err != nil || hits != 0 {
			t.Errorf("kind %q: res=%v err=%v hits=%d", kind, res, err, hits)
		}
	}
	if res, err := ol.Search(context.Background(), Query{Kind: "audiobook", Title: "x"}); err != nil || res == nil {
		t.Errorf("audiobook should be served: res=%v err=%v", res, err)
	}
}

func TestOpenLibraryFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/works/OL12345W.json":
			fmt.Fprint(w, olWorkStringDesc)
		case "/works/OL12345W/editions.json":
			fmt.Fprint(w, olEditionsFixture)
		case "/authors/OL41197A.json":
			fmt.Fprint(w, olAuthorHerbert)
		default:
			t.Errorf("path = %q", r.URL.Path)
		}
	}))
	defer srv.Close()
	ol := newTestOL(t, srv.URL, 0)

	res, err := ol.Fetch(context.Background(), "/works/OL12345W")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.ID != "OL12345W" || res.Title != "Dune" {
		t.Errorf("res = %+v", res)
	}
	if res.Author != "Frank Herbert" {
		t.Errorf("Author = %q", res.Author)
	}
	if res.Description != "A plain string description." {
		t.Errorf("Description = %q", res.Description)
	}
	if res.Year == nil || *res.Year != 1965 {
		t.Errorf("Year = %v", res.Year)
	}
	if want := "https://covers.openlibrary.org/b/id/8232841-L.jpg"; res.CoverURL != want {
		t.Errorf("CoverURL = %q (covers -1 sentinel skipped)", res.CoverURL)
	}
	if res.Extra["pageCount"] != "412" {
		t.Errorf("pageCount = %q, want 412", res.Extra["pageCount"])
	}
	if len(res.Genres) != 2 || res.Genres[0] != "Science Fiction" {
		t.Errorf("Genres = %v", res.Genres)
	}
}

func TestOpenLibraryFetchDescriptionVariants(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/works/OL12345W.json":
			fmt.Fprint(w, olWorkObjectDesc)
		case "/works/OL12345W/editions.json":
			fmt.Fprint(w, olEditionsPagesFallback)
		default:
			t.Errorf("path = %q", r.URL.Path)
		}
	}))
	defer srv.Close()
	ol := newTestOL(t, srv.URL, 0)

	res, err := ol.Fetch(context.Background(), "OL12345W")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Description != "An object-shaped description." {
		t.Errorf("Description = %q", res.Description)
	}
	if res.Author != "" {
		t.Errorf("Author = %q, want empty (work has no author refs)", res.Author)
	}
	if res.Extra["pageCount"] != "300" {
		t.Errorf("pageCount = %q, want 300 via number_of_pages fallback", res.Extra["pageCount"])
	}
}

func TestOpenLibraryFetchAuthors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/works/OL12345W.json":
			fmt.Fprint(w, olWorkTwoAuthors)
		case "/works/OL12345W/editions.json":
			fmt.Fprint(w, olEditionsPagesFallback)
		case "/authors/OL1A.json":
			fmt.Fprint(w, olAuthorPenName)
		case "/authors/OL2A.json":
			fmt.Fprint(w, olAuthorPlain)
		default:
			t.Errorf("path = %q", r.URL.Path)
		}
	}))
	defer srv.Close()
	ol := newTestOL(t, srv.URL, 0)

	res, err := ol.Fetch(context.Background(), "OL12345W")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Author != "Real Name, Second Author" {
		t.Errorf("Author = %q, want personal_name preferred and names joined", res.Author)
	}
}

func TestOpenLibraryFetchAuthorFailureBestEffort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/works/OL12345W.json":
			fmt.Fprint(w, olWorkStringDesc)
		case "/works/OL12345W/editions.json":
			fmt.Fprint(w, olEditionsPagesFallback)
		case "/authors/OL41197A.json":
			http.Error(w, "gone", http.StatusNotFound)
		default:
			t.Errorf("path = %q", r.URL.Path)
		}
	}))
	defer srv.Close()
	ol := newTestOL(t, srv.URL, 0)

	res, err := ol.Fetch(context.Background(), "OL12345W")
	if err != nil {
		t.Fatalf("Fetch must survive author failure: %v", err)
	}
	if res.Author != "" {
		t.Errorf("Author = %q, want empty", res.Author)
	}
}

func TestOpenLibraryCache(t *testing.T) {
	var mu sync.Mutex
	workHits, editionHits, authorHits := 0, 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/works/OL12345W.json":
			workHits++
			fmt.Fprint(w, olWorkStringDesc)
		case "/works/OL12345W/editions.json":
			editionHits++
			fmt.Fprint(w, olEditionsFixture)
		case "/authors/OL41197A.json":
			authorHits++
			fmt.Fprint(w, olAuthorHerbert)
		}
	}))
	defer srv.Close()
	ol := newTestOL(t, srv.URL, 0)

	for i := 0; i < 2; i++ {
		if _, err := ol.Fetch(context.Background(), "OL12345W"); err != nil {
			t.Fatalf("Fetch %d: %v", i+1, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if workHits != 1 || editionHits != 1 || authorHits != 1 {
		t.Errorf("workHits=%d editionHits=%d authorHits=%d, want 1/1/1 (second Fetch cached)", workHits, editionHits, authorHits)
	}
}

func TestOpenLibraryTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		fmt.Fprint(w, olSearchFixture)
	}))
	defer srv.Close()
	ol := newTestOL(t, srv.URL, 30*time.Millisecond)
	if _, err := ol.Search(context.Background(), Query{Kind: "book", Title: "slow"}); err == nil {
		t.Fatal("want timeout error, got nil")
	}
}
