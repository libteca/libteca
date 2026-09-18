package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/meta"
	"github.com/libteca/libteca/internal/podcast"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
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
	r.HandleFunc("POST /works/{id}/apply-episodes", a.applyEpisodes)
	r.HandleFunc("POST /works/{id}/skip", a.skipWork)
	r.HandleFunc("GET /matching/inbox", a.matchingInbox)
	r.HandleFunc("POST /libraries/{id}/refresh-meta", a.refreshMeta)
	r.HandleFunc("GET /libraries/{id}/refresh-meta", a.refreshMetaStatus)
	r.HandleFunc("GET /libraries/{id}/refresh-meta/events", a.refreshMetaEvents)
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
	case "games":
		return "game"
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

// seasonEpisodesFetcher is the optional per-season capability providers
// need to serve apply-episodes (TMDb implements it).
type seasonEpisodesFetcher interface {
	FetchSeasonEpisodes(ctx context.Context, tvID string, season int) ([]meta.EpisodeInfo, error)
}

func (a *API) findSeasonFetcher(providerName string) (seasonEpisodesFetcher, error) {
	for _, p := range a.metaProviders() {
		if p.Name() == providerName {
			if sf, ok := p.(seasonEpisodesFetcher); ok {
				return sf, nil
			}
			return nil, fmt.Errorf("provider %q has no season support", providerName)
		}
	}
	return nil, fmt.Errorf("unknown provider %q", providerName)
}

// titleLooksLikeFilename reports whether an edition title is raw scan
// residue: no spaces plus dots/underscores (raw filename), or the scanner's
// file-derived fallback form "S01E02". Real episode titles ("The Beginning")
// never match.
func titleLooksLikeFilename(title string) bool {
	if strings.Contains(title, " ") {
		return false
	}
	if strings.Contains(title, ".") || strings.Contains(title, "_") {
		return true
	}
	return reScanFallbackTitle.MatchString(title)
}

var reScanFallbackTitle = regexp.MustCompile(`^[Ss]\d{2}[Ee]\d{2,3}$`)

// applyEpisodes: POST /works/{id}/apply-episodes {provider, id} — TV only.
// For every season present on the work's editions, fetches the season and
// fills titles that look like filenames (good titles are never clobbered);
// stores the season's first-episode description only when empty. Genres
// from the main Fetch are written best-effort.
func (a *API) applyEpisodes(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
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
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	if kindForLibrary(lib.Type) != "tv" {
		writeJSON(w, 400, map[string]string{"error": "not a tv work"})
		return
	}
	if res, err := a.fetchResult(r.Context(), body.Provider, body.ID); err == nil && len(res.Genres) > 0 {
		_ = a.DB.SetWorkGenres(id, res.Genres)
	}
	updated, skipped, err := a.applyEpisodesToWork(r.Context(), id, body.Provider, body.ID)
	if err != nil {
		writeJSON(w, 502, map[string]any{"error": err.Error(), "updated": updated, "skipped": skipped})
		return
	}
	writeJSON(w, 200, map[string]any{"updated": updated, "skipped": skipped})
}

func (a *API) applyEpisodesToWork(ctx context.Context, workID int64, provider, id string) (updated, skipped int, err error) {
	sf, err := a.findSeasonFetcher(provider)
	if err != nil {
		return 0, 0, err
	}
	editions, err := a.DB.WorkEpisodes(workID)
	if err != nil {
		return 0, 0, err
	}
	season := -1
	var byEp map[int]meta.EpisodeInfo
	first := true
	var failures []error
	for _, ed := range editions {
		if ed.SeasonNum != season {
			season = ed.SeasonNum
			first = true
			infos, ferr := sf.FetchSeasonEpisodes(ctx, id, season)
			if ferr != nil {
				return updated, skipped, ferr
			}
			byEp = make(map[int]meta.EpisodeInfo, len(infos))
			for _, info := range infos {
				byEp[info.Episode] = info
			}
		}
		changed := false
		if info, ok := byEp[ed.EpisodeNum]; ok {
			if info.Title != "" && titleLooksLikeFilename(ed.Title) && info.Title != ed.Title {
				if err := a.DB.SetEpisodeTitle(ed.ID, info.Title); err != nil {
					failures = append(failures, fmt.Errorf("title for edition %d: %w", ed.ID, err))
				} else {
					changed = true
				}
			}
			if first && info.Description != "" && (ed.Description == nil || *ed.Description == "") {
				if err := a.DB.SetEpisodeDescription(ed.ID, info.Description); err != nil {
					failures = append(failures, fmt.Errorf("description for edition %d: %w", ed.ID, err))
				} else {
					changed = true
				}
			}
		}
		if changed {
			updated++
		} else {
			skipped++
		}
		first = false
	}
	return updated, skipped, errors.Join(failures...)
}

