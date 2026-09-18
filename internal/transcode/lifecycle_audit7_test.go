package transcode

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type trackingProcess struct {
	starts      atomic.Int32
	waits       atomic.Int32
	kills       atomic.Int32
	releaseWait chan struct{}
	once        sync.Once
}

func (p *trackingProcess) start() error {
	p.starts.Add(1)
	return nil
}

func (p *trackingProcess) wait() error {
	p.waits.Add(1)
	<-p.releaseWait
	return nil
}

func (p *trackingProcess) kill() {
	p.kills.Add(1)
	p.once.Do(func() { close(p.releaseWait) })
}

func TestKillAfterStartAlwaysReaps(t *testing.T) {
	m := New(t.TempDir())
	m.probeRun = func([]string) (string, error) { return "", errStartFail }
	stubFDArgs(m)
	tp := &trackingProcess{releaseWait: make(chan struct{})}
	m.spawn = func([]string, []*os.File) process { return tp }
	if _, err := m.Get("k", 1, "src", 0, tempOpener(t)); err != nil {
		t.Fatal(err)
	}
	m.Close("k")
	deadline := time.After(2 * time.Second)
	for {
		if tp.waits.Load() == 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("kill left the child unreaped: waits=%d starts=%d", tp.waits.Load(), tp.starts.Load())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if tp.waits.Load() != 1 {
		t.Fatalf("waits = %d, want exactly one", tp.waits.Load())
	}
	if _, err := os.Stat(filepath.Join(m.DataDir, "transcode", "k")); !os.IsNotExist(err) {
		t.Fatal("output directory must be removed after the child exits")
	}
}

func TestLaunchAfterKillRefused(t *testing.T) {
	m := New(t.TempDir())
	m.probeRun = func([]string) (string, error) { return "", errStartFail }
	stubFDArgs(m)
	tp := &trackingProcess{releaseWait: make(chan struct{})}
	m.spawn = func([]string, []*os.File) process { return tp }
	if _, err := m.Get("k2", 1, "src", 0, tempOpener(t)); err != nil {
		t.Fatal(err)
	}
	s := m.sessions["k2"]
	m.Close("k2")
	if err := s.launch(0, AccelNone); err == nil {
		t.Fatal("launch after kill must be refused")
	}
	if tp.starts.Load() != 1 {
		t.Fatalf("refused launch spawned another process: starts=%d", tp.starts.Load())
	}
}
