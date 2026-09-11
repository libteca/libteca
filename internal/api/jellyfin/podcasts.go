package jellyfin

import (
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

var rePodItem = regexp.MustCompile(`^pod(\d+)$`)
var rePodEpItem = regexp.MustCompile(`^pe(\d+)$`)

// MountPodcasts registers podcast surfacing for the Jellyfin face. Add this
// one line to (*API).Mount in jellyfin.go, after the g.HandleFunc block:
//
//	a.MountPodcasts(g)
//
// Id mapping: podcast shows are "pod{n}" and episodes "pe{n}", mirroring the
// w{n}/e{n}/lib{n} scheme (episodes must not collide with e{n} edition ids).
// /Users/{uid}/Items, /Shows/{seriesId}/Episodes and /Audio/{id}/{stream,universal}
// are registered by jellyfin.go against works and cannot serve podcasts
// without edits there (out of ownership), so podcast browsing rides
// GET /Items?ParentId= — a genuine Jellyfin endpoint — at both levels:
// ParentId=lib{n} yields shows, ParentId=pod{n} yields episodes with inline
// MediaSources pointing at the stream route below. Episode progress is
// persisted in podcast_episode_progress and surfaced as UserData on the
// episode items. POST /Sessions/Playing/Progress cannot be intercepted here:
// jellyfin.go owns that exact pattern (re-registering panics) and its
// resolvePlayable only knows w{n}/e{n}, so pe{n} progress reports posted
// there are silently dropped — writes go to POST /Audio/podcast/{id}/progress
// instead, and clients that only read item UserData still resume correctly.
func (a *API) MountPodcasts(g *neutron.Router) {
	g.HandleFunc("GET /Items", a.podcastItems)
	g.HandleFunc("GET /Audio/podcast/{id}/stream", a.podcastEpisodeStream)
	g.HandleFunc("POST /Audio/podcast/{id}/progress", a.podcastEpisodeProgress)
}

func (a *API) podcastItems(w http.ResponseWriter, r *http.Request) {
	parent := qget(r, "ParentId")
	limit, _ := strconv.Atoi(qget(r, "Limit"))
	start, _ := strconv.Atoi(qget(r, "StartIndex"))
	items := []map[string]any{}

	if m := reLibItem.FindStringSubmatch(parent); m != nil {
		lib, err := a.DB.Library(auth.Atoi64(m[1]))
		if err == nil && lib.Type == "podcasts" {
			podcasts, _ := a.DB.Podcasts()
			latest, _ := a.DB.LatestEpisodeProgressByPodcast(uid(r))
			for i := range podcasts {
				if podcasts[i].LibraryID == lib.ID {
					items = append(items, a.podcastShowItem(&podcasts[i], latest[podcasts[i].ID]))
				}
			}
		}
	} else if m := rePodItem.FindStringSubmatch(parent); m != nil {
		p, err := a.DB.Podcast(auth.Atoi64(m[1]))
		if err == nil {
			progs, _ := a.DB.EpisodeProgressByPodcast(uid(r), p.ID)
			eps, _ := a.DB.PodcastEpisodes(p.ID)
			for i := range eps {
				items = append(items, a.podcastEpisodeItem(p, &eps[i], r, progs[eps[i].ID]))
			}
		}
	}

	total := len(items)
	if start > total {
		start = total
	}
	if limit <= 0 {
		limit = total - start
	}
	end := start + limit
	if end > total {
		end = total
	}
	write(w, 200, map[string]any{"Items": items[start:end], "TotalRecordCount": total})
}

// corpus: Type "AudioPodcast" from Jellyfin's BaseItemKind; show item shape unverified against recorded traffic
func (a *API) podcastShowItem(p *store.Podcast, latest *store.EpisodeProgress) map[string]any {
	counts, _ := a.DB.PodcastsWithCounts()
	episodeCount := 0
	for i := range counts {
		if counts[i].ID == p.ID {
			episodeCount = counts[i].EpisodeCount
		}
	}
	// PlaybackPositionTicks carries the newest in-progress episode so clients
	// resuming the show land on the right position.
	ud := map[string]any{"PlaybackPositionTicks": 0, "IsPlayed": false, "PlayCount": 0}
	if latest != nil && !latest.IsFinished {
		ud["PlaybackPositionTicks"] = ticks(latest.PositionSecs)
	}
	return map[string]any{
		"Id": "pod" + strconv.FormatInt(p.ID, 10), "Name": p.Title,
		"Type": "AudioPodcast", "ServerId": "libteca-server",
		"ChildCount": episodeCount, "IsFolder": true, "LocationType": "FileSystem",
		"UserData": ud,
		"__sort":   strings.ToLower(p.Title), "__created": p.CreatedAt,
	}
}

// corpus: episode surfaced as Type "Audio" (AudioPodcastEpisode is not a real Jellyfin type); MediaSources/TranscodingUrl wiring unverified against recorded traffic
func (a *API) podcastEpisodeItem(p *store.Podcast, e *store.PodcastEpisode, r *http.Request, prog *store.EpisodeProgress) map[string]any {
	title := ""
	if e.Title != nil {
		title = *e.Title
	}
	rt := 0.0
	if e.DurationSecs != nil {
		rt = *e.DurationSecs
	}
	epID := "pe" + strconv.FormatInt(e.ID, 10)
	ud := map[string]any{"PlaybackPositionTicks": 0, "IsPlayed": false, "PlayCount": 0}
	if prog != nil {
		ud["PlaybackPositionTicks"] = ticks(prog.PositionSecs)
		ud["IsPlayed"] = prog.IsFinished
		if prog.IsFinished {
			ud["PlayCount"] = 1
		}
		if pct := episodePlayedPercent(e, prog); pct > 0 {
			ud["PlayedPercentage"] = pct
		}
	}
	it := map[string]any{
		"Id": epID, "Name": title, "Type": "Audio",
		"SeriesId": "pod" + strconv.FormatInt(p.ID, 10), "SeriesName": p.Title,
		"ParentIndexNumber": 1,
		"RunTimeTicks":      ticks(rt),
		"ServerId":          "libteca-server",
		"UserData":          ud,
		"__sort":            title, "__created": e.CreatedAt,
	}
	if e.PubDate != nil {
		it["PremiereDate"] = *e.PubDate
	}
	if e.FileID != nil {
		if path, err := a.DB.FilePath(*e.FileID); err == nil {
			url := "/Audio/podcast/" + epID + "/stream"
			if key := qget(r, "api_key"); key != "" {
				url += "?api_key=" + key
			}
			it["MediaSources"] = []any{map[string]any{
				"Id": epID, "Path": path, "Protocol": "File",
				"Container":          strings.TrimPrefix(filepath.Ext(path), "."),
				"SupportsDirectPlay": false, "SupportsDirectStream": false, "SupportsTranscoding": true,
				"TranscodingUrl": url,
				"MediaStreams":   []any{map[string]any{"Type": "Audio"}},
			}}
		}
	}
	return it
}

func (a *API) podcastEpisodeStream(w http.ResponseWriter, r *http.Request) {
	m := rePodEpItem.FindStringSubmatch(r.PathValue("id"))
	if m == nil {
		http.Error(w, "not found", 404)
		return
	}
	ep, err := a.DB.EpisodeByID(auth.Atoi64(m[1]))
	if errors.Is(err, store.ErrNotFound) || ep.FileID == nil {
		http.Error(w, "not found", 404)
		return
	}
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	path, err := a.DB.FilePath(*ep.FileID)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	serveFile(w, r, path)
}

func episodePlayedPercent(e *store.PodcastEpisode, p *store.EpisodeProgress) float64 {
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
	return p.PositionSecs / dur * 100
}

// podcastEpisodeProgress is the write side for pe{n} items: a Jellyfin-shaped
// body (ItemId/PositionTicks) upserted into podcast_episode_progress. See the
// MountPodcasts comment for why /Sessions/Playing/Progress cannot carry this.
// corpus: accepts the Progress-report body shape clients post to /Sessions/Playing/Progress
func (a *API) podcastEpisodeProgress(w http.ResponseWriter, r *http.Request) {
	m := rePodEpItem.FindStringSubmatch(r.PathValue("id"))
	if m == nil {
		http.Error(w, "not found", 404)
		return
	}
	ep, err := a.DB.EpisodeByID(auth.Atoi64(m[1]))
	if errors.Is(err, store.ErrNotFound) {
		http.Error(w, "not found", 404)
		return
	}
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	var body struct {
		ItemId        string `json:"ItemId"`
		PositionTicks int64  `json:"PositionTicks"`
		IsPaused      bool   `json:"IsPaused"`
		PlaySessionId string `json:"PlaySessionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		write(w, 400, map[string]any{"error": "invalid body"})
		return
	}
	pos := fromTicks(body.PositionTicks)
	p := &store.EpisodeProgress{
		UserID: uid(r), EpisodeID: ep.ID,
		PositionSecs: pos,
	}
	dur := 0.0
	if ep.DurationSecs != nil {
		dur = *ep.DurationSecs
	}
	if dur > 0 {
		p.DurationSecs = &dur
		if pos >= dur-5 {
			p.IsFinished = true
		}
	}
	if err := a.DB.SetEpisodeProgress(p); err != nil {
		write(w, 500, map[string]any{"error": err.Error()})
		return
	}
	write(w, 200, map[string]any{})
}