func (a *API) matchWork(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	id := auth.Atoi64(r.PathValue("id"))
	wv, err := a.DB.WorkByID(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "work not found"})
		return
	}
	lib, err := a.DB.Library(wv.LibraryID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
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
	if !a.requireAdmin(w, r) {
		return
	}
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
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	res, err := a.fetchResult(r.Context(), body.Provider, body.ID)
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	summary, err := a.applyResult(r.Context(), wv, lib.Type, res)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "apply": summary})
}

// applyResult writes a fetched result into the work: description + provider
// identity, cover download (house convention: covers/{workID}.jpg), Audible
// chapters for audiobooks (only where empty/generic), genres for movies/tv.
func (a *API) applyResult(ctx context.Context, w *store.Work, libType string, res *meta.Result) (map[string]any, error) {
	if err := a.DB.ApplyWorkMeta(w.ID, res.Description, res.Provider, res.ID); err != nil {
		return nil, err
	}
	summary := map[string]any{"cover": false, "chapters": int64(0), "genres": 0, "episodes": 0}
	if res.CoverURL != "" && (w.CoverPath == nil || *w.CoverPath == "") {
		saved, cerr := a.downloadCover(ctx, w.ID, res.CoverURL)
		if cerr != nil {
			slog.Warn("libteca: metadata cover download failed", "work", w.ID)
			summary["coverWarning"] = "cover download failed"
		}
		if cerr == nil && saved {
			if lerr := a.DB.SetWorkCover(w.ID, fmt.Sprintf("%d.jpg", w.ID)); lerr != nil {
				return nil, fmt.Errorf("link cover to work: %w", lerr)
			}
			summary["cover"] = true
		}
	}
	kind := kindForLibrary(libType)
	if kind == "audiobook" && len(res.Chapters) > 0 {
		summary["chapters"] = a.applyChapters(w.ID, res.Chapters)
	}
	if (kind == "movie" || kind == "tv") && len(res.Genres) > 0 {
		if err := a.DB.SetWorkGenres(w.ID, res.Genres); err == nil {
			summary["genres"] = len(res.Genres)
		}
	}
	if kind == "tv" && res.Provider != "" && res.ID != "" {
		if n, _, err := a.applyEpisodesToWork(ctx, w.ID, res.Provider, res.ID); err == nil {
			summary["episodes"] = n
		}
	}
	return summary, nil
}

// applyChapters writes Audible chapters to the work's editions: single-file
// m4b editions get the full timeline via FillEmptyChapters (empty only);
// multi-file editions get distributeChapters. Returns files written.
func (a *API) applyChapters(workID int64, chapters []meta.Chapter) int64 {
	var written int64
	full := make([]fileChapter, len(chapters))
	for i, c := range chapters {
		full[i] = fileChapter{ID: int64(i + 1), Start: c.StartSec, End: c.EndSec, Title: c.Title}
	}
	if b, err := json.Marshal(full); err == nil {
		if n, err := a.DB.FillEmptyChapters(workID, string(b)); err == nil {
			written += n
		}
	}
	return written + a.distributeChapters(workID, chapters)
}

type chapterFileRow struct {
	id       int64
	path     string
	duration float64
	chapters string
}

// distributeChapters spreads provider chapters across the files of every
// multi-file edition of the work by cumulative duration. Per-file chapter
// JSON is written only where the existing chapters are empty or generic
// (filename-titled) — ffprobe titles are never clobbered.
func (a *API) distributeChapters(workID int64, chapters []meta.Chapter) int64 {
	rows, err := a.DB.Query(`SELECT f.id, f.edition_id, f.path, f.duration_secs, f.chapters
		FROM files f
		JOIN editions e ON e.id = f.edition_id
		WHERE e.work_id = ? AND f.missing = 0
		ORDER BY f.edition_id, f.seq, f.path`, workID)
	if err != nil {
		return 0
	}
	editions := map[int64][]chapterFileRow{}
	var order []int64
	for rows.Next() {
		var edID int64
		var f chapterFileRow
		if err := rows.Scan(&f.id, &edID, &f.path, &f.duration, &f.chapters); err != nil {
			rows.Close()
			return 0
		}
		if _, seen := editions[edID]; !seen {
			order = append(order, edID)
		}
		editions[edID] = append(editions[edID], f)
	}
	rows.Close()
	var written int64
	for _, edID := range order {
		files := editions[edID]
		if len(files) < 2 {
			continue
		}
		durations := make([]float64, len(files))
		for i, f := range files {
			durations[i] = f.duration
		}
		perFile := distributeChapterMath(durations, chapters)
		for i, f := range files {
			if len(perFile[i]) == 0 || !genericChapters(f.path, f.chapters) {
				continue
			}
			if b, err := json.Marshal(perFile[i]); err == nil {
				if _, err := a.DB.Exec(`UPDATE files SET chapters = ? WHERE id = ? AND missing = 0`, string(b), f.id); err == nil {
					written++
				}
			}
		}
	}
	return written
}

