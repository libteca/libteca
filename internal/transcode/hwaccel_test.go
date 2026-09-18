package transcode

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"
)

func setFallbackWindow(t *testing.T, d time.Duration) {
	t.Helper()
	orig := fallbackWindow
	fallbackWindow = d
	t.Cleanup(func() { fallbackWindow = orig })
}

func has(args []string, v string) bool { return slices.Contains(args, v) }

func TestBuildArgsGolden(t *testing.T) {
	const src = "/media/x.mp4"
	const dir = "/data/transcode/s1"
	tail := []string{
		"-c:a", "aac", "-b:a", "192k", "-ac", "2",
		"-muxdelay", "0",
		"-f", "hls",
		"-hls_time", "4",
		"-hls_init_time", "2",
		"-hls_list_size", "0",
		"-hls_flags", "independent_segments",
		"-hls_segment_filename", "/data/transcode/s1/seg%05d.ts",
		"/data/transcode/s1/index.m3u8",
	}
	concat := func(parts ...[]string) []string {
		var out []string
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	cases := []struct {
		name   string
		accel  string
		seek   float64
		expect []string
	}{
		{"software", AccelNone, 0, concat(
			[]string{"-y", "-v", "quiet", "-i", src, "-map", "0:v:0?", "-map", "0:a:0?"},
			[]string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "21"}, tail)},
		{"software-seek", AccelNone, 12.34, concat(
			[]string{"-y", "-v", "quiet", "-ss", "12.34", "-i", src, "-map", "0:v:0?", "-map", "0:a:0?"},
			[]string{"-c:v", "libx264", "-preset", "veryfast", "-crf", "21"}, tail)},
		{"videotoolbox", AccelVideoToolbox, 0, concat(
			[]string{"-y", "-v", "quiet", "-i", src, "-map", "0:v:0?", "-map", "0:a:0?"},
			[]string{"-c:v", "h264_videotoolbox", "-allow_sw", "1", "-realtime", "1", "-b:v", "6M"}, tail)},
		{"vaapi", AccelVAAPI, 0, concat(
			[]string{"-y", "-v", "quiet", "-hwaccel", "vaapi", "-hwaccel_output_format", "vaapi", "-vaapi_device", "/dev/dri/renderD128", "-i", src, "-map", "0:v:0?", "-map", "0:a:0?"},
			[]string{"-vf", "format=nv12,hwupload", "-c:v", "h264_vaapi"}, tail)},
		{"nvenc", AccelNVENC, 0, concat(
			[]string{"-y", "-v", "quiet", "-i", src, "-map", "0:v:0?", "-map", "0:a:0?"},
			[]string{"-c:v", "h264_nvenc", "-preset", "p4", "-rc", "vbr", "-b:v", "6M"}, tail)},
		{"qsv", AccelQSV, 0, concat(
			[]string{"-y", "-v", "quiet", "-hwaccel", "qsv", "-i", src, "-map", "0:v:0?", "-map", "0:a:0?"},
			[]string{"-c:v", "h264_qsv"}, tail)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := buildArgs(c.accel, src, dir, c.seek, "")
			if !reflect.DeepEqual(got, c.expect) {
				t.Fatalf("args mismatch:\n got  %v\n want %v", got, c.expect)
			}
		})
	}
}

func TestBuildArgsBitrateOverride(t *testing.T) {
	for _, accel := range []string{AccelVideoToolbox, AccelNVENC} {
		args := buildArgs(accel, "/s.mp4", "/d", 0, "3M")
		if !has(args, "3M") || has(args, "6M") {
			t.Fatalf("%s: want override 3M without default, got %v", accel, args)
		}
	}
}

func TestParseHwaccels(t *testing.T) {
	darwin := "Hardware acceleration methods:\nvideotoolbox\n"
	if m := parseHwaccels(darwin); !m["videotoolbox"] || len(m) != 1 {
		t.Fatalf("darwin parse: %v", m)
	}
	linux := "Hardware acceleration methods:\nvdpau\nvaapi\ndrm\nopencl\ncuda\nqsv\n\n"
	m := parseHwaccels(linux)
	for _, w := range []string{"vaapi", "qsv", "cuda", "vdpau", "drm", "opencl"} {
		if !m[w] {
			t.Fatalf("linux parse missing %s: %v", w, m)
		}
	}
	if n := parseHwaccels("ffmpeg: command not found"); len(n) != 0 {
		t.Fatalf("garbage parse: %v", n)
	}
}

