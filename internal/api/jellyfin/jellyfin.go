package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/libteca/libteca/internal/transcode"
	"github.com/libteca/libteca/internal/trickplay"
	"github.com/neutron-build/neutron/go/neutron"
)

type API struct {
	DB           *store.DB
	Dir          string
	TC           *transcode.Manager
	LoginLimiter *auth.Limiter

	tpOnce sync.Once
	tp     *trickplay.Generator
	hub    *hub
}

func New(db *store.DB, dataDir string, tc *transcode.Manager) *API {
	return &API{DB: db, Dir: dataDir, TC: tc, hub: newHub()}
}

func (a *API) hubv() *hub {
	if a.hub == nil {
		a.hub = newHub()
	}
	return a.hub
}

func (a *API) trickplayer() *trickplay.Generator {
	a.tpOnce.Do(func() { a.tp = trickplay.New(a.Dir) })
	return a.tp
}

func (a *API) Mount(r *neutron.Router) {
	r.HandleFunc("GET /System/Info/Public", a.systemInfoPublic)
	r.HandleFunc("GET /System/Ping", a.systemPing)
	r.HandleFunc("GET /socket", a.handleSocket)
	r.HandleFunc("POST /Users/AuthenticateByName", a.authenticate)
	g := r.Group("", jfAuth(a.DB))
	g.HandleFunc("GET /System/Info", a.systemInfo)
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
	g.HandleFunc("GET /Videos/{id}/master.m3u8", a.hlsMaster)
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
	g.HandleFunc("GET /Videos/{id}/Trickplay/{width}/manifest.json", a.trickplayManifest)
	g.HandleFunc("GET /Videos/{id}/Trickplay/{width}/{file}", a.trickplayTile)
	a.MountPodcasts(g)
}

func jfAuth(db *store.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := r.Header.Get("X-Emby-Token")
			if token == "" {
				if m := reWSAuthToken.FindStringSubmatch(r.Header.Get("Authorization")); m != nil {
					token = m[1]
				}
			}
			if token == "" {
				if m := reWSAuthToken.FindStringSubmatch(r.Header.Get("X-Emby-Authorization")); m != nil {
					token = m[1]
				}
			}
			if token == "" {
				token = qget(r, "api_key")
			}
			if token == "" || r.URL.Path == "/System/Info/Public" {
				if r.URL.Path == "/System/Info/Public" {
					next.ServeHTTP(w, r)
					return
				}
			}
			if token != "" {
				if user, ok := auth.UserForToken(db, token); ok {
					r = r.WithContext(withUser(r, user.ID, user.IsAdmin))
					r = auth.WithToken(r, token)
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
const akey userKey = 2

func withUser(r *http.Request, id int64, isAdmin bool) context.Context {
	ctx := context.WithValue(r.Context(), ukey, id)
	return context.WithValue(ctx, akey, isAdmin)
}

func uid(r *http.Request) int64 {
	if v, ok := r.Context().Value(ukey).(int64); ok {
		return v
	}
	return 0
}

func adminRequest(r *http.Request) bool {
	v, _ := r.Context().Value(akey).(bool)
	return v
}

// ownsPlaySession reports whether the requesting user may control a
// generated playback session: session ids minted by playbackInfo carry
// "u<uid>-" and belong to that user alone; admins control any.
func ownsPlaySession(r *http.Request, sid string) bool {
	if adminRequest(r) || sid == "" {
		return true
	}
	var owner int64
	if _, err := fmt.Sscanf(sid, "u%d-", &owner); err != nil {
		return true // not one of ours (client-generated id): legacy semantics
	}
	return owner == uid(r)
}

func requestToken(r *http.Request) string {
	if t := qget(r, "api_key"); t != "" {
		return t
	}
	return auth.Token(r)
}

// qget returns the first query param matching name case-insensitively.
// Real Jellyfin servers bind query params case-insensitively (ASP.NET), and
// clients send a mix of ParentId/parentId, StartIndex/startIndex, etc.
func qget(r *http.Request, name string) string {
	q := r.URL.Query()
	if vs, ok := q[name]; ok && len(vs) > 0 {
		return vs[0]
	}
	for k, vs := range q {
		if strings.EqualFold(k, name) && len(vs) > 0 {
			return vs[0]
		}
	}
	return ""
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

func (a *API) systemPing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte("Jellyfin Server"))
}

func (a *API) systemInfo(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{
		"LocalAddress": "", "ServerName": "libteca", "Version": "10.10.0",
		"ProductName": "libteca", "OperatingSystem": "linux", "Id": "libteca-server",
		"StartupWizardCompleted": true, "HasUpdateAvailable": false,
		"FailedPluginAssemblies": []string{},
	})
}

func (a *API) configStub(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{})
}

