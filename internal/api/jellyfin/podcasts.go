package jellyfin

import (
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-dev/neutron-go/neutron"
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
// MediaSources pointing at the stream route below. Episode progress is not
// persisted (podcast episodes are not editions; no progress column).
func (a *API) MountPodcasts(g *neutron.Router) {
	g.HandleFunc("GET /Items", a.podcastItems)
	g.HandleFunc("GET /Audio/podcast/{id}/stream", a.podcastEpisodeStream)
}

func (a *API) podcastItems(w http.ResponseWriter, r *http.Request) {
	parent := r.URL.Query().Get("ParentId")
	limit, _ := strconv.Atoi(r.URL.Query().Get("Limit"))
	start, _ := strconv.Atoi(r.URL.Query().Get("StartIndex"))
	items := []map[string]any{}

	if m := reLibItem.FindStringSubmatch(parent); m != nil {
		lib, err := a.DB.Library(auth.Atoi64(m[1]))
		if err == nil && lib.Type == "podcasts" {
			podcasts, _ := a.DB.Podcasts()
			for i := range podcasts {
				if podcasts[i].LibraryID == lib.ID {
					items = append(items, a.podcastShowItem(&podcasts[i]))
				}
			}
		}
	} else if m := rePodItem.FindStringSubmatch(parent); m != nil {
		p, err := a.DB.Podcast(auth.Atoi64(m[1]))
		if err == nil {
			eps, _ := a.DB.PodcastEpisodes(p.ID)
			for i := range eps {
				items = append(items, a.podcastEpisodeItem(p, &eps[i], r))
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
func (a *API) podcastShowItem(p *store.Podcast) map[string]any {
	counts, _ := a.DB.PodcastsWithCounts()
	episodeCount := 0
	for i := range counts {
		if counts[i].ID == p.ID {
			episodeCount = counts[i].EpisodeCount
		}
	}
	return map[string]any{
		"Id": "pod" + strconv.FormatInt(p.ID, 10), "Name": p.Title,
		"Type": "AudioPodcast", "ServerId": "libteca-server",
		"ChildCount": episodeCount, "IsFolder": true, "LocationType": "FileSystem",
		"UserData": map[string]any{"PlaybackPositionTicks": 0, "IsPlayed": false, "PlayCount": 0},
		"__sort":   strings.ToLower(p.Title), "__created": p.CreatedAt,
	}
}

// corpus: episode surfaced as Type "Audio" (AudioPodcastEpisode is not a real Jellyfin type); MediaSources/TranscodingUrl wiring unverified against recorded traffic
func (a *API) podcastEpisodeItem(p *store.Podcast, e *store.PodcastEpisode, r *http.Request) map[string]any {
	title := ""
	if e.Title != nil {
		title = *e.Title
	}
	rt := 0.0
	if e.DurationSecs != nil {
		rt = *e.DurationSecs
	}
	epID := "pe" + strconv.FormatInt(e.ID, 10)
	it := map[string]any{
		"Id": epID, "Name": title, "Type": "Audio",
		"SeriesId": "pod" + strconv.FormatInt(p.ID, 10), "SeriesName": p.Title,
		"ParentIndexNumber": 1,
		"RunTimeTicks":      ticks(rt),
		"ServerId":          "libteca-server",
		"UserData":          map[string]any{"PlaybackPositionTicks": 0, "IsPlayed": false, "PlayCount": 0},
		"__sort":            title, "__created": e.CreatedAt,
	}
	if e.PubDate != nil {
		it["PremiereDate"] = *e.PubDate
	}
	if e.FileID != nil {
		if path, err := a.DB.FilePath(*e.FileID); err == nil {
			url := "/Audio/podcast/" + epID + "/stream"
			if key := r.URL.Query().Get("api_key"); key != "" {
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
