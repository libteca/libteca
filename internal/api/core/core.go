package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/podcast"
	"github.com/libteca/libteca/internal/scan"
	"github.com/libteca/libteca/internal/store"
	"github.com/libteca/libteca/internal/transcode"
	"github.com/libteca/libteca/internal/trickplay"
	"github.com/neutron-build/neutron/go/neutron"
)

type ScanFunc func(ctx context.Context, db *store.DB, lib *store.Library, coversDir string, onProgress scan.ProgressFn) (int, error)

type API struct {
	DB       *store.DB
	DataDir  string
	ScanFunc ScanFunc
	TC       *transcode.Manager
	Podcasts *podcast.Service

	LoginLimiter *auth.Limiter

	mu       sync.Mutex
	runs     map[int64]*scanRun
	shutdown context.Context

	metaMu   sync.Mutex
	metaRuns map[int64]*metaRun

	opmlMu  sync.Mutex
	opmlRun *opmlRun

	jobsMu      sync.Mutex
	jobsClosing bool
	jobsWG      sync.WaitGroup

	tpMu sync.Mutex
	tp   *trickplay.Generator
}

func New(db *store.DB, dataDir string) *API {
	if n, err := db.FailRunningScanJobs(); err == nil && n > 0 {
		fmt.Printf("libteca: marked %d interrupted scan job(s) as error\n", n)
	}
	return &API{DB: db, DataDir: dataDir, runs: map[int64]*scanRun{}, metaRuns: map[int64]*metaRun{}}
}

// launchJob registers owned background work so shutdown can drain it: a
// bare goroutine launch was invisible to WaitJobs, and work admitted during
// shutdown outlived the database close.
func (a *API) launchJob(fn func()) bool {
	a.jobsMu.Lock()
	if a.jobsClosing {
		a.jobsMu.Unlock()
		return false
	}
	a.jobsWG.Add(1)
	a.jobsMu.Unlock()
	go func() {
		defer a.jobsWG.Done()
		fn()
	}()
	return true
}

// WaitJobs stops admitting new background work and waits for registered
// jobs to finish. Callers must not hold API mutexes.
func (a *API) WaitJobs() {
	a.jobsMu.Lock()
	a.jobsClosing = true
	a.jobsMu.Unlock()
	a.jobsWG.Wait()
}

func (a *API) SetShutdownCtx(ctx context.Context) {
	a.mu.Lock()
	a.shutdown = ctx
	a.mu.Unlock()
}

func (a *API) scanCtx() context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.shutdown == nil {
		return context.Background()
	}
	return a.shutdown
}

func (a *API) MountPublic(r *neutron.Router) {
	r.HandleFunc("POST /login", a.login)
}

func (a *API) Mount(r *neutron.Router) {
	r.HandleFunc("GET /me", a.me)
	r.HandleFunc("GET /settings/providers", a.providerKeysGet)
	r.HandleFunc("PUT /settings/providers", a.providerKeysPut)
	r.HandleFunc("GET /libraries", a.libraries)
	r.HandleFunc("POST /libraries", a.addLibrary)
	r.HandleFunc("DELETE /libraries/{id}", a.deleteLibrary)
	r.HandleFunc("POST /libraries/{id}/scan", a.scanLibrary)
	r.HandleFunc("GET /libraries/{id}/scan/jobs", a.scanJobs)
	r.HandleFunc("GET /libraries/{id}/scan/events", a.scanEvents)
	r.HandleFunc("GET /scan-jobs/{id}", a.scanJob)
	r.HandleFunc("GET /resume", a.resume)
	r.HandleFunc("GET /search", a.search)
	r.HandleFunc("GET /recent", a.recent)
	r.HandleFunc("GET /nextup", a.nextUp)
	r.HandleFunc("GET /libraries/{id}/works", a.works)
	r.HandleFunc("GET /works/{id}", a.work)
	r.HandleFunc("GET /subtitles/{fileId}", a.subtitles)
	r.HandleFunc("GET /progress/{editionId}", a.getProgress)
	r.HandleFunc("POST /progress/{editionId}", a.setProgress)
	r.HandleFunc("GET /stream/{fileId}", a.stream)
	r.HandleFunc("GET /covers/{cover}", a.cover)
	a.MountReading(r)
	a.MountUsers(r)
	a.MountPlaylists(r)
	a.MountLinking(r)
	a.MountImport(r)
	a.MountProviders(r)
	a.MountPodcasts(r)
	a.MountHLS(r)
}

