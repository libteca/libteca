package transcode

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func genMedia(t *testing.T, path, container string, audioOnly bool) {
	t.Helper()
	args := []string{"-y", "-v", "error"}
	if audioOnly {
		args = append(args, "-f", "lavfi", "-i", "sine=frequency=440:duration=4")
	} else {
		args = append(args,
			"-f", "lavfi", "-i", "testsrc2=duration=8:size=320x240:rate=30",
			"-f", "lavfi", "-i", "sine=frequency=440:duration=8")
	}
	args = append(args, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", path)
	if out, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Skipf("could not generate %s source (missing x264?): %v: %s", container, err, out)
	}
}

func TestTranscodeDescriptorInputsIntegration(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	for _, tc := range []struct {
		name      string
		container string
		audioOnly bool
	}{
		{"mp4", "mp4", false},
		{"mkv", "mkv", false},
		{"audio-only", "m4a", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := filepath.Join(t.TempDir(), "src."+tc.container)
			genMedia(t, src, tc.container, tc.audioOnly)
			m := New(t.TempDir())
			if err := m.SetHwAccel(AccelNone); err != nil {
				t.Fatal(err)
			}
			defer m.CloseAll()
			s, err := m.Get("fdtest", 1, src, 0, fileOpener(t, src))
			if err != nil {
				t.Skipf("ffmpeg failed to start: %v", err)
			}
			n := s.Prebuffer(context.Background(), 2, 20*time.Second)
			if n < 1 {
				t.Fatalf("no segments prebuffered over the descriptor (%s input)", tc.name)
			}
			if !s.WaitForSegment(context.Background(), 0, 10*time.Second) {
				t.Fatal("first segment never became servable")
			}
		})
	}
}

func TestTranscodeSeekOverDescriptorIntegration(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	src := filepath.Join(t.TempDir(), "src.mp4")
	genMedia(t, src, "mp4", false)
	m := New(t.TempDir())
	if err := m.SetHwAccel(AccelNone); err != nil {
		t.Fatal(err)
	}
	defer m.CloseAll()
	s, err := m.Get("seektest", 1, src, 2.0, fileOpener(t, src))
	if err != nil {
		t.Skipf("ffmpeg failed to start: %v", err)
	}
	if !s.WaitForSegment(context.Background(), 0, 20*time.Second) {
		t.Fatal("input-seek transcode over the descriptor produced no segment")
	}
}

func TestGetFailsClosedWhenDescriptorArgsUnavailable(t *testing.T) {
	m := New(t.TempDir())
	m.fdArgs = func(...string) ([]string, error) { return nil, errors.New("confinement unavailable") }
	m.spawn = func([]string, []*os.File) process { return newSleepProcess() }
	if _, err := m.Get("closed", 1, "src", 0, tempOpener(t)); err == nil {
		t.Fatal("Get must refuse when descriptor confinement is unavailable")
	}
	if _, err := os.Stat(filepath.Join(m.DataDir, "transcode", "closed")); !os.IsNotExist(err) {
		t.Fatal("refused session must not leave a directory")
	}
	m.mu.Lock()
	n := len(m.sessions)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("refused session must not linger: %d", n)
	}
}

func TestGetFailsClosedWhenOpenRefuses(t *testing.T) {
	m := New(t.TempDir())
	stubFDArgs(m)
	m.spawn = func([]string, []*os.File) process { return newSleepProcess() }
	open := func() (*os.File, error) { return nil, os.ErrPermission }
	if _, err := m.Get("noopen", 1, "src", 0, open); err == nil {
		t.Fatal("Get must fail when the rooted open fails")
	}
	m.mu.Lock()
	n := len(m.sessions)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("failed open must not leave a session: %d", n)
	}
}

func TestFallbackReusesDescriptorWithResetOffset(t *testing.T) {
	setFallbackWindow(t, 2*time.Second)
	m := New(t.TempDir())
	if err := m.SetHwAccel(AccelVideoToolbox); err != nil {
		t.Fatal(err)
	}
	stubFDArgs(m)
	open := tempOpener(t)
	var mu sync.Mutex
	var extras [][]*os.File
	var offsets []int64
	m.spawn = func(argv []string, extra []*os.File) process {
		mu.Lock()
		defer mu.Unlock()
		snapshot := make([]*os.File, len(extra))
		copy(snapshot, extra)
		extras = append(extras, snapshot)
		if len(extras) == 1 {
			if _, err := extra[0].Seek(7, 0); err != nil {
				t.Errorf("seek setup: %v", err)
			}
		}
		off, _ := extra[0].Seek(0, 1)
		offsets = append(offsets, off)
		if len(extras) == 1 {
			return newFake(30 * time.Millisecond)
		}
		return newFake(-1)
	}
	if _, err := m.Get("reuse", 1, "src", 0, open); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(extras)
		mu.Unlock()
		if n >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(extras) != 2 {
		t.Fatalf("spawns = %d, want 2 (hw death + software fallback)", len(extras))
	}
	if extras[0][0] != extras[1][0] {
		t.Fatal("fallback must reuse the SAME session-owned descriptor")
	}
	if offsets[1] != 0 {
		t.Fatalf("descriptor offset before retry = %d, want reset to 0", offsets[1])
	}
	mu.Unlock()
	m.Close("reuse")
	mu.Lock()
}
