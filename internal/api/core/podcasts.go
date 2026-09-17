package core

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/podcast"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

const maxOPMLFeeds = 200

// MountPodcasts registers the podcast endpoints. Add this one line to
// API.Mount in core.go:
//
//	a.MountPodcasts(r)
func (a *API) MountPodcasts(r *neutron.Router) {
	if a.Podcasts == nil {
		a.Podcasts = podcast.New(a.DB, a.DataDir)
	}
	svc := a.Podcasts
	r.HandleFunc("POST /podcasts", func(w http.ResponseWriter, req *http.Request) { a.podcastSubscribe(svc, w, req) })
	r.HandleFunc("GET /podcasts", a.podcastList)
	r.HandleFunc("GET /podcasts/export-opml", a.podcastExportOPML)
	r.HandleFunc("POST /podcasts/import-opml", a.podcastImportOPML)
	r.HandleFunc("GET /podcasts/import-opml/status", a.podcastImportOPMLStatus)
	r.HandleFunc("GET /podcasts/episodes/{epId}/stream", a.podcastEpisodeStream)
	r.HandleFunc("POST /podcasts/episodes/{epId}/progress", a.podcastEpisodeProgress)
	r.HandleFunc("GET /podcasts/{id}", a.podcastDetail)
	r.HandleFunc("POST /podcasts/{id}/refresh", func(w http.ResponseWriter, req *http.Request) { a.podcastRefresh(svc, w, req) })
	r.HandleFunc("PATCH /podcasts/{id}", a.podcastPatch)
	r.HandleFunc("DELETE /podcasts/{id}", func(w http.ResponseWriter, req *http.Request) { a.podcastDelete(svc, w, req) })
}