func TestParseEncoders(t *testing.T) {
	sample := `Encoders:
 V..... = Video
 A..... = Audio
 S..... = Subtitle
 .F.... = Frame-level multithreading
 ..S... = Slice-level multithreading
 ...X.. = Codec is experimental
 ....B. = Supports draw_horiz_band
 .....D = Supports direct rendering method 1
 ------
 V....D a64multi             Multichannel (8 channel) audio (codec a64_multi)
 V....D libx264              libx264 H.264 / AVC / MPEG-4 AVC / MPEG-4 part 10 (codec h264)
 V....D h264_videotoolbox    VideoToolbox H.264 encoder (codec h264)
 V....D h264_vaapi           VAAPI-... H.264 encoder (codec h264)
 V....D h264_nvenc           NVIDIA NVENC H.264 encoder (codec h264)
 V....D h264_qsv             Intel Quick Sync Video H.264 encoder (codec h264)
`
	m := parseEncoders(sample)
	for _, w := range []string{"a64_multi_enc?", "libx264", "h264_videotoolbox", "h264_vaapi", "h264_nvenc", "h264_qsv"} {
		name := w
		if name == "a64_multi_enc?" {
			name = "a64multi"
		}
		if !m[name] {
			t.Fatalf("missing encoder %s: %v", name, m)
		}
	}
	if m["Video"] || m["="] || m["------"] {
		t.Fatalf("legend line parsed as encoder: %v", m)
	}
}

func TestSelectAccel(t *testing.T) {
	probe := func(hw []string, enc []string) hwProbe {
		p := hwProbe{hwaccels: map[string]bool{}, encoders: map[string]bool{}}
		for _, h := range hw {
			p.hwaccels[h] = true
		}
		for _, e := range enc {
			p.encoders[e] = true
		}
		return p
	}
	encAll := []string{"h264_videotoolbox", "h264_vaapi", "h264_nvenc", "h264_qsv"}
	hwAll := []string{"vaapi", "qsv", "cuda"}
	cases := []struct {
		name string
		goos string
		dri  bool
		hw   []string
		enc  []string
		env  string
		want string
	}{
		{"darwin-vt", "darwin", false, nil, encAll, "", AccelVideoToolbox},
		{"darwin-missing-encoder", "darwin", false, nil, nil, "", AccelNone},
		{"linux-dri-vaapi", "linux", true, hwAll, encAll, "", AccelVAAPI},
		{"linux-no-dri-nvenc", "linux", false, hwAll, encAll, "", AccelNVENC},
		{"linux-no-dri-qsv", "linux", false, hwAll, []string{"h264_qsv"}, "", AccelQSV},
		{"linux-no-hwaccel-flag", "linux", true, []string{"qsv"}, []string{"h264_vaapi", "h264_qsv"}, "", AccelQSV},
		{"nothing", "linux", false, nil, nil, "", AccelNone},
		{"windows-nvenc", "windows", false, hwAll, encAll, "", AccelNVENC},
		{"env-forces", "darwin", false, nil, nil, "nvenc", AccelNVENC},
		{"env-none", "darwin", false, nil, encAll, "none", AccelNone},
		{"env-bogus-ignored", "darwin", false, nil, encAll, "gpu", AccelVideoToolbox},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := selectAccel(c.goos, c.dri, probe(c.hw, c.enc), c.env); got != c.want {
				t.Fatalf("want %s, got %s", c.want, got)
			}
		})
	}
}

func TestSetHwAccelPinsWithoutProbe(t *testing.T) {
	m := New(t.TempDir())
	m.probeRun = func(argv []string) (string, error) {
		t.Error("probe must not run while a mode is pinned")
		return "", nil
	}
	if err := m.SetHwAccel(AccelVAAPI); err != nil {
		t.Fatal(err)
	}
	if m.HwAccel() != AccelVAAPI {
		t.Fatalf("want pinned vaapi, got %s", m.HwAccel())
	}
	if err := m.SetHwAccel("gpu"); err == nil {
		t.Fatal("unknown mode must error")
	}
	if err := m.SetHwAccel("auto"); err != nil {
		t.Fatal(err)
	}
	m.probeRun = func(argv []string) (string, error) {
		return "", context.Canceled
	}
	if m.HwAccel() != AccelNone {
		t.Fatalf("failed probe must resolve to none, got %s", m.HwAccel())
	}
}

