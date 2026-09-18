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
	"sync"
	"time"
)

// TheGamesDB provider (PLAN-GAMES G2). Key-gated like TMDB/ComicVine:
// LIBTECA_THEGAMESDB_KEY unset = disabled. Game results carry the platform
// tag in Extra["platform"] so matching can prefer a candidate released for
// a platform the library actually holds.

const (
	tgdbDefaultBase = "https://api.thegamesdb.net/v1"
	tgdbImageBase   = "https://cdn.thegamesdb.net/images/original"
	tgdbKeyEnv      = "LIBTECA_THEGAMESDB_KEY"
	tgdbTimeout     = 10 * time.Second
	tgdbMaxBody     = 8 << 20
	tgdbSearchLimit = 5
)

type TheGamesDB struct {
	base  string
	image string
	key   string
	http  *http.Client
}

var tgdbDisabledLogOnce sync.Once

// NewTheGamesDB returns nil when LIBTECA_THEGAMESDB_KEY is unset — the
// disabled signal for registration (logged once per process).
func NewTheGamesDB() *TheGamesDB {
	key := envKey(tgdbKeyEnv)
	if key == "" {
		tgdbDisabledLogOnce.Do(func() {
			log.Println("libteca: thegamesdb provider disabled: LIBTECA_THEGAMESDB_KEY not set")
		})
		return nil
	}
	return &TheGamesDB{base: tgdbDefaultBase, image: tgdbImageBase, key: key, http: &http.Client{Timeout: tgdbTimeout}}
}

func (p *TheGamesDB) Name() string { return "thegamesdb" }

// tgdbPlatformTag maps TheGamesDB platform names onto libteca's platform
// tags (the omilator-ported taxonomy). Unknown platforms stay unmapped and
// simply do not bias matching.
var tgdbPlatformTag = map[string]string{
	"Nintendo Entertainment System (NES)": "nes",
	"Super Nintendo (SNES)":               "snes",
	"Nintendo Game Boy":                   "gb",
	"Nintendo Game Boy Color":             "gbc",
	"Nintendo Game Boy Advance":           "gba",
	"Sega Mega Drive - Genesis":           "genesis",
	"Sega Genesis":                        "genesis",
	"Nintendo 64":                         "n64",
	"Sony Playstation":                    "psx",
	"Sony Playstation 2":                  "ps2",
	"Sony PSP":                            "psp",
	"Nintendo DS":                         "nds",
	"Nintendo 3DS":                        "n3ds",
	"Nintendo Gamecube":                   "gamecube",
	"Nintendo Wii":                        "wii",
	"Sega Dreamcast":                      "dreamcast",
	"Sega Saturn":                         "saturn",
}

type tgdbGamesResponse struct {
	Code   int    `json:"code"`
	Status string `json:"status"`
	Data   struct {
		Count int        `json:"count"`
		Games []tgdbGame `json:"games"`
		// include=platforms
		Platforms map[string]tgdbPlatform `json:"platforms"`
	} `json:"data"`
	Include tgdbIncludeJSON `json:"include"`
}

type tgdbIncludeJSON struct {
	Platform map[string]tgdbPlatform `json:"platform"`
}

type tgdbGame struct {
	ID          int    `json:"id"`
	GameTitle   string `json:"game_title"`
	ReleaseDate string `json:"release_date"`
	Platform    int    `json:"platform"`
	Players     int    `json:"players"`
	Overview    string `json:"overview"`
	Genres      []int  `json:"genres"`
}

type tgdbPlatform struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type tgdbImagesResponse struct {
	Code int `json:"code"`
	Data struct {
		BaseURL string      `json:"base_url"`
		Count   int         `json:"count"`
		Images  []tgdbImage `json:"images"`
	} `json:"data"`
}

type tgdbImage struct {
	Type string `json:"type"`
	// v1 returns either an id or a filename per image record.
	Filename string `json:"filename"`
}

