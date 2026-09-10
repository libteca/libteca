package abs

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-dev/neutron-go/neutron"
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
// edition_id NULL) and podcast_episodes has no progress column, so episode
// progress is not persisted — ABS stores it feed-position-based on the
// episode. /api/me/progress/pe-* falls through abs.go's edition resolver and
// 404s; apps keep local progress. No migrations added.
func (a *API) MountPodcasts(r *neutron.Router) {
	r.HandleFunc("GET /libraries/{id}/podcasts", a.libraryPodcasts)
	r.HandleFunc("GET /podcasts/{id}", a.podcastDetail)
	r.HandleFunc("GET /podcasts/{id}/cover", a.podcastCover)
	r.HandleFunc("GET /podcasts/{id}/episodes", a.podcastEpisodes)
	r.HandleFunc("GET /podcasts/episodes/{epId}/file", a.podcastEpisodeFile)
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
	epJSON := make([]map[string]any, 0, len(eps))
	for i := range eps {
		epJSON = append(epJSON, a.episodePayload(&eps[i]))
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
	limit := intQuery(r, "limit", 20)
	page := intQuery(r, "page", 0)
	results := make([]map[string]any, 0, len(eps))
	for i := range eps {
		results = append(results, a.episodePayload(&eps[i]))
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
	f, err := os.Open(filepath.Join(a.DataDir, "covers", *p.CoverPath))
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
func (a *API) episodePayload(e *store.PodcastEpisode) map[string]any {
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