func TestDetectEnvForcedAndCached(t *testing.T) {
	t.Setenv(envHwAccel, AccelQSV)
	m := New(t.TempDir())
	calls := 0
	m.probeRun = func(argv []string) (string, error) {
		calls++
		return "", nil
	}
	if m.HwAccel() != AccelQSV {
		t.Fatalf("env must force qsv, got %s", m.HwAccel())
	}
	if m.HwAccel() != AccelQSV {
		t.Fatal("second resolve inconsistent")
	}
	if calls != 2 {
		t.Fatalf("probe must run once (hwaccels+encoders) and be cached, ran %d times", calls)
	}
}

type fakeProcess struct {
	dieAfter time.Duration
	once     sync.Once
	exit     chan struct{}
	clean    bool
}

func (f *fakeProcess) start() error {
	if f.dieAfter >= 0 {
		go func() {
			time.Sleep(f.dieAfter)
			f.once.Do(func() { close(f.exit) })
		}()
	}
	return nil
}

func (f *fakeProcess) wait() error {
	<-f.exit
	if f.clean {
		return nil
	}
	return errFakeCrash
}

var errFakeCrash = errors.New("fake process crashed")

func (f *fakeProcess) kill() { f.once.Do(func() { close(f.exit) }) }

type spawnLog struct {
	mu    sync.Mutex
	calls [][]string
	procs []*fakeProcess
}

func (l *spawnLog) spawn(argv []string) process {
	l.mu.Lock()
	defer l.mu.Unlock()
	i := len(l.calls)
	l.calls = append(l.calls, argv)
	if i < len(l.procs) {
		return l.procs[i]
	}
	return l.procs[len(l.procs)-1]
}

func (l *spawnLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.calls)
}

func (l *spawnLog) args(i int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.calls[i]
}

