package podcast

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const UserAgent = "libteca/0.1 (+https://libteca.com)"

const itunesNS = "http://www.itunes.com/dtds/podcast-1.0.dtd"

const feedLimit = 20 << 20
const coverLimit = 5 << 20

type Episode struct {
	GUID           string
	Title          string
	Description    string
	PubDateMs      int64 // 0 = unknown
	DurationSecs   float64
	EnclosureURL   string
	EnclosureBytes int64
	EnclosureType  string
}

type Feed struct {
	Title       string
	Author      string
	Description string
	ImageURL    string
	Episodes    []Episode
}

type Fetcher struct {
	Client *http.Client
}

// FetchFeed fetches a feed with conditional-request headers. changed is false
// on 304 (etag/lastModified then carry the previous validators back).
func (f *Fetcher) FetchFeed(ctx context.Context, url, etag, lastModified string) (feed *Feed, changed bool, outETag, outModified string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, etag, lastModified, err
	}
	req.Header.Set("User-Agent", UserAgent)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	if lastModified != "" {
		req.Header.Set("If-Modified-Since", lastModified)
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, false, etag, lastModified, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return nil, false, etag, lastModified, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, etag, lastModified, fmt.Errorf("feed fetch: %s", resp.Status)
	}
	feed, err = ParseRSS(io.LimitReader(resp.Body, feedLimit))
	if err != nil {
		return nil, false, "", "", err
	}
	return feed, true, resp.Header.Get("ETag"), resp.Header.Get("Last-Modified"), nil
}

// FetchBytes downloads a small auxiliary resource (covers) with a size cap.
func (f *Fetcher) FetchBytes(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

type rssRoot struct {
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title        string `xml:"title"`
	Author       string `xml:"author"`
	ItunesAuthor string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd author"`
	Description  string `xml:"description"`
	// One field catches both rss <image><url> and itunes <image href>:
	// encoding/xml matches un-namespaced tags in any namespace, so separate
	// fields for the same local name would be a struct conflict.
	Image struct {
		URL  string `xml:"url"`
		Href string `xml:"href,attr"`
	} `xml:"image"`
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	GUID           string `xml:"guid"`
	Title          string `xml:"title"`
	Description    string `xml:"description"`
	ItunesSummary  string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd summary"`
	PubDate        string `xml:"pubDate"`
	ItunesDuration string `xml:"http://www.itunes.com/dtds/podcast-1.0.dtd duration"`
	Enclosure      struct {
		URL    string `xml:"url,attr"`
		Length int64  `xml:"length,attr"`
		Type   string `xml:"type,attr"`
	} `xml:"enclosure"`
}

// ParseRSS is namespace-aware on the canonical itunes namespace and otherwise
// best-effort: items without an enclosure are skipped, missing guids fall back
// to the enclosure URL, unparseable dates/durations become zero.
func ParseRSS(r io.Reader) (*Feed, error) {
	var root rssRoot
	dec := xml.NewDecoder(r)
	dec.Strict = false
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("parse rss: %w", err)
	}
	ch := root.Channel
	feed := &Feed{
		Title:       strings.TrimSpace(ch.Title),
		Author:      strings.TrimSpace(firstNonEmpty(ch.ItunesAuthor, ch.Author)),
		Description: strings.TrimSpace(ch.Description),
		ImageURL:    strings.TrimSpace(firstNonEmpty(ch.Image.Href, ch.Image.URL)),
	}
	for _, it := range ch.Items {
		if it.Enclosure.URL == "" {
			continue
		}
		guid := strings.TrimSpace(it.GUID)
		if guid == "" {
			guid = it.Enclosure.URL
		}
		feed.Episodes = append(feed.Episodes, Episode{
			GUID:           guid,
			Title:          strings.TrimSpace(it.Title),
			Description:    strings.TrimSpace(firstNonEmpty(it.Description, it.ItunesSummary)),
			PubDateMs:      parseDateMs(it.PubDate),
			DurationSecs:   parseDurationSecs(it.ItunesDuration),
			EnclosureURL:   strings.TrimSpace(it.Enclosure.URL),
			EnclosureBytes: it.Enclosure.Length,
			EnclosureType:  strings.TrimSpace(it.Enclosure.Type),
		})
	}
	if feed.Title == "" {
		return nil, fmt.Errorf("parse rss: channel has no title")
	}
	return feed, nil
}

var dateLayouts = []string{
	time.RFC1123Z,
	time.RFC1123,
	time.RFC822Z,
	time.RFC822,
	time.RFC3339,
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04:05 MST",
	"Mon, 2 Jan 2006",
	"2 Jan 2006 15:04:05 -0700",
	"2 Jan 2006 15:04:05 MST",
	"2 Jan 2006 15:04:05",
	"2 Jan 2006",
	"January 2, 2006",
	"2006-01-02",
}

func parseDateMs(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}

// parseDurationSecs accepts "SS", "MM:SS", "HH:MM:SS" (fractional allowed).
func parseDurationSecs(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if strings.Contains(s, ":") {
		var total float64
		for _, part := range strings.Split(s, ":") {
			v, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
			if err != nil {
				return 0
			}
			total = total*60 + v
		}
		return total
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
