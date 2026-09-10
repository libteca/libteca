package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/meta"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-dev/neutron-go/neutron"
)

const (
	providerUA    = "libteca/0.1"
	coverMaxBytes = 8 << 20
	coverTimeout  = 10 * time.Second
	autoApplyMin  = 0.85
	skipProvider  = "match-skip"
)

// MountProviders wires the metadata matching endpoints. Add to API.Mount:
//
//	a.MountProviders(r)
func (a *API) MountProviders(r *neutron.Router) {
	r.HandleFunc("POST /works/{id}/match", a.matchWork)
	r.HandleFunc("POST /works/{id}/apply", a.applyMatch)
	r.HandleFunc("POST /works/{id}/skip", a.skipWork)
	r.HandleFunc("GET /matching/inbox", a.matchingInbox)
	r.HandleFunc("POST /libraries/{id}/refresh-meta", a.refreshMeta)
}

var (
	metaMu        sync.RWMutex
	metaProviders func() []meta.Provider
)

// SetMetaProviders overrides the provider set used for matching (tests).
func (a *API) SetMetaProviders(fn func() []meta.Provider) {
	metaMu.Lock()
	metaProviders = fn
	metaMu.Unlock()
}

func (a *API) metaProviders() []meta.Provider {
	metaMu.RLock()
	fn := metaProviders
	metaMu.RUnlock()
	if fn != nil {
		return fn()
	}
	meta.SetCacheStore(a.DB)
	return meta.Registry()
}

func kindForLibrary(t string) string {
	switch t {
	case "movies":
		return "movie"
	case "tv":
		return "tv"
	case "music":
		return "music"
	case "books":
		return "book"
	case "comics":
		return "comic"
	default:
		return "audiobook"
	}
}

type matchCandidate struct {
	Provider    string `json:"provider"`
	ID          string `json:"id"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	Year        *int   `json:"year"`
	Description string `json:"description"`
	CoverURL    string `json:"coverURL"`
}

// searchAll queries every provider for the kind; failures are counted so
// auto-apply can require a fully-successful sweep.
func (a *API) searchAll(ctx context.Context, q meta.Query) ([]matchCandidate, int) {
	var cands []matchCandidate
	failures := 0
	for _, p := range a.metaProviders() {
		res, err := p.Search(ctx, q)
		if err != nil {
			failures++
			continue
		}
		for _, m := range res {
			if m.Title == "" {
				continue
			}
			cands = append(cands, matchCandidate{
				Provider: m.Provider, ID: m.ID, Title: m.Title, Author: m.Author,
				Year: m.Year, Description: m.Description, CoverURL: m.CoverURL,
			})
		}
	}
	return cands, failures
}

func (a *API) fetchResult(ctx context.Context, providerName, id string) (*meta.Result, error) {
	for _, p := range a.metaProviders() {
		if p.Name() == providerName {
			return p.Fetch(ctx, id)
		}
	}
	return nil, fmt.Errorf("unknown provider %q", providerName)
}

func (a *API) matchWork(w http.ResponseWriter, r *http.Request) {
	id := auth.Atoi64(r.PathValue("id"))
	wv, err := a.DB.WorkByID(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "work not found"})
		return
	}
	lib, err := a.DB.Library(wv.LibraryID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	q := meta.Query{Kind: kindForLibrary(lib.Type), Title: wv.Title}
	if wv.Author != nil {
		q.Author = *wv.Author
	}
	var body struct {
		Title  string `json:"title"`
		Author string `json:"author"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
		if strings.TrimSpace(body.Title) != "" {
			q.Title = body.Title
		}
		if strings.TrimSpace(body.Author) != "" {
			q.Author = body.Author
		}
	}
	cands, _ := a.searchAll(r.Context(), q)
	if cands == nil {
		cands = []matchCandidate{}
	}
	writeJSON(w, 200, map[string]any{"workId": id, "kind": q.Kind, "candidates": cands})
}