func waitSpawns(t *testing.T, l *spawnLog, n int, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if l.count() >= n {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return l.count() >= n
}

func waitCond(d time.Duration, f func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if f() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return f()
}

func newFake(d time.Duration) *fakeProcess {
	return &fakeProcess{dieAfter: d, exit: make(chan struct{})}
}

func newCleanFake(d time.Duration) *fakeProcess {
	return &fakeProcess{dieAfter: d, exit: make(chan struct{}), clean: true}
}

func TestFallbackNotOnCleanExit(t *testing.T) {
	setFallbackWindow(t, 2*time.Second)
	m := New(t.TempDir())
	if err := m.SetHwAccel(AccelVideoToolbox); err != nil {
		t.Fatal(err)
	}
	lg := &spawnLog{procs: []*fakeProcess{newCleanFake(40 * time.Millisecond), newFake(-1)}}
	m.spawn = lg.spawn
	s, err := m.Get("clean", 1, "/src.mp4", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !waitCond(2*time.Second, s.isDead) {
		t.Fatal("cleanly exited session should be dead")
	}
	time.Sleep(300 * time.Millisecond)
	if lg.count() != 1 {
		t.Fatalf("clean early exit must not respawn, spawns=%d", lg.count())
	}
	s.mu.Lock()
	down := s.downgraded
	s.mu.Unlock()
	if down {
		t.Fatal("clean exit must not mark downgrade")
	}
	m.Close("clean")
}

func watcherDone(s *Session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.fallbackPending
}

func TestFallbackRetriesSoftware(t *testing.T) {
	setFallbackWindow(t, 2*time.Second)
	m := New(t.TempDir())
	if err := m.SetHwAccel(AccelVideoToolbox); err != nil {
		t.Fatal(err)
	}
	lg := &spawnLog{procs: []*fakeProcess{newFake(40 * time.Millisecond), newFake(-1)}}
	m.spawn = lg.spawn
	s, err := m.Get("fb", 1, "/src.mp4", 0)
	if err != nil {
		t.Fatal(err)
	}
	if lg.count() != 1 || !has(lg.args(0), "h264_videotoolbox") {
		t.Fatalf("first spawn must be videotoolbox, got %v", lg.args(0))
	}
	if !waitSpawns(t, lg, 2, 2*time.Second) {
		t.Fatal("software fallback never spawned")
	}
	fallback := lg.args(1)
	if !has(fallback, "libx264") || has(fallback, "h264_videotoolbox") {
		t.Fatalf("fallback args must be software, got %v", fallback)
	}
	s.mu.Lock()
	down := s.downgraded
	s.mu.Unlock()
	if !down {
		t.Fatal("session must remember the downgrade")
	}
	if s.isDead() {
		t.Fatal("session must be live on the fallback process")
	}
	if !waitCond(2*time.Second, func() bool { return watcherDone(s) }) {
		t.Fatal("fallback watcher never exited")
	}
	m.Close("fb")
}

func TestFallbackNotAfterWindow(t *testing.T) {
	setFallbackWindow(t, 150*time.Millisecond)
	m := New(t.TempDir())
	if err := m.SetHwAccel(AccelNVENC); err != nil {
		t.Fatal(err)
	}
	lg := &spawnLog{procs: []*fakeProcess{newFake(400 * time.Millisecond)}}
	m.spawn = lg.spawn
	s, err := m.Get("late", 1, "/src.mp4", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !waitCond(2*time.Second, s.isDead) {
		t.Fatal("session should be dead after late ffmpeg exit")
	}
	time.Sleep(200 * time.Millisecond)
	if lg.count() != 1 {
		t.Fatalf("death after the window must not respawn, spawns=%d", lg.count())
	}
	s.mu.Lock()
	down := s.downgraded
	s.mu.Unlock()
	if down {
		t.Fatal("late death must not mark downgrade")
	}
	m.Close("late")
}

func TestKilledSessionNoFallback(t *testing.T) {
	setFallbackWindow(t, 5*time.Second)
	m := New(t.TempDir())
	if err := m.SetHwAccel(AccelVAAPI); err != nil {
		t.Fatal(err)
	}
	lg := &spawnLog{procs: []*fakeProcess{newFake(-1), newFake(-1)}}
	m.spawn = lg.spawn
	s, err := m.Get("k", 1, "/src.mp4", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Close("k")
	if !waitCond(2*time.Second, func() bool { return watcherDone(s) }) {
		t.Fatal("fallback watcher never exited after kill")
	}
	if lg.count() != 1 {
		t.Fatalf("killed session must not respawn, spawns=%d", lg.count())
	}
}

func TestSoftwareModeNoWatcher(t *testing.T) {
	setFallbackWindow(t, 2*time.Second)
	m := New(t.TempDir())
	if err := m.SetHwAccel(AccelNone); err != nil {
		t.Fatal(err)
	}
	lg := &spawnLog{procs: []*fakeProcess{newFake(30 * time.Millisecond), newFake(-1)}}
	m.spawn = lg.spawn
	s, err := m.Get("sw", 1, "/src.mp4", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !waitCond(2*time.Second, s.isDead) {
		t.Fatal("software session should die with its process")
	}
	time.Sleep(200 * time.Millisecond)
	if lg.count() != 1 {
		t.Fatalf("software mode must never respawn, spawns=%d", lg.count())
	}
	m.Close("sw")
}

func TestPrebufferHeldThroughFallback(t *testing.T) {
	setFallbackWindow(t, 3*time.Second)
	m := New(t.TempDir())
	if err := m.SetHwAccel(AccelVideoToolbox); err != nil {
		t.Fatal(err)
	}
	lg := &spawnLog{procs: []*fakeProcess{newFake(60 * time.Millisecond), newFake(-1)}}
	m.spawn = lg.spawn
	s, err := m.Get("pb", 1, "/src.mp4", 0)
	if err != nil {
		t.Fatal(err)
	}
	res := make(chan int, 1)
	go func() { res <- s.Prebuffer(context.Background(), 2, 5*time.Second) }()
	if !waitSpawns(t, lg, 2, 2*time.Second) {
		t.Fatal("fallback never spawned")
	}
	writeFile(t, filepath.Join(s.Dir, "seg00000.ts"), "x")
	writeFile(t, filepath.Join(s.Dir, "seg00001.ts"), "x")
	select {
	case n := <-res:
		if n != 2 {
			t.Fatalf("prebuffer must survive the fallback swap, got %d/2", n)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("prebuffer never returned")
	}
	if !waitCond(2*time.Second, func() bool { return watcherDone(s) }) {
		t.Fatal("fallback watcher never exited")
	}
	m.Close("pb")
}
