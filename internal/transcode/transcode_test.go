package transcode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPrebufferSatisfiedImmediately(t *testing.T) {
	s := &Session{ID: "x", Dir: t.TempDir()}
	writeFile(t, filepath.Join(s.Dir, "seg00000.ts"), "x")
	writeFile(t, filepath.Join(s.Dir, "seg00001.ts"), "x")
	done := make(chan int, 1)
	go func() { done <- s.Prebuffer(context.Background(), 2, 5*time.Second) }()
	select {
	case n := <-done:
		if n != 2 {
			t.Fatalf("want 2 ready segments, got %d", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Prebuffer did not return immediately when segments ready")
	}
}

func TestPrebufferTimeoutReturnsPartial(t *testing.T) {
	s := &Session{ID: "x", Dir: t.TempDir()}
	writeFile(t, filepath.Join(s.Dir, "seg00000.ts"), "x")
	if n := s.Prebuffer(context.Background(), 2, 400*time.Millisecond); n != 1 {
		t.Fatalf("want 1 ready segment on timeout, got %d", n)
	}
}

func TestPrebufferContextCancel(t *testing.T) {
	s := &Session{ID: "x", Dir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if n := s.Prebuffer(ctx, 2, 5*time.Second); n != 0 {
		t.Fatalf("want 0 segments, got %d", n)
	}
	if time.Since(start) > time.Second {
		t.Fatal("canceled context should return promptly")
	}
}

func TestPrebufferSessionDead(t *testing.T) {
	s := &Session{ID: "x", Dir: t.TempDir()}
	s.done = make(chan struct{})
	close(s.done)
	start := time.Now()
	if n := s.Prebuffer(context.Background(), 2, 5*time.Second); n != 0 {
		t.Fatalf("want 0 segments, got %d", n)
	}
	if time.Since(start) > time.Second {
		t.Fatal("dead session should return promptly")
	}
}

func TestWaitForSegmentPlaylistListing(t *testing.T) {
	s := &Session{ID: "x", Dir: t.TempDir()}
	writeFile(t, filepath.Join(s.Dir, "seg00000.ts"), "x")
	if s.WaitForSegment(context.Background(), 0, 300*time.Millisecond) {
		t.Fatal("segment must not be ready before playlist lists it")
	}
	writeFile(t, filepath.Join(s.Dir, "index.m3u8"), "#EXTM3U\n#EXTINF:4.0,\nseg00000.ts\n")
	if !s.WaitForSegment(context.Background(), 0, 2*time.Second) {
		t.Fatal("segment should be ready once playlist lists it")
	}
}

func TestWaitForSegmentDeadFallback(t *testing.T) {
	s := &Session{ID: "x", Dir: t.TempDir()}
	writeFile(t, filepath.Join(s.Dir, "seg00000.ts"), "x")
	s.done = make(chan struct{})
	close(s.done)
	if !s.WaitForSegment(context.Background(), 0, 2*time.Second) {
		t.Fatal("unlisted non-empty segment should be servable once ffmpeg exited")
	}
	if s.WaitForSegment(context.Background(), 5, 200*time.Millisecond) {
		t.Fatal("missing segment must stay not-ready")
	}
}

func TestWaitForSegmentFileValidation(t *testing.T) {
	m := New(t.TempDir())
	ctx := context.Background()
	if m.WaitForSegmentFile(ctx, "nope", "seg00000.ts", 100*time.Millisecond) {
		t.Fatal("unknown session must return false")
	}
	s := &Session{ID: "s1", Dir: filepath.Join(m.DataDir, "transcode", "s1")}
	m.mu.Lock()
	m.sessions["s1"] = s
	m.mu.Unlock()
	for _, bad := range []string{"../x.ts", "sub/seg00000.ts", "foo.ts", "seg.ts", "seg-1.ts", "index.m3u8/", ""} {
		if m.WaitForSegmentFile(ctx, "s1", bad, 100*time.Millisecond) {
			t.Fatalf("bad name accepted: %q", bad)
		}
	}
	writeFile(t, filepath.Join(s.Dir, "index.m3u8"), "#EXTM3U\n")
	if !m.WaitForSegmentFile(ctx, "s1", "index.m3u8", 2*time.Second) {
		t.Fatal("non-empty playlist should be servable")
	}
	if m.WaitForSegmentFile(ctx, "s1", "seg00003.ts", 200*time.Millisecond) {
		t.Fatal("absent segment must not be servable")
	}
	m.mu.Lock()
	m.sessions["s1"].lastHit.Store(time.Now().UnixNano())
	m.mu.Unlock()
}

func TestNewClearsOrphanSegments(t *testing.T) {
	data := t.TempDir()
	orphan := filepath.Join(data, "transcode", "stale-session")
	writeFile(t, filepath.Join(orphan, "seg00000.ts"), "x")
	New(data)
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan session dir survived restart: %v", err)
	}
}

type startFailProcess struct{}

func (startFailProcess) start() error { return errStartFail }
func (startFailProcess) wait() error  { return nil }
func (startFailProcess) kill()        {}

var errStartFail = os.ErrInvalid

func TestGetRejectsTraversalSessionIDs(t *testing.T) {
	m := New(t.TempDir())
	m.spawn = func([]string) process { return startFailProcess{} }
	for _, sid := range []string{"..", ".", "", "x/../../y", "a/b", `a\b`} {
		if _, err := m.Get(sid, 1, "src", 0); err == nil {
			t.Fatalf("Get(%q) must reject unsafe session ids", sid)
		}
	}
	if _, err := os.Stat(filepath.Join(m.DataDir, "transcode")); !os.IsNotExist(err) {
		t.Fatal("rejected ids must not create any directory")
	}
	if _, err := os.Stat(m.DataDir); err != nil {
		t.Fatalf("DataDir must be untouched: %v", err)
	}
}

func TestGetStartFailureCleansSession(t *testing.T) {
	m := New(t.TempDir())
	m.probeRun = func([]string) (string, error) { return "", errStartFail }
	m.spawn = func([]string) process { return startFailProcess{} }
	if _, err := m.Get("boom", 1, "src", 0); err == nil {
		t.Fatal("Get must fail when ffmpeg cannot start")
	}
	if _, err := os.Stat(filepath.Join(m.DataDir, "transcode", "boom")); !os.IsNotExist(err) {
		t.Fatal("failed session dir must be removed")
	}
	m.mu.Lock()
	n := len(m.sessions)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("failed session must not linger in the map: %d", n)
	}
}

type sleepProcess struct{ killed chan struct{} }

func newSleepProcess() *sleepProcess {
	return &sleepProcess{killed: make(chan struct{})}
}
func (p *sleepProcess) start() error { return nil }
func (p *sleepProcess) wait() error  {
	<-p.killed
	return nil
}
func (p *sleepProcess) kill() {
	select {
	case <-p.killed:
	default:
		close(p.killed)
	}
}

func TestMaxSessionsEvictsOldest(t *testing.T) {
	m := New(t.TempDir())
	m.probeRun = func([]string) (string, error) { return "", errStartFail }
	procs := map[string]*sleepProcess{}
	m.spawn = func(argv []string) process {
		p := newSleepProcess()
		procs[filepath.Base(filepath.Dir(argv[len(argv)-1]))] = p
		return p
	}
	first := ""
	for i := 0; i < MaxSessions; i++ {
		id := fmt.Sprintf("s%d", i)
		if i == 0 {
			first = id
		}
		if _, err := m.Get(id, int64(i+1), "src", 0); err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
	}
	m.mu.Lock()
	m.sessions[first].lastHit.Store(time.Now().Add(-time.Hour).UnixNano())
	m.mu.Unlock()
	if _, err := m.Get("overflow", 99, "src", 0); err != nil {
		t.Fatalf("Get(overflow): %v", err)
	}
	m.mu.Lock()
	count := len(m.sessions)
	_, oldestAlive := m.sessions[first]
	m.mu.Unlock()
	if count != MaxSessions {
		t.Fatalf("sessions = %d, want cap %d", count, MaxSessions)
	}
	if oldestAlive {
		t.Fatal("idle-expired session must be reclaimed at cap")
	}
	select {
	case <-procs[first].killed:
	default:
		t.Fatal("reclaimed session's ffmpeg must be killed")
	}
	m.CloseAll()
}

func TestMaxSessionsRejectsWhenAllActive(t *testing.T) {
	m := New(t.TempDir())
	m.probeRun = func([]string) (string, error) { return "", errStartFail }
	m.spawn = func([]string) process { return newSleepProcess() }
	defer m.CloseAll()
	for i := 0; i < MaxSessions; i++ {
		if _, err := m.Get(fmt.Sprintf("s%d", i), int64(i+1), "src", 0); err != nil {
			t.Fatalf("Get(s%d): %v", i, err)
		}
	}
	if _, err := m.Get("ninth", 99, "src", 0); err != ErrCapacity {
		t.Fatalf("Get at capacity = %v, want ErrCapacity", err)
	}
	m.mu.Lock()
	count := len(m.sessions)
	m.mu.Unlock()
	if count != MaxSessions {
		t.Fatalf("sessions = %d, want the original %d untouched", count, MaxSessions)
	}
}

func TestGetAfterCloseAll(t *testing.T) {
	data := t.TempDir()
	m := New(data)
	m.probeRun = func([]string) (string, error) { return "", errStartFail }
	m.spawn = func([]string) process { return newSleepProcess() }
	if _, err := m.Get("live", 1, "src", 0); err != nil {
		t.Fatal(err)
	}
	m.CloseAll()
	m.CloseAll()
	if _, err := m.Get("after", 2, "src", 0); err != ErrClosed {
		t.Fatalf("Get after CloseAll = %v, want ErrClosed", err)
	}
	if _, err := os.Stat(filepath.Join(data, "transcode", "after")); !os.IsNotExist(err) {
		t.Fatal("closed manager must not create session directories")
	}
}

func TestSessionLastHitConcurrentAccess(t *testing.T) {
	m := New(t.TempDir())
	m.probeRun = func([]string) (string, error) { return "", errStartFail }
	m.spawn = func([]string) process { return newSleepProcess() }
	defer m.CloseAll()
	for i := 0; i < MaxSessions; i++ {
		if _, err := m.Get(fmt.Sprintf("s%d", i), int64(i+1), "src", 0); err != nil {
			t.Fatalf("Get(s%d): %v", i, err)
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := fmt.Sprintf("s%d", i%MaxSessions)
				m.TouchSession(id)
				if _, err := m.Get(id, int64(i%MaxSessions+1), "src", 0); err != nil {
					t.Errorf("Get(%s): %v", id, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestPrebufferIntegrationFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	src := filepath.Join(t.TempDir(), "src.mp4")
	if err := exec.Command("ffmpeg", "-y", "-v", "quiet",
		"-f", "lavfi", "-i", "testsrc2=duration=12:size=320x240:rate=30",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", src).Run(); err != nil {
		t.Skipf("could not generate test source (missing x264?): %v", err)
	}
	m := New(t.TempDir())
	defer m.Close("itest")
	s, err := m.Get("itest", 1, src, 0)
	if err != nil {
		t.Skipf("ffmpeg failed to start: %v", err)
	}
	n := s.Prebuffer(context.Background(), 2, 15*time.Second)
	if n == 0 {
		select {
		case <-s.dead():
			t.Skip("ffmpeg exited without producing segments (missing x264?)")
		case <-time.After(2 * time.Second):
			t.Fatal("no segments prebuffered and ffmpeg still running")
		}
	}
	if n < 1 {
		t.Fatalf("want at least 1 prebuffered segment, got %d", n)
	}
	fi, err := os.Stat(filepath.Join(s.Dir, "seg00000.ts"))
	if err != nil || fi.Size() == 0 {
		t.Fatalf("first segment missing or empty (err=%v)", err)
	}
	if !s.waitForFile(context.Background(), s.Playlist(), 5*time.Second) {
		t.Fatal("playlist never written")
	}
	if !s.playlistLists("seg00000.ts") {
		t.Fatal("playlist does not list first segment")
	}
	if !s.WaitForSegment(context.Background(), 1, 12*time.Second) {
		t.Fatal("second segment never became servable")
	}
	m.mu.Lock()
	live := m.sessions["itest"] != nil
	m.mu.Unlock()
	if !live {
		t.Fatal("session should survive prebuffer")
	}
}
