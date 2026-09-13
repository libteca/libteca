package meta

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestTheGamesDBDisabledWithoutKey(t *testing.T) {
	os.Setenv("LIBTECA_THEGAMESDB_KEY", "")
	defer os.Unsetenv("LIBTECA_THEGAMESDB_KEY")
	if p := NewTheGamesDB(); p != nil {
		t.Fatal("provider must be nil without a key")
	}
}

func TestTheGamesDBSearchAndFetch(t *testing.T) {
	var gotPath, gotKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		gotKey = r.Header.Get("x-api-key")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/Games/ByGameName"):
			json.NewEncoder(w).Encode(map[string]any{
				"code": 200, "status": "Success",
				"data": map[string]any{
					"count": 1,
					"games": []map[string]any{{
						"id": 123, "game_title": "Super Mario World",
						"release_date": "1990-11-21", "platform": 6,
						"overview": "Mario's SNES debut.",
					}},
					"platforms": map[string]any{"6": map[string]any{"id": 6, "name": "Super Nintendo (SNES)"}},
				},
			})
		case strings.Contains(r.URL.Path, "/Games/ByGameID"):
			json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"data": map[string]any{
					"count": 1,
					"games": []map[string]any{{
						"id": 123, "game_title": "Super Mario World",
						"release_date": "1990-11-21", "platform": 6,
						"overview": "Mario's SNES debut.",
					}},
				},
				"include": map[string]any{
					"platform": map[string]any{"6": map[string]any{"id": 6, "name": "Super Nintendo (SNES)"}},
				},
			})
		case strings.Contains(r.URL.Path, "/Games/Images"):
			json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"data": map[string]any{
					"base_url": "https://cdn.example/original/",
					"count":    1,
					"images":  []map[string]any{{"type": "boxart_front", "filename": "123.jpg"}},
				},
			})
		default:
			http.Error(w, "not found", 404)
		}
	}))
	defer srv.Close()

	p := &TheGamesDB{base: srv.URL, image: tgdbImageBase, key: "k", http: srv.Client()}
	_ = gotAuth

	res, err := p.Search(context.Background(), Query{Kind: "game", Title: "Super Mario World"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Title != "Super Mario World" {
		t.Fatalf("search = %+v", res)
	}
	if res[0].Extra["platform"] != "snes" {
		t.Fatalf("platform tag = %q, want snes", res[0].Extra["platform"])
	}
	if res[0].Year == nil || *res[0].Year != 1990 {
		t.Fatalf("year = %v", res[0].Year)
	}
	if !strings.Contains(gotPath, "ByGameName") || gotKey != "k" {
		t.Fatalf("request = %s key=%q", gotPath, gotKey)
	}

	// Non-game kinds are ignored.
	if out, err := p.Search(context.Background(), Query{Kind: "book", Title: "x"}); err != nil || out != nil {
		t.Fatalf("book search = %v, %v", out, err)
	}

	fetched, err := p.Fetch(context.Background(), "123")
	if err != nil {
		t.Fatal(err)
	}
	if fetched.CoverURL != "https://cdn.example/original/123.jpg" {
		t.Fatalf("cover = %q", fetched.CoverURL)
	}
	if fetched.Extra["platform"] != "snes" {
		t.Fatalf("fetch platform = %+v", fetched.Extra)
	}
}
