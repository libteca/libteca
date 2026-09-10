package meta

import (
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
	olDefaultBase = "https://openlibrary.org"
	olCoverBase   = "https://covers.openlibrary.org/b/id"
	olSearchLimit = 5
	olTimeout     = 10 * time.Second
	olMaxBody     = 8 << 20
)

type OpenLibrary struct {
	base  string
	cover string
	http  *http.Client
}

func NewOpenLibrary() *OpenLibrary {
	return &OpenLibrary{
		base:  olDefaultBase,
		cover: olCoverBase,
		http:  &http.Client{Timeout: olTimeout},
	}
}

func (p *OpenLibrary) Name() string { return "openlibrary" }

func (p *OpenLibrary) Search(ctx context.Context, q Query) ([]Result, error) {
	if q.Kind != "" && q.Kind != "book" && q.Kind != "audiobook" {
		return nil, nil
	}
	params := url.Values{}
	if q.ISBN != "" {
		params.Set("q", q.ISBN)
	} else {
		if strings.TrimSpace(q.Title) == "" {
			return nil, fmt.Errorf("openlibrary: empty title")
		}
		params.Set("title", q.Title)
		if q.Author != "" {
			params.Set("author", q.Author)
		}
	}
	if q.Language != "" {
		params.Set("language", q.Language)
	}
	params.Set("limit", strconv.Itoa(olSearchLimit))
	u := p.base + "/search.json?" + params.Encode()
	var cached []Result
	if CacheGetJSON(p.Name(), "search:"+u, &cached) {
		return cached, nil
	}
	var resp olSearchResponse
	if err := p.getJSON(ctx, u, &resp); err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(resp.Docs))
	for _, d := range resp.Docs {
		if !strings.HasPrefix(d.Key, "/works/") {
			continue
		}
		res := Result{
			Provider:    p.Name(),
			ID:          strings.TrimPrefix(d.Key, "/works/"),
			Title:       d.Title,
			Author:      strings.Join(d.AuthorNames, ", "),
			Description: strings.Join(d.FirstSentence, " "),
			Year:        d.FirstPublishYear,
			Extra: map[string]string{
				"editionCount": strconv.Itoa(d.EditionCount),
			},
		}
		if d.CoverID != nil && *d.CoverID > 0 {
			res.CoverURL = p.cover + "/" + strconv.FormatInt(*d.CoverID, 10) + "-L.jpg"
		}
		n := len(d.Subject)
		if n > 4 {
			n = 4
		}
		res.Genres = d.Subject[:n]
		if len(d.Language) > 0 {
			res.Extra["language"] = d.Language[0]
		}
		results = append(results, res)
	}
	CachePut(p.Name(), "search:"+u, results)
	return results, nil
}

func (p *OpenLibrary) Fetch(ctx context.Context, id string) (*Result, error) {
	id = strings.TrimPrefix(strings.TrimSpace(id), "/works/")
	if id == "" {
		return nil, fmt.Errorf("openlibrary: empty id")
	}
	var fc *Result
	if CacheGetJSON(p.Name(), "fetch:"+p.base+"|"+id, &fc) {
		return fc, nil
	}
	var work olWork
	if err := p.getJSON(ctx, p.base+"/works/"+id+".json", &work); err != nil {
		return nil, err
	}
	pageCount := ""
	if editions, err := p.fetchEditions(ctx, id); err == nil && editions > 0 {
		pageCount = strconv.Itoa(editions)
	}
	res := &Result{
		Provider:    p.Name(),
		ID:          id,
		Title:       work.Title,
		Description: olDescription(work.Description),
		Year:        yearFromDate(work.FirstPublishDate),
		Genres:      work.Subjects,
		Extra:       map[string]string{},
	}
	if len(work.Authors) > 0 {
		names := make([]string, 0, len(work.Authors))
		for _, a := range work.Authors {
			if n := p.fetchAuthor(ctx, a.Author.Key); n != "" {
				names = append(names, n)
			}
		}
		res.Author = strings.Join(names, ", ")
	}
	if pageCount != "" {
		res.Extra["pageCount"] = pageCount
	}
	for _, c := range work.Covers {
		if c > 0 {
			res.CoverURL = p.cover + "/" + strconv.FormatInt(c, 10) + "-L.jpg"
			break
		}
	}
	CachePut(p.Name(), "fetch:"+p.base+"|"+id, res)
	return res, nil
}