func (p *TheGamesDB) Search(ctx context.Context, q Query) ([]Result, error) {
	if p == nil || p.key == "" {
		return nil, nil
	}
	if q.Kind != "game" {
		return nil, nil
	}
	if strings.TrimSpace(q.Title) == "" {
		return nil, fmt.Errorf("thegamesdb: empty title")
	}
	params := url.Values{}
	params.Set("name", q.Title)
	params.Set("include", "platforms")
	u := p.base + "/Games/ByGameName?" + params.Encode()
	sk := cacheKey("search", p.key, u)
	var cached []Result
	if CacheGetJSON(p.Name(), sk, &cached) {
		return cached, nil
	}
	var resp tgdbGamesResponse
	if err := p.getJSON(ctx, u, &resp); err != nil {
		return nil, err
	}
	out := make([]Result, 0, tgdbSearchLimit)
	for i := range resp.Data.Games {
		if len(out) >= tgdbSearchLimit {
			break
		}
		g := &resp.Data.Games[i]
		out = append(out, p.toResult(g, resp.platforms()))
	}
	_ = CachePut(p.Name(), sk, out)
	return out, nil
}

func (r tgdbGamesResponse) platforms() map[string]tgdbPlatform {
	if len(r.Include.Platform) > 0 {
		return r.Include.Platform
	}
	m := map[string]tgdbPlatform{}
	for k, v := range r.Data.Platforms {
		m[k] = v
	}
	return m
}

func (p *TheGamesDB) toResult(g *tgdbGame, plats map[string]tgdbPlatform) Result {
	res := Result{
		Provider:    p.Name(),
		ID:          strconv.Itoa(g.ID),
		Title:       g.GameTitle,
		Description: g.Overview,
		Extra:       map[string]string{},
	}
	if g.ReleaseDate != "" {
		if y, err := strconv.Atoi(strings.Split(g.ReleaseDate, "-")[0]); err == nil {
			res.Year = &y
		}
	}
	if plats != nil {
		if plat, ok := plats[strconv.Itoa(g.Platform)]; ok {
			if tag, ok := tgdbPlatformTag[plat.Name]; ok {
				res.Extra["platform"] = tag
				res.Extra["platform_name"] = plat.Name
			} else {
				res.Extra["platform_name"] = plat.Name
			}
		}
	}
	return res
}

func (p *TheGamesDB) Fetch(ctx context.Context, id string) (*Result, error) {
	if p == nil || p.key == "" {
		return nil, nil
	}
	sk := cacheKey("fetch", p.key, "id:"+id)
	var cached Result
	if CacheGetJSON(p.Name(), sk, &cached) {
		return &cached, nil
	}
	params := url.Values{}
	params.Set("id", id)
	params.Set("include", "platforms")
	u := p.base + "/Games/ByGameID?" + params.Encode()
	var resp tgdbGamesResponse
	if err := p.getJSON(ctx, u, &resp); err != nil {
		return nil, err
	}
	if len(resp.Data.Games) == 0 {
		return nil, fmt.Errorf("thegamesdb: no game %s", id)
	}
	res := p.toResult(&resp.Data.Games[0], resp.platforms())

	// Boxart is a second call; failures are non-fatal (cover simply absent).
	if img, err := p.frontCover(ctx, id); err == nil && img != "" {
		res.CoverURL = img
	}
	_ = CachePut(p.Name(), sk, res)
	return &res, nil
}

func (p *TheGamesDB) frontCover(ctx context.Context, id string) (string, error) {
	params := url.Values{}
	params.Set("games_id", id)
	params.Set("filter", "boxart")
	u := p.base + "/Games/Images?" + params.Encode()
	var resp tgdbImagesResponse
	if err := p.getJSON(ctx, u, &resp); err != nil {
		return "", err
	}
	base := resp.Data.BaseURL
	if base == "" {
		base = p.image
	}
	for _, img := range resp.Data.Images {
		if strings.EqualFold(img.Type, "boxart_front") && img.Filename != "" {
			return strings.TrimRight(base, "/") + "/" + img.Filename, nil
		}
	}
	return "", nil
}

func (p *TheGamesDB) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("x-api-key", p.key)
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("thegamesdb: %s: HTTP %d", u, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, tgdbMaxBody)).Decode(out); err != nil {
		return err
	}
	if resp2, ok := out.(*tgdbGamesResponse); ok && resp2.Code != 200 && resp2.Status != "Success" && resp2.Code != 0 {
		return fmt.Errorf("thegamesdb: api code %d (%s)", resp2.Code, resp2.Status)
	}
	return nil
}
