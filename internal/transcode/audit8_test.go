package transcode

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSetHwAccelAutoOverridesEnvironment(t *testing.T) {
	m := New(t.TempDir())
	hwaccels := "Hardware acceleration methods:\nvideotoolbox\nvaapi\n"
	encoders := " V....D h264_videotoolbox    VideoToolbox H.264 encoder (codec h264)\n V....D h264_vaapi           VAAPI H.264 encoder (codec h264)\n V....D h264_nvenc           NVIDIA NVENC H.264 encoder (codec h264)\n"
	t.Setenv("LIBTECA_HWACCEL", "none")
	m.probeRun = func(argv []string) (string, error) {
		if len(argv) == 2 && argv[1] == "-hwaccels" {
			return hwaccels, nil
		}
		return encoders, nil
	}
	if err := m.SetHwAccel("auto"); err != nil {
		t.Fatal(err)
	}
	if got := m.HwAccel(); got == AccelNone {
		t.Fatalf("explicit auto must not lose to the environment override: got %s", got)
	}
}

func TestUnsetHwAccelConsultsEnvironment(t *testing.T) {
	m := New(t.TempDir())
	t.Setenv("LIBTECA_HWACCEL", "none")
	if got := m.HwAccel(); got != AccelNone {
		t.Fatalf("env none should win when auto was not explicitly set: got %s", got)
	}
}

func TestSessionParametersChangedOnReuse(t *testing.T) {
	m := New(t.TempDir())
	if err := m.SetHwAccel(AccelNone); err != nil {
		t.Fatal(err)
	}
	lg := &spawnLog{procs: []*fakeProcess{newFake(-1)}}
	m.spawn = lg.spawn
	if _, err := m.Get("web-1-abc", 1, "/lib/a.mp4", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get("web-1-abc", 1, "/lib/a.mp4", 30); err != ErrSessionParams {
		t.Fatalf("seek with the same id = %v, want ErrSessionParams", err)
	}
	if _, err := m.Get("web-1-abc", 1, "/lib/b.mp4", 0); err != ErrSessionParams {
		t.Fatalf("source change with the same id = %v, want ErrSessionParams", err)
	}
	if _, err := m.Get("web-1-abc", 1, "/lib/a.mp4", 0); err != nil {
		t.Fatalf("identical reuse must keep working: %v", err)
	}
	m.CloseAll()
	if lg.count() != 1 {
		t.Fatalf("spawns = %d, want 1", lg.count())
	}
}

func TestProbeRunIsBounded(t *testing.T) {
	if _, err := os.Stat("/bin/sleep"); err != nil {
		t.Skip("no /bin/sleep")
	}
	m := New(t.TempDir())
	old := m.probeRun
	origPath := os.Getenv("PATH")
	bin := t.TempDir()
	if err := os.Symlink("/bin/sleep", filepath.Join(bin, "ffmpeg")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	t.Setenv("PATH", bin)
	defer os.Setenv("PATH", origPath)
	m.probeRun = old
	start := time.Now()
	_, err := m.probeRun([]string{"-hide_banner", "-hwaccels"})
	if err == nil {
		t.Fatal("hanging probe must fail")
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("probe took %s, want bounded by the 5s deadline", elapsed)
	}
}
