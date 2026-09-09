package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/scan"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-dev/neutron-go/neutron"
)

type API struct {
	DB       *store.DB
	DataDir  string
	scanning atomic.Bool
}

func New(db *store.DB, dataDir string) *API {
	return &API{DB: db, DataDir: dataDir}
}

func (a *API) MountPublic(r *neutron.Router) {
	r.HandleFunc("POST /login", a.login)
}

func (a *API) Mount(r *neutron.Router) {
	r.HandleFunc("GET /me", a.me)
	r.HandleFunc("GET /libraries", a.libraries)
	r.HandleFunc("POST /libraries", a.addLibrary)
	r.HandleFunc("POST /libraries/{id}/scan", a.scanLibrary)
	r.HandleFunc("GET /libraries/{id}/works", a.works)
	r.HandleFunc("GET /works/{id}", a.work)
	r.HandleFunc("GET /progress/{editionId}", a.getProgress)
	r.HandleFunc("POST /progress/{editionId}", a.setProgress)
	r.HandleFunc("GET /stream/{fileId}", a.stream)
	r.HandleFunc("GET /covers/{cover}", a.cover)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	u, err := a.DB.UserByName(body.Username)
	if err != nil || !auth.Verify(body.Password, u.PasswordHash) {
		writeJSON(w, 401, map[string]string{"error": "invalid credentials"})
		return
	}
	token, err := auth.IssueToken(a.DB, u.ID, r.UserAgent())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "token issue failed"})
		return
	}
	writeJSON(w, 200, map[string]any{"token": token, "user": map[string]any{"id": u.ID, "name": u.Name, "isAdmin": u.IsAdmin}})
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	u, err := a.DB.User(auth.UserID(r))
	if err != nil {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	list, _ := a.DB.UserProgressList(u.ID)
	progress := make([]map[string]any, 0, len(list))
	for _, p := range list {
		progress = append(progress, map[string]any{
			"editionId": p.EditionID, "position": p.EditionPositionSecs,
			"duration": p.DurationSecs, "isFinished": p.IsFinished, "updatedAt": p.UpdatedAt,
		})
	}
	writeJSON(w, 200, map[string]any{"id": u.ID, "name": u.Name, "isAdmin": u.IsAdmin, "progress": progress})
}

func (a *API) libraries(w http.ResponseWriter, r *http.Request) {
	libs, err := a.DB.Libraries()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, libs)
}

func (a *API) addLibrary(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" || body.Path == "" {
		writeJSON(w, 400, map[string]string{"error": "name and path required"})
		return
	}
	if body.Type == "" {
		body.Type = "audiobooks"
	}
	abs, err := filepath.Abs(body.Path)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad path"})
		return
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		writeJSON(w, 400, map[string]string{"error": "path is not a directory"})
		return
	}
	id, err := a.DB.AddLibrary(body.Name, body.Type, abs)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *API) scanLibrary(w http.ResponseWriter, r *http.Request) {
	id := auth.Atoi64(r.PathValue("id"))
	lib, err := a.DB.Library(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "library not found"})
		return
	}
	if !a.scanning.CompareAndSwap(false, true) {
		writeJSON(w, 409, map[string]string{"error": "scan already running"})
		return
	}
	go func() {
		defer a.scanning.Store(false)
		if _, err := scan.Library(a.DB, lib, filepath.Join(a.DataDir, "covers")); err != nil {
			fmt.Println("libteca: scan:", err)
		}
	}()
	writeJSON(w, 202, map[string]string{"status": "scanning"})
}

func (a *API) works(w http.ResponseWriter, r *http.Request) {
	id := auth.Atoi64(r.PathValue("id"))
	works, err := a.DB.WorksInLibrary(id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(works))
	for _, wv := range works {
		item := map[string]any{
			"id": wv.ID, "title": wv.Title, "author": wv.Author, "subtitle": wv.Subtitle,
			"description": wv.Description, "hasCover": wv.CoverPath != nil && *wv.CoverPath != "",
			"editions": make([]map[string]any, 0, len(wv.Editions)),
		}
		eds := item["editions"].([]map[string]any)
		for _, ev := range wv.Editions {
			eds = append(eds, map[string]any{
				"id": ev.ID, "format": ev.Format, "title": ev.Title,
				"duration": ev.TotalDuration(), "files": len(ev.Files),
			})
		}
		item["editions"] = eds
		out = append(out, item)
	}
	writeJSON(w, 200, out)
}