type fileChapter struct {
	ID    int64   `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

func (a *API) applyMatch(w http.ResponseWriter, r *http.Request) {
	id := auth.Atoi64(r.PathValue("id"))
	wv, err := a.DB.WorkByID(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "work not found"})
		return
	}
	var body struct {
		Provider string `json:"provider"`
		ID       string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Provider == "" || body.ID == "" {
		writeJSON(w, 400, map[string]string{"error": "provider and id required"})
		return
	}
	lib, err := a.DB.Library(wv.LibraryID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	res, err := a.fetchResult(r.Context(), body.Provider, body.ID)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	summary, err := a.applyResult(wv, lib.Type, res)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "apply": summary})
}

// applyResult writes a fetched result into the work: description + provider
// identity, cover download (house convention: covers/{workID}.jpg), Audible
// chapters for audiobooks (only where empty), genres for movies/tv.
func (a *API) applyResult(w *store.Work, libType string, res *meta.Result) (map[string]any, error) {
	if err := a.DB.ApplyWorkMeta(w.ID, res.Description, res.Provider, res.ID); err != nil {
		return nil, err
	}
	summary := map[string]any{"cover": false, "chapters": int64(0), "genres": 0}
	if res.CoverURL != "" && (w.CoverPath == nil || *w.CoverPath == "") {
		if saved, err := a.downloadCover(w.ID, res.CoverURL); err == nil && saved {
			_ = a.DB.SetWorkCover(w.ID, fmt.Sprintf("%d.jpg", w.ID))
			summary["cover"] = true
		}
	}
	kind := kindForLibrary(libType)
	if kind == "audiobook" && len(res.Chapters) > 0 {
		chs := make([]fileChapter, len(res.Chapters))
		for i, c := range res.Chapters {
			chs[i] = fileChapter{ID: int64(i + 1), Start: c.StartSec, End: c.EndSec, Title: c.Title}
		}
		if b, err := json.Marshal(chs); err == nil {
			if n, err := a.DB.FillEmptyChapters(w.ID, string(b)); err == nil {
				summary["chapters"] = n
			}
		}
	}
	if (kind == "movie" || kind == "tv") && len(res.Genres) > 0 {
		if err := a.DB.SetWorkGenres(w.ID, res.Genres); err == nil {
			summary["genres"] = len(res.Genres)
		}
	}
	return summary, nil
}

func (a *API) downloadCover(workID int64, url string) (bool, error) {
	dir := filepath.Join(a.DataDir, "covers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	name := fmt.Sprintf("%d.jpg", workID)
	dst := filepath.Join(dir, name)
	if _, err := os.Stat(dst); err == nil {
		return false, nil
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", providerUA)
	client := &http.Client{Timeout: coverTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return false, fmt.Errorf("cover download: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, coverMaxBytes))
	if err != nil {
		return false, err
	}
	if len(data) == 0 {
		return false, fmt.Errorf("cover download: empty body")
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func (a *API) skipWork(w http.ResponseWriter, r *http.Request) {
	id := auth.Atoi64(r.PathValue("id"))
	if _, err := a.DB.WorkByID(id); err != nil {
		writeJSON(w, 404, map[string]string{"error": "work not found"})
		return
	}
	if err := a.DB.PutCached(skipProvider, strconv.FormatInt(id, 10), "{}"); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *API) matchingInbox(w http.ResponseWriter, r *http.Request) {
	works, err := a.DB.MatchingInbox(0)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(works))
	for _, iw := range works {
		out = append(out, map[string]any{
			"id": iw.ID, "libraryId": iw.LibraryID, "libraryName": iw.LibraryName,
			"libraryType": iw.LibraryType, "title": iw.Title, "author": iw.Author,
			"hasCover": iw.CoverPath != nil && *iw.CoverPath != "",
		})
	}
	writeJSON(w, 200, out)
}

// refreshMeta matches every inbox work in the library (works with a
// match-skip marker are already excluded by the inbox query). AUTO-APPLY only
// when every provider answered, exactly one candidate exists, and the title
// similarity is >= autoApplyMin; everything else stays in the inbox for
// manual review.
func (a *API) refreshMeta(w http.ResponseWriter, r *http.Request) {
	id := auth.Atoi64(r.PathValue("id"))
	if _, err := a.DB.Library(id); err != nil {
		writeJSON(w, 404, map[string]string{"error": "library not found"})
		return
	}
	inbox, err := a.DB.MatchingInbox(id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	var matched, applied int
	for i := range inbox {
		iw := &inbox[i]
		wv, err := a.DB.WorkByID(iw.ID)
		if err != nil {
			continue
		}
		q := meta.Query{Kind: kindForLibrary(iw.LibraryType), Title: iw.Title}
		if iw.Author != nil {
			q.Author = *iw.Author
		}
		matched++
		cands, failures := a.searchAll(r.Context(), q)
		if failures == 0 && len(cands) == 1 && titleSimilarity(iw.Title, cands[0].Title) >= autoApplyMin {
			if res, err := a.fetchResult(r.Context(), cands[0].Provider, cands[0].ID); err == nil {
				if _, err := a.applyResult(wv, iw.LibraryType, res); err == nil {
					applied++
				}
			}
		}
	}
	writeJSON(w, 200, map[string]any{"matched": matched, "autoApplied": applied})
}

// titleSimilarity is a case-insensitive Levenshtein ratio (1 = identical).
func titleSimilarity(a, b string) float64 {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 1
	}
	ar, br := []rune(a), []rune(b)
	la, lb := len(ar), len(br)
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, min(curr[j-1]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return 1 - float64(prev[lb])/float64(max(la, lb))
}
