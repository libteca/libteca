package transcode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

var (
	ErrCapacity = errors.New("transcode capacity exhausted")
	ErrClosed   = errors.New("transcode manager closed")
)

const (
	DefaultPrebufferSegments = 2
	DefaultPrebufferTimeout  = 10 * time.Second
	DefaultSegmentTimeout    = 10 * time.Second
	pollInterval             = 150 * time.Millisecond
	MaxSessions              = 8
	idleSessionTTL           = 90 * time.Second
)

// A hardware-accelerated ffmpeg dying within fallbackWindow of start is
// retried once on software (probe said available, runtime disagreed).
var fallbackWindow = 5 * time.Second

var reSessionFile = regexp.MustCompile(`^(seg\d+\.ts|index\.m3u8)$`)

type process interface {
	start() error
	wait() error
	kill()
}

type realProcess struct{ cmd *exec.Cmd }

func (p *realProcess) start() error {
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return p.cmd.Start()
}

func (p *realProcess) wait() error { return p.cmd.Wait() }

func (p *realProcess) kill() {
	if p.cmd.Process == nil {
		return
	}
	pgid, _ := syscall.Getpgid(p.cmd.Process.Pid)
	if pgid > 0 {
		syscall.Kill(-pgid, syscall.SIGKILL)
		return
	}
	p.cmd.Process.Kill()
}

type Session struct {
	ID      string
	Edition int64
	Dir     string
	Source  string

	accel           string
	spawn           func(argv []string) process
	proc            process
	done            chan struct{}
	exitErr         error
	fallbackPending bool
	downgraded      bool
	killed          bool
	lastHit         atomic.Int64
	mu              sync.Mutex
	lifecycle sync.Mutex
}

type Manager struct {
	DataDir string

	mu       sync.Mutex
	sessions map[string]*Session
	closed   bool

	stop     chan struct{}
	stopOnce sync.Once

	hwMu   sync.Mutex
	hwSet  string
	hwMode string

	spawn    func(argv []string) process
	probeRun func(argv []string) (string, error)
}

func New(dataDir string) *Manager {
	m := &Manager{DataDir: dataDir, sessions: map[string]*Session{}, stop: make(chan struct{})}
	m.spawn = func(argv []string) process {
		return &realProcess{cmd: exec.Command("ffmpeg", argv...)}
	}
	m.probeRun = func(argv []string) (string, error) {
		out, err := exec.Command("ffmpeg", argv...).Output()
		return string(out), err
	}
	os.RemoveAll(filepath.Join(dataDir, "transcode"))
	go m.reaper()
	return m
}

// NewSessionID builds a playback session id that carries the edition for
// routing but ends in an unpredictable suffix, so two viewers, tabs or seeks
// on the same edition never share one ffmpeg process: Manager.Get returns the
// existing session on an edition match without consulting the requested
// start position, and a shared id means one client's stop kills the other's
// playback mid-stream.
func NewSessionID(prefix string, editionID int64) (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%d-%s", prefix, editionID, hex.EncodeToString(b[:])), nil
}