// fetchAuthor resolves an /authors/{key} reference to a display name,
// personal_name first. Best-effort: failures yield "" and never fail the
// work fetch.
func (p *OpenLibrary) fetchAuthor(ctx context.Context, key string) string {
	key = strings.TrimPrefix(strings.TrimSpace(key), "/authors/")
	if key == "" {
		return ""
	}
	var a olAuthor
	if err := p.getJSON(ctx, p.base+"/authors/"+key+".json", &a); err != nil {
		return ""
	}
	if a.PersonalName != "" {
		return a.PersonalName
	}
	return a.Name
}

func (p *OpenLibrary) fetchEditions(ctx context.Context, id string) (int, error) {
	var editions olEditions
	if err := p.getJSON(ctx, p.base+"/works/"+id+"/editions.json", &editions); err != nil {
		return 0, err
	}
	for _, e := range editions.Entries {
		if e.PageCount != nil && *e.PageCount > 0 {
			return *e.PageCount, nil
		}
		if e.NumberOfPages != nil && *e.NumberOfPages > 0 {
			return *e.NumberOfPages, nil
		}
	}
	return 0, nil
}

func (p *OpenLibrary) getJSON(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", mbUserAgent)
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return fmt.Errorf("openlibrary: HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, olMaxBody)).Decode(out)
}

// olDescription tolerates OpenLibrary's two description shapes: plain string
// or {type: "/type/text", value: "..."}.
func olDescription(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var obj struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &obj) == nil {
		return obj.Value
	}
	return ""
}

// corpus: OpenLibrary search.json — docs[] with key "/works/OL..W",
// author_name[], first_sentence[] (array), first_publish_year, cover_i,
// subject[], edition_count. Shapes per openlibrary.org search API docs.
type olSearchResponse struct {
	NumFound int     `json:"numFound"`
	Docs     []olDoc `json:"docs"`
}

type olDoc struct {
	Key              string   `json:"key"`
	Title            string   `json:"title"`
	AuthorNames      []string `json:"author_name"`
	FirstSentence    []string `json:"first_sentence"`
	FirstPublishYear *int     `json:"first_publish_year"`
	CoverID          *int64   `json:"cover_i"`
	Language         []string `json:"language"`
	Subject          []string `json:"subject"`
	EditionCount     int      `json:"edition_count"`
}

// corpus: OpenLibrary works JSON — description string OR {type,value}; covers[]
// numeric (may contain -1 sentinels); authors[] carry {author:{key}} refs
// resolved via a separate /authors/{key}.json fetch (personal_name||name);
// subjects[] string array.
type olWork struct {
	Key              string          `json:"key"`
	Title            string          `json:"title"`
	Description      json.RawMessage `json:"description"`
	Covers           []int64         `json:"covers"`
	FirstPublishDate string          `json:"first_publish_date"`
	Subjects         []string        `json:"subjects"`
	Authors          []olWorkAuthor  `json:"authors"`
}

type olWorkAuthor struct {
	Author struct {
		Key string `json:"key"`
	} `json:"author"`
}

// corpus: OpenLibrary authors JSON — personal_name preferred over name.
type olAuthor struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	PersonalName string `json:"personal_name"`
}

// corpus: OpenLibrary editions.json — entries[] with page_count OR
// number_of_pages (field name varies by record age).
type olEditions struct {
	Entries []olEdition `json:"entries"`
}

type olEdition struct {
	Key            string `json:"key"`
	PageCount      *int   `json:"page_count"`
	NumberOfPages  *int   `json:"number_of_pages"`
	PublishDate    string `json:"publish_date"`
	PhysicalFormat string `json:"physical_format"`
}
