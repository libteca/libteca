package abs

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron-go/neutron"
)

// MountPodcasts registers the ABS podcast endpoints. Add this one line to
// (*API).Mount in abs.go, after the existing routes:
//
//	a.MountPodcasts(r)
//
// Id mapping: podcast items carry the raw podcast row id (as a string);
// episodes are namespaced "pe-{id}" so they can never collide with the
// edition ids abs.go resolves /me/progress/{itemId} and /items/{itemId}
// against. Progress: podcast episodes are not editions (their file rows have
// edition_id NULL), so episode progress lives in its own table and is served
// here. /api/me/progress/pe-* itself cannot be routed from this file: the
// segment "pe-{id}" is not a valid ServeMux wildcard (wildcards must span a
// whole segment) and "/me/progress/{itemId}" already belongs to abs.go, so a
// second registration panics — the pe- progress endpoints live under
// /podcasts/episodes/{epId}/progress with the same payload shape abs.go's
// book progress uses.
func (a *API) MountPodcasts(r *neutron.Router) {
	r.HandleFunc("GET /libraries/{id}/podcasts", a.libraryPodcasts)
	r.HandleFunc("GET /podcasts/{id}", a.podcastDetail)
	r.HandleFunc("GET /podcasts/{id}/cover", a.podcastCover)
	r.HandleFunc("GET /podcasts/{id}/episodes", a.podcastEpisodes)
	r.HandleFunc("GET /podcasts/episodes/{epId}/file", a.podcastEpisodeFile)
	r.HandleFunc("GET /podcasts/episodes/{epId}/progress", a.podcastEpisodeProgressGet)
	r.HandleFunc("POST /podcasts/episodes/{epId}/progress", a.podcastEpisodeProgressPost)
}

func (a *API) libraryPodcasts(w http.ResponseWriter, r *http.Request) {
	libID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	podcasts, err := a.DB.Podcasts()
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	limit := intQuery(r, "limit", 20)
	page := intQuery(r, "page", 0)
	results := make([]map[string]any, 0, len(podcasts))
	for i := range podcasts {
		if podcasts[i].LibraryID != libID {
			continue
		}
		results = append(results, a.podcastItemPayload(&podcasts[i]))
	}
	total := len(results)
	start, end := pageWindow(page, limit, total)
	write(w, 200, map[string]any{
		"results": results[start:end], "total": total, "limit": limit, "page": page,
		"sortBy": "media.metadata.title", "sortDesc": false, "filterBy": "all",
		"minified": false, "collapseSeries": false,
	})
}

