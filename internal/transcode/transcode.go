package transcode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	DefaultPrebufferSegments = 2
	DefaultPrebufferTimeout  = 10 * time.Second
	DefaultSegmentTimeout    = 10 * time.Second
	pollInterval             = 150 * time.Millisecond
)

var reSessionFile = regexp.MustCompile(`^(seg\d+\.ts|index\.m3u8)$`)

type Session struct {
	ID      string
	Edition int64
	Dir     string
	Source  string
	cmd     *exec.Cmd
	done    chan struct{}
	lastHit time.Time
	mu      sync.Mutex
}

type Manager struct {
	DataDir  string
	mu       sync.Mutex
	sessions map[string]*Session
}

func New(dataDir string) *Manager {
	m := &Manager{DataDir: dataDir, sessions: map[string]*Session{}}
	go m.reaper()
	return m
}

func (m *Manager) Get(sessionID string, edition int64, source string, startSecs float64) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[sessionID]; ok {
		if s.Edition == edition {
			s.lastHit = time.Now()
			return s, nil
		}
		s.kill()
	}
	dir := filepath.Join(m.DataDir, "transcode", sessionID)
	os.MkdirAll(dir, 0o700)
	s := &Session{ID: sessionID, Edition: edition, Dir: dir, Source: source, lastHit: time.Now()}
	m.sessions[sessionID] = s
	return s, s.start(startSecs)
}

func (s *Session) start(startSecs float64) error {
	args := []string{"-y", "-v", "quiet"}
	if startSecs > 1 {
		args = append(args, "-ss", fmt.Sprintf("%.2f", startSecs))
	}
	args = append(args,
		"-i", s.Source,
		"-map", "0:v:0", "-map", "0:a:0?",
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "21",
		"-c:a", "aac", "-b:a", "192k", "-ac", "2",
		"-muxdelay", "0",
		"-f", "hls",
		"-hls_time", "4",
		"-hls_init_time", "2",
		"-hls_list_size", "0",
		"-hls_flags", "independent_segments",
		"-hls_segment_filename", filepath.Join(s.Dir, "seg%05d.ts"),
		filepath.Join(s.Dir, "index.m3u8"),
	)
	s.cmd = exec.Command("ffmpeg", args...)
	s.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := s.cmd.Start(); err != nil {
		return err
	}
	s.done = make(chan struct{})
	go func() {
		s.cmd.Wait()
		close(s.done)
	}()
	return nil
}

func (s *Session) kill() {
	if s.cmd != nil && s.cmd.Process != nil {
		pgid, _ := syscall.Getpgid(s.cmd.Process.Pid)
		if pgid > 0 {
			syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			s.cmd.Process.Kill()
		}
	}
	os.RemoveAll(s.Dir)
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
	for {
		time.Sleep(15 * time.Second)
		m.mu.Lock()
		for id, s := range m.sessions {
			if time.Since(s.lastHit) > 90*time.Second {
				s.kill()
				delete(m.sessions, id)
			}
		}
		m.mu.Unlock()
	}
}

func (s *Session) Playlist() string        { return filepath.Join(s.Dir, "index.m3u8") }
func (s *Session) Path(name string) string { return filepath.Join(s.Dir, filepath.Base(name)) }
func (s *Session) Touch()                  { s.mu.Lock(); s.lastHit = time.Now(); s.mu.Unlock() }

func (m *Manager) TouchSession(id string) {
	m.mu.Lock()
	if s, ok := m.sessions[id]; ok {
		s.Touch()
	}
	m.mu.Unlock()
}

// dead returns a channel closed when ffmpeg exits (nil if never started).
// cmd/done are immutable per Session object after start returns.
func (s *Session) dead() <-chan struct{} { return s.done }

func (s *Session) isDead() bool {
	select {
	case <-s.dead():
		return true
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
// ffmpeg exits, ctx is done, or timeout passes. Returns how many of the first
// k segments are on disk; never hangs.
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
		select {
		case <-ctx.Done():
			return ready
		case <-s.dead():
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
		case <-s.dead():
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
		case <-s.dead():
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
