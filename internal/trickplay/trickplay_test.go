package trickplay

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSheetPath(t *testing.T) {
	got := SheetPath("/data", "e12", 320, 3)
	want := filepath.Join("/data", "trickplay", "e12", "320", "3.jpg")
	if got != want {
		t.Fatalf("SheetPath = %q, want %q", got, want)
	}
}

func TestParseTileName(t *testing.T) {
	cases := []struct {
		in   string
		idx  int
		want bool
	}{
		{"0.jpg", 0, true},
		{"17.jpg", 17, true},
		{"1.jpeg", 0, false},
		{"a.jpg", 0, false},
		{"-1.jpg", 0, false},
		{"1.png", 0, false},
	}
	for _, c := range cases {
		idx, ok := ParseTileName(c.in)
		if ok != c.want || (ok && idx != c.idx) {
			t.Errorf("ParseTileName(%q) = (%d, %v), want (%d, %v)", c.in, idx, ok, c.idx, c.want)
		}
	}
}

func fakeGen(t *testing.T) func(context.Context, string, ...string) ([]byte, error) {
	return func(_ context.Context, _ string, args ...string) ([]byte, error) {
		dir := filepath.Dir(args[len(args)-1])
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		for i := 0; i < 4; i++ {
			os.WriteFile(filepath.Join(dir, fmt.Sprintf("%d.jpg", i)), []byte("x"), 0o644)
		}
		return nil, nil
	}
}

func newTestGen(t *testing.T, run func(context.Context, string, ...string) ([]byte, error)) *Generator {
	t.Helper()
	return &Generator{dir: t.TempDir(), ffmpeg: "ffmpeg-fake", gen: map[string]*generation{}, run: run}
}

func TestTileGeneratesAndCaches(t *testing.T) {
	g := newTestGen(t, fakeGen(t))
	p, err := g.Tile(context.Background(), "e7", "src.mkv", 320, 2)
	if err != nil {
		t.Fatalf("Tile: %v", err)
	}
	if p != SheetPath(g.dir, "e7", 320, 2) {
		t.Fatalf("path %q", p)
	}
	// Second call must not re-run ffmpeg (runner would fail).
	g.run = func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("should not regenerate")
	}
	if _, err := g.Tile(context.Background(), "e7", "src.mkv", 320, 3); err != nil {
		t.Fatalf("cached Tile: %v", err)
	}
	if _, err := g.Tile(context.Background(), "e7", "src.mkv", 320, 9); !errors.Is(err, ErrNotFound) {
		t.Fatalf("beyond set: err = %v, want ErrNotFound", err)
	}
}

func TestTileSingleflight(t *testing.T) {
	var calls atomic.Int64
	ready := make(chan struct{})
	g := newTestGen(t, func(ctx context.Context, name string, args ...string) ([]byte, error) {
		calls.Add(1)
		<-ready
		dir := filepath.Dir(args[len(args)-1])
		os.WriteFile(filepath.Join(dir, "0.jpg"), []byte("x"), 0o644)
		return nil, nil
	})
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = g.Tile(context.Background(), "e1", "src", 160, 0)
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(ready)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("ffmpeg ran %d times, want 1", calls.Load())
	}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
}

func TestTileBadInputs(t *testing.T) {
	g := newTestGen(t, nil)
	if _, err := g.Tile(context.Background(), "../evil", "src", 320, 0); !errors.Is(err, ErrBadItemID) {
		t.Fatalf("item id: err = %v", err)
	}
	if _, err := g.Tile(context.Background(), "e1", "src", 8, 0); !errors.Is(err, ErrBadWidth) {
		t.Fatalf("width: err = %v", err)
	}
}

func TestNoFFmpeg(t *testing.T) {
	g := newTestGen(t, nil)
	g.ffmpeg = ""
	if _, err := g.Tile(context.Background(), "e1", "src", 320, 0); !errors.Is(err, ErrNoFFmpeg) {
		t.Fatalf("err = %v, want ErrNoFFmpeg", err)
	}
}