// distributeChapterMath assigns each chapter to the file whose cumulative
// duration range contains its start, converting start/end to file-relative
// coordinates clamped to that file's duration. Chapters starting at or past
// the edition total (or collapsing to nothing) are dropped.
func distributeChapterMath(durations []float64, chapters []meta.Chapter) [][]fileChapter {
	out := make([][]fileChapter, len(durations))
	for idx, c := range chapters {
		base := 0.0
		at, offset := -1, 0.0
		for i, d := range durations {
			if c.StartSec >= base && c.StartSec < base+d {
				at, offset = i, base
				break
			}
			base += d
		}
		if at < 0 || durations[at] <= 0 {
			continue
		}
		start := c.StartSec - offset
		end := c.EndSec - offset
		if end > durations[at] {
			end = durations[at]
		}
		if end <= start {
			continue
		}
		out[at] = append(out[at], fileChapter{ID: int64(idx + 1), Start: start, End: end, Title: c.Title})
	}
	return out
}

// genericChapters reports whether a file's stored chapters are safe to
// replace: empty or unparseable JSON, or titles that all equal the filename
// stem the scanner falls back to when a file has no embedded chapters.
func genericChapters(path, stored string) bool {
	stored = strings.TrimSpace(stored)
	if stored == "" || stored == "[]" {
		return true
	}
	var chs []fileChapter
	if json.Unmarshal([]byte(stored), &chs) != nil {
		return true
	}
	if len(chs) == 0 {
		return true
	}
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	for _, c := range chs {
		if c.Title != stem {
			return false
		}
	}
	return true
}

var coverHTTPClient = podcast.EgressGuardedClient(coverTimeout)

func (a *API) downloadCover(ctx context.Context, workID int64, url string) (bool, error) {
	dir := filepath.Join(a.DataDir, "covers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	name := fmt.Sprintf("%d.jpg", workID)
	dst := filepath.Join(dir, name)
	if _, err := os.Stat(dst); err == nil {
		return true, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("User-Agent", providerUA)
	resp, err := coverHTTPClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return false, fmt.Errorf("cover download: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, coverMaxBytes+1))
	if err != nil {
		return false, err
	}
	if int64(len(data)) > coverMaxBytes {
		return false, fmt.Errorf("cover download: body exceeds the %d-byte limit", coverMaxBytes)
	}
	if len(data) == 0 {
		return false, fmt.Errorf("cover download: empty body")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return false, fmt.Errorf("cover download: not a decodable image")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 16384 || cfg.Height > 16384 ||
		int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
		return false, fmt.Errorf("cover download: dimensions exceed budget")
	}
	if format != "jpeg" && format != "png" && format != "gif" {
		return false, fmt.Errorf("cover download: unsupported image format %q", format)
	}
	tmp, err := os.CreateTemp(dir, ".cover-*")
	if err != nil {
		return false, err
	}
	tmpName := tmp.Name()
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Close()
	} else {
		tmp.Close()
	}
	if werr != nil {
		os.Remove(tmpName)
		return false, werr
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return false, err
	}
	return true, nil
}

func (a *API) skipWork(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	id := auth.Atoi64(r.PathValue("id"))
	if _, err := a.DB.WorkByID(id); err != nil {
		writeJSON(w, 404, map[string]string{"error": "work not found"})
		return
	}
	if err := a.DB.PutCached(skipProvider, strconv.FormatInt(id, 10), "{}"); err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *API) matchingInbox(w http.ResponseWriter, r *http.Request) {
	limit := 500
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}
	works, err := a.DB.MatchingInbox(0, limit)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
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

type metaSnap struct {
	Status      string `json:"status"`
	Matched     int    `json:"matched"`
	AutoApplied int    `json:"autoApplied"`
	Total       int    `json:"total"`
	Error       string `json:"error,omitempty"`
}

type metaRun struct {
	mu     sync.Mutex
	snap   metaSnap
	subs   map[chan metaSnap]struct{}
	closed bool
}

func (r *metaRun) publish(s metaSnap) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snap = s
	for ch := range r.subs {
		select {
		case ch <- s:
		default:
		}
	}
}

func (r *metaRun) snapshot() metaSnap {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snap
}

func (r *metaRun) subscribe() chan metaSnap {
	ch := make(chan metaSnap, 8)
	r.mu.Lock()
	s := r.snap
	if r.closed {
		r.mu.Unlock()
		ch <- s
		close(ch)
		return ch
	}
	if r.subs == nil {
		r.subs = map[chan metaSnap]struct{}{}
	}
	r.subs[ch] = struct{}{}
	r.mu.Unlock()
	ch <- s
	return ch
}

func (r *metaRun) unsubscribe(ch chan metaSnap) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subs, ch)
}

