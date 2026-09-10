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
	"sync"
	"time"
)

// LOCAL CACHE fallback (their cache.go not landed / helper not exported to us).
const (
	mbDefaultBase = "https://ws.audioscrobbler.com/ws/2"
	mbCoverBase   = "https://coverartarchive.org/release"
	mbUserAgent   = "libteca/0.1 (metadata)"
	mbSearchLimit = 5
	mbMinInterval = time.Second
	mbTimeout     = 10 * time.Second
	mbMaxBody     = 8 << 20
)

type MusicBrainz struct {
	base    string
	cover   string
	http    *http.Client
	limiter *mbLimiter
}

func NewMusicBrainz() *MusicBrainz {
	return &MusicBrainz{
		base:    mbDefaultBase,
		cover:   mbCoverBase,
		http:    &http.Client{Timeout: mbTimeout},
		limiter: newMBLimiter(time.Now, mbCtxSleep),
	}
}

func (p *MusicBrainz) Name() string { return "musicbrainz" }

func (p *MusicBrainz) Search(ctx context.Context, q Query) ([]Result, error) {
	if q.Kind != "" && q.Kind != "music" {
		return nil, nil
	}
	if strings.TrimSpace(q.Title) == "" {
		return nil, fmt.Errorf("musicbrainz: empty title")
	}
	query := `release:"` + mbSanitize(q.Title) + `"`
	if strings.TrimSpace(q.Author) != "" {
		query += ` AND artist:"` + mbSanitize(q.Author) + `"`
	}
	params := url.Values{}
	params.Set("query", query)
	params.Set("fmt", "json")
	params.Set("limit", strconv.Itoa(mbSearchLimit))
	u := p.base + "/release?" + params.Encode()
	var cached []Result
	if CacheGetJSON(p.Name(), "search:"+u, &cached) {
		return cached, nil
	}
	var resp mbSearchResponse
	if err := p.getJSON(ctx, u, &resp); err != nil {
		return nil, err
	}
	results := make([]Result, 0, len(resp.Releases))
	for _, r := range resp.Releases {
		res := Result{
			Provider:    p.Name(),
			ID:          r.ID,
			Title:       r.Title,
			Author:      mbJoinArtistCredit(r.ArtistCredit),
			Description: r.Disambiguation,
			Year:        yearFromDate(r.Date),
			CoverURL:    p.cover + "/" + r.ID + "/front-250",
			Extra: map[string]string{
				"status":  r.Status,
				"country": r.Country,
			},
		}
		if r.ReleaseGroup != nil {
			res.Extra["primaryType"] = r.ReleaseGroup.PrimaryType
		}
		results = append(results, res)
	}
	CachePut(p.Name(), "search:"+u, results)
	return results, nil
}

func (p *MusicBrainz) Fetch(ctx context.Context, id string) (*Result, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, fmt.Errorf("musicbrainz: empty id")
	}
	var fc *Result
	if CacheGetJSON(p.Name(), "fetch:"+p.base+"|"+id, &fc) {
		return fc, nil
	}
	params := url.Values{}
	params.Set("inc", "recordings+artist-credits+release-groups")
	params.Set("fmt", "json")
	u := p.base + "/release/" + id + "?" + params.Encode()
	var resp mbFetchResponse
	if err := p.getJSON(ctx, u, &resp); err != nil {
		return nil, err
	}
	media := append([]mbMedia(nil), resp.Media...)
	sort.Slice(media, func(i, j int) bool { return media[i].Position < media[j].Position })
	chapters, trackCount, formats := mbChapters(media)
	res := &Result{
		Provider:    p.Name(),
		ID:          resp.ID,
		Title:       resp.Title,
		Author:      mbJoinArtistCredit(resp.ArtistCredit),
		Description: resp.Disambiguation,
		Year:        yearFromDate(resp.Date),
		CoverURL:    p.cover + "/" + id + "/front-250",
		Genres:      mbGenreNames(resp.Genres),
		Chapters:    chapters,
		Extra: map[string]string{
			"status":     resp.Status,
			"country":    resp.Country,
			"format":     formats,
			"trackCount": strconv.Itoa(trackCount),
		},
	}
	CachePut(p.Name(), "fetch:"+p.base+"|"+id, res)
	return res, nil
}

func (p *MusicBrainz) getJSON(ctx context.Context, u string, out any) error {
	if err := p.limiter.wait(ctx); err != nil {
		return err
	}
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
		return fmt.Errorf("musicbrainz: HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, mbMaxBody)).Decode(out)
}

func mbSanitize(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), `"`, "")
}

