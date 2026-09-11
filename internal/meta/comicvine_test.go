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

func newTestCV(t *testing.T, base string, timeout time.Duration) *ComicVine {
	t.Helper()
	if timeout == 0 {
		timeout = cvTimeout
	}
	return &ComicVine{
		base: base,
		key:  "test-key",
		http: &http.Client{Timeout: timeout},
	}
}

const cvSearchFixture = `{
 "error": "OK",
 "limit": 5,
 "offset": 0,
 "number_of_page_results": 2,
 "number_of_total_results": 2,
 "status_code": 1,
 "results": [
  {
   "id": 41333,
   "name": "Saga",
   "description": "<p><b>Saga</b> is an epic space opera/fantasy comic series.</p>",
   "start_year": "2012",
   "image": {
    "icon_url": "https://comicvine.gamespot.com/a/uploads/square_avatar/0/1/1.jpg",
    "thumb_url": "https://comicvine.gamespot.com/a/uploads/thumb/0/1/1.jpg",
    "small_url": "https://comicvine.gamespot.com/a/uploads/small/0/1/1.jpg",
    "screen_url": "https://comicvine.gamespot.com/a/uploads/screen/0/1/1.jpg",
    "medium_url": "https://comicvine.gamespot.com/a/uploads/medium/0/1/1.jpg",
    "super_url": "https://comicvine.gamespot.com/a/uploads/super/0/1/1.jpg",
    "original_url": "https://comicvine.gamespot.com/a/uploads/original/0/1/1.jpg"
   },
   "publisher": {"id": 521, "name": "Image"}
  },
  {
   "id": 24270,
   "name": "Y: The Last Man",
   "description": "<p>Yorick Brown is the last man alive.</p>",
   "start_year": null,
   "image": {
    "original_url": "https://comicvine.gamespot.com/a/uploads/original/0/2/2.jpg"
   },
   "publisher": null
  }
 ]
}`

const cvFetchFixture = `{
 "error": "OK",
 "status_code": 1,
 "results": {
  "id": 41333,
  "name": "Saga",
  "description": "<p><b>Saga</b> is an epic space opera/fantasy comic series.</p>",
  "start_year": 2012,
  "image": {
   "medium_url": "https://comicvine.gamespot.com/a/uploads/medium/0/1/1.jpg"
  },
  "publisher": {"id": 521, "name": "Image"},
  "count_of_issues": 66
 }
}`

const cvErrorFixture = `{
 "error": "Invalid API Key",
 "limit": 0,
 "offset": 0,
 "number_of_page_results": 0,
 "number_of_total_results": 0,
 "status_code": 101,
 "results": null
}`

func TestComicVineDisabledWithoutKey(t *testing.T) {
	t.Setenv(cvKeyEnv, "")
	if cv := NewComicVine(); cv != nil {
		t.Fatal("NewComicVine with unset key should be nil (disabled)")
	}
	var cv *ComicVine
	if res, err := cv.Search(context.Background(), Query{Kind: "comic", Title: "saga"}); res != nil || err != nil {
		t.Errorf("nil-provider Search = %v, %v; want nil, nil", res, err)
	}
	if res, err := cv.Fetch(context.Background(), "41333"); res != nil || err != nil {
		t.Errorf("nil-provider Fetch = %v, %v; want nil, nil", res, err)
	}
}

func TestComicVineEnabledWithKey(t *testing.T) {
	t.Setenv(cvKeyEnv, "some-key")
	if cv := NewComicVine(); cv == nil {
		t.Fatal("NewComicVine with key set should not be nil")
	}
}

