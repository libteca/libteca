package watch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/libteca/libteca/internal/store"
)

// Scanner is the core API seam the watcher drives scan jobs through.
type Scanner interface {
	TriggerScan(ctx context.Context, libraryID int64) (int64, error)
}

const (
	DefaultDebounce   = 2 * time.Second
	DefaultStaleAfter = 24 * time.Hour
	DefaultSyncEvery  = time.Minute
)

type Config struct {
	Debounce   time.Duration
	SweepEvery time.Duration // <= 0 disables the periodic sweep
	StaleAfter time.Duration // boot reconciliation threshold on the last scan job
	SyncEvery  time.Duration // library list refresh interval
}

type Watcher struct {
	scan       Scanner
	db         *store.DB
	debounce   time.Duration
	sweepEvery time.Duration
	staleAfter time.Duration
	syncEvery  time.Duration

	fw *fsnotify.Watcher

	mu      sync.Mutex
	dirs    map[string]int64
	timers  map[int64]*time.Timer
	stopped bool
	ctx     context.Context

	sweeping atomic.Bool
}

func New(scan Scanner, db *store.DB, cfg Config) *Watcher {
	if cfg.Debounce <= 0 {
		cfg.Debounce = DefaultDebounce
	}
	if cfg.StaleAfter <= 0 {
		cfg.StaleAfter = DefaultStaleAfter
	}
	if cfg.SyncEvery <= 0 {
		cfg.SyncEvery = DefaultSyncEvery
	}
	return &Watcher{
		scan: scan, db: db,
		debounce: cfg.Debounce, sweepEvery: cfg.SweepEvery, staleAfter: cfg.StaleAfter, syncEvery: cfg.SyncEvery,
		dirs: map[string]int64{}, timers: map[int64]*time.Timer{},
	}
}

func (w *Watcher) Run(ctx context.Context) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		fmt.Fprintln(os.Stderr, "libteca: watch:", err)
		return
	}
	w.mu.Lock()
	w.fw = fw
	w.ctx = ctx
	w.stopped = false
	w.mu.Unlock()
	defer fw.Close()

	w.syncLibraries(false)
	go w.scanAll(ctx, w.staleLibrary)

	syncT := time.NewTicker(w.syncEvery)
	defer syncT.Stop()
	var sweepC <-chan time.Time
	if w.sweepEvery > 0 {
		sweepT := time.NewTicker(w.sweepEvery)
		defer sweepT.Stop()
		sweepC = sweepT.C
	}
	for {
		select {
		case <-ctx.Done():
			w.stop()
			return
		case err, ok := <-fw.Errors:
			if !ok {
				w.stop()
				return
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "libteca: watch:", err)
				if errors.Is(err, fsnotify.ErrEventOverflow) {
					w.markAllDirty()
				}
			}
		case e, ok := <-fw.Events:
			if !ok {
				w.stop()
				return
			}
			w.handleEvent(e)
		case <-syncT.C:
			w.syncLibraries(true)
		case <-sweepC:
			go w.scanAll(ctx, func(*store.Library) bool { return true })
		}
	}
}

func (w *Watcher) stop() {
	w.mu.Lock()
	w.stopped = true
	for id, t := range w.timers {
		t.Stop()
		delete(w.timers, id)
	}
	w.mu.Unlock()
}

func (w *Watcher) handleEvent(e fsnotify.Event) {
	if !e.Has(fsnotify.Create | fsnotify.Write | fsnotify.Rename | fsnotify.Remove) {
		return
	}
	name := filepath.Clean(e.Name)
	w.mu.Lock()
	libID, isDir := w.dirs[name]
	w.mu.Unlock()
	if isDir {
		switch {
		case e.Has(fsnotify.Remove | fsnotify.Rename):
			w.unwatchDir(name)
		case e.Has(fsnotify.Create):
			w.watchTree(name, libID)
		}
		w.markDirty(libID)
		return
	}
	if ignoredName(name) {
		return
	}
	w.mu.Lock()
	libID, ok := w.dirs[filepath.Dir(name)]
	w.mu.Unlock()
	if !ok {
		return
	}
	if e.Has(fsnotify.Create) {
		if fi, err := os.Stat(name); err == nil && fi.IsDir() && !ignoredDir(name) {
			w.watchTree(name, libID)
		}
	}
	w.markDirty(libID)
}

func (w *Watcher) markDirty(libID int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	if t := w.timers[libID]; t != nil {
		t.Stop()
	}
	w.timers[libID] = time.AfterFunc(w.debounce, func() { w.fire(libID) })
}

func (w *Watcher) fire(libID int64) {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	delete(w.timers, libID)
	ctx := w.ctx
	w.mu.Unlock()
	w.triggerAndWait(ctx, libID)
}

