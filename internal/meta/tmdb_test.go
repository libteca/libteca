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

type memCacheEntry struct {
	response  string
	fetchedAt int64
}

type memCacheStore struct {
	mu      sync.Mutex
	entries map[string]memCacheEntry
	hits    int
}

func newMemCacheStore(t *testing.T) *memCacheStore {
	t.Helper()
	cs := &memCacheStore{entries: map[string]memCacheEntry{}}
	SetCacheStore(cs)
	t.Cleanup(func() { SetCacheStore(nil) })
	return cs
}

func (m *memCacheStore) GetCached(provider, key string) (string, int64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[provider+"\x00"+key]
	if !ok {
		return "", 0, false
	}
	m.hits++
	return e.response, e.fetchedAt, true
}

func (m *memCacheStore) PutCached(provider, key, response string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries[provider+"\x00"+key] = memCacheEntry{response: response, fetchedAt: time.Now().UnixMilli()}
	return nil
}

func TestCacheDisabledWithoutStore(t *testing.T) {
	SetCacheStore(nil)
	if _, ok := CacheGet("p", "k"); ok {
		t.Fatal("CacheGet without store should miss")
	}
	if err := CachePut("p", "k", []string{"x"}); err != nil {
		t.Fatalf("CachePut without store = %v, want nil no-op", err)
	}
}

func TestCacheRoundTripAndTTL(t *testing.T) {
	cs := newMemCacheStore(t)
	if err := CachePut("tmdb", "search:x", []Result{{Provider: "tmdb", ID: "movie:1"}}); err != nil {
		t.Fatalf("CachePut: %v", err)
	}
	var out []Result
	if !CacheGetJSON("tmdb", "search:x", &out) || len(out) != 1 || out[0].ID != "movie:1" {
		t.Fatalf("CacheGetJSON = %v", out)
	}
	cs.mu.Lock()
	e := cs.entries["tmdb\x00search:x"]
	e.fetchedAt = time.Now().UnixMilli() - (CacheTTL + time.Second).Milliseconds()
	cs.entries["tmdb\x00search:x"] = e
	cs.mu.Unlock()
	if CacheGetJSON("tmdb", "search:x", &out) {
		t.Fatal("expired entry should miss")
	}
}

func TestRegistryFiltersDisabled(t *testing.T) {
	t.Setenv("LIBTECA_TMDB_KEY", "")
	t.Setenv("LIBTECA_COMICVINE_KEY", "")
	names := map[string]bool{}
	for _, p := range Registry() {
		names[p.Name()] = true
	}
	for _, want := range []string{"audible", "musicbrainz", "openlibrary"} {
		if !names[want] {
			t.Errorf("registry missing %q (got %v)", want, names)
		}
	}
	if names["tmdb"] || names["comicvine"] {
		t.Errorf("keyless providers should be filtered (got %v)", names)
	}
}

func TestRegistryWithKeys(t *testing.T) {
	t.Setenv("LIBTECA_TMDB_KEY", "k")
	t.Setenv("LIBTECA_COMICVINE_KEY", "k")
	var order []string
	for _, p := range Registry() {
		order = append(order, p.Name())
	}
	want := []string{"tmdb", "audible", "musicbrainz", "openlibrary", "comicvine"}
	if len(order) != len(want) {
		t.Fatalf("registry = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("registry order = %v, want %v", order, want)
		}
	}
}

func newTestTMDB(t *testing.T, base string, timeout time.Duration) *TMDB {
	t.Helper()
	if timeout == 0 {
		timeout = tmdbTimeout
	}
	return &TMDB{base: base, image: "https://i.example/w500", key: "test-key", http: &http.Client{Timeout: timeout}}
}

const tmdbSearchFixture = `{
 "page": 1,
 "results": [
  {"id": 438631, "title": "Dune", "overview": "A noble family becomes embroiled in a war.", "release_date": "2021-10-22", "poster_path": "/d5.jpg", "vote_average": 7.8},
  {"id": 11241, "title": "Old Dune Hook", "overview": "", "release_date": "", "poster_path": "", "vote_average": 0}
 ]
}`

const tmdbDetailFixture = `{
 "id": 438631,
 "title": "Dune",
 "overview": "A noble family becomes embroiled in a war.",
 "release_date": "2021-10-22",
 "poster_path": "/d5.jpg",
 "vote_average": 7.8,
 "genres": [{"id": 878, "name": "Science Fiction"}, {"id": 12, "name": "Adventure"}]
}`

func TestTMDBDisabledWithoutKey(t *testing.T) {
	t.Setenv("LIBTECA_TMDB_KEY", "")
	if p := NewTMDB(); p != nil {
		t.Fatal("NewTMDB with unset key should be nil (disabled)")
	}
	var p *TMDB
	if res, err := p.Search(context.Background(), Query{Kind: "movie", Title: "dune"}); res != nil || err != nil {
		t.Errorf("nil-provider Search = %v, %v; want nil, nil", res, err)
	}
	if res, err := p.Fetch(context.Background(), "movie:1"); res != nil || err != nil {
		t.Errorf("nil-provider Fetch = %v, %v; want nil, nil", res, err)
	}
}