// writeJSON emits application/json. For 404s it emits neutron's RFC 7807
// problem+json instead: the router's errInterceptor replaces any non-problem
// 404 body with a generic "No route matches" document, which would swallow
// the handler's specific message; problem+json passes through untouched.
// The body is serialized BEFORE the status is written: a value that cannot
// marshal (e.g. NaN imported through a non-JSON ingress) used to commit a
// 200 and then emit an empty or truncated body.
func writeJSON(w http.ResponseWriter, status int, v any) {
	contentType := "application/json"
	if status == http.StatusNotFound {
		detail := "Not Found"
		if m, ok := v.(map[string]string); ok && m["error"] != "" {
			detail = m["error"]
		}
		contentType = "application/problem+json"
		v = map[string]any{
			"type": "https://neutron.dev/errors/not-found", "title": "Not Found",
			"status": status, "detail": detail,
		}
	}
	data, err := json.Marshal(v)
	if err != nil {
		slog.Error("libteca: json serialization failed", "err", err)
		status = http.StatusInternalServerError
		contentType = "application/json"
		data = []byte(`{"error":"internal error"}`)
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	w.Write(append(data, '\n'))
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
	// Principal-aware bucket: a success for one account must not erase the
	// failures accumulated against another from the same IP.
	ip := auth.ClientIP(r) + "|" + strings.ToLower(strings.TrimSpace(body.Username))
	if a.LoginLimiter != nil {
		if ok, retry := a.LoginLimiter.Allow(ip); !ok {
			auth.WriteRetryAfter(w, retry)
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts, try again later"})
			return
		}
	}
	u, err := auth.CheckPassword(r.Context(), a.DB, body.Username, body.Password)
	if err != nil {
		if errors.Is(err, auth.ErrKDFBusy) {
			auth.WriteRetryAfter(w, time.Second)
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "server busy, try again later"})
			return
		}
		if errors.Is(err, auth.ErrBadCredentials) {
			if a.LoginLimiter != nil {
				a.LoginLimiter.Failure(ip)
			}
			writeJSON(w, 401, map[string]string{"error": "invalid credentials"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	if a.LoginLimiter != nil {
		a.LoginLimiter.Success(ip)
	}
	// Issuance is conditional on the hash that just verified: a concurrent
	// password rotation revokes tokens in the same transaction that swaps
	// the hash, so an old-password login cannot mint a surviving token.
	token, err := auth.IssueTokenForPassword(a.DB, u.ID, r.UserAgent(), u.PasswordHash)
	if err != nil {
		if errors.Is(err, auth.ErrCredentialsChanged) {
			writeJSON(w, 401, map[string]string{"error": "invalid credentials"})
			return
		}
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
	list, err := a.DB.UserProgressList(u.ID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
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
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	admin := a.isAdminRequest(r)
	out := make([]map[string]any, 0, len(libs))
	for _, l := range libs {
		item := map[string]any{
			"id": l.ID, "name": l.Name, "type": l.Type,
		}
		if admin {
			item["path"] = l.Path
		}
		out = append(out, item)
	}
	writeJSON(w, 200, out)
}

func (a *API) addLibrary(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	var body struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		writeJSON(w, 400, map[string]string{"error": "name and path required"})
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 200 || strings.ContainsRune(body.Path, '\x00') {
		writeJSON(w, 400, map[string]string{"error": "invalid name or path"})
		return
	}
	if body.Type == "" {
		body.Type = "audiobooks"
	}
	switch body.Type {
	case "movies", "tv", "music", "audiobooks", "books", "comics", "games":
	default:
		writeJSON(w, 400, map[string]string{"error": "unsupported library type"})
		return
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
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

// scanDedupWindow suppresses back-to-back rescans: a repeat POST within the
// window after a scan finished 409s with the last job id instead of starting
// a new no-op job (warm rescans finish in milliseconds, so a second click
// lands after the in-flight conflict window closes).
var scanDedupWindow = 10 * time.Second

func (a *API) scanLibrary(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
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
	if jobs, err := a.DB.ListScanJobs(id, 1); err == nil && len(jobs) > 0 {
		j := jobs[0]
		if j.Status == "running" {
			a.mu.Unlock()
			writeJSON(w, 409, map[string]any{"error": "scan already running", "status": "already_scanning", "jobId": j.ID})
			return
		}
		if j.Status == "done" && j.FinishedAt != nil && time.Since(time.UnixMilli(*j.FinishedAt)) < scanDedupWindow {
			a.mu.Unlock()
			writeJSON(w, 409, map[string]any{"error": "scan already completed", "status": "already_done", "jobId": j.ID})
			return
		}
	}
	jobID, err := a.DB.CreateScanJob(id)
	if err != nil {
		a.mu.Unlock()
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	run := newScanRun(jobID, id)
	a.runs[id] = run
	a.mu.Unlock()
	if !a.launchJob(func() { a.runScan(a.scanCtx(), run, lib) }) {
		a.mu.Lock()
		delete(a.runs, id)
		a.mu.Unlock()
		msg := "server shutting down"
		a.DB.FinishScanJob(run.jobID, "error", &msg)
		writeJSON(w, 503, map[string]string{"error": "server shutting down"})
		return
	}
	writeJSON(w, 202, map[string]any{"status": "scanning", "jobId": jobID})
}

var ErrScanRunning = errors.New("scan already running")

// TriggerScan starts a library scan through the same lock and job machinery
// as POST /libraries/{id}/scan. It returns ErrScanRunning when a scan is
// already in flight for the library (in-process run or running job row).
func (a *API) TriggerScan(ctx context.Context, libraryID int64) (int64, error) {
	lib, err := a.DB.Library(libraryID)
	if err != nil {
		return 0, err
	}
	if jobs, err := a.DB.ListScanJobs(libraryID, 1); err == nil && len(jobs) > 0 && jobs[0].Status == "running" {
		return 0, ErrScanRunning
	}
	a.mu.Lock()
	if run := a.runs[libraryID]; run != nil && !run.isClosed() {
		a.mu.Unlock()
		return 0, ErrScanRunning
	}
	delete(a.runs, libraryID)
	jobID, err := a.DB.CreateScanJob(libraryID)
	if err != nil {
		a.mu.Unlock()
		return 0, err
	}
	run := newScanRun(jobID, libraryID)
	a.runs[libraryID] = run
	a.mu.Unlock()
	if !a.launchJob(func() { a.runScan(ctx, run, lib) }) {
		a.mu.Lock()
		delete(a.runs, libraryID)
		a.mu.Unlock()
		msg := "server shutting down"
		a.DB.FinishScanJob(run.jobID, "error", &msg)
		return 0, fmt.Errorf("server shutting down")
	}
	return jobID, nil
}

const scanPersistInterval = 2 * time.Second

// persistScanTerminal writes the terminal job state with bounded retry: a
// discarded failure left the durable row 'running' while memory said
// finished, wedging the admission and watcher paths until restart. Startup
// reconciliation (FailRunningScanJobs) remains the backstop for a hard
// crash between retries.
func (a *API) persistScanTerminal(run *scanRun, status string, message *string) {
	p := run.snapshot()
	if err := a.DB.UpdateScanJobCounts(run.jobID, int64(p.FilesSeen), int64(p.FilesProbed), int64(p.FilesAdded), int64(p.FilesUpdated), int64(p.WorksChanged)); err != nil {
		slog.Warn("libteca: scan counts persistence failed", "job", run.jobID, "err", err)
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if err := a.DB.FinishScanJob(run.jobID, status, message); err == nil {
			return
		} else {
			lastErr = err
		}
		if attempt < 2 {
			time.Sleep(time.Duration(attempt+1) * 100 * time.Millisecond)
		}
	}
	slog.Error("libteca: scan terminal state persistence failed", "job", run.jobID, "status", status, "err", lastErr)
}

func (a *API) runScan(ctx context.Context, run *scanRun, lib *store.Library) {
	// Bare goroutine, outside request recovery: a panic here used to take
	// the whole server down.
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("scan panicked", "library", lib.ID, "panic", rec)
			// Persist the terminal status too: an in-memory-only finish left
			// the scan_jobs row 'running' forever - the watcher polled it
			// endlessly and the library returned 409 until restart.
			msg := fmt.Sprintf("scan panicked: %v", rec)
			a.persistScanTerminal(run, "error", &msg)
			run.finish("error", msg)
		}
	}()
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
			if err := a.DB.UpdateScanJobCounts(run.jobID, int64(p.FilesSeen), int64(p.FilesProbed), int64(p.FilesAdded), int64(p.FilesUpdated), int64(p.WorksChanged)); err != nil {
				slog.Warn("libteca: scan progress persistence failed", "job", run.jobID, "err", err)
			}
		}
	}
	_, err := scanFn(ctx, a.DB, lib, filepath.Join(a.DataDir, "covers"), onProgress)
	if err != nil {
		msg := err.Error()
		if ctx.Err() != nil {
			msg = "cancelled"
		}
		a.persistScanTerminal(run, "error", &msg)
		run.finish("error", msg)
		fmt.Println("libteca: scan:", err)
		return
	}
	_, _ = a.DB.PruneProviderCache()
	a.persistScanTerminal(run, "done", nil)
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
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	admin := a.isAdminRequest(r)
	out := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		out = append(out, jobJSON(&jobs[i], admin))
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
	writeJSON(w, 200, jobJSON(j, a.isAdminRequest(r)))
}

// jobJSON renders a scan job. The error text can embed server filesystem
// paths, so it is dropped for non-admin callers.
func jobJSON(j *store.ScanJob, admin bool) map[string]any {
	errStr := any(nil)
	if j.Error != nil {
		if admin {
			errStr = *j.Error
		} else {
			errStr = "error"
		}
	}
	return map[string]any{
		"id": j.ID, "libraryId": j.LibraryID, "status": j.Status, "error": errStr,
		"filesSeen": j.FilesSeen, "filesProbed": j.FilesProbed, "filesAdded": j.FilesAdded,
		"filesUpdated": j.FilesUpdated, "worksChanged": j.WorksChanged,
		"startedAt": j.StartedAt, "finishedAt": j.FinishedAt, "createdAt": j.CreatedAt,
	}
}

// maskScanEvent hides server filesystem details (current file path, error
// text) from non-admin subscribers; counts and statuses stay intact.
func maskScanEvent(ev scanEvent, admin bool) scanEvent {
	if admin {
		return ev
	}
	ev.CurrentPath = ""
	ev.Error = ""
	return ev
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
	admin := a.isAdminRequest(r)

	// Not scanning: send one baseline event (last job, or idle) and close.
	// The retry hint spaces EventSource reconnects out.
	a.mu.Lock()
	run := a.runs[id]
	a.mu.Unlock()
	if run == nil {
		fmt.Fprint(w, "retry: 5000\n\n")
		jobs, _ := a.DB.ListScanJobs(id, 1)
		if len(jobs) > 0 {
			writeSSE(w, fl, maskScanEvent(jobEvent(&jobs[0]), admin))
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
			writeSSE(w, fl, maskScanEvent(jobEvent(&jobs[0]), admin))
		}
		return
	}
	defer run.unsubscribe(ch)

	lastSent := time.Now()
	writeSSE(w, fl, maskScanEvent(snap, admin))
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
				writeSSE(w, fl, maskScanEvent(ev, admin))
				return
			}
			if time.Since(lastSent) < sseMinInterval {
				continue
			}
			lastSent = time.Now()
			if !writeSSE(w, fl, maskScanEvent(ev, admin)) {
				return
			}
		case <-run.done:
			run.mu.Lock()
			final := run.final
			run.mu.Unlock()
			writeSSE(w, fl, maskScanEvent(final, admin))
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

func fileOK(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Size() > 0
}

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
	limit := 200
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 1000 {
		limit = 1000
	}
	offset := 0
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			offset = n
		}
	}
	works, err := a.DB.WorksInLibraryFiltered(id, auth.UserID(r), sort, dir, filter, limit, offset)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	out := make([]map[string]any, 0, len(works))
	for _, wv := range works {
		item := map[string]any{
			"id": wv.ID, "title": wv.Title, "author": wv.Author, "subtitle": wv.Subtitle,
			"description": wv.Description, "hasCover": wv.CoverPath != nil && *wv.CoverPath != "",
			"editions": make([]map[string]any, 0, len(wv.Editions)),
		}
		if wv.Percent != nil {
			item["percent"] = *wv.Percent
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
	full, err := a.DB.WorkViewByID(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	lib, err := a.DB.Library(full.LibraryID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	progress, err := a.DB.UserProgressList(auth.UserID(r))
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	pmap := map[int64]*store.Progress{}
	for i := range progress {
		pmap[progress[i].EditionID] = &progress[i]
	}
	pageCounts, err := a.DB.PageCountsByWork(id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	rmap, err := a.DB.ReadingListByUser(auth.UserID(r))
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	eds := make([]map[string]any, 0, len(full.Editions))
	for _, ev := range full.Editions {
		chapters := []map[string]any{}
		cum := 0.0
		for _, f := range ev.Files {
			var ch []audioChapter
			json.Unmarshal([]byte(f.Chapters), &ch)
			for _, c := range ch {
				chapters = append(chapters, map[string]any{
					"title": c.Title, "start": cum + c.Start, "end": cum + c.End, "fileId": f.ID,
				})
			}
			cum += f.DurationSecs
		}
		files := make([]map[string]any, 0, len(ev.Files))
		for _, f := range ev.Files {
			file := map[string]any{
				"id": f.ID, "seq": f.Seq, "duration": f.DurationSecs, "size": f.SizeBytes,
			}
			if f.VideoCodec != nil {
				file["videoCodec"] = *f.VideoCodec
			}
			if f.Codec != nil {
				file["codec"] = *f.Codec
			}
			if f.Width != nil {
				file["width"] = *f.Width
			}
			if f.Height != nil {
				file["height"] = *f.Height
			}
			files = append(files, file)
		}
		e := map[string]any{
			"id": ev.ID, "format": ev.Format, "title": ev.Title, "duration": ev.TotalDuration(),
			"files": files, "chapters": chapters,
		}
		if ev.SeasonNum != nil {
			e["seasonNum"] = *ev.SeasonNum
		}
		if ev.EpisodeNum != nil {
			e["episodeNum"] = *ev.EpisodeNum
		}
		if pc := pageCounts[ev.ID]; pc != nil {
			e["pageCount"] = *pc
		}
		if p, ok := pmap[ev.ID]; ok {
			e["position"] = p.EditionPositionSecs
			e["isFinished"] = p.IsFinished
		}
		if rp, ok := rmap[ev.ID]; ok {
			if rp.Page != nil {
				e["page"] = *rp.Page
			}
			if rp.Percent != nil {
				e["percent"] = *rp.Percent
			}
		}
		eds = append(eds, e)
	}
	writeJSON(w, 200, map[string]any{
		"id": full.ID, "libraryId": full.LibraryID, "libraryName": lib.Name,
		"title": full.Title, "subtitle": full.Subtitle, "author": full.Author,
		"description": full.Description, "hasCover": full.CoverPath != nil && *full.CoverPath != "",
		"hasFanart": fileOK(filepath.Join(a.DataDir, "covers", fmt.Sprintf("%d-fanart.jpg", full.ID))),
		"genres":    a.DB.WorkGenres(id), "editions": eds,
	})
}

type audioChapter struct {
	ID    int64   `json:"id"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Title string  `json:"title"`
}

func (a *API) getProgress(w http.ResponseWriter, r *http.Request) {
	eid := auth.Atoi64(r.PathValue("editionId"))
	var exists int
	if err := a.DB.QueryRow(`SELECT 1 FROM editions WHERE id = ?`, eid).Scan(&exists); err != nil {
		writeJSON(w, 404, map[string]string{"error": "edition not found"})
		return
	}
	p, err := a.DB.GetReadingProgress(auth.UserID(r), eid)
	// Only ErrNotFound means a valid edition with no saved progress; any
	// other read failure used to be served as a successful zero position,
	// which clients could persist over real progress.
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 500, map[string]string{"error": "progress unavailable"})
		return
	}
	m := map[string]any{"editionId": eid, "position": 0, "isFinished": false}
	if err == nil {
		m = map[string]any{
			"editionId": p.EditionID, "fileId": p.FileID, "offset": p.FileOffsetSecs,
			"position": p.EditionPositionSecs, "duration": p.DurationSecs, "isFinished": p.IsFinished,
			"updatedAt": p.UpdatedAt,
		}
		if p.Page != nil {
			m["page"] = *p.Page
		}
		if p.Percent != nil {
			m["percent"] = *p.Percent
		}
		if p.Locator != nil {
			m["locator"] = *p.Locator
		}
	}
	var pc *int64
	if err := a.DB.QueryRow(`SELECT page_count FROM editions WHERE id = ?`, eid).Scan(&pc); err == nil && pc != nil && *pc > 0 {
		m["pageCount"] = *pc
	}
	writeJSON(w, 200, m)
}

func (a *API) setProgress(w http.ResponseWriter, r *http.Request) {
	eid := auth.Atoi64(r.PathValue("editionId"))
	ed, err := a.DB.EditionByID(eid)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "edition not found"})
		return
	}
	var body struct {
		Position float64  `json:"position"`
		Duration float64  `json:"duration"`
		Finished bool     `json:"finished"`
		Device   string   `json:"device"`
		Page     *int64   `json:"page"`
		Percent  *float64 `json:"percent"`
		Locator  *string  `json:"locator"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	if body.Percent != nil && (*body.Percent < 0 || *body.Percent > 1) {
		writeJSON(w, 400, map[string]string{"error": "percent must be between 0 and 1"})
		return
	}
	if math.IsNaN(body.Position) || math.IsInf(body.Position, 0) || body.Position < 0 ||
		math.IsNaN(body.Duration) || math.IsInf(body.Duration, 0) || body.Duration < 0 {
		writeJSON(w, 400, map[string]string{"error": "invalid position or duration"})
		return
	}
	if body.Percent != nil && (math.IsNaN(*body.Percent) || math.IsInf(*body.Percent, 0)) {
		writeJSON(w, 400, map[string]string{"error": "invalid percent"})
		return
	}
	if body.Page != nil && *body.Page < 0 {
		writeJSON(w, 400, map[string]string{"error": "page must be nonnegative"})
		return
	}
	if len(body.Device) > 256 || (body.Locator != nil && len(*body.Locator) > 8192) {
		writeJSON(w, 400, map[string]string{"error": "progress metadata too large"})
		return
	}
	fileID, offset := ed.Locate(body.Position)
	var dur *float64
	if body.Duration > 0 {
		dur = &body.Duration
	}
	p := &store.ReadingProgress{
		Progress: store.Progress{
			UserID: auth.UserID(r), EditionID: eid, FileID: &fileID, FileOffsetSecs: offset,
			EditionPositionSecs: body.Position, DurationSecs: dur, IsFinished: body.Finished,
		},
		Page: body.Page, Percent: body.Percent, Locator: body.Locator,
	}
	if body.Device != "" {
		p.Device = &body.Device
	}
	if err := a.DB.SetReadingProgress(p); err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
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
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		writeJSON(w, 404, map[string]string{"error": "no cover"})
		return
	}
	path := filepath.Join(a.DataDir, "covers", name)
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		writeJSON(w, 404, map[string]string{"error": "no cover"})
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	serveFile(w, r, path)
}

// serveFile serves a stored media path. Stat errors and non-regular files
// are handled instead of dereferenced: a vanished or replaced path used to
// panic the request on a nil FileInfo.
func serveFile(w http.ResponseWriter, r *http.Request, path string) {
	before, err := os.Stat(path)
	if err != nil || !before.Mode().IsRegular() {
		http.Error(w, "gone", 404)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "gone", 404)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, "internal error", 500)
		return
	}
	if !fi.Mode().IsRegular() {
		http.Error(w, "gone", 404)
		return
	}
	http.ServeContent(w, r, filepath.Base(path), fi.ModTime(), f)
}