func mbJoinArtistCredit(credits []mbArtistCredit) string {
	var sb strings.Builder
	for _, c := range credits {
		sb.WriteString(c.Name)
		sb.WriteString(c.JoinPhrase)
	}
	return sb.String()
}

func mbGenreNames(genres []mbGenre) []string {
	if len(genres) == 0 {
		return nil
	}
	out := make([]string, 0, len(genres))
	for _, g := range genres {
		if g.Name != "" {
			out = append(out, g.Name)
		}
	}
	return out
}

// mbChapters turns media track lists into cumulative Chapters (music
// "chapters" = tracks). Track length is ms, on track or recording.
func mbChapters(media []mbMedia) ([]Chapter, int, string) {
	var chapters []Chapter
	var pos float64
	trackCount := 0
	formatSet := map[string]bool{}
	var formats []string
	for _, m := range media {
		if m.Format != "" && !formatSet[m.Format] {
			formatSet[m.Format] = true
			formats = append(formats, m.Format)
		}
		for _, t := range m.Tracks {
			trackCount++
			title := t.Title
			if title == "" && t.Recording != nil {
				title = t.Recording.Title
			}
			ms := 0
			if t.Length != nil {
				ms = *t.Length
			} else if t.Recording != nil && t.Recording.Length != nil {
				ms = *t.Recording.Length
			}
			dur := float64(ms) / 1000
			chapters = append(chapters, Chapter{Title: title, StartSec: pos, EndSec: pos + dur})
			pos += dur
		}
	}
	return chapters, trackCount, strings.Join(formats, "/")
}

func yearFromDate(s string) *int {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return nil
	}
	y, err := strconv.Atoi(s[:4])
	if err != nil || y < 0 {
		return nil
	}
	return &y
}

// mbLimiter enforces MusicBrainz's 1 req/sec policy with a mutex timestamp
// guard. now and delay are injectable so tests can assert spacing without
// sleeping.
type mbLimiter struct {
	mu       sync.Mutex
	now      func() time.Time
	delay    func(ctx context.Context, d time.Duration) error
	interval time.Duration
	last     time.Time
}

func newMBLimiter(now func() time.Time, delay func(ctx context.Context, d time.Duration) error) *mbLimiter {
	return &mbLimiter{now: now, delay: delay, interval: mbMinInterval}
}

func (l *mbLimiter) wait(ctx context.Context) error {
	l.mu.Lock()
	var sleep time.Duration
	if !l.last.IsZero() {
		if elapsed := l.now().Sub(l.last); elapsed < l.interval {
			sleep = l.interval - elapsed
		}
	}
	if sleep > 0 {
		l.last = l.now().Add(sleep)
	} else {
		l.last = l.now()
	}
	l.mu.Unlock()
	if sleep <= 0 {
		return nil
	}
	return l.delay(ctx, sleep)
}

func mbCtxSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// corpus: MusicBrainz ws/2 search — releases[] with artist-credit[]
// (joinphrase trails each credit), date "YYYY[-MM[-DD]]", release-group,
// score. Shapes per MusicBrainz JSON web service docs.
type mbSearchResponse struct {
	Releases []mbRelease `json:"releases"`
}

// corpus: MusicBrainz ws/2 release lookup inc=recordings+artist-credits+
// release-groups — media[].tracks[] with length in ms (track or recording
// fallback); genres only present when the payload carries them.
type mbFetchResponse struct {
	mbRelease
	Media  []mbMedia `json:"media"`
	Genres []mbGenre `json:"genres"`
}

type mbRelease struct {
	ID             string           `json:"id"`
	Title          string           `json:"title"`
	Date           string           `json:"date"`
	Disambiguation string           `json:"disambiguation"`
	Country        string           `json:"country"`
	Status         string           `json:"status"`
	ArtistCredit   []mbArtistCredit `json:"artist-credit"`
	ReleaseGroup   *mbReleaseGroup  `json:"release-group"`
}

type mbArtistCredit struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
}

type mbReleaseGroup struct {
	PrimaryType    string   `json:"primary-type"`
	SecondaryTypes []string `json:"secondary-types"`
}

type mbMedia struct {
	Position int       `json:"position"`
	Format   string    `json:"format"`
	Tracks   []mbTrack `json:"tracks"`
}

type mbTrack struct {
	Number    string       `json:"number"`
	Title     string       `json:"title"`
	Length    *int         `json:"length"`
	Recording *mbRecording `json:"recording"`
}

type mbRecording struct {
	Title  string `json:"title"`
	Length *int   `json:"length"`
}

type mbGenre struct {
	Name string `json:"name"`
}