func (w *Watcher) triggerAndWait(ctx context.Context, libID int64) {
	// Missing-file reconciliation lives inside the shared scan path:
	// HTTP-, CLI- and watch-triggered scans all record removals
	// identically, instead of only whichever one went through the watcher.
	if _, err := w.scan.TriggerScan(ctx, libID); err != nil {
		// A change that arrived while a scan was already running must not be
		// dropped: its debounce timer fired into the running scan and was
		// consumed. Re-arm so a fresh scan catches it once the current one
		// finishes; persistent failures (library gone) do not re-arm.
		if w.scanInFlight(libID) {
			w.markDirty(libID)
		}
		fmt.Fprintf(os.Stderr, "libteca: watch: scan for library %d skipped: %v\n", libID, err)
	}
}

func (w *Watcher) scanInFlight(libID int64) bool {
	jobs, err := w.db.ListScanJobs(libID, 1)
	return err == nil && len(jobs) > 0 && jobs[0].Status == "running"
}

// markAllDirty schedules a scan of every configured library: an fsnotify
// overflow means events were LOST, and logging it without recovery left the
// changes invisible until the next sweep.
func (w *Watcher) markAllDirty() {
	libs, err := w.db.Libraries()
	if err != nil {
		fmt.Fprintf(os.Stderr, "libteca: watch: overflow recovery failed: %v\n", err)
		return
	}
	for _, l := range libs {
		w.markDirty(l.ID)
	}
}

func (w *Watcher) scanAll(ctx context.Context, include func(*store.Library) bool) {
	if !w.sweeping.CompareAndSwap(false, true) {
		return
	}
	defer w.sweeping.Store(false)
	libs, err := w.db.Libraries()
	if err != nil {
		fmt.Fprintf(os.Stderr, "libteca: watch: libraries: %v\n", err)
		return
	}
	for i := range libs {
		if ctx.Err() != nil {
			return
		}
		if include(&libs[i]) {
			w.triggerAndWait(ctx, libs[i].ID)
		}
	}
}

func (w *Watcher) staleLibrary(lib *store.Library) bool {
	jobs, err := w.db.ListScanJobs(lib.ID, 1)
	if err != nil || len(jobs) == 0 {
		return true
	}
	if jobs[0].Status == "running" {
		return false
	}
	return time.Since(time.UnixMilli(jobs[0].CreatedAt)) >= w.staleAfter
}

// syncLibraries arms watches for every library. With markNew it also marks
// libraries whose root was armed just now dirty, so files dropped between
// library creation and arming are caught; the boot call leaves that to the
// reconciliation pass, which applies the staleness filter.
func (w *Watcher) syncLibraries(markNew bool) {
	libs, err := w.db.Libraries()
	if err != nil {
		fmt.Fprintf(os.Stderr, "libteca: watch: libraries: %v\n", err)
		return
	}
	live := make(map[int64]bool, len(libs))
	for _, l := range libs {
		live[l.ID] = true
	}
	w.mu.Lock()
	for dir, id := range w.dirs {
		if !live[id] {
			delete(w.dirs, dir)
			w.fw.Remove(dir)
		}
	}
	w.mu.Unlock()
	for _, l := range libs {
		if w.watchTree(l.Path, l.ID) && markNew {
			w.markDirty(l.ID)
		}
	}
}

func (w *Watcher) watchTree(root string, libID int64) bool {
	root = filepath.Clean(root)
	w.mu.Lock()
	_, wasWatched := w.dirs[root]
	w.mu.Unlock()
	// Children are always revisited even when the root is already armed: a
	// subdirectory whose fw.Add once failed was previously never retried.
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") && path != root {
			return filepath.SkipDir
		}
		w.addDir(path, libID)
		return nil
	})
	w.mu.Lock()
	defer w.mu.Unlock()
	_, armed := w.dirs[root]
	return armed && !wasWatched
}

func (w *Watcher) addDir(dir string, libID int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, ok := w.dirs[dir]; ok {
		return
	}
	if err := w.fw.Add(dir); err != nil {
		fmt.Fprintf(os.Stderr, "libteca: watch add %s: %v\n", dir, err)
		return
	}
	w.dirs[dir] = libID
}

func (w *Watcher) unwatchDir(dir string) {
	w.mu.Lock()
	delete(w.dirs, dir)
	w.mu.Unlock()
	w.fw.Remove(dir)
}

func ignoredName(path string) bool {
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") {
		return true
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".part", ".tmp":
		return true
	}
	return false
}

func ignoredDir(path string) bool {
	return strings.HasPrefix(filepath.Base(path), ".")
}