func TestJPEGDims(t *testing.T) {
	sof := []byte{0xFF, 0xD8, 0xFF, 0xC0, 0x00, 0x0B, 0x08, 0x00, 0x78, 0x01, 0x40, 0x01, 0x01, 0xFF, 0xD9}
	w, h, err := jpegDims(sof)
	if err != nil {
		t.Fatalf("jpegDims: %v", err)
	}
	if w != 320 || h != 120 {
		t.Fatalf("dims = %dx%d, want 320x120", w, h)
	}
	if _, _, err := jpegDims([]byte{0xFF, 0xD8, 0xFF, 0xD9}); err == nil {
		t.Fatal("expected error for jpeg without SOF")
	}
}

func TestManifestFromFakeSheets(t *testing.T) {
	sof := []byte{0xFF, 0xD8, 0xFF, 0xC0, 0x00, 0x0B, 0x08, 0x01, 0x2C, 0x03, 0x20, 0x01, 0x01, 0xFF, 0xD9}
	var g *Generator
	g = newTestGen(t, func(context.Context, string, ...string) ([]byte, error) {
		dir := filepath.Join(g.dir, "trickplay", "e1", "160")
		os.MkdirAll(dir, 0o700)
		os.WriteFile(filepath.Join(dir, "0.jpg"), sof, 0o644)
		os.WriteFile(filepath.Join(dir, "1.jpg"), sof, 0o644)
		return nil, nil
	})
	m, err := g.Manifest(context.Background(), "e1", "src", 160)
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if m.Width != 160 || m.Height != 30 || m.TileWidth != 10 || m.TileHeight != 10 || m.Interval != 10 {
		t.Fatalf("manifest fields: %+v", m)
	}
	if m.TileCount != 2 {
		t.Fatalf("TileCount = %d, want 2", m.TileCount)
	}
	if m.Bandwidth != int64(2*len(sof)) {
		t.Fatalf("Bandwidth = %d, want %d", m.Bandwidth, 2*len(sof))
	}
}

func TestGenerateFailureClearsPartialTiles(t *testing.T) {
	g := newTestGen(t, func(_ context.Context, _ string, args ...string) ([]byte, error) {
		dir := filepath.Dir(args[len(args)-1])
		os.MkdirAll(dir, 0o700)
		os.WriteFile(filepath.Join(dir, "0.jpg"), []byte("partial"), 0o644)
		return nil, errors.New("ffmpeg died mid-encode")
	})
	if _, err := g.Tile(context.Background(), "e9", "src.mkv", 320, 1); err == nil {
		t.Fatal("Tile must fail when ffmpeg fails")
	}
	if _, err := os.Stat(g.widthDir("e9", 320)); !os.IsNotExist(err) {
		t.Fatal("partial tile dir must be removed after a failed generation")
	}
	g.run = fakeGen(t)
	if _, err := g.Tile(context.Background(), "e9", "src.mkv", 320, 1); err != nil {
		t.Fatalf("Tile after cleanup-retry: %v", err)
	}
}

func TestFFmpegIntegration(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not in PATH")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	out, err := exec.Command("ffmpeg", "-y", "-v", "error", "-f", "lavfi",
		"-i", "testsrc=duration=32:size=320x240:rate=10", src).CombinedOutput()
	if err != nil {
		t.Fatalf("synthetic video: %v: %s", err, out)
	}
	g := New(dir)
	m, err := g.Manifest(context.Background(), "e1", src, 160)
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	if m.TileCount < 1 || m.Height != 120 {
		t.Fatalf("manifest: %+v", m)
	}
	p, err := g.Tile(context.Background(), "e1", src, 160, 0)
	if err != nil {
		t.Fatalf("Tile: %v", err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read tile: %v", err)
	}
	if w, h, err := jpegDims(data); err != nil || w != 160*TileCols || h != 120*TileRows {
		t.Fatalf("sheet dims = %dx%d (err %v), want %dx%d", w, h, err, 160*TileCols, 120*TileRows)
	}
	if _, err := g.Tile(context.Background(), "e1", src, 160, 50); !errors.Is(err, ErrNotFound) {
		t.Fatalf("beyond set: err = %v, want ErrNotFound", err)
	}
}
