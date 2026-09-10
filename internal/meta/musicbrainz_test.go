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

func newTestMB(t *testing.T, base string, now func() time.Time, delay func(context.Context, time.Duration) error) *MusicBrainz {
	t.Helper()
	if now == nil {
		now = time.Now
	}
	if delay == nil {
		delay = mbCtxSleep
	}
	return &MusicBrainz{
		base:    base,
		cover:   "https://coverartarchive.org/release",
		http:    &http.Client{Timeout: mbTimeout},
		limiter: newMBLimiter(now, delay),
	}
}

const mbSearchFixture = `{
 "created": "2026-09-09T00:00:00.000+00:00",
 "count": 2,
 "offset": 0,
 "releases": [
  {
   "id": "rel-1",
   "title": "The Dark Side of the Moon",
   "status": "Official",
   "date": "1973-03-01",
   "country": "GB",
   "score": 100,
   "artist-credit": [
    {"name": "Pink Floyd", "artist": {"id": "art-1", "name": "Pink Floyd", "sort-name": "Floyd, Pink"}}
   ],
   "release-group": {"id": "rg-1", "primary-type": "Album", "secondary-types": []}
  },
  {
   "id": "rel-2",
   "title": "Live at Pompeii",
   "disambiguation": "concert film soundtrack",
   "date": "1972",
   "country": "JP",
   "score": 83,
   "artist-credit": [
    {"name": "David Gilmour", "joinphrase": " & "},
    {"name": "Roger Waters"}
   ],
   "release-group": {"id": "rg-2", "primary-type": "Album"}
  }
 ]
}`

const mbFetchFixture = `{
 "id": "rel-1",
 "title": "The Dark Side of the Moon",
 "status": "Official",
 "date": "1973-03-01",
 "country": "GB",
 "disambiguation": "1973 remaster",
 "artist-credit": [{"name": "Pink Floyd"}],
 "genres": [{"count": 2, "name": "progressive rock"}],
 "media": [
  {
   "position": 2,
   "format": "SACD",
   "tracks": [
    {"id": "t-4", "number": "1", "title": "Eclipse (alternate)", "length": 130000}
   ]
  },
  {
   "position": 1,
   "format": "CD",
   "tracks": [
    {"id": "t-1", "number": "1", "title": "Speak to Me", "length": 67000},
    {"id": "t-2", "number": "2", "title": "On the Run", "recording": {"id": "r-2", "title": "On the Run", "length": 213000}},
    {"id": "t-3", "number": "3", "title": "Time"}
   ]
  }
 ]
}`

func TestMusicBrainzSearch(t *testing.T) {
	var gotUA, gotQuery, gotFmt, gotLimit string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		q := r.URL.Query()
		gotQuery, gotFmt, gotLimit = q.Get("query"), q.Get("fmt"), q.Get("limit")
		if r.URL.Path != "/release" {
			t.Errorf("path = %q, want /release", r.URL.Path)
		}
		fmt.Fprint(w, mbSearchFixture)
	}))
	defer srv.Close()
	mb := newTestMB(t, srv.URL, nil, nil)

	res, err := mb.Search(context.Background(), Query{Kind: "music", Title: "dark side of the moon", Author: "pink floyd"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotUA != "libteca/0.1 (metadata)" {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if want := `release:"dark side of the moon" AND artist:"pink floyd"`; gotQuery != want {
		t.Errorf("query = %q, want %q", gotQuery, want)
	}
	if gotFmt != "json" || gotLimit != "5" {
		t.Errorf("fmt=%q limit=%q", gotFmt, gotLimit)
	}
	if len(res) != 2 {
		t.Fatalf("results = %d, want 2", len(res))
	}
	first := res[0]
	if first.Provider != "musicbrainz" || first.ID != "rel-1" || first.Title != "The Dark Side of the Moon" {
		t.Errorf("first = %+v", first)
	}
	if first.Author != "Pink Floyd" {
		t.Errorf("Author = %q", first.Author)
	}
	if first.Year == nil || *first.Year != 1973 {
		t.Errorf("Year = %v", first.Year)
	}
	if want := "https://coverartarchive.org/release/rel-1/front-250"; first.CoverURL != want {
		t.Errorf("CoverURL = %q", first.CoverURL)
	}
	if first.Extra["status"] != "Official" || first.Extra["country"] != "GB" || first.Extra["primaryType"] != "Album" {
		t.Errorf("Extra = %v", first.Extra)
	}
	second := res[1]
	if second.Author != "David Gilmour & Roger Waters" {
		t.Errorf("joinphrase Author = %q", second.Author)
	}
	if second.Description != "concert film soundtrack" {
		t.Errorf("Description = %q", second.Description)
	}
	if second.Year == nil || *second.Year != 1972 {
		t.Errorf("Year (year-only date) = %v", second.Year)
	}
}

func TestMusicBrainzSearchKindFilter(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer srv.Close()
	mb := newTestMB(t, srv.URL, nil, nil)
	res, err := mb.Search(context.Background(), Query{Kind: "book", Title: "x"})
	if res != nil || err != nil || hits != 0 {
		t.Errorf("res=%v err=%v hits=%d, want nil,nil,0", res, err, hits)
	}
	if _, err := mb.Search(context.Background(), Query{Title: ""}); err == nil {
		t.Error("empty title: want error")
	}
}

func TestMusicBrainzFetch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/release/rel-1" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if inc := r.URL.Query().Get("inc"); inc != "recordings+artist-credits+release-groups" {
			t.Errorf("inc = %q", inc)
		}
		fmt.Fprint(w, mbFetchFixture)
	}))
	defer srv.Close()
	mb := newTestMB(t, srv.URL, nil, nil)

	res, err := mb.Fetch(context.Background(), "rel-1")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if res.Title != "The Dark Side of the Moon" || res.Author != "Pink Floyd" || res.Description != "1973 remaster" {
		t.Errorf("res = %+v", res)
	}
	if res.Year == nil || *res.Year != 1973 {
		t.Errorf("Year = %v", res.Year)
	}
	if want := "https://coverartarchive.org/release/rel-1/front-250"; res.CoverURL != want {
		t.Errorf("CoverURL = %q", res.CoverURL)
	}
	if len(res.Genres) != 1 || res.Genres[0] != "progressive rock" {
		t.Errorf("Genres = %v", res.Genres)
	}
	wantChapters := []Chapter{
		{Title: "Speak to Me", StartSec: 0, EndSec: 67},
		{Title: "On the Run", StartSec: 67, EndSec: 280},
		{Title: "Time", StartSec: 280, EndSec: 280},
		{Title: "Eclipse (alternate)", StartSec: 280, EndSec: 410},
	}
	if len(res.Chapters) != len(wantChapters) {
		t.Fatalf("chapters = %+v", res.Chapters)
	}
	for i, want := range wantChapters {
		if res.Chapters[i] != want {
			t.Errorf("chapter[%d] = %+v, want %+v", i, res.Chapters[i], want)
		}
	}
	if res.Extra["trackCount"] != "4" || res.Extra["format"] != "CD/SACD" {
		t.Errorf("Extra = %v", res.Extra)
	}
}

