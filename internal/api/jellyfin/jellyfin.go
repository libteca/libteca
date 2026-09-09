package jellyfin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/libteca/libteca/internal/transcode"
	"github.com/neutron-dev/neutron-go/neutron"
)

type API struct {
	DB  *store.DB
	Dir string
	TC  *transcode.Manager
}

func New(db *store.DB, dataDir string, tc *transcode.Manager) *API {
	return &API{DB: db, Dir: dataDir, TC: tc}
}

func (a *API) Mount(r *neutron.Router) {
	r.HandleFunc("GET /System/Info/Public", a.systemInfoPublic)
	r.HandleFunc("POST /Users/AuthenticateByName", a.authenticate)
	g := r.Group("", jfAuth(a.DB))
	g.HandleFunc("GET /System/Configuration", a.configStub)
	g.HandleFunc("GET /branding/configuration", a.brandingStub)
	g.HandleFunc("GET /Users/{uid}/Views", a.views)
	g.HandleFunc("GET /Users/{uid}/Items", a.userItems)
	g.HandleFunc("GET /Users/{uid}/Items/Resume", a.resume)
	g.HandleFunc("GET /Users/{uid}/Items/{id}", a.itemDetail)
	g.HandleFunc("GET /Items/{id}/Images/{type}", a.image)
	g.HandleFunc("POST /Items/{id}/PlaybackInfo", a.playbackInfo)
	g.HandleFunc("GET /Videos/{id}/stream", a.videoStream)
	g.HandleFunc("GET /videos/{id}/main.m3u8", a.hlsMaster)
	g.HandleFunc("GET /videos/{id}/hls/{sid}/{file}", a.hlsSegment)
	g.HandleFunc("GET /Audio/{id}/universal", a.audioUniversal)
	g.HandleFunc("GET /Audio/{id}/stream", a.audioUniversal)
	g.HandleFunc("POST /Sessions/Playing", a.sessionPlaying)
	g.HandleFunc("POST /Sessions/Playing/Progress", a.sessionProgress)
	g.HandleFunc("POST /Sessions/Playing/Stopped", a.sessionStopped)
	g.HandleFunc("GET /Sessions", a.sessionsList)
	g.HandleFunc("GET /Shows/NextUp", a.nextUp)
	g.HandleFunc("GET /Shows/{seriesId}/Seasons", a.seasons)
	g.HandleFunc("GET /Shows/{seriesId}/Episodes", a.episodes)
	g.HandleFunc("GET /Items/{id}/Ancestors", a.ancestors)
}

func jfAuth(db *store.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.Header.Get("X-Emby-Token")
			if token == "" {
				m := regexp.MustCompile(`Token="([^"]+)"`).FindStringSubmatch(r.Header.Get("Authorization"))
				if m != nil {
					token = m[1]
				}
			}
			if token == "" {
				token = r.URL.Query().Get("api_key")
			}
			if token == "" || r.URL.Path == "/System/Info/Public" {
				if r.URL.Path == "/System/Info/Public" {
					next.ServeHTTP(w, r)
					return
				}
			}
			if token != "" {
				if user, ok := auth.UserForToken(db, token); ok {
					r = r.WithContext(withUser(r, user.ID))
					next.ServeHTTP(w, r)
					return
				}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(401)
			w.Write([]byte(`{"error":"unauthorized"}`))
		})
	}
}

type userKey int

const ukey userKey = 1

func withUser(r *http.Request, id int64) context.Context {
	return context.WithValue(r.Context(), ukey, id)
}

func uid(r *http.Request) int64 {
	if v, ok := r.Context().Value(ukey).(int64); ok {
		return v
	}
	return 0
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func ticks(secs float64) int64  { return int64(secs * 10000000) }
func fromTicks(t int64) float64 { return float64(t) / 10000000 }

func (a *API) systemInfoPublic(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{
		"LocalAddress": "", "ServerName": "libteca", "Version": "10.10.0",
		"ProductName": "libteca", "OperatingSystem": "linux", "Id": "libteca-server",
		"StartupWizardCompleted": true,
	})
}

func (a *API) configStub(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{})
}

