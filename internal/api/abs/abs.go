package abs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

type API struct {
	DB      *store.DB
	DataDir string

	LoginLimiter *auth.Limiter
}

func New(db *store.DB, dataDir string) *API {
	return &API{DB: db, DataDir: dataDir}
}

func (a *API) Mount(r *neutron.Router) {
	r.HandleFunc("GET /status", a.status)
	r.HandleFunc("GET /me", a.me)
	r.HandleFunc("GET /libraries", a.libraries)
	r.HandleFunc("GET /libraries/{id}/items", a.items)
	r.HandleFunc("GET /libraries/{id}/items/{itemId}", a.itemDetail)
	r.HandleFunc("GET /libraries/{id}/items/{itemId}/cover", a.itemCover)
	r.HandleFunc("GET /libraries/{id}/personalized", a.personalized)
	r.HandleFunc("GET /me/progress/{itemId}", a.getProgress)
	r.HandleFunc("POST /me/progress/{itemId}", a.postProgress)
	r.HandleFunc("PATCH /me/progress/{itemId}", a.postProgress)
	r.HandleFunc("DELETE /me/progress/{itemId}", a.deleteProgress)
	r.HandleFunc("GET /me/listening-sessions", a.listeningSessions)
	a.MountPodcasts(r)
	r.HandleFunc("POST /items/{itemId}/play", a.play)
	r.HandleFunc("GET /items/{itemId}/file/{fileId}", a.itemFile)
	r.HandleFunc("POST /session/{id}/sync", a.sessionSync)
	r.HandleFunc("POST /session/{id}/close", a.sessionClose)
}

func (a *API) Login(w http.ResponseWriter, r *http.Request) {
	ip := auth.ClientIP(r)
	if a.LoginLimiter != nil {
		if ok, retry := a.LoginLimiter.Allow(ip); !ok {
			auth.WriteRetryAfter(w, retry)
			fail(w, 429, "Too many attempts, try again later")
			return
		}
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, 400, "Invalid request body")
		return
	}
	u, err := a.DB.UserByName(body.Username)
	if err != nil || !auth.Verify(body.Password, u.PasswordHash) {
		if a.LoginLimiter != nil {
			a.LoginLimiter.Failure(ip)
		}
		fail(w, 401, "Invalid username or password")
		return
	}
	if a.LoginLimiter != nil {
		a.LoginLimiter.Success(ip)
	}
	token, err := auth.IssueToken(a.DB, u.ID, "abs-app")
	if err != nil {
		serverError(w, r, err)
		return
	}
	write(w, 200, map[string]any{"user": a.userPayload(u.ID), "userToken": token})
}

func (a *API) Ping(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"success": true})
}

func (a *API) Healthcheck(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"success": true})
}

func (a *API) status(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{
		"success": true, "isDefaultSingleUser": false, "Language": "en",
		"Version": "2.19.4", "ServerVersion": "2.19.4", "Commit": "", "LogLevel": 1,
		"buildNumberLatest": "0",
	})
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	write(w, 200, a.userPayload(auth.UserID(r)))
}

func (a *API) userPayload(userID int64) map[string]any {
	u, _ := a.DB.User(userID)
	list, _ := a.DB.UserProgressList(userID)
	progress := make([]map[string]any, 0, len(list))
	for _, p := range list {
		progress = append(progress, a.progressPayload(&p))
	}
	return map[string]any{
		"id":       "u-" + strconv.FormatInt(u.ID, 10),
		"username": u.Name, "type": "user",
		"mediaProgress":                   progress,
		"seriesHideFromContinueListening": []string{},
		"bookmarks":                       []string{},
		"isActive":                        true, "isLocked": false, "isDefaultUser": false,
		"permissions": map[string]any{
			"download": true, "update": true, "delete": false, "create": true,
			"accessAllLibraries": true, "accessAllTags": true, "selectedTagsNotAccessible": false,
			"librariesAccessible": []int{}, "tagsAccessible": []string{},
		},
		"settings": map[string]any{
			"orderBy": "media.metadata.title", "orderDesc": false, "filterBy": "all",
			"playbackRate": 1, "bookshelfCoverSize": 120, "language": "en",
		},
		"librariesAccessible": []int{},
	}
}