func (r *metaRun) finish(s metaSnap) {
	r.mu.Lock()
	r.snap = s
	r.closed = true
	subs := r.subs
	r.subs = nil
	r.mu.Unlock()
	for ch := range subs {
		select {
		case ch <- s:
		default:
		}
		close(ch)
	}
}

// refreshMeta starts a background match pass over the library inbox.
// AUTO-APPLY only when every provider answered, exactly one candidate exists,
// and the title similarity is >= autoApplyMin. Returns 202 immediately;
// progress is on GET /libraries/{id}/refresh-meta and the SSE events route.
func (a *API) refreshMeta(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	id := auth.Atoi64(r.PathValue("id"))
	if _, err := a.DB.Library(id); err != nil {
		writeJSON(w, 404, map[string]string{"error": "library not found"})
		return
	}
	a.metaMu.Lock()
	if run := a.metaRuns[id]; run != nil {
		s := run.snapshot()
		if s.Status == "running" {
			a.metaMu.Unlock()
			writeJSON(w, 409, map[string]any{"status": "already_running", "matched": s.Matched, "autoApplied": s.AutoApplied, "total": s.Total})
			return
		}
	}
	run := &metaRun{snap: metaSnap{Status: "running"}}
	a.metaRuns[id] = run
	a.metaMu.Unlock()
	if !a.launchJob(func() { a.runRefreshMeta(context.WithoutCancel(r.Context()), id, run) }) {
		a.metaMu.Lock()
		if a.metaRuns[id] == run {
			delete(a.metaRuns, id)
		}
		a.metaMu.Unlock()
		run.finish(metaSnap{Status: "error", Error: "server shutting down"})
		writeJSON(w, 503, map[string]string{"error": "server shutting down"})
		return
	}
	writeJSON(w, 202, map[string]any{"status": "running", "matched": 0, "autoApplied": 0, "total": 0})
}

func (a *API) refreshMetaStatus(w http.ResponseWriter, r *http.Request) {
	id := auth.Atoi64(r.PathValue("id"))
	if _, err := a.DB.Library(id); err != nil {
		writeJSON(w, 404, map[string]string{"error": "library not found"})
		return
	}
	a.metaMu.Lock()
	run := a.metaRuns[id]
	a.metaMu.Unlock()
	if run == nil {
		writeJSON(w, 200, map[string]any{"status": "idle", "matched": 0, "autoApplied": 0, "total": 0})
		return
	}
	writeJSON(w, 200, run.snapshot())
}

func (a *API) refreshMetaEvents(w http.ResponseWriter, r *http.Request) {
	id := auth.Atoi64(r.PathValue("id"))
	if _, err := a.DB.Library(id); err != nil {
		writeJSON(w, 404, map[string]string{"error": "library not found"})
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no flush", 500)
		return
	}
	a.metaMu.Lock()
	run := a.metaRuns[id]
	a.metaMu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	if run == nil {
		fmt.Fprintf(w, "event: progress\ndata: {\"status\":\"idle\",\"matched\":0,\"autoApplied\":0,\"total\":0}\n\n")
		fl.Flush()
		return
	}
	ch := run.subscribe()
	defer run.unsubscribe(ch)
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		case s, ok := <-ch:
			if !ok {
				return
			}
			b, _ := json.Marshal(s)
			fmt.Fprintf(w, "event: progress\ndata: %s\n\n", b)
			fl.Flush()
			if s.Status != "running" {
				return
			}
		}
	}
}

func (a *API) runRefreshMeta(ctx context.Context, libID int64, run *metaRun) {
	inbox, err := a.DB.MatchingInbox(libID, 0)
	if err != nil {
		run.finish(metaSnap{Status: "error", Error: "internal error"})
		return
	}
	total := len(inbox)
	run.publish(metaSnap{Status: "running", Total: total})
	var matched, applied int
	for i := range inbox {
		if ctx.Err() != nil {
			run.finish(metaSnap{Status: "error", Matched: matched, AutoApplied: applied, Total: total, Error: "canceled"})
			return
		}
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
		run.publish(metaSnap{Status: "running", Matched: matched, AutoApplied: applied, Total: total})
		cands, failures := a.searchAll(ctx, q)
		if failures == 0 && len(cands) == 1 && titleSimilarity(iw.Title, cands[0].Title) >= autoApplyMin {
			if res, err := a.fetchResult(ctx, cands[0].Provider, cands[0].ID); err == nil {
				if _, err := a.applyResult(ctx, wv, iw.LibraryType, res); err == nil {
					applied++
					run.publish(metaSnap{Status: "running", Matched: matched, AutoApplied: applied, Total: total})
				}
			}
		}
	}
	run.finish(metaSnap{Status: "done", Matched: matched, AutoApplied: applied, Total: total})
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
