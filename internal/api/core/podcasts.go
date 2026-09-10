package core

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/podcast"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-dev/neutron-go/neutron"
)

const maxOPMLFeeds = 200

// MountPodcasts registers the podcast endpoints. Add this one line to
// API.Mount in core.go:
//
//	a.MountPodcasts(r)
func (a *API) MountPodcasts(r *neutron.Router) {
	svc := podcast.New(a.DB, a.DataDir)
	r.HandleFunc("POST /podcasts", func(w http.ResponseWriter, req *http.Request) { a.podcastSubscribe(svc, w, req) })
	r.HandleFunc("GET /podcasts", a.podcastList)
	r.HandleFunc("GET /podcasts/export-opml", a.podcastExportOPML)
	r.HandleFunc("POST /podcasts/import-opml", a.podcastImportOPML)
	r.HandleFunc("GET /podcasts/episodes/{epId}/stream", a.podcastEpisodeStream)
	r.HandleFunc("GET /podcasts/{id}", a.podcastDetail)
	r.HandleFunc("POST /podcasts/{id}/refresh", func(w http.ResponseWriter, req *http.Request) { a.podcastRefresh(svc, w, req) })
	r.HandleFunc("PATCH /podcasts/{id}", a.podcastPatch)
	r.HandleFunc("DELETE /podcasts/{id}", func(w http.ResponseWriter, req *http.Request) { a.podcastDelete(svc, w, req) })
}

func (a *API) podcastSubscribe(svc *podcast.Service, w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	a.writePodcastDetail(w, 201, p)
}

func (a *API) podcastList(w http.ResponseWriter, r *http.Request) {
	podcasts, err := a.DB.PodcastsWithCounts()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
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
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	a.writePodcastDetail(w, 200, p)
}

func (a *API) writePodcastDetail(w http.ResponseWriter, status int, p *store.Podcast) {
	writeJSON(w, status, a.podcastDetailBody(p))
}

func (a *API) podcastDetailBody(p *store.Podcast) map[string]any {
	eps, err := a.DB.PodcastEpisodes(p.ID)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	downloaded := 0
	epJSON := make([]map[string]any, 0, len(eps))
	for i := range eps {
		if eps[i].FileID != nil {
			downloaded++
		}
		epJSON = append(epJSON, episodeJSON(&eps[i]))
	}
	body := podcastJSON(p, len(eps), downloaded)
	body["episodes"] = epJSON
	return body
}

func (a *API) podcastRefresh(svc *podcast.Service, w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, 502, map[string]string{"error": err.Error()})
		return
	}
	body := a.podcastDetailBody(p)
	body["changed"] = changed
	writeJSON(w, 200, body)
}

func (a *API) podcastPatch(w http.ResponseWriter, r *http.Request) {
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
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	p, err := a.DB.Podcast(id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	a.writePodcastDetail(w, 200, p)
}

func (a *API) podcastDelete(svc *podcast.Service, w http.ResponseWriter, r *http.Request) {
	err := svc.DeletePodcast(auth.Atoi64(r.PathValue("id")))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "podcast not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) podcastImportOPML(w http.ResponseWriter, r *http.Request) {
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
	svc := podcast.New(a.DB, a.DataDir)
	type result struct {
		FeedURL   string `json:"feedUrl"`
		Status    string `json:"status"`
		PodcastID *int64 `json:"podcastId,omitempty"`
		Error     string `json:"error,omitempty"`
	}
	results := make([]result, 0, len(urls))
	subscribed, existed, failed := 0, 0, 0
	for _, feedURL := range urls {
		p, err := svc.Subscribe(r.Context(), feedURL, true, 3)
		switch {
		case errors.Is(err, podcast.ErrDuplicateFeed):
			existed++
			results = append(results, result{FeedURL: feedURL, Status: "exists", PodcastID: &p.ID})
		case err != nil:
			failed++
			results = append(results, result{FeedURL: feedURL, Status: "error", Error: err.Error()})
		default:
			subscribed++
			id := p.ID
			results = append(results, result{FeedURL: feedURL, Status: "subscribed", PodcastID: &id})
		}
	}
	writeJSON(w, 200, map[string]any{
		"results": results, "subscribed": subscribed, "exists": existed, "failed": failed,
	})
}

func (a *API) podcastExportOPML(w http.ResponseWriter, r *http.Request) {
	podcasts, err := a.DB.Podcasts()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	data, err := podcast.BuildOPML("libteca podcasts", podcasts)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
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
		writeJSON(w, 500, map[string]string{"error": err.Error()})
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

func episodeJSON(e *store.PodcastEpisode) map[string]any {
	m := map[string]any{
		"id": e.ID, "podcastId": e.PodcastID, "guid": e.GUID, "title": e.Title,
		"description": e.Description, "pubDate": e.PubDate, "durationSecs": e.DurationSecs,
		"enclosureUrl": e.EnclosureURL, "enclosureBytes": e.EnclosureBytes,
		"downloadedAt": e.DownloadedAt, "hasFile": e.FileID != nil,
	}
	if e.FileID != nil {
		m["fileId"] = *e.FileID
		m["streamUrl"] = "/api/core/podcasts/episodes/" + strconv.FormatInt(e.ID, 10) + "/stream"
	}
	return m
}

func coverURL(p *string) string {
	if p == nil || *p == "" {
		return ""
	}
	return "/api/core/covers/" + *p
}
