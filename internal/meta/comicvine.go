package meta

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	cvDefaultBase = "https://comicvine.gamespot.com/api"
	cvSearchLimit = 5
	cvTimeout     = 10 * time.Second
	cvMaxBody     = 8 << 20
	cvKeyEnv      = "LIBTECA_COMICVINE_KEY"
	cvFieldList   = "id,name,description,start_year,image,publisher"
)

type ComicVine struct {
	base string
	key  string
	http *http.Client
}

// NewComicVine returns nil when LIBTECA_COMICVINE_KEY is unset — the disabled
// signal for registration.
func NewComicVine() *ComicVine {
	key := envKey(cvKeyEnv)
	if key == "" {
		return nil
	}
	return &ComicVine{
		base: cvDefaultBase,
		key:  key,
		http: &http.Client{Timeout: cvTimeout},
	}
}

func (p *ComicVine) Name() string { return "comicvine" }

func (p *ComicVine) Search(ctx context.Context, q Query) ([]Result, error) {
	if p == nil || p.key == "" {
		return nil, nil
	}
	if q.Kind != "" && q.Kind != "comic" {
		return nil, nil
	}
	if strings.TrimSpace(q.Title) == "" {
		return nil, fmt.Errorf("comicvine: empty title")
	}
	params := url.Values{}
	params.Set("api_key", p.key)
	params.Set("format", "json")
	params.Set("resources", "volume")
	params.Set("query", q.Title)
	params.Set("field_list", cvFieldList)
	params.Set("limit", strconv.Itoa(cvSearchLimit))
	u := p.base + "/search?" + params.Encode()
	var cached []Result
	if CacheGetJSON(p.Name(), "search:"+u, &cached) {
		return cached, nil
	}
	var volumes []cvVolume
	if err := p.get(ctx, u, &volumes); err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(volumes))
	for _, v := range volumes {
		results = append(results, v.toResult())
	}
	CachePut(p.Name(), "search:"+u, results)
	return results, nil
}

func (p *ComicVine) Fetch(ctx context.Context, id string) (*Result, error) {
	if p == nil || p.key == "" {
		return nil, nil
	}
	id = strings.TrimPrefix(strings.TrimSpace(id), "4050-")
	if id == "" {
		return nil, fmt.Errorf("comicvine: empty id")
	}
	var fc *Result
	if CacheGetJSON(p.Name(), "fetch:"+p.base+"|"+id, &fc) {
		return fc, nil
	}
	params := url.Values{}
	params.Set("api_key", p.key)
	params.Set("format", "json")
	u := p.base + "/volume/4050-" + id + "?" + params.Encode()
	var volume cvVolume
	if err := p.get(ctx, u, &volume); err != nil {
		return nil, err
	}
	res := volume.toResult()
	res.ID = id
	CachePut(p.Name(), "fetch:"+p.base+"|"+id, res)
	return &res, nil
}

func (p *ComicVine) get(ctx context.Context, u string, out any) error {
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
		return fmt.Errorf("comicvine: HTTP %d", resp.StatusCode)
	}
	// corpus: ComicVine envelope — error is the STRING "OK" on success (any
	// other value is a failure with numeric status_code); results is an array
	// for /search and a bare object for /volume/4050-{id} fetches.
	var envelope struct {
		Error      string          `json:"error"`
		StatusCode int             `json:"status_code"`
		Results    json.RawMessage `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, cvMaxBody)).Decode(&envelope); err != nil {
		return err
	}
	if envelope.Error != "OK" {
		return fmt.Errorf("comicvine: API error %q (status %d)", envelope.Error, envelope.StatusCode)
	}
	if len(envelope.Results) == 0 || string(bytes.TrimSpace(envelope.Results)) == "null" {
		return fmt.Errorf("comicvine: empty results")
	}
	return json.Unmarshal(envelope.Results, out)
}

func (v cvVolume) toResult() Result {
	res := Result{
		Provider:    "comicvine",
		ID:          strconv.FormatInt(v.ID, 10),
		Title:       v.Name,
		Description: v.Description,
		Year:        yearFromDate(v.StartYear.String()),
		CoverURL:    v.Image.cover(),
		Extra: map[string]string{
			"publisher": v.Publisher.name(),
		},
	}
	if v.StartYear != "" {
		res.Extra["startYear"] = v.StartYear.String()
	}
	return res
}

// corpus: ComicVine volume — id numeric, start_year arrives as string, number,
// or null depending on endpoint/record; publisher nested {id,name}; description
// is HTML.
type cvVolume struct {
	ID          int64        `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	StartYear   cvFlexString `json:"start_year"`
	Image       *cvImage     `json:"image"`
	Publisher   *cvPublisher `json:"publisher"`
}

type cvPublisher struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func (p *cvPublisher) name() string {
	if p == nil {
		return ""
	}
	return p.Name
}

// corpus: ComicVine image block — original_url largest; medium_url is the
// right size for covers.
type cvImage struct {
	IconURL     string `json:"icon_url"`
	ThumbURL    string `json:"thumb_url"`
	SmallURL    string `json:"small_url"`
	ScreenURL   string `json:"screen_url"`
	MediumURL   string `json:"medium_url"`
	SuperURL    string `json:"super_url"`
	OriginalURL string `json:"original_url"`
}

func (i *cvImage) cover() string {
	if i == nil {
		return ""
	}
	for _, u := range []string{i.MediumURL, i.SuperURL, i.ScreenURL, i.OriginalURL, i.SmallURL, i.ThumbURL} {
		if u != "" {
			return u
		}
	}
	return ""
}

// cvFlexString absorbs ComicVine's inconsistent scalar typing (string, number,
// null) without failing the surrounding decode.
type cvFlexString string

func (f cvFlexString) String() string { return string(f) }

func (f *cvFlexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*f = ""
		return nil
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		*f = cvFlexString(s)
		return nil
	}
	var n json.Number
	if json.Unmarshal(b, &n) == nil {
		*f = cvFlexString(n.String())
		return nil
	}
	*f = ""
	return nil
}