func (a *API) progressPayload(p *store.Progress) map[string]any {
	dur := 0.0
	if p.DurationSecs != nil {
		dur = *p.DurationSecs
	}
	frac := 0.0
	if dur > 0 {
		frac = p.EditionPositionSecs / dur
	}
	now := time.Now().UnixMilli()
	return map[string]any{
		"id":            "u-" + strconv.FormatInt(p.UserID, 10) + "-" + strconv.FormatInt(p.EditionID, 10),
		"userId":        "u-" + strconv.FormatInt(p.UserID, 10),
		"libraryItemId": strconv.FormatInt(p.EditionID, 10),
		"duration":      dur, "durationTimeSeconds": int64(dur),
		"progress": frac, "currentTime": p.EditionPositionSecs,
		"isFinished": p.IsFinished, "lastUpdate": p.UpdatedAt,
		"createdAt": p.UpdatedAt, "serverTime": now,
	}
}

func (a *API) libraries(w http.ResponseWriter, r *http.Request) {
	libs, err := a.DB.Libraries()
	if err != nil {
		serverError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(libs))
	for i, l := range libs {
		mediaType, icon := "book", "audiobook" // corpus: 'podcast' mediaType per ABS enum, icon unverified
		if l.Type == "podcasts" {
			mediaType, icon = "podcast", "podcast"
		}
		out = append(out, map[string]any{
			"id": strconv.FormatInt(l.ID, 10), "name": l.Name,
			"displayOrder": i + 1, "icon": icon, "mediaType": mediaType,
		})
	}
	def := ""
	if len(out) > 0 {
		def = out[0]["id"].(string)
	}
	write(w, 200, map[string]any{"libraries": out, "defaultLibraryId": def})
}

type itemCtx struct {
	ed *store.EditionView
	wv *store.Work
}

func (a *API) resolveItem(itemId string) (*itemCtx, error) {
	eid, err := strconv.ParseInt(itemId, 10, 64)
	if err != nil {
		return nil, store.ErrNotFound
	}
	ed, err := a.DB.EditionByID(eid)
	if err != nil {
		return nil, err
	}
	wv, err := a.DB.WorkByID(ed.WorkID)
	if err != nil {
		return nil, err
	}
	return &itemCtx{ed: ed, wv: wv}, nil
}

func (a *API) itemPayload(ctx *itemCtx) map[string]any {
	e := ctx.ed
	metadata := map[string]any{
		"title": ctx.wv.Title, "titleIgnoreSubtitle": ctx.wv.Title,
	}
	if ctx.wv.Subtitle != nil {
		metadata["subtitle"] = *ctx.wv.Subtitle
	}
	if ctx.wv.Author != nil {
		metadata["authorName"] = *ctx.wv.Author
		metadata["authorNameLF"] = *ctx.wv.Author
	}
	if ctx.wv.Description != nil {
		metadata["description"] = *ctx.wv.Description
	}
	chapters := make([]map[string]any, 0)
	cum := 0.0
	for _, f := range e.Files {
		var chs []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Title string  `json:"title"`
		}
		json.Unmarshal([]byte(f.Chapters), &chs)
		for i, c := range chs {
			chapters = append(chapters, map[string]any{
				"id": i + 1, "start": cum + c.Start, "end": cum + c.End, "title": c.Title,
			})
		}
		cum += f.DurationSecs
	}
	tracks := make([]map[string]any, 0, len(e.Files))
	cum = 0
	for i, f := range e.Files {
		start := cum
		cum += f.DurationSecs
		tracks = append(tracks, map[string]any{
			"index": i, "startOffset": int64(start), "duration": f.DurationSecs,
			"contentUrl": "/api/items/" + strconv.FormatInt(e.ID, 10) + "/file/" + strconv.FormatInt(f.ID, 10),
			"mimeType":   "audio/mp4",
			"metadata":   map[string]any{"title": ctx.wv.Title, "authorName": ctx.wv.Author},
		})
	}
	var size int64
	for _, f := range e.Files {
		size += f.SizeBytes
	}
	media := map[string]any{
		"id":         strconv.FormatInt(e.ID, 10),
		"metadata":   metadata,
		"coverPath":  "/api/libraries/" + strconv.FormatInt(ctx.wv.LibraryID, 10) + "/items/" + strconv.FormatInt(e.ID, 10) + "/cover",
		"tags":       []string{},
		"audioFiles": len(e.Files), "numAudioFiles": len(e.Files), "numTracks": len(e.Files),
		"chapters": chapters, "tracks": tracks,
		"duration": e.TotalDuration(), "size": size,
	}
	return map[string]any{
		"id": strconv.FormatInt(e.ID, 10), "ino": strconv.FormatInt(e.ID, 10),
		"libraryId": strconv.FormatInt(ctx.wv.LibraryID, 10),
		"mediaType": "book", "media": media,
		"addedAt": e.CreatedAt, "updatedAt": e.CreatedAt,
	}
}

