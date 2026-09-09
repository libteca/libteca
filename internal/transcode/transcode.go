package transcode

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

type Session struct {
	ID      string
	Edition int64
	Dir     string
	Source  string
	cmd     *exec.Cmd
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
		"-f", "hls",
		"-hls_time", "4",
		"-hls_list_size", "0",
		"-hls_segment_filename", filepath.Join(s.Dir, "seg%05d.ts"),
		filepath.Join(s.Dir, "index.m3u8"),
	)
	s.cmd = exec.Command("ffmpeg", args...)
	s.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return s.cmd.Start()
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
