package scan

import (
	"sync"
	"time"
)

type Progress struct {
	FilesSeen    int
	FilesProbed  int
	FilesAdded   int
	FilesUpdated int
	WorksChanged int
	CurrentPath  string
}

type ProgressFn func(Progress)

const progressInterval = 250 * time.Millisecond

type tracker struct {
	mu   sync.Mutex
	fn   ProgressFn
	p    Progress
	last time.Time
}

func newTracker(fn ProgressFn) *tracker {
	return &tracker{fn: fn}
}

// all mutators are called with t.mu held; maybeEmit drops the lock around the
// callback so a slow observer never blocks the scan loop.

func (t *tracker) seen(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p.FilesSeen++
	t.p.CurrentPath = path
	t.maybeEmit()
}

func (t *tracker) probed() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p.FilesProbed++
	t.maybeEmit()
}

func (t *tracker) file(inserted bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if inserted {
		t.p.FilesAdded++
	} else {
		t.p.FilesUpdated++
	}
	t.maybeEmit()
}

func (t *tracker) work() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p.WorksChanged++
}

func (t *tracker) maybeEmit() {
	if t.fn == nil || time.Since(t.last) < progressInterval {
		return
	}
	t.last = time.Now()
	p := t.p
	t.mu.Unlock()
	t.fn(p)
	t.mu.Lock()
}

func (t *tracker) flush() {
	t.mu.Lock()
	if t.fn == nil {
		t.mu.Unlock()
		return
	}
	t.last = time.Now()
	p := t.p
	t.mu.Unlock()
	t.fn(p)
}