func (a *API) podcastSubscribe(svc *podcast.Service, w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	var body struct {
		FeedURL      string `json:"feedUrl"`
		AutoDownload *bool  `json:"autoDownload"`
		MaxEpisodes  *int   `json:"maxEpisodes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.FeedURL == "" {
		writeJSON(w, 400, map[string]string{"error": "feedUrl required"})
		return
	}
	if !absoluteHTTPURL(body.FeedURL) {
		writeJSON(w, 400, map[string]string{"error": "feedUrl must be an absolute http(s) URL"})
		return
	}
	autoDownload := true
	if body.AutoDownload != nil {
		autoDownload = *body.AutoDownload
	}
	maxEpisodes := 3
	if body.MaxEpisodes != nil {
		if *body.MaxEpisodes < 1 || *body.MaxEpisodes > 1000 {
			writeJSON(w, 400, map[string]string{"error": "maxEpisodes must be 1-1000"})
			return
		}
		maxEpisodes = *body.MaxEpisodes
	}
	p, err := svc.Subscribe(r.Context(), body.FeedURL, autoDownload, maxEpisodes)
	if errors.Is(err, podcast.ErrDuplicateFeed) {
		writeJSON(w, 409, map[string]any{"error": "already subscribed", "podcastId": p.ID})
		return
	}
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "feed subscribe failed"})
		return
	}
	a.writePodcastDetail(w, r, 201, p)
}

func (a *API) podcastList(w http.ResponseWriter, r *http.Request) {
	podcasts, err := a.DB.PodcastsWithCounts()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	out := make([]map[string]any, 0, len(podcasts))
	for i := range podcasts {
		out = append(out, podcastJSON(&podcasts[i].Podcast, podcasts[i].EpisodeCount, podcasts[i].DownloadedCount))
	}
	writeJSON(w, 200, out)
}

func (a *API) podcastDetail(w http.ResponseWriter, r *http.Request) {
	p, err := a.DB.Podcast(auth.Atoi64(r.PathValue("id")))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "podcast not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	a.writePodcastDetail(w, r, 200, p)
}

func (a *API) writePodcastDetail(w http.ResponseWriter, r *http.Request, status int, p *store.Podcast) {
	writeJSON(w, status, a.podcastDetailBody(p, auth.UserID(r)))
}

func (a *API) podcastDetailBody(p *store.Podcast, userID int64) map[string]any {
	eps, err := a.DB.PodcastEpisodes(p.ID)
	if err != nil {
		return map[string]any{"error": "internal error"}
	}
	progs, _ := a.DB.EpisodeProgressByPodcast(userID, p.ID)
	downloaded := 0
	epJSON := make([]map[string]any, 0, len(eps))
	for i := range eps {
		if eps[i].FileID != nil {
			downloaded++
		}
		epJSON = append(epJSON, episodeJSON(&eps[i], progs[eps[i].ID]))
	}
	body := podcastJSON(p, len(eps), downloaded)
	body["episodes"] = epJSON
	return body
}

func (a *API) podcastRefresh(svc *podcast.Service, w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	p, changed, err := svc.RefreshPodcast(r.Context(), auth.Atoi64(r.PathValue("id")))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "podcast not found"})
		return
	}
	if errors.Is(err, podcast.ErrRefreshBusy) {
		writeJSON(w, 409, map[string]string{"error": "refresh already running"})
		return
	}
	if err != nil {
		writeJSON(w, 502, map[string]string{"error": "feed refresh failed"})
		return
	}
	body := a.podcastDetailBody(p, auth.UserID(r))
	body["changed"] = changed
	writeJSON(w, 200, body)
}

func (a *API) podcastPatch(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	id := auth.Atoi64(r.PathValue("id"))
	if _, err := a.DB.Podcast(id); errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "podcast not found"})
		return
	}
	var body struct {
		AutoDownload *bool `json:"autoDownload"`
		MaxEpisodes  *int  `json:"maxEpisodes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || (body.AutoDownload == nil && body.MaxEpisodes == nil) {
		writeJSON(w, 400, map[string]string{"error": "autoDownload or maxEpisodes required"})
		return
	}
	if body.MaxEpisodes != nil && (*body.MaxEpisodes < 1 || *body.MaxEpisodes > 1000) {
		writeJSON(w, 400, map[string]string{"error": "maxEpisodes must be 1-1000"})
		return
	}
	if err := a.DB.UpdatePodcastSettings(id, body.AutoDownload, body.MaxEpisodes); err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	p, err := a.DB.Podcast(id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	a.writePodcastDetail(w, r, 200, p)
}

func (a *API) podcastDelete(svc *podcast.Service, w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	err := svc.DeletePodcast(auth.Atoi64(r.PathValue("id")))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "podcast not found"})
		return
	}
	if errors.Is(err, podcast.ErrRefreshBusy) {
		writeJSON(w, 409, map[string]string{"error": "podcast refresh or download in progress"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

type opmlImportStatus struct {
	Status     string `json:"status"`
	Added      int    `json:"added"`
	Failed     int    `json:"failed"`
	Total      int    `json:"total"`
	CurrentURL string `json:"currentUrl"`
}

type opmlRun struct {
	mu   sync.Mutex
	snap opmlImportStatus
}

func (run *opmlRun) update(s opmlImportStatus) {
	run.mu.Lock()
	run.snap = s
	run.mu.Unlock()
}

func (run *opmlRun) snapshot() opmlImportStatus {
	run.mu.Lock()
	defer run.mu.Unlock()
	return run.snap
}

// podcastImportOPML validates the outline and starts a background subscribe
// pass; progress is on GET /podcasts/import-opml/status.
func (a *API) podcastImportOPML(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	var body struct {
		OPML string `json:"opml"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.OPML) == "" {
		writeJSON(w, 400, map[string]string{"error": "opml required"})
		return
	}
	urls, err := podcast.ParseOPML([]byte(body.OPML))
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if len(urls) == 0 {
		writeJSON(w, 400, map[string]string{"error": "no feed urls found"})
		return
	}
	if len(urls) > maxOPMLFeeds {
		writeJSON(w, 400, map[string]any{"error": "too many feeds", "limit": maxOPMLFeeds})
		return
	}
	a.opmlMu.Lock()
	run := a.opmlRun
	if run != nil && run.snapshot().Status == "running" {
		a.opmlMu.Unlock()
		writeJSON(w, 409, map[string]any{"error": "import already running"})
		return
	}
	run = &opmlRun{snap: opmlImportStatus{Status: "running", Total: len(urls)}}
	a.opmlRun = run
	a.opmlMu.Unlock()
	if !a.launchJob(func() { a.runOPMLImport(context.WithoutCancel(r.Context()), run, urls) }) {
		a.opmlMu.Lock()
		if a.opmlRun == run {
			a.opmlRun = nil
		}
		a.opmlMu.Unlock()
		run.update(opmlImportStatus{Status: "error", Total: len(urls), Failed: len(urls)})
		writeJSON(w, 503, map[string]string{"error": "server shutting down"})
		return
	}
	writeJSON(w, 202, run.snapshot())
}

func (a *API) podcastImportOPMLStatus(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	a.opmlMu.Lock()
	run := a.opmlRun
	a.opmlMu.Unlock()
	if run == nil {
		writeJSON(w, 200, opmlImportStatus{Status: "idle"})
		return
	}
	writeJSON(w, 200, run.snapshot())
}

func (a *API) runOPMLImport(ctx context.Context, run *opmlRun, urls []string) {
	if a.Podcasts == nil {
		a.Podcasts = podcast.New(a.DB, a.DataDir)
	}
	added, failed := 0, 0
	for _, feedURL := range urls {
		run.update(opmlImportStatus{Status: "running", Added: added, Failed: failed, Total: len(urls), CurrentURL: feedURL})
		_, err := a.Podcasts.Subscribe(ctx, feedURL, true, 3)
		switch {
		case errors.Is(err, podcast.ErrDuplicateFeed):
		case err != nil:
			failed++
		default:
			added++
		}
		run.update(opmlImportStatus{Status: "running", Added: added, Failed: failed, Total: len(urls), CurrentURL: feedURL})
	}
	run.update(opmlImportStatus{Status: "done", Added: added, Failed: failed, Total: len(urls)})
}

func (a *API) podcastExportOPML(w http.ResponseWriter, r *http.Request) {
	podcasts, err := a.DB.Podcasts()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	data, err := podcast.BuildOPML("libteca podcasts", podcasts)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(200)
	w.Write(data)
}

var podcastStreamMIME = map[string]string{
	"mp3": "audio/mpeg", "m4a": "audio/mp4", "m4b": "audio/mp4", "mp4": "audio/mp4",
	"aac": "audio/aac", "ogg": "audio/ogg", "oga": "audio/ogg", "opus": "audio/opus",
	"wav": "audio/wav", "flac": "audio/flac",
}

func (a *API) podcastEpisodeStream(w http.ResponseWriter, r *http.Request) {
	ep, err := a.DB.EpisodeByID(auth.Atoi64(r.PathValue("epId")))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "episode not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	if ep.FileID == nil {
		writeJSON(w, 404, map[string]string{"error": "episode not downloaded"})
		return
	}
	path, err := a.DB.FilePath(*ep.FileID)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "file not found"})
		return
	}
	if ct := podcastStreamMIME[strings.TrimPrefix(filepath.Ext(path), ".")]; ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	serveFile(w, r, path)
}

func (a *API) podcastEpisodeProgress(w http.ResponseWriter, r *http.Request) {
	ep, err := a.DB.EpisodeByID(auth.Atoi64(r.PathValue("epId")))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "episode not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	var body struct {
		Position float64 `json:"position"`
		Duration float64 `json:"duration"`
		Finished *bool   `json:"finished"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid body"})
		return
	}
	if body.Position < 0 {
		writeJSON(w, 400, map[string]string{"error": "position must be >= 0"})
		return
	}
	p := &store.EpisodeProgress{
		UserID: auth.UserID(r), EpisodeID: ep.ID,
		PositionSecs: body.Position,
		IsFinished:   body.Finished != nil && *body.Finished,
	}
	dur := body.Duration
	if dur == 0 && ep.DurationSecs != nil {
		dur = *ep.DurationSecs
	}
	if dur > 0 {
		p.DurationSecs = &dur
		if body.Position >= dur-5 {
			p.IsFinished = true
		}
	}
	if err := a.DB.SetEpisodeProgress(p); err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	pct, pos, fin := episodeProgressView(ep, p)
	writeJSON(w, 200, map[string]any{"positionSecs": pos, "percent": pct, "isFinished": fin})
}

func absoluteHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func podcastJSON(p *store.Podcast, episodeCount, downloadedCount int) map[string]any {
	return map[string]any{
		"id": p.ID, "feedUrl": p.FeedURL, "title": p.Title, "author": p.Author,
		"description": p.Description, "hasCover": p.CoverPath != nil && *p.CoverPath != "",
		"coverUrl":     coverURL(p.CoverPath),
		"autoDownload": p.AutoDownload, "maxEpisodes": p.MaxEpisodes,
		"lastFetchAt": p.LastFetchAt, "createdAt": p.CreatedAt,
		"episodeCount": episodeCount, "downloadedCount": downloadedCount,
	}
}

func episodeJSON(e *store.PodcastEpisode, prog *store.EpisodeProgress) map[string]any {
	m := map[string]any{
		"id": e.ID, "podcastId": e.PodcastID, "guid": e.GUID, "title": e.Title,
		"description": e.Description, "pubDate": e.PubDate, "durationSecs": e.DurationSecs,
		"enclosureUrl": e.EnclosureURL, "enclosureBytes": e.EnclosureBytes,
		"downloadedAt": e.DownloadedAt, "hasFile": e.FileID != nil,
	}
	pct, pos, fin := episodeProgressView(e, prog)
	m["positionSecs"] = pos
	m["percent"] = pct
	m["isFinished"] = fin
	if e.FileID != nil {
		m["fileId"] = *e.FileID
		m["streamUrl"] = "/podcasts/episodes/" + strconv.FormatInt(e.ID, 10) + "/stream"
	}
	return m
}

func episodeProgressView(e *store.PodcastEpisode, prog *store.EpisodeProgress) (pct, pos float64, fin bool) {
	if prog == nil {
		return 0, 0, false
	}
	dur := 0.0
	if prog.DurationSecs != nil {
		dur = *prog.DurationSecs
	}
	if dur <= 0 && e.DurationSecs != nil {
		dur = *e.DurationSecs
	}
	if dur > 0 {
		pct = prog.PositionSecs / dur
		if pct > 1 {
			pct = 1
		}
	}
	return pct, prog.PositionSecs, prog.IsFinished
}

func coverURL(p *string) string {
	if p == nil || *p == "" {
		return ""
	}
	return "/api/core/covers/" + *p
}
