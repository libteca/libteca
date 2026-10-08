package transcode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/resourcebudget"
)

func TestDiskReservationBeforeSpawnAndReleaseOnClose(t *testing.T) {
	t.Setenv("LIBTECA_HLS_SESSION_BYTES", "100")
	t.Setenv("LIBTECA_HLS_TOTAL_BYTES", "100")
	m := New(t.TempDir())
	defer m.CloseAll()
	stubFDArgs(m)
	m.spawn = func([]string, []*os.File) process { return &fakeProcess{dieAfter: -1, exit: make(chan struct{})} }
	first, err := m.Get("first", 1, "src", 0, tempOpener(t))
	if err != nil {
		t.Fatal(err)
	}
	opened := false
	if _, err := m.Get("second", 1, "src", 0, func() (*os.File, error) { opened = true; return nil, errors.New("opened") }); !errors.Is(err, resourcebudget.ErrLimit) {
		t.Fatal(err)
	}
	if opened {
		t.Fatal("opened before reservation")
	}
	m.Close(first.ID)
	if _, err := m.Get("second", 1, "src", 0, tempOpener(t)); err != nil {
		t.Fatal(err)
	}
}
func TestDiskLimitRetainsAdvertisedSegmentsAndDisablesFallback(t *testing.T) {
	t.Setenv("LIBTECA_HLS_SESSION_BYTES", "100")
	m := New(t.TempDir())
	defer m.CloseAll()
	stubFDArgs(m)
	m.spawn = func([]string, []*os.File) process { return &fakeProcess{dieAfter: -1, exit: make(chan struct{})} }
	s, err := m.Get("session", 1, "src", 0, tempOpener(t))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, s.segmentPath(0), "segment")
	writeFile(t, s.Playlist(), "#EXTM3U\nseg00000.ts\n")
	writeFile(t, filepath.Join(s.Dir, "seg00001.ts.tmp"), string(make([]byte, 101)))
	select {
	case <-s.dead():
	case <-time.After(time.Second):
		t.Fatal("encoder did not stop")
	}
	if !errors.Is(s.ResourceError(), resourcebudget.ErrLimit) {
		t.Fatal(s.ResourceError())
	}
	if !s.WaitForSegment(context.Background(), 0, time.Second) {
		t.Fatal("advertised segment lost")
	}
	if _, err := os.Stat(s.segmentPath(0)); err != nil {
		t.Fatal(err)
	}
	m.Close(s.ID)
	if _, err := os.Stat(s.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
func TestRestartRemovesOldTranscodeBeforeAdmission(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "transcode", "old", "segment"), "old")
	m := New(root)
	defer m.CloseAll()
	if _, err := os.Stat(filepath.Join(root, "transcode", "old")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}
func TestFailedOpenReleasesDiskReservation(t *testing.T) {
	t.Setenv("LIBTECA_HLS_SESSION_BYTES", "100")
	t.Setenv("LIBTECA_HLS_TOTAL_BYTES", "100")
	m := New(t.TempDir())
	defer m.CloseAll()
	stubFDArgs(m)
	if _, err := m.Get("fail", 1, "src", 0, func() (*os.File, error) { return nil, errors.New("failure") }); err == nil {
		t.Fatal("accepted failure")
	}
	m.spawn = func([]string, []*os.File) process { return &fakeProcess{dieAfter: -1, exit: make(chan struct{})} }
	if _, err := m.Get("retry", 1, "src", 0, tempOpener(t)); err != nil {
		t.Fatal(err)
	}
}