func (m *Manager) Get(sessionID string, edition int64, source string, startSecs float64) (*Session, error) {
	// Session ids become directory names under DataDir/transcode via
	// filepath.Join, which cleans ".." — an unvalidated id resolves outside
	// its slot, and Session.kill() runs os.RemoveAll(s.Dir). Reject anything
	// that is not a single safe path component, before any map or disk work.
	if sessionID == "" || sessionID == "." || sessionID == ".." || strings.ContainsAny(sessionID, `/\`) {
		return nil, fmt.Errorf("invalid transcode session id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, ErrClosed
	}
	if s, ok := m.sessions[sessionID]; ok {
		if s.Edition == edition {
			s.lastHit.Store(time.Now().UnixNano())
			return s, nil
		}
		s.kill()
		delete(m.sessions, sessionID)
	}
	now := time.Now()
	for id, s := range m.sessions {
		if now.Sub(time.Unix(0, s.lastHit.Load())) > idleSessionTTL {
			s.kill()
			delete(m.sessions, id)
		}
	}
	if len(m.sessions) >= MaxSessions {
		return nil, ErrCapacity
	}
	dir := filepath.Join(m.DataDir, "transcode", sessionID)
	os.MkdirAll(dir, 0o700)
	s := &Session{
		ID:      sessionID,
		Edition: edition,
		Dir:     dir,
		Source:  source,
		accel:   m.accelMode(),
		spawn:   m.spawn,
	}
	s.lastHit.Store(time.Now().UnixNano())
	m.sessions[sessionID] = s
	if err := s.start(startSecs); err != nil {
		delete(m.sessions, sessionID)
		os.RemoveAll(dir)
		return nil, err
	}
	return s, nil
}

func (s *Session) start(startSecs float64) error {
	if s.accel == "" {
		s.accel = AccelNone
	}
	if s.accel != AccelNone {
		s.mu.Lock()
		s.fallbackPending = true
		s.mu.Unlock()
	}
	if err := s.launch(startSecs, s.accel); err != nil {
		s.mu.Lock()
		s.fallbackPending = false
		s.mu.Unlock()
		return err
	}
	if s.accel != AccelNone {
		go s.watchFallback(startSecs)
	}
	return nil
}

func (s *Session) launch(startSecs float64, accel string) error {
	args := buildArgs(accel, s.Source, s.Dir, startSecs, DefaultVideoBitrate)
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.mu.Lock()
	closed := s.killed
	s.mu.Unlock()
	if closed {
		return ErrClosed
	}
	p := s.spawn(args)
	if err := p.start(); err != nil {
		return err
	}
	done := make(chan struct{})
	s.mu.Lock()
	s.proc = p
	s.done = done
	s.exitErr = nil
	s.mu.Unlock()
	go func() {
		err := p.wait()
		s.mu.Lock()
		if s.done == done {
			s.exitErr = err
		}
		s.mu.Unlock()
		close(done)
	}()
	return nil
}

func (s *Session) watchFallback(startSecs float64) {
	defer func() {
		s.mu.Lock()
		s.fallbackPending = false
		s.mu.Unlock()
	}()
	s.mu.Lock()
	first := s.done
	accel := s.accel
	s.mu.Unlock()
	timer := time.NewTimer(fallbackWindow)
	defer timer.Stop()
	select {
	case <-timer.C:
		return
	case <-first:
	}
	s.mu.Lock()
	killed := s.killed
	exitErr := s.exitErr
	stillCurrent := s.done == first
	s.mu.Unlock()
	if killed || !stillCurrent || exitErr == nil {
		return
	}
	s.mu.Lock()
	s.downgraded = true
	s.mu.Unlock()
	slog.Warn("transcode: hwaccel session died at startup, retrying with software", "session", s.ID, "accel", accel)
	if err := s.launch(startSecs, AccelNone); err != nil {
		slog.Warn("transcode: software fallback failed", "session", s.ID, "err", err)
	}
}

func (s *Session) kill() {
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.mu.Lock()
	s.killed = true
	p, done := s.proc, s.done
	s.mu.Unlock()
	if p != nil && done != nil {
		select {
		case <-done:
		default:
			p.kill()
		}
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			slog.Warn("transcode exit pending; preserving output directory", "session", s.ID)
			return
		}
	}
	if err := os.RemoveAll(s.Dir); err != nil {
		slog.Warn("transcode cleanup failed", "session", s.ID, "err", err)
	}
}

func (m *Manager) Existing(sessionID string, edition int64) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, false
	}
	s, ok := m.sessions[sessionID]
	if !ok || s.Edition != edition {
		return nil, false
	}
	s.Touch()
	return s, true
}

func (m *Manager) Close(sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[sessionID]; ok {
		s.kill()
		delete(m.sessions, sessionID)
	}
}

func (m *Manager) reaper() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			m.mu.Lock()
			for id, s := range m.sessions {
				if time.Since(time.Unix(0, s.lastHit.Load())) > idleSessionTTL {
					s.kill()
					delete(m.sessions, id)
				}
			}
			m.mu.Unlock()
		}
	}
}

func (m *Manager) CloseAll() {
	m.stopOnce.Do(func() { close(m.stop) })
	m.mu.Lock()
	m.closed = true
	defer m.mu.Unlock()
	for id, s := range m.sessions {
		s.kill()
		delete(m.sessions, id)
	}
}

func (s *Session) Playlist() string        { return filepath.Join(s.Dir, "index.m3u8") }
func (s *Session) Path(name string) string { return filepath.Join(s.Dir, filepath.Base(name)) }
func (s *Session) Touch()                  { s.lastHit.Store(time.Now().UnixNano()) }

func (m *Manager) TouchSession(id string) {
	m.mu.Lock()
	if s, ok := m.sessions[id]; ok {
		s.Touch()
	}
	m.mu.Unlock()
}

// dead returns a channel closed when the current ffmpeg process exits (nil
// if never started). Swapped once by the software fallback.
func (s *Session) dead() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.done
}

// isDead reports whether the session's ffmpeg has exited for good: the
// current process is gone and no software fallback is pending.
func (s *Session) isDead() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done == nil {
		return false
	}
	select {
	case <-s.done:
		return !s.fallbackPending
	default:
		return false
	}
}

func (s *Session) segmentPath(index int) string {
	return filepath.Join(s.Dir, fmt.Sprintf("seg%05d.ts", index))
}

func fileReady(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() > 0
}

func (s *Session) playlistLists(name string) bool {
	data, err := os.ReadFile(s.Playlist())
	if err != nil {
		return false
	}
	return strings.Contains(string(data), name)
}

// Prebuffer blocks until the first k segment files exist and are non-empty,
// ffmpeg exits for good, ctx is done, or timeout passes. Returns how many of
// the first k segments are on disk; never hangs.
func (s *Session) Prebuffer(ctx context.Context, k int, timeout time.Duration) int {
	if k <= 0 {
		k = DefaultPrebufferSegments
	}
	if timeout <= 0 {
		timeout = DefaultPrebufferTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		ready := 0
		for i := 0; i < k; i++ {
			if fileReady(s.segmentPath(i)) {
				ready++
			}
		}
		if ready >= k {
			return ready
		}
		if s.isDead() {
			return ready
		}
		select {
		case <-ctx.Done():
			return ready
		case <-timer.C:
			return ready
		case <-time.After(pollInterval):
		}
	}
}

// WaitForSegment blocks until segment index is safe to serve: the file is
// non-empty AND either the playlist references it (ffmpeg closed it) or
// ffmpeg has exited. Returns false on ctx cancel or timeout.
func (s *Session) WaitForSegment(ctx context.Context, index int, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = DefaultSegmentTimeout
	}
	name := fmt.Sprintf("seg%05d.ts", index)
	path := s.segmentPath(index)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		if fileReady(path) && (s.playlistLists(name) || s.isDead()) {
			return true
		}
		if !fileReady(path) && s.isDead() {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-time.After(pollInterval):
		}
	}
}

func (s *Session) waitForFile(ctx context.Context, path string, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		if fileReady(path) {
			return true
		}
		if s.isDead() {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		case <-time.After(pollInterval):
		}
	}
}

// WaitForSegmentFile waits for a session artifact ("segNNNNN.ts" or
// "index.m3u8") to become servable, touching the session. Returns false for
// unknown sessions, invalid names, ctx cancel, or timeout.
func (m *Manager) WaitForSegmentFile(ctx context.Context, sessionID, name string, timeout time.Duration) bool {
	if !reSessionFile.MatchString(name) {
		return false
	}
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	if ok {
		s.Touch()
	}
	m.mu.Unlock()
	if !ok {
		return false
	}
	if strings.HasSuffix(name, ".m3u8") {
		return s.waitForFile(ctx, s.Playlist(), timeout)
	}
	idx, err := strconv.Atoi(name[3 : len(name)-3])
	if err != nil || idx < 0 {
		return false
	}
	return s.WaitForSegment(ctx, idx, timeout)
}