func TestMusicBrainzRateLimit(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		fmt.Fprint(w, mbSearchFixture)
	}))
	defer srv.Close()
	frozen := time.Date(2026, 9, 9, 0, 0, 0, 0, time.UTC)
	var delays []time.Duration
	var dmu sync.Mutex
	delay := func(ctx context.Context, d time.Duration) error {
		dmu.Lock()
		delays = append(delays, d)
		dmu.Unlock()
		return nil
	}
	mb := newTestMB(t, srv.URL, func() time.Time { return frozen }, delay)

	for _, title := range []string{"one", "two"} {
		if _, err := mb.Search(context.Background(), Query{Kind: "music", Title: title}); err != nil {
			t.Fatalf("Search(%q): %v", title, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	dmu.Lock()
	defer dmu.Unlock()
	if hits != 2 {
		t.Errorf("hits = %d, want 2 (guard spaces calls, never drops them)", hits)
	}
	if len(delays) != 1 || delays[0] < 900*time.Millisecond {
		t.Errorf("delays = %v, want a single >=900ms gap for the second call", delays)
	}
}

func TestMusicBrainzCache(t *testing.T) {
	var mu sync.Mutex
	searchHits, fetchHits := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if strings.HasPrefix(r.URL.Path, "/release/") {
			fetchHits++
			fmt.Fprint(w, mbFetchFixture)
			return
		}
		searchHits++
		fmt.Fprint(w, mbSearchFixture)
	}))
	defer srv.Close()
	mb := newTestMB(t, srv.URL, nil, nil)

	q := Query{Kind: "music", Title: "same"}
	if _, err := mb.Search(context.Background(), q); err != nil {
		t.Fatalf("Search 1: %v", err)
	}
	if _, err := mb.Search(context.Background(), q); err != nil {
		t.Fatalf("Search 2: %v", err)
	}
	if searchHits != 1 {
		t.Errorf("searchHits = %d, want 1 (second served from cache)", searchHits)
	}
	for i := 0; i < 2; i++ {
		if _, err := mb.Fetch(context.Background(), "rel-1"); err != nil {
			t.Fatalf("Fetch %d: %v", i+1, err)
		}
	}
	if fetchHits != 1 {
		t.Errorf("fetchHits = %d, want 1 (second served from cache)", fetchHits)
	}
}

func TestMusicBrainzTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		fmt.Fprint(w, mbSearchFixture)
	}))
	defer srv.Close()
	mb := newTestMB(t, srv.URL, nil, nil)
	mb.http = &http.Client{Timeout: 30 * time.Millisecond}
	if _, err := mb.Search(context.Background(), Query{Kind: "music", Title: "slow"}); err == nil {
		t.Fatal("want timeout error, got nil")
	}
}