func (a *API) work(w http.ResponseWriter, r *http.Request) {
	id := auth.Atoi64(r.PathValue("id"))
	wv, err := a.DB.WorkByID(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	lib, _ := a.DB.Library(wv.LibraryID)
	works, err := a.DB.WorksInLibrary(wv.LibraryID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	for _, full := range works {
		if full.ID != id {
			continue
		}
		progress, _ := a.DB.UserProgressList(auth.UserID(r))
		pmap := map[int64]*store.Progress{}
		for i := range progress {
			pmap[progress[i].EditionID] = &progress[i]
		}
		eds := make([]map[string]any, 0, len(full.Editions))
		for _, ev := range full.Editions {
			chapters := []map[string]any{}
			cum := 0.0
			for fi, f := range ev.Files {
				var ch []audioChapter
				json.Unmarshal([]byte(f.Chapters), &ch)
				for _, c := range ch {
					chapters = append(chapters, map[string]any{
						"title": c.Title, "start": cum + c.Start, "end": cum + c.End, "fileId": f.ID,
					})
				}
				_ = fi
				cum += f.DurationSecs
			}
			files := make([]map[string]any, 0, len(ev.Files))
			for _, f := range ev.Files {
				files = append(files, map[string]any{
					"id": f.ID, "seq": f.Seq, "duration": f.DurationSecs, "size": f.SizeBytes,
				})
			}
			e := map[string]any{
				"id": ev.ID, "format": ev.Format, "title": ev.Title, "duration": ev.TotalDuration(),
				"files": files, "chapters": chapters, "position": ev.Position,
			}
			if ev.SeasonNum != nil {
				e["seasonNum"] = *ev.SeasonNum
			}
			if ev.EpisodeNum != nil {
				e["episodeNum"] = *ev.EpisodeNum
			}
			if p, ok := pmap[ev.ID]; ok {
				e["position"] = p.EditionPositionSecs
				e["isFinished"] = p.IsFinished
			}
			eds = append(eds, e)
		}
		writeJSON(w, 200, map[string]any{
			"id": full.ID, "libraryId": full.LibraryID, "libraryName": lib.Name,
			"title": full.Title, "subtitle": full.Subtitle, "author": full.Author,
			"description": full.Description, "hasCover": full.CoverPath != nil && *full.CoverPath != "",
			"editions": eds,
		})
		return
	}
	writeJSON(w, 404, map[string]string{"error": "not found"})
}

type audioChapter struct {
	ID    int64   `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

func (a *API) getProgress(w http.ResponseWriter, r *http.Request) {
	eid := auth.Atoi64(r.PathValue("editionId"))
	p, err := a.DB.GetProgress(auth.UserID(r), eid)
	if err != nil {
		writeJSON(w, 200, map[string]any{"editionId": eid, "position": 0, "isFinished": false})
		return
	}
	writeJSON(w, 200, map[string]any{
		"editionId": p.EditionID, "fileId": p.FileID, "offset": p.FileOffsetSecs,
		"position": p.EditionPositionSecs, "duration": p.DurationSecs, "isFinished": p.IsFinished,
		"updatedAt": p.UpdatedAt,
	})
}

func (a *API) setProgress(w http.ResponseWriter, r *http.Request) {
	eid := auth.Atoi64(r.PathValue("editionId"))
	if _, err := a.DB.EditionByID(eid); err != nil {
		writeJSON(w, 404, map[string]string{"error": "edition not found"})
		return
	}
	var body struct {
		Position float64 `json:"position"`
		Duration float64 `json:"duration"`
		Finished bool    `json:"finished"`
		Device   string  `json:"device"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	ed, _ := a.DB.EditionByID(eid)
	fileID, offset := ed.Locate(body.Position)
	var dur *float64
	if body.Duration > 0 {
		dur = &body.Duration
	}
	p := &store.Progress{
		UserID: auth.UserID(r), EditionID: eid, FileID: &fileID, FileOffsetSecs: offset,
		EditionPositionSecs: body.Position, DurationSecs: dur, IsFinished: body.Finished,
	}
	if err := a.DB.SetProgress(p); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *API) stream(w http.ResponseWriter, r *http.Request) {
	fid := auth.Atoi64(r.PathValue("fileId"))
	f, err := a.DB.FileByID(fid)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "file not found"})
		return
	}
	serveFile(w, r, f.Path)
}

func (a *API) cover(w http.ResponseWriter, r *http.Request) {
	name := filepath.Base(r.PathValue("cover"))
	path := filepath.Join(a.DataDir, "covers", name)
	if _, err := os.Stat(path); err != nil {
		writeJSON(w, 404, map[string]string{"error": "no cover"})
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	serveFile(w, r, path)
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