func (a *API) podcastDetail(w http.ResponseWriter, r *http.Request) {
	p, err := a.resolvePodcast(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		fail(w, 404, "Podcast not found")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	body := a.podcastItemPayload(p)
	eps, err := a.DB.PodcastEpisodes(p.ID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	progs, _ := a.DB.EpisodeProgressByPodcast(auth.UserID(r), p.ID)
	epJSON := make([]map[string]any, 0, len(eps))
	for i := range eps {
		epJSON = append(epJSON, a.episodePayload(&eps[i], progs[eps[i].ID]))
	}
	media := body["media"].(map[string]any)
	metadata := media["metadata"].(map[string]any)
	metadata["feedUrl"] = p.FeedURL
	if p.Description != nil {
		metadata["description"] = *p.Description
	}
	media["episodes"] = epJSON
	write(w, 200, body)
}

func (a *API) podcastEpisodes(w http.ResponseWriter, r *http.Request) {
	p, err := a.resolvePodcast(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		fail(w, 404, "Podcast not found")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	eps, err := a.DB.PodcastEpisodes(p.ID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	progs, _ := a.DB.EpisodeProgressByPodcast(auth.UserID(r), p.ID)
	limit := intQuery(r, "limit", 20)
	page := intQuery(r, "page", 0)
	results := make([]map[string]any, 0, len(eps))
	for i := range eps {
		results = append(results, a.episodePayload(&eps[i], progs[eps[i].ID]))
	}
	total := len(results)
	start, end := pageWindow(page, limit, total)
	write(w, 200, map[string]any{
		"results": results[start:end], "total": total, "limit": limit, "page": page,
	})
}

func (a *API) podcastEpisodeFile(w http.ResponseWriter, r *http.Request) {
	ep, err := a.resolvePodcastEpisode(r.PathValue("epId"))
	if errors.Is(err, store.ErrNotFound) {
		fail(w, 404, "Episode not found")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if ep.FileID == nil {
		fail(w, 404, "Episode not downloaded")
		return
	}
	path, err := a.DB.FilePath(*ep.FileID)
	if err != nil {
		fail(w, 404, "File not found")
		return
	}
	serveAudio(w, r, path)
}

// episodeProgressPayload mirrors abs.go's progressPayload field-for-field
// (currentTime, progress 0-1, isFinished, lastUpdate, serverTime, ...) with
// the pe-{id} episode id in libraryItemId; p == nil yields the zero-progress
// shape abs.go's getProgress returns for editions without a row.
// corpus: fields matched against abs.go progressPayload, unverified against recorded traffic
func (a *API) episodeProgressPayload(userID int64, e *store.PodcastEpisode, p *store.EpisodeProgress) map[string]any {
	dur := 0.0
	if e.DurationSecs != nil {
		dur = *e.DurationSecs
	}
	pos, fin := 0.0, false
	lastUpdate := time.Now().UnixMilli()
	if p != nil {
		if p.DurationSecs != nil && *p.DurationSecs > 0 {
			dur = *p.DurationSecs
		}
		pos, fin, lastUpdate = p.PositionSecs, p.IsFinished, p.UpdatedAt
	}
	frac := 0.0
	if dur > 0 {
		frac = pos / dur
	}
	now := time.Now().UnixMilli()
	return map[string]any{
		"id":            "u-" + strconv.FormatInt(userID, 10) + "-pe-" + strconv.FormatInt(e.ID, 10),
		"userId":        "u-" + strconv.FormatInt(userID, 10),
		"libraryItemId": "pe-" + strconv.FormatInt(e.ID, 10),
		"duration":      dur, "durationTimeSeconds": int64(dur),
		"progress": frac, "currentTime": pos,
		"isFinished": fin, "lastUpdate": lastUpdate,
		"createdAt": lastUpdate, "serverTime": now,
	}
}

func (a *API) podcastEpisodeProgressGet(w http.ResponseWriter, r *http.Request) {
	ep, err := a.resolvePodcastEpisode(r.PathValue("epId"))
	if errors.Is(err, store.ErrNotFound) {
		fail(w, 404, "Episode not found")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	p, _ := a.DB.GetEpisodeProgress(auth.UserID(r), ep.ID)
	write(w, 200, a.episodeProgressPayload(auth.UserID(r), ep, p))
}

func (a *API) podcastEpisodeProgressPost(w http.ResponseWriter, r *http.Request) {
	ep, err := a.resolvePodcastEpisode(r.PathValue("epId"))
	if errors.Is(err, store.ErrNotFound) {
		fail(w, 404, "Episode not found")
		return
	}
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	var body struct {
		CurrentTime  float64 `json:"currentTime"`
		TimeListened float64 `json:"timeListened"`
		Duration     float64 `json:"duration"`
		Progress     float64 `json:"progress"`
		IsFinished   bool    `json:"isFinished"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, 400, "Invalid body")
		return
	}
	position := body.CurrentTime
	if body.Progress > 0 && body.CurrentTime == 0 && body.Duration > 0 {
		position = body.Progress * body.Duration
	}
	p := &store.EpisodeProgress{
		UserID: auth.UserID(r), EpisodeID: ep.ID,
		PositionSecs: position, IsFinished: body.IsFinished,
	}
	dur := body.Duration
	if dur == 0 && ep.DurationSecs != nil {
		dur = *ep.DurationSecs
	}
	if dur > 0 {
		p.DurationSecs = &dur
		if position >= dur-5 {
			p.IsFinished = true
		}
	}
	if err := a.DB.SetEpisodeProgress(p); err != nil {
		fail(w, 500, err.Error())
		return
	}
	write(w, 200, a.episodeProgressPayload(auth.UserID(r), ep, p))
}

func (a *API) podcastCover(w http.ResponseWriter, r *http.Request) {
	p, err := a.resolvePodcast(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if p.CoverPath == nil || *p.CoverPath == "" {
		http.Error(w, "no cover", 404)
		return
	}
	f, err := os.Open(filepath.Join(a.DataDir, "covers", filepath.Base(*p.CoverPath)))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

func (a *API) resolvePodcast(id string) (*store.Podcast, error) {
	pid, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return nil, store.ErrNotFound
	}
	return a.DB.Podcast(pid)
}

func (a *API) resolvePodcastEpisode(id string) (*store.PodcastEpisode, error) {
	eid, err := strconv.ParseInt(strings.TrimPrefix(id, "pe-"), 10, 64)
	if err != nil {
		return nil, store.ErrNotFound
	}
	return a.DB.EpisodeByID(eid)
}

// corpus: libraryItem shape for podcasts built from ABS client code knowledge, unverified against recorded traffic
func (a *API) podcastItemPayload(p *store.Podcast) map[string]any {
	counts, _ := a.DB.PodcastsWithCounts()
	episodeCount := 0
	for i := range counts {
		if counts[i].ID == p.ID {
			episodeCount = counts[i].EpisodeCount
		}
	}
	metadata := map[string]any{"title": p.Title}
	if p.Author != nil {
		metadata["author"] = *p.Author
	}
	updatedAt := p.CreatedAt
	if p.LastFetchAt != nil {
		updatedAt = *p.LastFetchAt
	}
	return map[string]any{
		"id": strconv.FormatInt(p.ID, 10), "ino": strconv.FormatInt(p.ID, 10),
		"libraryId": strconv.FormatInt(p.LibraryID, 10),
		"mediaType": "podcast",
		"media": map[string]any{
			"id":          strconv.FormatInt(p.ID, 10),
			"metadata":    metadata,
			"numEpisodes": episodeCount,
			"coverPath":   "/api/podcasts/" + strconv.FormatInt(p.ID, 10) + "/cover",
			"tags":        []string{},
		},
		"addedAt": p.CreatedAt, "updatedAt": updatedAt,
	}
}

// corpus: episode shape (id/index/enclosure/audioFile.progress) built from ABS client code knowledge, unverified against recorded traffic
func (a *API) episodePayload(e *store.PodcastEpisode, prog *store.EpisodeProgress) map[string]any {
	title := ""
	if e.Title != nil {
		title = *e.Title
	}
	m := map[string]any{
		"id":      "pe-" + strconv.FormatInt(e.ID, 10),
		"title":   title,
		"pubDate": e.PubDate, "addedAt": e.CreatedAt,
		"enclosure": map[string]any{"url": e.EnclosureURL, "length": e.EnclosureBytes},
		"progress":  nil,
	}
	if prog != nil {
		// corpus: inline per-user episode progress object, unverified against recorded traffic
		m["progress"] = map[string]any{
			"id":            "pe-" + strconv.FormatInt(e.ID, 10),
			"userId":        "u-" + strconv.FormatInt(prog.UserID, 10),
			"libraryItemId": "pe-" + strconv.FormatInt(e.ID, 10),
			"currentTime":   prog.PositionSecs,
			"isFinished":    prog.IsFinished,
			"lastUpdate":    prog.UpdatedAt,
			"progress":      episodeProgressFrac(e, prog),
		}
	}
	if e.Description != nil {
		m["description"] = *e.Description
	}
	if e.DurationSecs != nil {
		m["duration"] = *e.DurationSecs
	}
	if e.FileID != nil {
		path, err := a.DB.FilePath(*e.FileID)
		if err == nil {
			var size int64
			if fi, err := os.Stat(path); err == nil {
				size = fi.Size()
			}
			m["audioFile"] = map[string]any{
				"ino": strconv.FormatInt(*e.FileID, 10),
				// corpus: contentUrl on the episode audioFile is a guess; ABS serves episode bytes under /api/items/{itemId}/file/{ino}
				"contentUrl": "/api/podcasts/episodes/pe-" + strconv.FormatInt(e.ID, 10) + "/file",
				"duration":   e.DurationSecs, "size": size,
				"mimeType": "audio/" + strings.TrimPrefix(filepath.Ext(path), "."),
				"metadata": map[string]any{"filename": filepath.Base(path), "ext": strings.TrimPrefix(filepath.Ext(path), ".")},
			}
		}
	}
	return m
}

func episodeProgressFrac(e *store.PodcastEpisode, p *store.EpisodeProgress) float64 {
	dur := 0.0
	if p.DurationSecs != nil {
		dur = *p.DurationSecs
	}
	if dur <= 0 && e.DurationSecs != nil {
		dur = *e.DurationSecs
	}
	if dur <= 0 {
		return 0
	}
	return p.PositionSecs / dur
}

func pageWindow(page, limit, total int) (int, int) {
	start := page * limit
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	return start, end
}
