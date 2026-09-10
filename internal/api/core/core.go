package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/scan"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-dev/neutron-go/neutron"
)

type ScanFunc func(db *store.DB, lib *store.Library, coversDir string, onProgress scan.ProgressFn) (int, error)

type API struct {
	DB       *store.DB
	DataDir  string
	ScanFunc ScanFunc

	mu   sync.Mutex
	runs map[int64]*scanRun
}

func New(db *store.DB, dataDir string) *API {
	if n, err := db.FailRunningScanJobs(); err == nil && n > 0 {
		fmt.Printf("libteca: marked %d interrupted scan job(s) as error\n", n)
	}
	return &API{DB: db, DataDir: dataDir, runs: map[int64]*scanRun{}}
}

func (a *API) MountPublic(r *neutron.Router) {
	r.HandleFunc("POST /login", a.login)
}

func (a *API) Mount(r *neutron.Router) {
	r.HandleFunc("GET /me", a.me)
	r.HandleFunc("GET /libraries", a.libraries)
	r.HandleFunc("POST /libraries", a.addLibrary)
	r.HandleFunc("POST /libraries/{id}/scan", a.scanLibrary)
	r.HandleFunc("GET /libraries/{id}/scan/jobs", a.scanJobs)
	r.HandleFunc("GET /libraries/{id}/scan/events", a.scanEvents)
	r.HandleFunc("GET /scan-jobs/{id}", a.scanJob)
	r.HandleFunc("GET /resume", a.resume)
	r.HandleFunc("GET /search", a.search)
	r.HandleFunc("GET /recent", a.recent)
	r.HandleFunc("GET /libraries/{id}/works", a.works)
	r.HandleFunc("GET /works/{id}", a.work)
	r.HandleFunc("GET /subtitles/{fileId}", a.subtitles)
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
	a.mu.Lock()
	if run := a.runs[id]; run != nil && !run.isClosed() {
		a.mu.Unlock()
		writeJSON(w, 409, map[string]any{"error": "scan already running", "status": "already_scanning", "jobId": run.jobID})
		return
	}
	delete(a.runs, id)
	jobID, err := a.DB.CreateScanJob(id)
	if err != nil {
		a.mu.Unlock()
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	run := newScanRun(jobID, id)
	a.runs[id] = run
	a.mu.Unlock()
	go a.runScan(run, lib)
	writeJSON(w, 202, map[string]any{"status": "scanning", "jobId": jobID})
}

const scanPersistInterval = 2 * time.Second

func (a *API) runScan(run *scanRun, lib *store.Library) {
	defer func() {
		a.mu.Lock()
		if a.runs[run.libraryID] == run {
			delete(a.runs, run.libraryID)
		}
		a.mu.Unlock()
	}()
	scanFn := a.ScanFunc
	if scanFn == nil {
		scanFn = scan.Library
	}
	var lastPersist time.Time
	onProgress := func(p scan.Progress) {
		run.publish(p)
		if time.Since(lastPersist) >= scanPersistInterval {
			lastPersist = time.Now()
			a.DB.UpdateScanJobCounts(run.jobID, int64(p.FilesSeen), int64(p.FilesProbed), int64(p.FilesAdded), int64(p.FilesUpdated), int64(p.WorksChanged))
		}
	}
	_, err := scanFn(a.DB, lib, filepath.Join(a.DataDir, "covers"), onProgress)
	final := run.snapshot()
	a.DB.UpdateScanJobCounts(run.jobID, int64(final.FilesSeen), int64(final.FilesProbed), int64(final.FilesAdded), int64(final.FilesUpdated), int64(final.WorksChanged))
	if err != nil {
		msg := err.Error()
		a.DB.FinishScanJob(run.jobID, "error", &msg)
		run.finish("error", msg)
		fmt.Println("libteca: scan:", err)
		return
	}
	a.DB.FinishScanJob(run.jobID, "done", nil)
	run.finish("done", "")
}

type scanEvent struct {
	JobID        int64  `json:"jobId"`
	LibraryID    int64  `json:"libraryId"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	FilesSeen    int    `json:"filesSeen"`
	FilesProbed  int    `json:"filesProbed"`
	FilesAdded   int    `json:"filesAdded"`
	FilesUpdated int    `json:"filesUpdated"`
	WorksChanged int    `json:"worksChanged"`
	CurrentPath  string `json:"currentPath,omitempty"`
	StartedAt    int64  `json:"startedAt"`
	FinishedAt   *int64 `json:"finishedAt,omitempty"`
}

type scanRun struct {
	jobID     int64
	libraryID int64
	startedAt int64
	done      chan struct{}

	mu     sync.Mutex
	prog   scan.Progress
	subs   map[chan scanEvent]struct{}
	final  scanEvent
	closed bool
}

func newScanRun(jobID, libraryID int64) *scanRun {
	return &scanRun{
		jobID: jobID, libraryID: libraryID, startedAt: nowMilli(),
		done: make(chan struct{}),
		subs: map[chan scanEvent]struct{}{},
	}
}

func (r *scanRun) eventLocked(status string, finishedAt *int64, errMsg string) scanEvent {
	return scanEvent{
		JobID: r.jobID, LibraryID: r.libraryID, Status: status, Error: errMsg,
		FilesSeen: r.prog.FilesSeen, FilesProbed: r.prog.FilesProbed,
		FilesAdded: r.prog.FilesAdded, FilesUpdated: r.prog.FilesUpdated,
		WorksChanged: r.prog.WorksChanged, CurrentPath: r.prog.CurrentPath,
		StartedAt: r.startedAt, FinishedAt: finishedAt,
	}
}

func (r *scanRun) publish(p scan.Progress) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prog = p
	ev := r.eventLocked("running", nil, "")
	for ch := range r.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (r *scanRun) finish(status, errMsg string) {
	r.mu.Lock()
	fin := nowMilli()
	ev := r.eventLocked(status, &fin, errMsg)
	r.final = ev
	r.closed = true
	for ch := range r.subs {
		select {
		case ch <- ev:
		default:
		}
	}
	r.mu.Unlock()
	close(r.done)
}

func (r *scanRun) snapshot() scan.Progress {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prog
}

func (r *scanRun) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

// subscribe returns (channel, snapshot, ok). ok is false when the job already
// finished; the DB then already holds the terminal row.
func (r *scanRun) subscribe() (chan scanEvent, scanEvent, bool) {
	ch := make(chan scanEvent, 128)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, scanEvent{}, false
	}
	r.subs[ch] = struct{}{}
	return ch, r.eventLocked("running", nil, ""), true
}

func (r *scanRun) unsubscribe(ch chan scanEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subs, ch)
}

func (a *API) scanJobs(w http.ResponseWriter, r *http.Request) {
	id := auth.Atoi64(r.PathValue("id"))
	if _, err := a.DB.Library(id); err != nil {
		writeJSON(w, 404, map[string]string{"error": "library not found"})
		return
	}
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 100 {
		limit = 100
	}
	jobs, err := a.DB.ListScanJobs(id, limit)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		out = append(out, jobJSON(&jobs[i]))
	}
	writeJSON(w, 200, out)
}

func (a *API) scanJob(w http.ResponseWriter, r *http.Request) {
	id := auth.Atoi64(r.PathValue("id"))
	j, err := a.DB.GetScanJob(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "job not found"})
		return
	}
	writeJSON(w, 200, jobJSON(j))
}

func jobJSON(j *store.ScanJob) map[string]any {
	return map[string]any{
		"id": j.ID, "libraryId": j.LibraryID, "status": j.Status, "error": j.Error,
		"filesSeen": j.FilesSeen, "filesProbed": j.FilesProbed, "filesAdded": j.FilesAdded,
		"filesUpdated": j.FilesUpdated, "worksChanged": j.WorksChanged,
		"startedAt": j.StartedAt, "finishedAt": j.FinishedAt, "createdAt": j.CreatedAt,
	}
}

const sseMinInterval = 250 * time.Millisecond

func (a *API) scanEvents(w http.ResponseWriter, r *http.Request) {
	id := auth.Atoi64(r.PathValue("id"))
	if _, err := a.DB.Library(id); err != nil {
		writeJSON(w, 404, map[string]string{"error": "library not found"})
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, 500, map[string]string{"error": "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Not scanning: send one baseline event (last job, or idle) and close.
	// The retry hint spaces EventSource reconnects out.
	a.mu.Lock()
	run := a.runs[id]
	a.mu.Unlock()
	if run == nil {
		fmt.Fprint(w, "retry: 5000\n\n")
		jobs, _ := a.DB.ListScanJobs(id, 1)
		if len(jobs) > 0 {
			writeSSE(w, fl, jobEvent(&jobs[0]))
		} else {
			writeSSE(w, fl, scanEvent{LibraryID: id, Status: "idle"})
		}
		return
	}

	ch, snap, ok := run.subscribe()
	if !ok {
		fmt.Fprint(w, "retry: 5000\n\n")
		jobs, _ := a.DB.ListScanJobs(id, 1)
		if len(jobs) > 0 {
			writeSSE(w, fl, jobEvent(&jobs[0]))
		}
		return
	}
	defer run.unsubscribe(ch)

	lastSent := time.Now()
	writeSSE(w, fl, snap)
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		case ev := <-ch:
			if ev.Status != "running" {
				writeSSE(w, fl, ev)
				return
			}
			if time.Since(lastSent) < sseMinInterval {
				continue
			}
			lastSent = time.Now()
			if !writeSSE(w, fl, ev) {
				return
			}
		case <-run.done:
			run.mu.Lock()
			final := run.final
			run.mu.Unlock()
			writeSSE(w, fl, final)
			return
		}
	}
}

func jobEvent(j *store.ScanJob) scanEvent {
	ev := scanEvent{
		JobID: j.ID, LibraryID: j.LibraryID, Status: j.Status,
		FilesSeen: int(j.FilesSeen), FilesProbed: int(j.FilesProbed),
		FilesAdded: int(j.FilesAdded), FilesUpdated: int(j.FilesUpdated),
		WorksChanged: int(j.WorksChanged), StartedAt: j.StartedAt, FinishedAt: j.FinishedAt,
	}
	if j.Error != nil {
		ev.Error = *j.Error
	}
	return ev
}

func writeSSE(w http.ResponseWriter, fl http.Flusher, ev scanEvent) bool {
	data, err := json.Marshal(ev)
	if err != nil {
		return false
	}
	if _, err := fmt.Fprintf(w, "event: progress\ndata: %s\n\n", data); err != nil {
		return false
	}
	fl.Flush()
	return true
}

func nowMilli() int64 { return time.Now().UnixMilli() }

func (a *API) works(w http.ResponseWriter, r *http.Request) {
	id := auth.Atoi64(r.PathValue("id"))
	q := r.URL.Query()
	sort, dir, filter := q.Get("sort"), q.Get("dir"), q.Get("filter")
	switch sort {
	case "title", "author", "added", "updated":
	default:
		sort = "title"
	}
	if dir != "asc" && dir != "desc" {
		if sort == "added" || sort == "updated" {
			dir = "desc"
		} else {
			dir = "asc"
		}
	}
	switch filter {
	case "in_progress", "unplayed", "finished":
	default:
		filter = "all"
	}
	works, err := a.DB.WorksInLibraryFiltered(id, auth.UserID(r), sort, dir, filter)
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