func (a *API) brandingStub(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"LoginDisclaimer": "", "CustomCss": ""})
}

func (a *API) authenticate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"Username"`
		Pw       string `json:"Pw"`
		Password string `json:"Password"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	pass := body.Pw
	if pass == "" {
		pass = body.Password
	}
	u, err := a.DB.UserByName(body.Username)
	if err != nil || !auth.Verify(pass, u.PasswordHash) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"invalid"}`))
		return
	}
	token, _ := auth.IssueToken(a.DB, u.ID, "jellyfin-client")
	write(w, 200, map[string]any{
		"User": map[string]any{
			"Id": strconv.FormatInt(u.ID, 10), "Name": u.Name, "HasPassword": true,
			"Configuration": map[string]any{}, "Policy": map[string]any{"IsAdministrator": u.IsAdmin},
		},
		"AccessToken": token,
		"ServerId":    "libteca-server",
	})
}

func collectionType(t string) string {
	switch t {
	case "movies":
		return "movies"
	case "tv":
		return "tvshows"
	case "music":
		return "music"
	case "audiobooks":
		return "books"
	case "books", "comics":
		return "books"
	}
	return "mixed"
}

func (a *API) views(w http.ResponseWriter, r *http.Request) {
	libs, _ := a.DB.Libraries()
	items := make([]map[string]any, 0, len(libs))
	for _, l := range libs {
		items = append(items, map[string]any{
			"Id": "lib" + strconv.FormatInt(l.ID, 10), "Name": l.Name,
			"Type": "CollectionFolder", "CollectionType": collectionType(l.Type),
			"ChildCount": 0, "LocationType": "FileSystem",
		})
	}
	write(w, 200, map[string]any{"Items": items, "TotalRecordCount": len(items)})
}

type resolvedItem struct {
	work    *store.WorkView
	lib     *store.Library
	edition *store.EditionView
}

var reLibItem = regexp.MustCompile(`^lib(\d+)$`)
var reWorkItem = regexp.MustCompile(`^w(\d+)$`)
var reEdItem = regexp.MustCompile(`^e(\d+)$`)

func (a *API) resolveLib(id string) (*store.Library, bool) {
	m := reLibItem.FindStringSubmatch(id)
	if m == nil {
		return nil, false
	}
	l, err := a.DB.Library(auth.Atoi64(m[1]))
	return l, err == nil
}

func (a *API) itemPayload(uid int64, wv *store.WorkView, ed *store.EditionView, lib *store.Library) map[string]any {
	return nil
}

func (a *API) userItems(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	parent := q.Get("ParentId")
	types := strings.ToLower(q.Get("IncludeItemTypes"))
	sortBy := strings.ToLower(q.Get("SortBy"))
	limit, _ := strconv.Atoi(q.Get("Limit"))
	start, _ := strconv.Atoi(q.Get("StartIndex"))

	items := []map[string]any{}
	userID := uid(r)

	if m := reLibItem.FindStringSubmatch(parent); m != nil {
		lib, err := a.DB.Library(auth.Atoi64(m[1]))
		if err != nil {
			write(w, 200, map[string]any{"Items": items, "TotalRecordCount": 0})
			return
		}
		works, _ := a.DB.WorksInLibrary(lib.ID)
		includeSeries := strings.Contains(types, "series")
		includeMovies := strings.Contains(types, "movie") || types == ""
		for i := range works {
			wv := &works[i]
			if lib.Type == "tv" {
				if includeSeries {
					items = append(items, a.seriesItem(userID, wv))
				} else if strings.Contains(types, "episode") {
					for j := range wv.Editions {
						items = append(items, a.episodeItem(userID, wv, &wv.Editions[j]))
					}
				}
				continue
			}
			if lib.Type == "music" {
				if strings.Contains(types, "musicalbum") || types == "" {
					items = append(items, a.albumItem(wv))
				} else if strings.Contains(types, "audio") {
					for j := range wv.Editions {
						items = append(items, a.trackItem(userID, wv, &wv.Editions[j]))
					}
				}
				continue
			}
			if includeMovies {
				items = append(items, a.movieItem(userID, wv))
			}
		}
	}

	if sortBy == "runtime" {
	} else if strings.Contains(sortBy, "date") || strings.Contains(sortBy, "created") {
		sort.SliceStable(items, func(i, j int) bool {
			return items[i]["__created"].(int64) > items[j]["__created"].(int64)
		})
	} else {
		sort.SliceStable(items, func(i, j int) bool {
			return items[i]["__sort"].(string) < items[j]["__sort"].(string)
		})
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
	for _, it := range items {
		delete(it, "__sort")
		delete(it, "__created")
	}
	write(w, 200, map[string]any{"Items": items[start:end], "TotalRecordCount": total})
}

func (a *API) coverTags(wv *store.WorkView) map[string]any {
	if wv.CoverPath != nil && *wv.CoverPath != "" {
		return map[string]any{"Primary": "c"}
	}
	return map[string]any{}
}

func (a *API) userData(userID int64, ed *store.EditionView) map[string]any {
	base := map[string]any{"PlaybackPositionTicks": 0, "PlayedPercentage": 0.0, "IsPlayed": false, "Key": ""}
	if p, err := a.DB.GetProgress(userID, ed.ID); err == nil {
		pos := p.EditionPositionSecs
		dur := ed.TotalDuration()
		pct := 0.0
		if dur > 0 {
			pct = pos / dur * 100
		}
		base["PlaybackPositionTicks"] = ticks(pos)
		base["PlayedPercentage"] = pct
		base["IsPlayed"] = p.IsFinished
	}
	return base
}

func (a *API) movieItem(userID int64, wv *store.WorkView) map[string]any {
	year := ""
	if wv.Author != nil && *wv.Author != "" {
		year = *wv.Author
	}
	rt := 0.0
	var userData map[string]any = map[string]any{"PlaybackPositionTicks": 0, "IsPlayed": false}
	if len(wv.Editions) > 0 {
		rt = wv.Editions[0].TotalDuration()
		userData = a.userData(userID, &wv.Editions[0])
	}
	it := map[string]any{
		"Id": "w" + strconv.FormatInt(wv.ID, 10), "Name": wv.Title, "Type": "Movie",
		"ServerId": "libteca-server", "ImageTags": a.coverTags(wv),
		"RunTimeTicks": ticks(rt), "ProductionYear": nil,
		"UserData": userData, "IsFolder": false,
		"__sort": strings.ToLower(wv.Title), "__created": wv.CreatedAt,
	}
	if y, err := strconv.Atoi(year); err == nil && y > 1900 {
		it["ProductionYear"] = y
	}
	return it
}

func (a *API) seriesItem(userID int64, wv *store.WorkView) map[string]any {
	return map[string]any{
		"Id": "w" + strconv.FormatInt(wv.ID, 10), "Name": wv.Title, "Type": "Series",
		"ServerId": "libteca-server", "ImageTags": a.coverTags(wv),
		"ChildCount": len(wv.Editions), "UserData": map[string]any{"IsPlayed": false},
		"__sort": strings.ToLower(wv.Title), "__created": wv.CreatedAt,
	}
}

func (a *API) seasonItem(wv *store.WorkView, season int) map[string]any {
	return map[string]any{
		"Id": fmt.Sprintf("s%d-%d", wv.ID, season), "Name": "Season " + strconv.Itoa(season),
		"Type": "Season", "SeriesId": "w" + strconv.FormatInt(wv.ID, 10),
		"SeriesName": wv.Title, "IndexNumber": season, "ServerId": "libteca-server",
		"ImageTags": a.coverTags(wv),
	}
}

func (a *API) episodeItem(userID int64, wv *store.WorkView, ed *store.EditionView) map[string]any {
	season, episode := 0, 0
	if ed.SeasonNum != nil {
		season = *ed.SeasonNum
	}
	if ed.EpisodeNum != nil {
		episode = *ed.EpisodeNum
	}
	return map[string]any{
		"Id": "e" + strconv.FormatInt(ed.ID, 10), "Name": ed.Title, "Type": "Episode",
		"SeriesId": "w" + strconv.FormatInt(wv.ID, 10), "SeriesName": wv.Title,
		"ParentId":    fmt.Sprintf("s%d-%d", wv.ID, season),
		"IndexNumber": episode, "ParentIndexNumber": season,
		"RunTimeTicks": ticks(ed.TotalDuration()),
		"ImageTags":    map[string]any{},
		"UserData":     a.userData(userID, ed),
		"ServerId":     "libteca-server",
		"__sort":       fmt.Sprintf("s%03de%03d %s", season, episode, strings.ToLower(ed.Title)),
		"__created":    ed.CreatedAt,
	}
}

func (a *API) albumItem(wv *store.WorkView) map[string]any {
	artist := ""
	if wv.Author != nil {
		artist = *wv.Author
	}
	return map[string]any{
		"Id": "w" + strconv.FormatInt(wv.ID, 10), "Name": wv.Title, "Type": "MusicAlbum",
		"AlbumArtist": artist, "Artists": []string{artist},
		"ServerId": "libteca-server", "ImageTags": a.coverTags(wv),
		"ChildCount": len(wv.Editions),
		"__sort":     strings.ToLower(wv.Title), "__created": wv.CreatedAt,
	}
}

func (a *API) trackItem(userID int64, wv *store.WorkView, ed *store.EditionView) map[string]any {
	artist := ""
	if wv.Author != nil {
		artist = *wv.Author
	}
	return map[string]any{
		"Id": "e" + strconv.FormatInt(ed.ID, 10), "Name": ed.Title, "Type": "Audio",
		"AlbumArtist": artist, "Artists": []string{artist}, "Album": wv.Title,
		"RunTimeTicks": ticks(ed.TotalDuration()),
		"IndexNumber":  ed.Position, "ParentIndexNumber": 1,
		"ServerId": "libteca-server", "UserData": a.userData(userID, ed),
		"__sort": fmt.Sprintf("%03d", ed.Position), "__created": ed.CreatedAt,
	}
}

func (a *API) itemDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	userID := uid(r)
	if it, ok := a.detailFor(userID, id); ok {
		delete(it, "__sort")
		delete(it, "__created")
		write(w, 200, it)
		return
	}
	write(w, 404, map[string]any{"error": "not found"})
}

func (a *API) detailFor(userID int64, id string) (map[string]any, bool) {
	if m := reWorkItem.FindStringSubmatch(id); m != nil {
		wv, err := a.DB.WorkByID(auth.Atoi64(m[1]))
		if err != nil {
			return nil, false
		}
		works, err := a.DB.WorksInLibrary(wv.LibraryID)
		for i := range works {
			if works[i].ID == wv.ID {
				lib, _ := a.DB.Library(wv.LibraryID)
				switch lib.Type {
				case "tv":
					return a.seriesItem(userID, &works[i]), true
				case "music":
					return a.albumItem(&works[i]), true
				default:
					return a.movieItem(userID, &works[i]), true
				}
			}
		}
		return nil, false
	}
	if m := reSeasonItem.FindStringSubmatch(id); m != nil {
		wv, _ := a.DB.WorkByID(auth.Atoi64(m[1]))
		n, _ := strconv.Atoi(m[2])
		works, _ := a.DB.WorksInLibrary(wv.LibraryID)
		for i := range works {
			if works[i].ID == wv.ID {
				return a.seasonItem(&works[i], n), true
			}
		}
		return nil, false
	}
	if m := reEdItem.FindStringSubmatch(id); m != nil {
		ed, err := a.DB.EditionByID(auth.Atoi64(m[1]))
		if err != nil {
			return nil, false
		}
		wv, _ := a.DB.WorkByID(ed.WorkID)
		works, _ := a.DB.WorksInLibrary(wv.LibraryID)
		for i := range works {
			if works[i].ID == ed.WorkID {
				lib, _ := a.DB.Library(wv.LibraryID)
				if lib.Type == "music" {
					return a.trackItem(userID, &works[i], ed), true
				}
				return a.episodeItem(userID, &works[i], ed), true
			}
		}
	}
	return nil, false
}

var reSeasonItem = regexp.MustCompile(`^s(\d+)-(\d+)$`)

func (a *API) seasons(w http.ResponseWriter, r *http.Request) {
	seriesID := strings.TrimPrefix(r.PathValue("seriesId"), "w")
	wv, err := a.DB.WorkByID(auth.Atoi64(seriesID))
	if err != nil {
		write(w, 200, map[string]any{"Items": []any{}, "TotalRecordCount": 0})
		return
	}
	works, _ := a.DB.WorksInLibrary(wv.LibraryID)
	seen := map[int]bool{}
	items := []map[string]any{}
	for i := range works {
		if works[i].ID != wv.ID {
			continue
		}
		for j := range works[i].Editions {
			if works[i].Editions[j].SeasonNum != nil {
				s := *works[i].Editions[j].SeasonNum
				if !seen[s] {
					seen[s] = true
					items = append(items, a.seasonItem(&works[i], s))
				}
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["IndexNumber"].(int) < items[j]["IndexNumber"].(int) })
	write(w, 200, map[string]any{"Items": items, "TotalRecordCount": len(items)})
}

func (a *API) episodes(w http.ResponseWriter, r *http.Request) {
	seriesID := strings.TrimPrefix(r.PathValue("seriesId"), "w")
	userID := uid(r)
	wv, err := a.DB.WorkByID(auth.Atoi64(seriesID))
	if err != nil {
		write(w, 200, map[string]any{"Items": []any{}, "TotalRecordCount": 0})
		return
	}
	seasonFilter := 0
	if sid := r.URL.Query().Get("seasonId"); sid != "" {
		if m := reSeasonItem.FindStringSubmatch(sid); m != nil {
			seasonFilter, _ = strconv.Atoi(m[2])
		}
	}
	works, _ := a.DB.WorksInLibrary(wv.LibraryID)
	items := []map[string]any{}
	for i := range works {
		if works[i].ID != wv.ID {
			continue
		}
		for j := range works[i].Editions {
			ed := &works[i].Editions[j]
			if ed.SeasonNum == nil {
				continue
			}
			if seasonFilter > 0 && *ed.SeasonNum != seasonFilter {
				continue
			}
			items = append(items, a.episodeItem(userID, &works[i], ed))
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["__sort"].(string) < items[j]["__sort"].(string) })
	for _, it := range items {
		delete(it, "__sort")
		delete(it, "__created")
	}
	write(w, 200, map[string]any{"Items": items, "TotalRecordCount": len(items)})
}

func (a *API) resume(w http.ResponseWriter, r *http.Request) {
	eds, _ := a.DB.EditionsInProgress(uid(r))
	items := []map[string]any{}
	for _, eid := range eds {
		if it, ok := a.detailFor(uid(r), "e"+strconv.FormatInt(eid, 10)); ok {
			if it["Type"] == "Episode" || it["Type"] == "Movie" || it["Type"] == "Audio" {
				delete(it, "__sort")
				delete(it, "__created")
				items = append(items, it)
			}
		}
	}
	write(w, 200, map[string]any{"Items": items, "TotalRecordCount": len(items)})
}

func (a *API) nextUp(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"Items": []any{}, "TotalRecordCount": 0})
}

func (a *API) ancestors(w http.ResponseWriter, r *http.Request) {
	write(w, 200, []any{})
}

func (a *API) sessionsList(w http.ResponseWriter, r *http.Request) {
	write(w, 200, []any{})
}

func (a *API) image(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var workID int64
	if m := reWorkItem.FindStringSubmatch(id); m != nil {
		workID = auth.Atoi64(m[1])
	} else if m := reEdItem.FindStringSubmatch(id); m != nil {
		ed, err := a.DB.EditionByID(auth.Atoi64(m[1]))
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		workID = ed.WorkID
	} else {
		http.Error(w, "not found", 404)
		return
	}
	wv, err := a.DB.WorkByID(workID)
	if err != nil || wv.CoverPath == nil || *wv.CoverPath == "" {
		http.Error(w, "not found", 404)
		return
	}
	f, err := os.Open(filepath.Join(a.Dir, "covers", *wv.CoverPath))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, "cover.jpg", fi.ModTime(), f)
}

func (a *API) resolvePlayable(id string) (*store.EditionView, error) {
	if m := reWorkItem.FindStringSubmatch(id); m != nil {
		wv, err := a.DB.WorkByID(auth.Atoi64(m[1]))
		if err != nil {
			return nil, err
		}
		works, _ := a.DB.WorksInLibrary(wv.LibraryID)
		for i := range works {
			if works[i].ID == wv.ID && len(works[i].Editions) > 0 {
				return &works[i].Editions[0], nil
			}
		}
		return nil, store.ErrNotFound
	}
	if m := reEdItem.FindStringSubmatch(id); m != nil {
		return a.DB.EditionByID(auth.Atoi64(m[1]))
	}
	return nil, store.ErrNotFound
}

func browserPlayable(ed *store.EditionView) bool {
	if len(ed.Files) == 0 {
		return false
	}
	f := ed.Files[0]
	vcodec := ""
	container := ""
	if f.VideoCodec != nil {
		vcodec = *f.VideoCodec
	}
	if f.Container != nil {
		container = *f.Container
	}
	acodec := ""
	if f.Codec != nil {
		acodec = *f.Codec
	}
	audioOK := acodec == "aac" || acodec == "mp3" || acodec == "flac" || acodec == "opus" || acodec == "vorbis"
	first := strings.Split(container, ",")[0]
	containerOK := first == "mp4" || first == "mov" || first == "m4v" ||
		(strings.Contains(container, "webm") && (vcodec == "vp9" || vcodec == "vp8" || vcodec == "av1"))
	if vcodec == "" {
		return audioOK
	}
	return (vcodec == "h264" || vcodec == "vp9" || vcodec == "av1" || vcodec == "hevc") && audioOK && containerOK
}

func (a *API) playbackInfo(w http.ResponseWriter, r *http.Request) {
	ed, err := a.resolvePlayable(r.PathValue("id"))
	if err != nil {
		write(w, 404, map[string]any{"error": "not found"})
		return
	}
	f := ed.Files[0]
	fid := "f" + strconv.FormatInt(f.ID, 10)
	playSession := "ps-" + strconv.FormatInt(ed.ID, 10)
	mediaSource := map[string]any{
		"Id": fid, "Path": f.Path, "Protocol": "File",
		"SupportsDirectPlay": false, "SupportsDirectStream": false, "SupportsTranscoding": true,
	}
	container := ""
	if f.Container != nil {
		container = *f.Container
		mediaSource["Container"] = strings.Split(container, ",")[0]
	}
	mediaSource["MediaStreams"] = a.mediaStreams(&f)
	if browserPlayable(ed) {
		mediaSource["SupportsDirectPlay"] = true
		mediaSource["SupportsDirectStream"] = true
	} else {
		mediaSource["TranscodingUrl"] = fmt.Sprintf("/videos/e%d/main.m3u8?MediaSourceId=%s&VideoCodec=h264&AudioCodec=aac&PlaySessionId=%s&api_key=%s",
			ed.ID, fid, playSession, r.URL.Query().Get("api_key"))
	}
	write(w, 200, map[string]any{
		"MediaSources":  []any{mediaSource},
		"PlaySessionId": playSession,
	})
}

func (a *API) mediaStreams(f *store.FileRec) []map[string]any {
	streams := []map[string]any{}
	if f.VideoCodec != nil {
		s := map[string]any{"Type": "Video", "Codec": *f.VideoCodec, "IsInterlaced": false}
		if f.Width != nil {
			s["Width"] = *f.Width
			s["Height"] = *f.Height
		}
		if f.Bitrate != nil {
			s["BitRate"] = *f.Bitrate
		}
		streams = append(streams, s)
	}
	if f.Codec != nil {
		s := map[string]any{"Type": "Audio", "Codec": *f.Codec}
		if f.Channels != nil {
			s["Channels"] = *f.Channels
		}
		if f.Bitrate != nil {
			s["BitRate"] = *f.Bitrate
		}
		streams = append(streams, s)
	}
	return streams
}

func (a *API) videoStream(w http.ResponseWriter, r *http.Request) {
	ed, err := a.resolvePlayable(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	serveFile(w, r, ed.Files[0].Path)
}

func (a *API) hlsMaster(w http.ResponseWriter, r *http.Request) {
	ed, err := a.resolvePlayable(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	start := 0.0
	if t := r.URL.Query().Get("startTimeTicks"); t != "" {
		start = fromTicks(auth.Atoi64(t))
	}
	sessionID := r.URL.Query().Get("PlaySessionId")
	if sessionID == "" {
		sessionID = fmt.Sprintf("t%d-%d", ed.ID, time.Now().UnixMilli())
	}
	s, err := a.TC.Get(sessionID, ed.ID, ed.Files[0].Path, start)
	if err != nil {
		http.Error(w, "transcode failed", 500)
		return
	}
	for i := 0; i < 100; i++ {
		if fi, err := os.Stat(s.Playlist()); err == nil && fi.Size() > 0 {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	data, err := os.ReadFile(s.Playlist())
	if err != nil {
		http.Error(w, "no playlist", 500)
		return
	}
	s.Touch()
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Write(data)
}

func (a *API) hlsSegment(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	file := r.PathValue("file")
	if !regexp.MustCompile(`^seg\d+\.ts$|^index\.m3u8$`).MatchString(file) {
		http.Error(w, "bad", 400)
		return
	}
	a.TC.TouchSession(sid)
	path := filepath.Join(a.Dir, "transcode", sid, file)
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	if strings.HasSuffix(file, ".m3u8") {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	}
	http.ServeContent(w, r, file, fi.ModTime(), f)
}

func (a *API) audioUniversal(w http.ResponseWriter, r *http.Request) {
	ed, err := a.resolvePlayable(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	f := ed.Files[0]
	codec := ""
	if f.Codec != nil {
		codec = *f.Codec
	}
	switch codec {
	case "flac":
		w.Header().Set("Content-Type", "audio/flac")
	case "aac":
		w.Header().Set("Content-Type", "audio/mp4")
	case "opus", "vorbis":
		w.Header().Set("Content-Type", "audio/ogg")
	default:
		w.Header().Set("Content-Type", "audio/mpeg")
	}
	serveFile(w, r, f.Path)
}

func (a *API) sessionPlaying(w http.ResponseWriter, r *http.Request) {
	a.saveFromSession(w, r)
}

func (a *API) sessionProgress(w http.ResponseWriter, r *http.Request) {
	a.saveFromSession(w, r)
}

func (a *API) sessionStopped(w http.ResponseWriter, r *http.Request) {
	a.saveFromSession(w, r)
	if sid := r.URL.Query().Get("PlaySessionId"); sid != "" {
	} else {
		var body struct {
			PlaySessionId string `json:"PlaySessionId"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.PlaySessionId != "" {
			a.TC.Close(strings.TrimPrefix(body.PlaySessionId, "ps-"))
		}
	}
	write(w, 200, map[string]any{})
}

func (a *API) saveFromSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ItemId        string `json:"ItemId"`
		PositionTicks int64  `json:"PositionTicks"`
		IsPaused      bool   `json:"IsPaused"`
		PlaySessionId string `json:"PlaySessionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		write(w, 200, map[string]any{})
		return
	}
	ed, err := a.resolvePlayable(body.ItemId)
	if err != nil {
		write(w, 200, map[string]any{})
		return
	}
	pos := fromTicks(body.PositionTicks)
	fileID, offset := ed.Locate(pos)
	dur := ed.TotalDuration()
	device := "jellyfin-client"
	p := &store.Progress{
		UserID: uid(r), EditionID: ed.ID, FileID: &fileID, FileOffsetSecs: offset,
		EditionPositionSecs: pos, DurationSecs: &dur, Device: &device,
		IsFinished: dur > 0 && pos >= dur-5,
	}
	a.DB.SetProgress(p)
	write(w, 200, map[string]any{})
}

func serveFile(w http.ResponseWriter, r *http.Request, path string) {
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "gone", 404)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	http.ServeContent(w, r, filepath.Base(path), fi.ModTime(), f)
}
