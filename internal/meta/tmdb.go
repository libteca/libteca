package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	tmdbDefaultBase = "https://api.themoviedb.org/3"
	tmdbImageBase   = "https://image.tmdb.org/t/p/w500"
	tmdbKeyEnv      = "LIBTECA_TMDB_KEY"
	tmdbTimeout     = 10 * time.Second
	tmdbMaxBody     = 8 << 20
	tmdbSearchLimit = 5
)

type TMDB struct {
	base  string
	image string
	key   string
	http  *http.Client
}

// NewTMDB returns nil when LIBTECA_TMDB_KEY is unset — the disabled signal
// for registration (logged once per process).
func NewTMDB() *TMDB {
	key := envKey(tmdbKeyEnv)
	if key == "" {
		tmdbDisabledLogOnce.Do(func() {
			log.Println("libteca: tmdb provider disabled: LIBTECA_TMDB_KEY not set")
		})
		return nil
	}
	return &TMDB{base: tmdbDefaultBase, image: tmdbImageBase, key: key, http: &http.Client{Timeout: tmdbTimeout}}
}

func (p *TMDB) Name() string { return "tmdb" }

// Search hits /search/{movie,tv}. Result IDs carry the kind prefix
// ("movie:123", "tv:45") so Fetch knows which detail endpoint to call.
func (p *TMDB) Search(ctx context.Context, q Query) ([]Result, error) {
	if p == nil || p.key == "" {
		return nil, nil
	}
	kind := q.Kind
	if kind == "" {
		kind = "movie"
	}
	if kind != "movie" && kind != "tv" {
		return nil, nil
	}
	if strings.TrimSpace(q.Title) == "" {
		return nil, fmt.Errorf("tmdb: empty title")
	}
	params := url.Values{}
	params.Set("query", q.Title)
	if q.Year != nil {
		if kind == "movie" {
			params.Set("year", strconv.Itoa(*q.Year))
		} else {
			params.Set("first_air_date_year", strconv.Itoa(*q.Year))
		}
	}
	u := p.base + "/search/" + kind + "?" + params.Encode()
	var cached []Result
	if CacheGetJSON(p.Name(), "search:"+u, &cached) {
		return cached, nil
	}
	var resp tmdbSearchResponse
	if err := p.getJSON(ctx, u, &resp); err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(resp.Results))
	for _, it := range resp.Results {
		if len(results) >= tmdbSearchLimit {
			break
		}
		results = append(results, it.toResult(kind, p.image))
	}
	CachePut(p.Name(), "search:"+u, results)
	return results, nil
}

func (p *TMDB) Fetch(ctx context.Context, id string) (*Result, error) {
	if p == nil || p.key == "" {
		return nil, nil
	}
	kind, num, err := tmdbParseID(id)
	if err != nil {
		return nil, err
	}
	var cached Result
	if CacheGetJSON(p.Name(), "fetch:"+p.base+"|"+id, &cached) {
		return &cached, nil
	}
	u := p.base + "/" + kind + "/" + num
	var it tmdbItem
	if err := p.getJSON(ctx, u, &it); err != nil {
		return nil, err
	}
	res := it.toResult(kind, p.image)
	res.ID = id
	CachePut(p.Name(), "fetch:"+p.base+"|"+id, res)
	return &res, nil
}

func (p *TMDB) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Authorization", "Bearer "+p.key)
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("tmdb: HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, tmdbMaxBody)).Decode(out)
}

func tmdbParseID(id string) (string, string, error) {
	id = strings.TrimSpace(id)
	kind, num, ok := strings.Cut(id, ":")
	if !ok || (kind != "movie" && kind != "tv") || num == "" {
		return "", "", fmt.Errorf("tmdb: bad id %q", id)
	}
	return kind, num, nil
}

func (it tmdbItem) toResult(kind, imageBase string) Result {
	title, date := it.Title, it.ReleaseDate
	if kind == "tv" {
		title, date = it.Name, it.FirstAirDate
	}
	res := Result{
		Provider:    "tmdb",
		ID:          kind + ":" + strconv.FormatInt(it.ID, 10),
		Title:       title,
		Description: it.Overview,
		Year:        yearFromDate(date),
		Rating:      it.VoteAverage,
	}
	if it.PosterPath != "" {
		res.CoverURL = imageBase + it.PosterPath
	}
	for _, g := range it.Genres {
		if g.Name != "" {
			res.Genres = append(res.Genres, g.Name)
		}
	}
	return res
}

// corpus: TMDB /3/search — results[] with id, title (movies) or name (tv),
// overview, release_date / first_air_date "YYYY-MM-DD", poster_path,
// vote_average 0-10. Shapes per developers.themoviedb.org reference.
type tmdbSearchResponse struct {
	Page    int        `json:"page"`
	Results []tmdbItem `json:"results"`
}

// corpus: TMDB /3/{movie,tv}/{id} detail — search fields plus genres[]
// {id,name} (search results omit genres).
type tmdbItem struct {
	ID           int64       `json:"id"`
	Title        string      `json:"title"`
	Name         string      `json:"name"`
	Overview     string      `json:"overview"`
	ReleaseDate  string      `json:"release_date"`
	FirstAirDate string      `json:"first_air_date"`
	PosterPath   string      `json:"poster_path"`
	VoteAverage  float64     `json:"vote_average"`
	Genres       []tmdbGenre `json:"genres"`
}

type tmdbGenre struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}
