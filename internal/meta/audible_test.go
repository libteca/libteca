package meta

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func newTestAudible(t *testing.T, base string, timeout time.Duration) *Audible {
	t.Helper()
	if timeout == 0 {
		timeout = audibleTimeout
	}
	return &Audible{base: base, http: &http.Client{Timeout: timeout}}
}

const auSearchFixture = `{
 "products": [
  {
   "asin": "B07ZLS7MK8",
   "title": "Project Hail Mary",
   "authors": [{"name": "Andy Weir", "asin": "B00AHP7GSC"}],
   "narrators": [{"name": "Ray Porter"}],
   "publisher_summary": "<p>Ryland Grace is the sole survivor on a desperate mission.</p>",
   "release_date": "2021-05-04",
   "product_images": {"342": "https://m.media-amazon.com/images/I/342.jpg", "500": "https://m.media-amazon.com/images/I/500.jpg"}
  },
  {
   "asin": "",
   "title": "broken row",
   "authors": [],
   "product_images": {}
  }
 ]
}`

const auDetailFixture = `{
 "item": {
  "asin": "B07ZLS7MK8",
  "title": "Project Hail Mary",
  "authors": [{"name": "Andy Weir"}],
  "narrators": [{"name": "Ray Porter"}],
  "publisher_summary": "<p>Ryland Grace is the sole survivor.</p>",
  "release_date": "2021-05-04",
  "product_images": {"500": "https://m.media-amazon.com/images/I/500.jpg"},
  "chapter_info": {
   "runtime_length_ms": 38400000,
   "chapters": [
    {"title": "Chapter 1", "start_offset_ms": 0, "start_duration_ms": 60000},
    {"title": "Chapter 2", "start_offset_ms": 60000, "start_duration_ms": 90000}
   ]
  }
 }
}`

func TestAudibleSearch(t *testing.T) {
	var gotTitle, gotGroups, gotNum, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/products" {
			t.Errorf("path = %q, want /products", r.URL.Path)
		}
		q := r.URL.Query()
		gotTitle = q.Get("title")
		gotGroups = q.Get("response_groups")
		gotNum = q.Get("num_results")
		gotUA = r.Header.Get("User-Agent")
		fmt.Fprint(w, auSearchFixture)
	}))
	defer srv.Close()
	p := newTestAudible(t, srv.URL, 0)

	res, err := p.Search(context.Background(), Query{Kind: "audiobook", Title: "Project Hail Mary", Author: "Andy Weir"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotTitle != "Project Hail Mary" {
		t.Errorf("title param = %q", gotTitle)
	}
	if gotGroups != "media,product_desc,contributors" {
		t.Errorf("response_groups = %q", gotGroups)
	}
	if gotNum != "5" {
		t.Errorf("num_results = %q", gotNum)
	}
	if gotUA != "libteca/0.1" {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if len(res) != 1 {
		t.Fatalf("results = %d, want 1 (asin-less row dropped)", len(res))
	}
	first := res[0]
	if first.Provider != "audible" || first.ID != "B07ZLS7MK8" || first.Title != "Project Hail Mary" {
		t.Errorf("first = %+v", first)
	}
	if first.Author != "Andy Weir" {
		t.Errorf("Author = %q", first.Author)
	}
	if first.Extra["narrator"] != "Ray Porter" {
		t.Errorf("Extra = %v", first.Extra)
	}
	if first.Year == nil || *first.Year != 2021 {
		t.Errorf("Year = %v", first.Year)
	}
	if first.CoverURL != "https://m.media-amazon.com/images/I/500.jpg" {
		t.Errorf("CoverURL = %q", first.CoverURL)
	}
}

func TestAudibleFetchChapters(t *testing.T) {
	var gotGroups string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/products/B07ZLS7MK8" {
			t.Errorf("path = %q, want /products/B07ZLS7MK8", r.URL.Path)
		}
		gotGroups = r.URL.Query().Get("response_groups")
		fmt.Fprint(w, auDetailFixture)
	}))
	defer srv.Close()
	p := newTestAudible(t, srv.URL, 0)

	res, err := p.Fetch(context.Background(), "B07ZLS7MK8")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotGroups != "media,product_desc,contributors,chapter_info" {
		t.Errorf("response_groups = %q", gotGroups)
	}
	if len(res.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(res.Chapters))
	}
	c1, c2 := res.Chapters[0], res.Chapters[1]
	if c1.Title != "Chapter 1" || c1.StartSec != 0 || c1.EndSec != 60 {
		t.Errorf("chapter 1 = %+v", c1)
	}
	if c2.StartSec != 60 || c2.EndSec != 150 {
		t.Errorf("chapter 2 = %+v (ms -> sec conversion)", c2)
	}
}

func TestAudibleFetchProductKeyVariant(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"product": {"asin": "B0001", "title": "Variant Shape", "authors": [{"name": "A"}]}}`)
	}))
	defer srv.Close()
	p := newTestAudible(t, srv.URL, 0)
	res, err := p.Fetch(context.Background(), "B0001")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Title != "Variant Shape" {
		t.Errorf("title = %q", res.Title)
	}
}

func TestAudibleKindFilter(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer srv.Close()
	p := newTestAudible(t, srv.URL, 0)
	res, err := p.Search(context.Background(), Query{Kind: "book", Title: "x"})
	if res != nil || err != nil || hits != 0 {
		t.Errorf("res=%v err=%v hits=%d, want nil,nil,0", res, err, hits)
	}
}

func TestAudibleTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		fmt.Fprint(w, auSearchFixture)
	}))
	defer srv.Close()
	p := newTestAudible(t, srv.URL, 30*time.Millisecond)
	if _, err := p.Search(context.Background(), Query{Kind: "audiobook", Title: "slow"}); err == nil {
		t.Fatal("want timeout error, got nil")
	}
}

func TestAudibleCacheHitNoSecondRequest(t *testing.T) {
	newMemCacheStore(t)
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		fmt.Fprint(w, auDetailFixture)
	}))
	defer srv.Close()
	p := newTestAudible(t, srv.URL, 0)

	for i := 0; i < 2; i++ {
		res, err := p.Fetch(context.Background(), "B07ZLS7MK8")
		if err != nil || res.ID != "B07ZLS7MK8" {
			t.Fatalf("fetch %d = %v, %v", i, res, err)
		}
	}
	if hits != 1 {
		t.Fatalf("upstream hits = %d, want 1 (second fetch served from cache)", hits)
	}
}