func TestComicVineSearch(t *testing.T) {
	var gotKey, gotResources, gotFormat, gotFieldList, gotQuery, gotLimit, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Errorf("path = %q", r.URL.Path)
		}
		q := r.URL.Query()
		gotKey = q.Get("api_key")
		gotResources = q.Get("resources")
		gotFormat = q.Get("format")
		gotFieldList = q.Get("field_list")
		gotQuery = q.Get("query")
		gotLimit = q.Get("limit")
		gotUA = r.Header.Get("User-Agent")
		fmt.Fprint(w, cvSearchFixture)
	}))
	defer srv.Close()
	cv := newTestCV(t, srv.URL, 0)

	res, err := cv.Search(context.Background(), Query{Kind: "comic", Title: "saga"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotKey != "test-key" || gotResources != "volume" || gotFormat != "json" || gotLimit != "5" {
		t.Errorf("params api_key=%q resources=%q format=%q limit=%q", gotKey, gotResources, gotFormat, gotLimit)
	}
	if want := "id,name,description,start_year,image,publisher"; gotFieldList != want {
		t.Errorf("field_list = %q, want %q", gotFieldList, want)
	}
	if gotQuery != "saga" || gotUA == "" {
		t.Errorf("query=%q ua=%q", gotQuery, gotUA)
	}
	if len(res) != 2 {
		t.Fatalf("results = %d, want 2", len(res))
	}
	first := res[0]
	if first.Provider != "comicvine" || first.ID != "41333" || first.Title != "Saga" {
		t.Errorf("first = %+v", first)
	}
	if first.Year == nil || *first.Year != 2012 {
		t.Errorf("Year = %v", first.Year)
	}
	if want := "https://comicvine.gamespot.com/a/uploads/medium/0/1/1.jpg"; first.CoverURL != want {
		t.Errorf("CoverURL = %q (medium preferred)", first.CoverURL)
	}
	if first.Extra["publisher"] != "Image" || first.Extra["startYear"] != "2012" {
		t.Errorf("Extra = %v", first.Extra)
	}
	if !strings.Contains(first.Description, "<p>") {
		t.Errorf("Description kept raw HTML = %q", first.Description)
	}
	second := res[1]
	if second.Year != nil {
		t.Errorf("null start_year should yield nil Year, got %v", *second.Year)
	}
	if second.CoverURL != "https://comicvine.gamespot.com/a/uploads/original/0/2/2.jpg" {
		t.Errorf("CoverURL fallback = %q", second.CoverURL)
	}
	if second.Extra["publisher"] != "" || second.Extra["startYear"] != "" {
		t.Errorf("Extra on null publisher/start_year = %v", second.Extra)
	}
}

func TestComicVineFetch(t *testing.T) {
	var gotKey, gotFormat string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/volume/4050-41333" {
			t.Errorf("path = %q, want /volume/4050-41333", r.URL.Path)
		}
		q := r.URL.Query()
		gotKey, gotFormat = q.Get("api_key"), q.Get("format")
		fmt.Fprint(w, cvFetchFixture)
	}))
	defer srv.Close()
	cv := newTestCV(t, srv.URL, 0)

	res, err := cv.Fetch(context.Background(), "4050-41333")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotKey != "test-key" || gotFormat != "json" {
		t.Errorf("api_key=%q format=%q", gotKey, gotFormat)
	}
	if res.ID != "41333" || res.Title != "Saga" || res.Author != "" {
		t.Errorf("res = %+v", res)
	}
	if res.Year == nil || *res.Year != 2012 {
		t.Errorf("Year = %v (numeric start_year quirk)", res.Year)
	}
	if res.Extra["publisher"] != "Image" {
		t.Errorf("Extra = %v", res.Extra)
	}
}

func TestComicVineFetchCacheKeyIncludesAPIKey(t *testing.T) {
	newMemCacheStore(t)
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		fmt.Fprint(w, cvFetchFixture)
	}))
	defer srv.Close()

	a := newTestCV(t, srv.URL, 0)
	if _, err := a.Fetch(context.Background(), "41333"); err != nil {
		t.Fatal(err)
	}
	b := newTestCV(t, srv.URL, 0)
	b.key = "rotated-key"
	if _, err := b.Fetch(context.Background(), "41333"); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("upstream hits = %d, want 2 (rotated key must not serve the old key's cache)", hits)
	}
}

func TestComicVineAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, cvErrorFixture)
	}))
	defer srv.Close()
	cv := newTestCV(t, srv.URL, 0)
	if _, err := cv.Search(context.Background(), Query{Kind: "comic", Title: "saga"}); err == nil {
		t.Fatal("want API error surfaced, got nil")
	} else if !strings.Contains(err.Error(), "Invalid API Key") {
		t.Errorf("err = %v", err)
	}
}

func TestComicVineKindFilter(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer srv.Close()
	cv := newTestCV(t, srv.URL, 0)
	res, err := cv.Search(context.Background(), Query{Kind: "book", Title: "x"})
	if res != nil || err != nil || hits != 0 {
		t.Errorf("res=%v err=%v hits=%d, want nil,nil,0", res, err, hits)
	}
}

func TestComicVineTimeout(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		mu.Lock()
		hits++
		mu.Unlock()
		fmt.Fprint(w, cvSearchFixture)
	}))
	defer srv.Close()
	cv := newTestCV(t, srv.URL, 30*time.Millisecond)
	if _, err := cv.Search(context.Background(), Query{Kind: "comic", Title: "slow"}); err == nil {
		t.Fatal("want timeout error, got nil")
	}
}