func (a *API) brandingStub(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"LoginDisclaimer": "", "CustomCss": ""})
}

func (a *API) authenticate(w http.ResponseWriter, r *http.Request) {
	ip := auth.ClientIP(r)
	if a.LoginLimiter != nil {
		if ok, retry := a.LoginLimiter.Allow(ip); !ok {
			auth.WriteRetryAfter(w, retry)
			write(w, 429, map[string]any{"error": "too many attempts, try again later"})
			return
		}
	}
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
		if a.LoginLimiter != nil {
			a.LoginLimiter.Failure(ip)
		}
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"invalid"}`))
		return
	}
	if a.LoginLimiter != nil {
		a.LoginLimiter.Success(ip)
	}
	token, err := auth.IssueToken(a.DB, u.ID, "jellyfin-client")
	if err != nil {
		write(w, 500, map[string]any{"error": "internal error"})
		return
	}
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
	parent := qget(r, "ParentId")
	types := strings.ToLower(qget(r, "IncludeItemTypes"))
	sortBy := strings.ToLower(qget(r, "SortBy"))
	limit, _ := strconv.Atoi(qget(r, "Limit"))
	start, _ := strconv.Atoi(qget(r, "StartIndex"))

	items := []map[string]any{}
	userID := uid(r)

	if m := reSeasonItem.FindStringSubmatch(parent); m != nil {
		a.respondItems(w, a.seasonItems(auth.Atoi64(m[1]), auth.Atoi64(m[2]), types, userID), sortBy, limit, start)
		return
	}
	if m := reWorkItem.FindStringSubmatch(parent); m != nil {
		a.respondItems(w, a.seriesItems(auth.Atoi64(m[1]), types, userID), sortBy, limit, start)
		return
	}

	if m := reLibItem.FindStringSubmatch(parent); m != nil {
		lib, err := a.DB.Library(auth.Atoi64(m[1]))
		if err != nil {
			write(w, 200, map[string]any{"Items": items, "TotalRecordCount": 0})
			return
		}
		works, _ := a.DB.WorksInLibrary(lib.ID)
		includeSeries := strings.Contains(types, "series") || types == ""
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
				items = append(items, a.movieItem(userID, wv, lib.Type))
			}
		}
	}

	a.respondItems(w, items, sortBy, limit, start)
}

func sliceWindow(start, limit, total int) (int, int) {
	if total < 0 {
		total = 0
	}
	if start < 0 {
		start = 0
	}
	if start > total {
		start = total
	}
	remaining := total - start
	if limit <= 0 || limit > remaining {
		limit = remaining
	}
	return start, start + limit
}

func (a *API) respondItems(w http.ResponseWriter, items []map[string]any, sortBy string, limit, start int) {
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
	start, end := sliceWindow(start, limit, total)
	for _, it := range items {
		delete(it, "__sort")
		delete(it, "__created")
	}
	write(w, 200, map[string]any{"Items": items[start:end], "TotalRecordCount": total})
}

// seasonItems lists a season's episodes (ParentId=s{work}-{season}).
func (a *API) seasonItems(workID, season int64, types string, userID int64) []map[string]any {
	wv, err := a.DB.WorkByID(workID)
	if err != nil {
		return nil
	}
	works, _ := a.DB.WorksInLibrary(wv.LibraryID)
	items := []map[string]any{}
	for i := range works {
		if works[i].ID != workID {
			continue
		}
		for j := range works[i].Editions {
			ed := &works[i].Editions[j]
			if ed.SeasonNum == nil || *ed.SeasonNum != int(season) {
				continue
			}
			items = append(items, a.episodeItem(userID, &works[i], ed))
		}
	}
	return items
}

// seriesItems lists a series' children (ParentId=w{work}): seasons by
// default, episodes when IncludeItemTypes asks for them.
func (a *API) seriesItems(workID int64, types string, userID int64) []map[string]any {
	wv, err := a.DB.WorkByID(workID)
	if err != nil {
		return nil
	}
	works, _ := a.DB.WorksInLibrary(wv.LibraryID)
	items := []map[string]any{}
	for i := range works {
		if works[i].ID != workID {
			continue
		}
		if strings.Contains(types, "episode") {
			for j := range works[i].Editions {
				items = append(items, a.episodeItem(userID, &works[i], &works[i].Editions[j]))
			}
			continue
		}
		seen := map[int]bool{}
		for j := range works[i].Editions {
			if works[i].Editions[j].SeasonNum != nil {
				s := *works[i].Editions[j].SeasonNum
				if !seen[s] {
					seen[s] = true
					it := a.seasonItem(&works[i], s)
					it["__sort"] = fmt.Sprintf("s%03d", s)
					it["__created"] = works[i].CreatedAt
					items = append(items, it)
				}
			}
		}
	}
	return items
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

func movieItemType(libType string) string {
	switch libType {
	case "audiobooks":
		return "AudioBook"
	case "books", "comics":
		return "Book"
	default:
		return "Movie"
	}
}

func (a *API) movieItem(userID int64, wv *store.WorkView, libType string) map[string]any {
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
		"Id": "w" + strconv.FormatInt(wv.ID, 10), "Name": wv.Title, "Type": movieItemType(libType),
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
					return a.movieItem(userID, &works[i], lib.Type), true
				}
			}
		}
		return nil, false
	}
	if m := reSeasonItem.FindStringSubmatch(id); m != nil {
		wv, err := a.DB.WorkByID(auth.Atoi64(m[1]))
		if err != nil || wv == nil {
			return nil, false
		}
		n, _ := strconv.Atoi(m[2])
		works, err := a.DB.WorksInLibrary(wv.LibraryID)
		if err != nil {
			return nil, false
		}
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
	if sid := qget(r, "SeasonId"); sid != "" {
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
	userID := uid(r)
	var seriesID int64
	if s := qget(r, "SeriesId"); s != "" {
		seriesID = auth.Atoi64(strings.TrimPrefix(s, "w"))
	}
	limit := 20
	if v, err := strconv.Atoi(qget(r, "Limit")); err == nil && v > 0 {
		limit = v
	}
	start := 0
	if v, err := strconv.Atoi(qget(r, "StartIndex")); err == nil && v > 0 {
		start = v
	}
	rows, err := a.DB.NextUp(userID, seriesID, qget(r, "DisableFirstEpisode") == "true")
	items := []map[string]any{}
	if err == nil {
		for _, row := range rows {
			if it, ok := a.detailFor(userID, "e"+strconv.FormatInt(row.EditionID, 10)); ok {
				delete(it, "__sort")
				delete(it, "__created")
				items = append(items, it)
			}
		}
	}
	total := len(items)
	start, end := sliceWindow(start, limit, total)
	write(w, 200, map[string]any{"Items": items[start:end], "TotalRecordCount": total, "StartIndex": start})
}

func (a *API) ancestors(w http.ResponseWriter, r *http.Request) {
	write(w, 200, []any{})
}

func (a *API) sessionsList(w http.ResponseWriter, r *http.Request) {
	dtos := a.sessionDTOs()
	if !adminRequest(r) {
		// The full session table (users, devices, play sessions) is an
		// admin view; ordinary users see their own sessions only.
		mine := strconv.FormatInt(uid(r), 10)
		filtered := make([]wsSessionDTO, 0, len(dtos))
		for _, d := range dtos {
			if d.UserId == mine {
				filtered = append(filtered, d)
			}
		}
		dtos = filtered
	}
	write(w, 200, dtos)
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
	f, err := os.Open(filepath.Join(a.Dir, "covers", filepath.Base(*wv.CoverPath)))
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
				for j := range works[i].Editions {
					if len(works[i].Editions[j].Files) > 0 {
						return &works[i].Editions[j], nil
					}
				}
			}
		}
		return nil, store.ErrNotFound
	}
	if m := reEdItem.FindStringSubmatch(id); m != nil {
		ed, err := a.DB.EditionByID(auth.Atoi64(m[1]))
		if err != nil {
			return nil, err
		}
		if len(ed.Files) == 0 {
			return nil, store.ErrNotFound
		}
		return ed, nil
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
	audioOK := acodec == "" || acodec == "aac" || acodec == "mp3" || acodec == "flac" || acodec == "opus" || acodec == "vorbis"
	first := strings.Split(container, ",")[0]
	containerOK := first == "mp4" || first == "mov" || first == "m4v" ||
		(strings.Contains(container, "webm") && (vcodec == "vp9" || vcodec == "vp8" || vcodec == "av1"))
	if vcodec == "" {
		return audioOK
	}
	return (vcodec == "h264" || vcodec == "vp9" || vcodec == "av1") && audioOK && containerOK
}

func (a *API) playbackInfo(w http.ResponseWriter, r *http.Request) {
	ed, err := a.resolvePlayable(r.PathValue("id"))
	if err != nil {
		write(w, 404, map[string]any{"error": "not found"})
		return
	}
	f := ed.Files[0]
	fid := "f" + strconv.FormatInt(f.ID, 10)
	// u<uid>- prefix: session ids are visible through /Sessions and
	// controllable through stop/WS commands, so the minting user must be
	// recoverable from the id itself.
	playSession, err := transcode.NewSessionID(fmt.Sprintf("u%d-ps-", uid(r)), ed.ID)
	if err != nil {
		write(w, 500, map[string]any{"error": "internal error"})
		return
	}
	mediaSource := map[string]any{
		"Id": fid, "Path": filepath.Base(f.Path), "Protocol": "File",
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
			ed.ID, fid, playSession, requestToken(r))
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
	if sidecarSubtitle(f.Path) {
		streams = append(streams, map[string]any{
			"Type": "Subtitle", "Index": 0, "Codec": "vtt",
			"IsExternal": true, "DeliveryMethod": "External",
			"IsTextSubtitleStream": true,
		})
	}
	return streams
}

func sidecarSubtitle(media string) bool {
	base := strings.TrimSuffix(media, filepath.Ext(media))
	for _, ext := range []string{".srt", ".vtt"} {
		if _, err := os.Stat(base + ext); err == nil {
			return true
		}
	}
	return false
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
	if t := qget(r, "StartTimeTicks"); t != "" {
		start = fromTicks(auth.Atoi64(t))
	}
	sessionID := qget(r, "PlaySessionId")
	if sessionID == "" {
		sessionID = fmt.Sprintf("t%d-%d", ed.ID, time.Now().UnixMilli())
	} else if !validHLSSessionID(sessionID) {
		http.Error(w, "bad", 400)
		return
	} else if !ownsPlaySession(r, sessionID) {
		// Manager.Get kills an existing session on an edition mismatch;
		// without this, supplying a victim's session id here tore down
		// their playback.
		http.Error(w, "not found", 404)
		return
	}
	if a.TC == nil {
		http.Error(w, "transcode unavailable", 503)
		return
	}
	s, err := a.TC.Get(sessionID, ed.ID, ed.Files[0].Path, start)
	if err != nil {
		http.Error(w, "transcode failed", 500)
		return
	}
	s.Prebuffer(r.Context(), 2, 10*time.Second)
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
	w.Write(rewriteHLSPlaylist(data, r.PathValue("id"), sessionID, requestToken(r)))
}

var (
	reHLSURI         = regexp.MustCompile(`^(seg\d+\.ts|index\.m3u8)$`)
	reHLSSessionID   = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	reHLSSegmentFile = regexp.MustCompile(`^(seg\d+\.ts|index\.m3u8)$`)
)

// validHLSSessionID guards ids that reach the transcoder, where they become
// directory names under DataDir. The charset regex alone admits "." and "..",
// which filepath.Join cleans upward — the explicit dot-name rejection closes
// that hole for every caller.
func validHLSSessionID(s string) bool {
	return reHLSSessionID.MatchString(s) && s != "." && s != ".."
}

func rewriteHLSPlaylist(playlist []byte, itemID, sessionID, apiKey string) []byte {
	prefix := "/videos/" + itemID + "/hls/" + sessionID + "/"
	q := ""
	if apiKey != "" {
		q = "?api_key=" + url.QueryEscape(apiKey)
	}
	lines := strings.Split(string(playlist), "\n")
	for i, line := range lines {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		file, _, _ := strings.Cut(s, "?")
		if reHLSURI.MatchString(file) {
			lines[i] = prefix + file + q
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func (a *API) hlsSegment(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	file := r.PathValue("file")
	if !validHLSSessionID(sid) {
		http.Error(w, "bad", 400)
		return
	}
	if !reHLSSegmentFile.MatchString(file) {
		http.Error(w, "bad", 400)
		return
	}
	if !a.TC.WaitForSegmentFile(r.Context(), sid, file, 10*time.Second) {
		http.Error(w, "not found", 404)
		return
	}
	path := filepath.Join(a.Dir, "transcode", sid, file)
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	defer f.Close()
	if strings.HasSuffix(file, ".m3u8") {
		data, err := io.ReadAll(f)
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Write(rewriteHLSPlaylist(data, r.PathValue("id"), sid, requestToken(r)))
		return
	}
	fi, _ := f.Stat()
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
	sid := a.saveFromSession(w, r)
	if q := qget(r, "PlaySessionId"); q != "" {
		if !ownsPlaySession(r, q) {
			// Closing another user's ffmpeg session is not this caller's
			// call; report acceptance without tearing anything down.
			write(w, 200, map[string]bool{"ok": true})
			return
		}
		sid = q
	}
	// The body-only path resolves to the same id space; ownership applies
	// there too, or the query check was bypassable by moving the field.
	if sid != "" && !ownsPlaySession(r, sid) {
		write(w, 200, map[string]bool{"ok": true})
		return
	}
	if a.TC != nil && sid != "" {
		a.TC.Close(sid)
	}
}

func (a *API) saveFromSession(w http.ResponseWriter, r *http.Request) string {
	var body struct {
		ItemId        string `json:"ItemId"`
		PositionTicks int64  `json:"PositionTicks"`
		IsPaused      bool   `json:"IsPaused"`
		PlaySessionId string `json:"PlaySessionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		write(w, 200, map[string]any{})
		return ""
	}
	ed, err := a.resolvePlayable(body.ItemId)
	if err != nil {
		write(w, 200, map[string]any{})
		return body.PlaySessionId
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
	if err := a.DB.SetProgress(p); err != nil {
		write(w, 500, map[string]any{"error": "internal error"})
		return body.PlaySessionId
	}
	a.ReportPlayback(r, body.ItemId, body.PlaySessionId, body.PositionTicks, body.IsPaused)
	write(w, 200, map[string]any{})
	return body.PlaySessionId
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

// corpus: tile URL shape and 0-based {file} index unverified against 10.10 traffic
func (a *API) trickplayTile(w http.ResponseWriter, r *http.Request) {
	ed, err := a.resolvePlayable(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	width, err := strconv.Atoi(r.PathValue("width"))
	if err != nil {
		http.Error(w, "bad width", 400)
		return
	}
	index, ok := trickplay.ParseTileName(r.PathValue("file"))
	if !ok {
		http.Error(w, "bad tile", 400)
		return
	}
	itemID := "e" + strconv.FormatInt(ed.ID, 10)
	path, err := a.trickplayer().Tile(r.Context(), itemID, ed.Files[0].Path, width, index)
	if errors.Is(err, trickplay.ErrNotFound) {
		http.Error(w, "not found", 404)
		return
	}
	if errors.Is(err, trickplay.ErrNoFFmpeg) {
		w.Header().Set("Retry-After", "120")
		http.Error(w, "ffmpeg unavailable", 503)
		return
	}
	if err != nil {
		http.Error(w, "tile generation failed", 500)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, filepath.Base(path), fi.ModTime(), f)
}

func (a *API) trickplayManifest(w http.ResponseWriter, r *http.Request) {
	ed, err := a.resolvePlayable(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	width, err := strconv.Atoi(r.PathValue("width"))
	if err != nil {
		http.Error(w, "bad width", 400)
		return
	}
	itemID := "e" + strconv.FormatInt(ed.ID, 10)
	m, err := a.trickplayer().Manifest(r.Context(), itemID, ed.Files[0].Path, width)
	if errors.Is(err, trickplay.ErrNoFFmpeg) {
		w.Header().Set("Retry-After", "120")
		http.Error(w, "ffmpeg unavailable", 503)
		return
	}
	if err != nil {
		http.Error(w, "manifest failed", 500)
		return
	}
	write(w, 200, m)
}