func (a *API) items(w http.ResponseWriter, r *http.Request) {
	libID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	works, err := a.DB.WorksInLibrary(libID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	limit := intQuery(r, "limit", 20)
	page := intQuery(r, "page", 0)
	sortMode := r.URL.Query().Get("sort")
	results := make([]map[string]any, 0, len(works))
	for i := range works {
		for j := range works[i].Editions {
			ctx := &itemCtx{ed: &works[i].Editions[j], wv: &works[i].Work}
			results = append(results, a.itemPayload(ctx))
		}
	}
	switch sortMode {
	case "addedAt", "media.metadata.addedAt", "":
		if desc := r.URL.Query().Get("desc"); desc == "1" || desc == "true" {
			sort.Slice(results, func(x, y int) bool {
				return results[x]["addedAt"].(int64) > results[y]["addedAt"].(int64)
			})
		}
	}
	total := len(results)
	start := page * limit
	if start > total {
		start = total
	}
	end := start + limit
	if end > total {
		end = total
	}
	write(w, 200, map[string]any{
		"results": results[start:end], "total": total, "limit": limit, "page": page,
		"sortBy": sortMode, "sortDesc": false, "filterBy": "all",
		"minified": false, "collapseSeries": false,
	})
}

func (a *API) itemDetail(w http.ResponseWriter, r *http.Request) {
	ctx, err := a.resolveItem(r.PathValue("itemId"))
	if err != nil {
		fail(w, 404, "Item not found")
		return
	}
	write(w, 200, a.itemPayload(ctx))
}

func (a *API) itemCover(w http.ResponseWriter, r *http.Request) {
	ctx, err := a.resolveItem(r.PathValue("itemId"))
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	if ctx.wv.CoverPath == nil || *ctx.wv.CoverPath == "" {
		http.Error(w, "no cover", 404)
		return
	}
	path := filepath.Join(a.DataDir, "covers", filepath.Base(*ctx.wv.CoverPath))
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

func (a *API) personalized(w http.ResponseWriter, r *http.Request) {
	libID, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	works, err := a.DB.WorksInLibrary(libID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	byEdition := map[int64]*itemCtx{}
	var recent []*itemCtx
	for i := range works {
		for j := range works[i].Editions {
			ctx := &itemCtx{ed: &works[i].Editions[j], wv: &works[i].Work}
			byEdition[works[i].Editions[j].ID] = ctx
			recent = append(recent, ctx)
		}
	}
	eds, _ := a.DB.EditionsInProgress(auth.UserID(r))
	continueItems := make([]map[string]any, 0, len(eds))
	for _, eid := range eds {
		if ctx, ok := byEdition[eid]; ok {
			continueItems = append(continueItems, a.itemPayload(ctx))
		}
	}
	recentItems := make([]map[string]any, 0, len(recent))
	for _, ctx := range recent {
		recentItems = append(recentItems, a.itemPayload(ctx))
	}
	shelves := []map[string]any{}
	if len(continueItems) > 0 {
		shelves = append(shelves, map[string]any{
			"id": "continue-listening", "label": "Continue Listening", "type": "book",
			"entities": continueItems, "total": len(continueItems),
		})
	}
	shelves = append(shelves, map[string]any{
		"id": "recently-added", "label": "Recently Added", "type": "book",
		"entities": recentItems, "total": len(recentItems),
	})
	write(w, 200, shelves)
}

func (a *API) getProgress(w http.ResponseWriter, r *http.Request) {
	ctx, err := a.resolveItem(r.PathValue("itemId"))
	if err != nil {
		fail(w, 404, "Item not found")
		return
	}
	p, err := a.DB.GetProgress(auth.UserID(r), ctx.ed.ID)
	if err != nil {
		now := time.Now().UnixMilli()
		write(w, 200, map[string]any{
			"id":            "u-" + strconv.FormatInt(auth.UserID(r), 10) + "-" + strconv.FormatInt(ctx.ed.ID, 10),
			"userId":        "u-" + strconv.FormatInt(auth.UserID(r), 10),
			"libraryItemId": strconv.FormatInt(ctx.ed.ID, 10),
			"duration":      ctx.ed.TotalDuration(), "progress": 0, "currentTime": 0,
			"isFinished": false, "lastUpdate": now, "createdAt": now, "serverTime": now,
		})
		return
	}
	write(w, 200, a.progressPayload(p))
}

func (a *API) postProgress(w http.ResponseWriter, r *http.Request) {
	ctx, err := a.resolveItem(r.PathValue("itemId"))
	if err != nil {
		fail(w, 404, "Item not found")
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
	dur := body.Duration
	if dur == 0 {
		dur = ctx.ed.TotalDuration()
	}
	fileID, offset := ctx.ed.Locate(position)
	device := r.UserAgent()
	p := &store.Progress{
		UserID: auth.UserID(r), EditionID: ctx.ed.ID, FileID: &fileID, FileOffsetSecs: offset,
		EditionPositionSecs: position, DurationSecs: &dur,
		IsFinished: body.IsFinished || (dur > 0 && position >= dur-5), Device: &device,
	}
	if err := a.DB.SetProgress(p); err != nil {
		serverError(w, r, err)
		return
	}
	write(w, 200, a.progressPayload(p))
}

func (a *API) deleteProgress(w http.ResponseWriter, r *http.Request) {
	ctx, err := a.resolveItem(r.PathValue("itemId"))
	if err != nil {
		fail(w, 404, "Item not found")
		return
	}
	a.DB.DeleteProgress(auth.UserID(r), ctx.ed.ID)
	write(w, 200, map[string]any{"success": true})
}

func (a *API) listeningSessions(w http.ResponseWriter, r *http.Request) {
	write(w, 200, map[string]any{"results": []any{}, "total": 0, "page": 0, "limit": 20})
}

func (a *API) play(w http.ResponseWriter, r *http.Request) {
	ctx, err := a.resolveItem(r.PathValue("itemId"))
	if err != nil {
		fail(w, 404, "Item not found")
		return
	}
	var body struct {
		DeviceInfo         map[string]any `json:"deviceInfo"`
		MediaPlayer        string         `json:"mediaPlayer"`
		SupportedMimeTypes []string       `json:"supportedMimeTypes"`
		ForceDirectPlay    bool           `json:"forceDirectPlay"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	raw := make([]byte, 16)
	rand.Read(raw)
	sid := hex.EncodeToString(raw)
	deviceJSON, _ := json.Marshal(body.DeviceInfo)
	if body.DeviceInfo == nil {
		deviceJSON = []byte("{}")
	}
	now := time.Now().UnixMilli()
	s := &store.Session{
		ID: sid, UserID: auth.UserID(r), EditionID: ctx.ed.ID,
		StartedAt: now, UpdatedAt: now, DeviceInfo: string(deviceJSON),
	}
	if err := a.DB.CreateSession(s); err != nil {
		serverError(w, r, err)
		return
	}
	tracks := make([]map[string]any, 0, len(ctx.ed.Files))
	cum := 0
	for i, f := range ctx.ed.Files {
		start := cum
		cum += int(f.DurationSecs)
		var codec, container string
		if f.Codec != nil {
			codec = *f.Codec
		}
		if f.Container != nil {
			container = *f.Container
		}
		tracks = append(tracks, map[string]any{
			"index": i, "startOffset": start, "duration": f.DurationSecs,
			"contentUrl": "/s/" + sid + "/t/" + strconv.Itoa(i),
			"mimeType":   mimeType(codec, container),
			"metadata":   map[string]any{"title": ctx.wv.Title, "authorName": ctx.wv.Author},
		})
	}
	var progress *store.Progress
	if p, err := a.DB.GetProgress(auth.UserID(r), ctx.ed.ID); err == nil {
		progress = p
	}
	progressOut := map[string]any{}
	if progress != nil {
		progressOut = a.progressPayload(progress)
	}
	write(w, 200, map[string]any{
		"id": sid, "userId": "u-" + strconv.FormatInt(auth.UserID(r), 10),
		"libraryItemId": strconv.FormatInt(ctx.ed.ID, 10),
		"mediaType":     "book", "mediaPlayer": body.MediaPlayer,
		"deviceInfo":  body.DeviceInfo,
		"audioTracks": tracks,
		"startTime":   now, "currentTime": 0,
		"libraryItem":       a.itemPayload(ctx),
		"userMediaProgress": progressOut,
	})
}

func (a *API) itemFile(w http.ResponseWriter, r *http.Request) {
	ctx, err := a.resolveItem(r.PathValue("itemId"))
	if err != nil {
		fail(w, 404, "Item not found")
		return
	}
	fid, _ := strconv.ParseInt(r.PathValue("fileId"), 10, 64)
	for _, f := range ctx.ed.Files {
		if f.ID == fid {
			serveAudio(w, r, f.Path)
			return
		}
	}
	fail(w, 404, "File not found")
}

func (a *API) SessionTrack(w http.ResponseWriter, r *http.Request) {
	s, err := a.DB.Session(r.PathValue("sid"))
	if err != nil || s.ClosedAt != nil {
		fail(w, 404, "Session not found")
		return
	}
	index, err := strconv.Atoi(r.PathValue("index"))
	if err != nil || index < 0 {
		fail(w, 400, "Bad track index")
		return
	}
	ed, err := a.DB.EditionByID(s.EditionID)
	if err != nil || index >= len(ed.Files) {
		fail(w, 404, "Track not found")
		return
	}
	serveAudio(w, r, ed.Files[index].Path)
}

func (a *API) sessionSync(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CurrentTime  float64 `json:"currentTime"`
		TimeListened float64 `json:"timeListened"`
		Duration     float64 `json:"duration"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		fail(w, 400, "Invalid body")
		return
	}
	s, err := a.DB.Session(r.PathValue("id"))
	if err != nil || s.ClosedAt != nil || s.UserID != auth.UserID(r) {
		fail(w, 404, "Session not found")
		return
	}
	if err := a.DB.UpdateSession(s.ID, body.CurrentTime, body.TimeListened); err != nil {
		serverError(w, r, err)
		return
	}
	ed, err := a.DB.EditionByID(s.EditionID)
	if err == nil {
		fileID, offset := ed.Locate(body.CurrentTime)
		dur := body.Duration
		if dur == 0 {
			dur = ed.TotalDuration()
		}
		device := "abs-app"
		p := &store.Progress{
			UserID: s.UserID, EditionID: s.EditionID, FileID: &fileID, FileOffsetSecs: offset,
			EditionPositionSecs: body.CurrentTime, DurationSecs: &dur, Device: &device,
			IsFinished: dur > 0 && body.CurrentTime >= dur-5,
		}
		if err := a.DB.SetProgress(p); err != nil {
			serverError(w, r, err)
			return
		}
	}
	write(w, 200, map[string]any{"success": true})
}

func (a *API) sessionClose(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CurrentTime  float64 `json:"currentTime"`
		TimeListened float64 `json:"timeListened"`
		Duration     float64 `json:"duration"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	s, err := a.DB.Session(r.PathValue("id"))
	if err != nil || s.UserID != auth.UserID(r) {
		fail(w, 404, "Session not found")
		return
	}
	if s.ClosedAt == nil {
		if err := a.DB.CloseSession(s.ID, body.CurrentTime, body.TimeListened); err != nil {
			serverError(w, r, err)
			return
		}
		if ed, err := a.DB.EditionByID(s.EditionID); err == nil && body.CurrentTime > 0 {
			fileID, offset := ed.Locate(body.CurrentTime)
			dur := body.Duration
			if dur == 0 {
				dur = ed.TotalDuration()
			}
			device := "abs-app"
			p := &store.Progress{
				UserID: s.UserID, EditionID: s.EditionID, FileID: &fileID, FileOffsetSecs: offset,
				EditionPositionSecs: body.CurrentTime, DurationSecs: &dur, Device: &device,
				IsFinished: dur > 0 && body.CurrentTime >= dur-5,
			}
			if err := a.DB.SetProgress(p); err != nil {
				serverError(w, r, err)
				return
			}
		}
	}
	write(w, 200, map[string]any{"success": true})
}

func mimeType(codec, container string) string {
	switch codec {
	case "mp3":
		return "audio/mpeg"
	case "flac":
		return "audio/flac"
	case "opus", "vorbis":
		return "audio/ogg"
	case "aac":
		return "audio/mp4"
	}
	if container != "" {
		return "audio/mp4"
	}
	return "audio/mpeg"
}

func serveAudio(w http.ResponseWriter, r *http.Request, path string) {
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "gone", 404)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	http.ServeContent(w, r, filepath.Base(path), fi.ModTime(), f)
}

func intQuery(r *http.Request, key string, def int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	if n == 0 {
		return def
	}
	return n
}

func serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Warn("libteca: abs request failed", "path", r.URL.Path, "err", err)
	fail(w, 500, "Internal server error")
}

func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	write(w, status, map[string]any{"error": msg})
}
