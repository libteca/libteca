package transcode

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/libteca/libteca/internal/procfd"
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
	ErrCapacity      = errors.New("transcode capacity exhausted")
	ErrClosed        = errors.New("transcode manager closed")
	ErrSessionParams = errors.New("transcode session parameters changed")
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

type realProcess struct {
	cmd   *exec.Cmd
	files []*os.File
}

func (p *realProcess) start() error {
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	p.cmd.ExtraFiles = p.files
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

	StartSecs       float64
	accel           string
	spawn           func(argv []string, extra []*os.File) process
	fdArgs          func(extra ...string) ([]string, error)
	input           *os.File
	proc            process
	done            chan struct{}
	exitErr         error
	fallbackPending bool
	downgraded      bool
	killed          bool
	closing         bool
	lastHit         atomic.Int64
	mu              sync.Mutex
	lifecycle       sync.Mutex
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

	spawn    func(argv []string, extra []*os.File) process
	fdArgs   func(extra ...string) ([]string, error)
	probeRun func(argv []string) (string, error)
}

func New(dataDir string) *Manager {
	m := &Manager{DataDir: dataDir, sessions: map[string]*Session{}, stop: make(chan struct{})}
	m.spawn = func(argv []string, extra []*os.File) process {
		return &realProcess{cmd: exec.Command("ffmpeg", argv...), files: extra}
	}
	m.fdArgs = func(extra ...string) ([]string, error) {
		return procfd.Args("ffmpeg", extra...)
	}
	m.probeRun = func(argv []string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "ffmpeg", argv...)
		cmd.WaitDelay = time.Second
		out, err := cmd.Output()
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
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

// Get resolves or creates a session. The manager lock is only held for map
// admission: process spawn, file open and directory setup happen outside
// it, so a slow start or cleanup cannot stall unrelated viewers (a session
// is reserved in the map first so concurrent starts cannot exceed
// MaxSessions, and closing sessions keep their slot and directory name
// until cleanup has actually finished).
func (m *Manager) Get(sessionID string, edition int64, source string, startSecs float64, open func() (*os.File, error)) (*Session, error) {
	// Session ids become directory names under DataDir/transcode via
	// filepath.Join, which cleans ".." — an unvalidated id resolves outside
	// its slot, and Session.kill() runs os.RemoveAll(s.Dir). Reject anything
	// that is not a single safe path component, before any map or disk work.
	if sessionID == "" || sessionID == "." || sessionID == ".." || strings.ContainsAny(sessionID, `/\`) {
		return nil, fmt.Errorf("invalid transcode session id")
	}
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			return nil, ErrClosed
		}
		if s, ok := m.sessions[sessionID]; ok {
			if s.closing {
				m.mu.Unlock()
				return nil, ErrCapacity
			}
			if s.Edition == edition {
				if s.Source != source || s.StartSecs != startSecs {
					m.mu.Unlock()
					return nil, ErrSessionParams
				}
				s.lastHit.Store(time.Now().UnixNano())
				m.mu.Unlock()
				return s, nil
			}
			s.closing = true
			m.mu.Unlock()
			m.finalize(s)
			continue
		}
		now := time.Now()
		var expired []*Session
		for id, s := range m.sessions {
			if !s.closing && now.Sub(time.Unix(0, s.lastHit.Load())) > idleSessionTTL {
				s.closing = true
				expired = append(expired, s)
				delete(m.sessions, id)
			}
		}
		if len(m.sessions) >= MaxSessions {
			m.mu.Unlock()
			for _, s := range expired {
				m.finalize(s)
			}
			return nil, ErrCapacity
		}
		s := &Session{
			ID:      sessionID,
			Edition: edition,
			Dir:     filepath.Join(m.DataDir, "transcode", sessionID),
			Source:  source,

			StartSecs: startSecs,
			accel:     m.accelMode(),
			spawn:     m.spawn,
			fdArgs:    m.fdArgs,
		}
		s.lastHit.Store(time.Now().UnixNano())
		m.sessions[sessionID] = s
		m.mu.Unlock()
		for _, e := range expired {
			m.finalize(e)
		}
		input, err := open()
		if err != nil {
			m.mu.Lock()
			if m.sessions[sessionID] == s {
				delete(m.sessions, sessionID)
			}
			m.mu.Unlock()
			return nil, err
		}
		s.mu.Lock()
		s.input = input
		s.mu.Unlock()
		if err := os.MkdirAll(s.Dir, 0o700); err != nil {
			m.mu.Lock()
			if m.sessions[sessionID] == s {
				delete(m.sessions, sessionID)
			}
			m.mu.Unlock()
			s.releaseInput()
			return nil, err
		}
		if err := s.start(startSecs); err != nil {
			m.mu.Lock()
			if m.sessions[sessionID] == s {
				delete(m.sessions, sessionID)
			}
			m.mu.Unlock()
			s.releaseInput()
			os.RemoveAll(s.Dir)
			return nil, err
		}
		return s, nil
	}
}

// finalize terminates a reserved (closing) session and removes its map
// entry only after the process is confirmed gone and the directory is
// removed. A kill that times out keeps the reservation so the reaper can
// retry; the directory name is never handed to a new session while the old
// cleanup may still delete it.
func (m *Manager) finalize(s *Session) {
	if !s.kill() {
		return
	}
	m.mu.Lock()
	if cur, ok := m.sessions[s.ID]; ok && cur == s {
		delete(m.sessions, s.ID)
	}
	m.mu.Unlock()
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
	inArgs, err := s.fdArgs("file")
	if err != nil {
		return err
	}
	args := buildArgs(accel, inArgs, s.Dir, startSecs, DefaultVideoBitrate)
	s.lifecycle.Lock()
	defer s.lifecycle.Unlock()
	s.mu.Lock()
	closed := s.killed
	input := s.input
	s.mu.Unlock()
	if closed {
		return ErrClosed
	}
	if input != nil {
		if _, serr := input.Seek(0, 0); serr != nil {
			return serr
		}
	}
	p := s.spawn(args, []*os.File{input})
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

// kill terminates the session's process and removes its output directory.
// It reports whether cleanup fully completed: a process that ignores the
// kill for two seconds or a failing RemoveAll leaves the reservation in
// place for a later retry (the directory must not be reused meanwhile).
func (s *Session) kill() bool {
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
			s.releaseInput()
			return false
		}
	}
	s.releaseInput()
	if err := os.RemoveAll(s.Dir); err != nil {
		slog.Warn("transcode cleanup failed", "session", s.ID, "err", err)
		return false
	}
	return true
}

func (s *Session) releaseInput() {
	s.mu.Lock()
	input := s.input
	s.input = nil
	s.mu.Unlock()
	if input != nil {
		input.Close()
	}
}

func (m *Manager) Existing(sessionID string, edition int64) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil, false
	}
	s, ok := m.sessions[sessionID]
	if !ok || s.closing || s.Edition != edition {
		return nil, false
	}
	s.Touch()
	return s, true
}

// Close reserves the session for teardown under the lock and performs the
// slow kill + directory removal outside it, so unrelated session lookups
// never wait on process termination or filesystem cleanup.
func (m *Manager) Close(sessionID string) {
	m.mu.Lock()
	s, ok := m.sessions[sessionID]
	if !ok || s.closing {
		m.mu.Unlock()
		return
	}
	s.closing = true
	m.mu.Unlock()
	m.finalize(s)
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
			var collected []*Session
			for id, s := range m.sessions {
				expired := time.Since(time.Unix(0, s.lastHit.Load())) > idleSessionTTL
				if s.closing {
					collected = append(collected, s)
					continue
				}
				if expired {
					s.closing = true
					collected = append(collected, s)
					delete(m.sessions, id)
				}
			}
			m.mu.Unlock()
			for _, s := range collected {
				m.finalize(s)
			}
		}
	}
}

func (m *Manager) CloseAll() {
	m.stopOnce.Do(func() { close(m.stop) })
	m.mu.Lock()
	m.closed = true
	var collected []*Session
	for id, s := range m.sessions {
		if s.closing {
			collected = append(collected, s)
			continue
		}
		s.closing = true
		collected = append(collected, s)
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	for _, s := range collected {
		m.finalize(s)
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
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

func (s *Session) segmentReady(index int) bool {
	return fileReady(s.segmentPath(index)) && s.playlistLists(fmt.Sprintf("seg%05d.ts", index))
}

// Prebuffer blocks until the first k segments are finalized (published at
// their final name AND listed by the playlist), ffmpeg exits for good, ctx
// is done, or timeout passes. Returns how many of the first k segments are
// servable; never hangs.
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
			if s.segmentReady(i) {
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

// WaitForSegment blocks until segment index is safe to serve: the segment
// is published at its final name (ffmpeg writes to a temp file and renames
// on completion) AND the playlist references it. A dead encoder never
// makes an unfinished segment servable — a failed exit with nonempty bytes
// on disk is a playback error, not a 200. Returns false on ctx cancel or
// timeout.
func (s *Session) WaitForSegment(ctx context.Context, index int, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = DefaultSegmentTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		if s.segmentReady(index) {
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