func TestTMDBSearch(t *testing.T) {
	var gotAuth, gotUA, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/movie" {
			t.Errorf("path = %q, want /search/movie", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		gotQuery = r.URL.Query().Get("query")
		fmt.Fprint(w, tmdbSearchFixture)
	}))
	defer srv.Close()
	p := newTestTMDB(t, srv.URL, 0)

	res, err := p.Search(context.Background(), Query{Kind: "movie", Title: "Dune"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotUA != "libteca/0.1" {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if gotQuery != "Dune" {
		t.Errorf("query = %q", gotQuery)
	}
	if len(res) != 2 {
		t.Fatalf("results = %d, want 2", len(res))
	}
	first := res[0]
	if first.Provider != "tmdb" || first.ID != "movie:438631" || first.Title != "Dune" {
		t.Errorf("first = %+v", first)
	}
	if first.Year == nil || *first.Year != 2021 {
		t.Errorf("Year = %v", first.Year)
	}
	if first.Rating != 7.8 {
		t.Errorf("Rating = %v", first.Rating)
	}
	if want := "https://i.example/w500/d5.jpg"; first.CoverURL != want {
		t.Errorf("CoverURL = %q, want %q", first.CoverURL, want)
	}
	if second := res[1]; second.Year != nil || second.CoverURL != "" {
		t.Errorf("second = %+v, want nil year and no cover", second)
	}
}

func TestTMDBTVSearch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/tv" {
			t.Errorf("path = %q, want /search/tv", r.URL.Path)
		}
		fmt.Fprint(w, `{"results": [{"id": 94605, "name": "Arcane", "first_air_date": "2021-11-06", "poster_path": "/a.jpg", "vote_average": 8.7}]}`)
	}))
	defer srv.Close()
	p := newTestTMDB(t, srv.URL, 0)
	res, err := p.Search(context.Background(), Query{Kind: "tv", Title: "arcane"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) != 1 || res[0].ID != "tv:94605" || res[0].Title != "Arcane" {
		t.Fatalf("res = %+v", res)
	}
	if res[0].Year == nil || *res[0].Year != 2021 {
		t.Errorf("Year = %v", res[0].Year)
	}
}

func TestTMDBFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/movie/438631" {
			t.Errorf("path = %q, want /movie/438631", r.URL.Path)
		}
		fmt.Fprint(w, tmdbDetailFixture)
	}))
	defer srv.Close()
	p := newTestTMDB(t, srv.URL, 0)

	res, err := p.Fetch(context.Background(), "movie:438631")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.ID != "movie:438631" || res.Title != "Dune" {
		t.Errorf("res = %+v", res)
	}
	if len(res.Genres) != 2 || res.Genres[0] != "Science Fiction" {
		t.Errorf("Genres = %v", res.Genres)
	}
	if res.Year == nil || *res.Year != 2021 {
		t.Errorf("Year = %v", res.Year)
	}
}

func TestTMDBBadID(t *testing.T) {
	p := newTestTMDB(t, "http://unused.example", 0)
	if _, err := p.Fetch(context.Background(), "438631"); err == nil {
		t.Fatal("unprefixed id should error")
	}
}

func TestTMDBKindFilter(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer srv.Close()
	p := newTestTMDB(t, srv.URL, 0)
	res, err := p.Search(context.Background(), Query{Kind: "book", Title: "x"})
	if res != nil || err != nil || hits != 0 {
		t.Errorf("res=%v err=%v hits=%d, want nil,nil,0", res, err, hits)
	}
}

func TestTMDBTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		fmt.Fprint(w, tmdbSearchFixture)
	}))
	defer srv.Close()
	p := newTestTMDB(t, srv.URL, 30*time.Millisecond)
	if _, err := p.Search(context.Background(), Query{Kind: "movie", Title: "slow"}); err == nil {
		t.Fatal("want timeout error, got nil")
	}
}

func TestTMDBCacheHitNoSecondRequest(t *testing.T) {
	newMemCacheStore(t)
	var hits int
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		fmt.Fprint(w, tmdbSearchFixture)
	}))
	defer srv.Close()
	p := newTestTMDB(t, srv.URL, 0)

	for i := 0; i < 2; i++ {
		res, err := p.Search(context.Background(), Query{Kind: "movie", Title: "Dune"})
		if err != nil || len(res) != 2 {
			t.Fatalf("search %d = %v, %v", i, res, err)
		}
	}
	if hits != 1 {
		t.Fatalf("upstream hits = %d, want 1 (second search served from cache)", hits)
	}
}
