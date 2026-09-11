package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	audibleDefaultBase = "https://api.audible.com/1.0/catalog"
	audibleTimeout     = 10 * time.Second
	audibleMaxBody     = 8 << 20
	audibleSearchLimit = 5
)

// Audible uses the unofficial catalog API — no key, but no contract either;
// decode tolerantly and expect breakage.
type Audible struct {
	base string
	http *http.Client
}

func NewAudible() *Audible {
	return &Audible{base: audibleDefaultBase, http: &http.Client{Timeout: audibleTimeout}}
}

func (p *Audible) Name() string { return "audible" }

func (p *Audible) Search(ctx context.Context, q Query) ([]Result, error) {
	if q.Kind != "" && q.Kind != "audiobook" {
		return nil, nil
	}
	if strings.TrimSpace(q.Title) == "" {
		return nil, fmt.Errorf("audible: empty title")
	}
	params := url.Values{}
	params.Set("title", q.Title)
	if strings.TrimSpace(q.Author) != "" {
		params.Set("author", q.Author)
	}
	params.Set("response_groups", "media,product_desc,contributors")
	params.Set("num_results", strconv.Itoa(audibleSearchLimit))
	u := p.base + "/products?" + params.Encode()
	sk := cacheKey("search", "", u)
	var cached []Result
	if CacheGetJSON(p.Name(), sk, &cached) {
		return cached, nil
	}
	var resp auSearchResponse
	if err := p.getJSON(ctx, u, &resp); err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(resp.Products))
	for _, pr := range resp.Products {
		if pr.ASIN == "" || pr.Title == "" {
			continue
		}
		results = append(results, pr.toResult())
	}
	CachePut(p.Name(), sk, results)
	return results, nil
}

func (p *Audible) Fetch(ctx context.Context, id string) (*Result, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("audible: empty id")
	}
	fk := cacheKey("fetch", "", p.base+"|"+id)
	var cached Result
	if CacheGetJSON(p.Name(), fk, &cached) {
		return &cached, nil
	}
	params := url.Values{}
	params.Set("response_groups", "media,product_desc,contributors,chapter_info")
	u := p.base + "/products/" + id + "?" + params.Encode()
	var resp auDetailResponse
	if err := p.getJSON(ctx, u, &resp); err != nil {
		return nil, err
	}
	pr := resp.item()
	if pr == nil || pr.ASIN == "" {
		return nil, fmt.Errorf("audible: empty product for %q", id)
	}
	res := pr.toResult()
	if ci := pr.ChapterInfo; ci != nil {
		chapters := make([]Chapter, 0, len(ci.Chapters))
		for _, c := range ci.Chapters {
			chapters = append(chapters, Chapter{
				Title:    c.Title,
				StartSec: float64(c.StartOffsetMs) / 1000,
				EndSec:   float64(c.StartOffsetMs+c.StartDurationMs) / 1000,
			})
		}
		res.Chapters = chapters
	}
	CachePut(p.Name(), fk, res)
	return &res, nil
}

func (p *Audible) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("audible: HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, audibleMaxBody)).Decode(out)
}

func (pr auProduct) toResult() Result {
	res := Result{
		Provider:    "audible",
		ID:          pr.ASIN,
		Title:       pr.Title,
		Author:      auJoinNames(pr.Authors),
		Description: pr.PublisherSummary,
		Year:        yearFromDate(pr.ReleaseDate),
		CoverURL:    auCover(pr.ProductImages),
	}
	if n := auJoinNames(pr.Narrators); n != "" {
		res.Extra = map[string]string{"narrator": n}
	}
	return res
}

func auJoinNames(cs []auContributor) string {
	names := make([]string, 0, len(cs))
	for _, c := range cs {
		if c.Name != "" {
			names = append(names, c.Name)
		}
	}
	return strings.Join(names, ", ")
}

// auCover prefers the 500px image, then any present size deterministically.
func auCover(images map[string]string) string {
	if v, ok := images["500"]; ok && v != "" {
		return v
	}
	keys := make([]string, 0, len(images))
	for k, v := range images {
		if v != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		return images[k]
	}
	return ""
}

// corpus: Audible catalog /1.0/catalog/products search — products[] with
// asin, title, authors[]{name,asin}, narrators[]{name}, product_images
// (size -> URL, e.g. "500"), release_date "YYYY-MM-DD", publisher_summary
// (HTML). Unofficial API; shapes per audible-api docs.
type auSearchResponse struct {
	Products []auProduct `json:"products"`
}

// corpus: Audible /1.0/catalog/products/{asin} detail — the product object
// arrives under "item" (documented) or "product" (observed variant); both
// accepted. chapter_info only with response_groups=chapter_info.
type auDetailResponse struct {
	Item    *auProduct `json:"item"`
	Product *auProduct `json:"product"`
}

func (r auDetailResponse) item() *auProduct {
	if r.Item != nil {
		return r.Item
	}
	return r.Product
}

type auProduct struct {
	ASIN             string            `json:"asin"`
	Title            string            `json:"title"`
	Authors          []auContributor   `json:"authors"`
	Narrators        []auContributor   `json:"narrators"`
	PublisherSummary string            `json:"publisher_summary"`
	ReleaseDate      string            `json:"release_date"`
	ProductImages    map[string]string `json:"product_images"`
	ChapterInfo      *auChapterInfo    `json:"chapter_info"`
}

type auContributor struct {
	Name string `json:"name"`
	ASIN string `json:"asin"`
}

// corpus: chapter_info — runtime_length_ms; chapters[] carry title,
// start_offset_ms (offset from program start) and start_duration_ms; all
// offsets milliseconds.
type auChapterInfo struct {
	RuntimeLengthMs int64       `json:"runtime_length_ms"`
	Chapters        []auChapter `json:"chapters"`
}

type auChapter struct {
	Title           string `json:"title"`
	StartOffsetMs   int64  `json:"start_offset_ms"`
	StartDurationMs int64  `json:"start_duration_ms"`
}
