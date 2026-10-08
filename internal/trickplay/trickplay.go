package trickplay

import (
	"context"
	"errors"
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

	"github.com/libteca/libteca/internal/procfd"
	"github.com/libteca/libteca/internal/resourcebudget"
)

const (
	Interval = 10 // seconds between sampled frames
	TileCols = 10
	TileRows = 10
)

var (
	ErrNotFound  = errors.New("trickplay: tile not found")
	ErrNoFFmpeg  = errors.New("trickplay: ffmpeg not available")
	ErrBadItemID = errors.New("trickplay: invalid item id")
	ErrBadWidth  = errors.New("trickplay: invalid width")
	ErrBusy      = errors.New("trickplay: generation capacity exhausted")
)

var generationSlots = make(chan struct{}, 2)

func supportedWidth(width int) bool {
	return width == 160 || width == 320
}

// corpus: manifest field names/semantics unverified against real 10.10 clients
type Manifest struct {
	Width      int   `json:"Width"`
	Height     int   `json:"Height"`
	TileWidth  int   `json:"TileWidth"`
	TileHeight int   `json:"TileHeight"`
	Interval   int   `json:"Interval"`
	TileCount  int   `json:"TileCount"`
	Bandwidth  int64 `json:"Bandwidth"`
}

type generation struct {
	done chan struct{}
	err  error
}

type Generator struct {
	diskPool  *resourcebudget.DiskPool
	configErr error
	dir       string
	ffmpeg    string
	mu        sync.Mutex
	gen       map[string]*generation
	run       func(ctx context.Context, name string, files []*os.File, args ...string) ([]byte, error)
	fdArgs    func(extra ...string) ([]string, error)
	cleanup   func(string) error
}

func New(dataDir string) *Generator {
	ff := ""
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		ff = "ffmpeg"
	}
	per, perErr := resourcebudget.Env("LIBTECA_TRICKPLAY_JOB_BYTES")
	total, totalErr := resourcebudget.Env("LIBTECA_TRICKPLAY_TOTAL_BYTES")
	return &Generator{diskPool: resourcebudget.NewDiskPool(filepath.Join(dataDir, "trickplay"), per, total), configErr: errors.Join(perErr, totalErr), dir: dataDir, ffmpeg: ff, gen: map[string]*generation{}, run: ffmpegRun, fdArgs: func(extra ...string) ([]string, error) {
		return procfd.Args("ffmpeg", extra...)
	}}
}

func ffmpegRun(ctx context.Context, name string, files []*os.File, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.ExtraFiles = files
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	return cmd.CombinedOutput()
}

var reItemID = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// ParseTileName maps a URL filename like "3.jpg" to its 0-based sheet index.
// corpus: real Jellyfin tile index base (0 vs 1) unverified
func ParseTileName(name string) (int, bool) {
	i := strings.LastIndexByte(name, '.')
	if i < 0 || name[i:] != ".jpg" {
		return 0, false
	}
	n, err := strconv.Atoi(name[:i])
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func SheetPath(dataDir, itemID string, width, index int) string {
	return filepath.Join(dataDir, "trickplay", itemID, strconv.Itoa(width), strconv.Itoa(index)+".jpg")
}

func (g *Generator) widthDir(itemID string, width int) string {
	return filepath.Join(g.dir, "trickplay", itemID, strconv.Itoa(width))
}

// Tile returns the cache path of sheet `index` for (itemID, width), generating
// the full sheet set lazily. Concurrent callers for the same (itemID, width)
// block until the winning generation finishes. Returns ErrNotFound when the
// index is beyond the generated set, ErrNoFFmpeg when ffmpeg is absent.
// open supplies the rooted input descriptor; the source pathname never
// reaches ffmpeg (audit F03).
func (g *Generator) Tile(ctx context.Context, itemID string, open func() (*os.File, error), width, index int) (string, error) {
	if !reItemID.MatchString(itemID) {
		return "", ErrBadItemID
	}
	if !supportedWidth(width) {
		return "", ErrBadWidth
	}
	if index < 0 {
		return "", ErrNotFound
	}
	path := SheetPath(g.dir, itemID, width, index)
	if g.complete(itemID, width) {
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		return "", ErrNotFound
	}
	if err := g.ensure(ctx, itemID, open, width); err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		return "", ErrNotFound
	}
	return path, nil
}

// Manifest generates (lazily) and describes the tile set for (itemID, width).
// Height is the per-frame pixel height derived from the first sheet's JPEG
// dimensions; TileCount counts sheets on disk; Bandwidth is their total bytes.
func (g *Generator) Manifest(ctx context.Context, itemID string, open func() (*os.File, error), width int) (*Manifest, error) {
	if _, err := g.Tile(ctx, itemID, open, width, 0); err != nil {
		return nil, err
	}
	dir := g.widthDir(itemID, width)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	m := &Manifest{Width: width, TileWidth: TileCols, TileHeight: TileRows, Interval: Interval}
	for _, e := range entries {
		idx, ok := ParseTileName(e.Name())
		if !ok {
			continue
		}
		m.TileCount++
		if fi, err := e.Info(); err == nil {
			m.Bandwidth += fi.Size()
		}
		if idx == 0 {
			if data, err := os.ReadFile(filepath.Join(dir, e.Name())); err == nil {
				if _, sh, err := jpegDims(data); err == nil && sh >= TileRows {
					m.Height = sh / TileRows
				}
			}
		}
	}
	return m, nil
}

func (g *Generator) ensure(ctx context.Context, itemID string, open func() (*os.File, error), width int) error {
	key := itemID + "/" + strconv.Itoa(width)
	g.mu.Lock()
	if call, ok := g.gen[key]; ok {
		g.mu.Unlock()
		select {
		case <-call.done:
			return call.err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	call := &generation{done: make(chan struct{})}
	g.gen[key] = call
	g.mu.Unlock()

	call.err = g.generate(ctx, itemID, open, width)
	close(call.done)

	g.mu.Lock()
	if g.gen[key] == call {
		delete(g.gen, key)
	}
	g.mu.Unlock()
	return call.err
}

func (g *Generator) complete(itemID string, width int) bool {
	_, err := os.Stat(filepath.Join(g.widthDir(itemID, width), "COMPLETE"))
	return err == nil
}

func (g *Generator) generate(ctx context.Context, itemID string, open func() (*os.File, error), width int) error {
	if g.configErr != nil {
		return g.configErr
	}
	dir := g.widthDir(itemID, width)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(dir+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return ErrBusy
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if g.complete(itemID, width) {
		return nil
	}
	if g.ffmpeg == "" {
		return ErrNoFFmpeg
	}
	inArgs, err := g.fdArgs("file")
	if err != nil {
		return err
	}
	input, err := open()
	if err != nil {
		return err
	}
	defer input.Close()
	select {
	case generationSlots <- struct{}{}:
		defer func() { <-generationSlots }()
	default:
		return ErrBusy
	}
	remove := g.cleanup
	if remove == nil {
		remove = os.RemoveAll
	}
	if err := recoverIncomplete(dir, remove); err != nil {
		return err
	}
	var lease *resourcebudget.DiskLease
	if g.diskPool != nil {
		lease, err = g.diskPool.Acquire(dir)
		if err != nil {
			return err
		}
		defer lease.Release()
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	args := []string{
		"-y", "-nostdin", "-v", "error",
	}
	args = append(args, inArgs...)
	args = append(args,
		"-vf", fmt.Sprintf("fps=1/%d,scale=%d:-2,tile=%dx%d", Interval, width, TileCols, TileRows),
		"-q:v", "4",
		"-start_number", "0",
		filepath.Join(dir, "%d.jpg"),
	)
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	var finish func() error
	if lease != nil && lease.Limit() > 0 {
		finish = resourcebudget.Monitor(runCtx, dir, lease.Limit(), stop)
	}
	out, runErr := g.run(runCtx, g.ffmpeg, []*os.File{input}, args...)
	if finish != nil {
		runErr = errors.Join(finish(), runErr)
	}
	if runErr == nil {
		runErr = ctx.Err()
	}
	if runErr != nil {
		cleanupErr := os.RemoveAll(dir)
		return errors.Join(fmt.Errorf("trickplay ffmpeg: %w: %s", runErr, out), cleanupErr)
	}
	marker, err := os.Create(filepath.Join(dir, "COMPLETE"))
	if err != nil {
		os.RemoveAll(dir)
		return err
	}
	if err := marker.Close(); err != nil {
		os.RemoveAll(dir)
		return err
	}
	return nil
}

func recoverIncomplete(dir string, remove func(string) error) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == "COMPLETE" {
			return ErrBusy
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return ErrBusy
		}
		if entry.Name() == "OWNED" {
			marker, err := os.ReadFile(filepath.Join(dir, entry.Name()))
			if err != nil {
				return err
			}
			if string(marker) != "libteca-trickplay-v1" {
				return ErrBusy
			}
		} else if _, ok := ParseTileName(entry.Name()); !ok {
			return ErrBusy
		}
	}
	return remove(dir)
}

// jpegDims reads the SOF marker of a baseline/progressive JPEG.
func jpegDims(data []byte) (width, height int, err error) {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 0, 0, errors.New("not a jpeg")
	}
	i := 2
	for i+3 < len(data) {
		if data[i] != 0xFF {
			i++
			continue
		}
		marker := data[i+1]
		if marker == 0xD8 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD7) {
			i += 2
			continue
		}
		if i+4 > len(data) {
			break
		}
		segLen := int(data[i+2])<<8 | int(data[i+3])
		if marker >= 0xC0 && marker <= 0xCF && marker != 0xC4 && marker != 0xC8 && marker != 0xCC {
			if i+9 <= len(data) && segLen >= 7 {
				height = int(data[i+5])<<8 | int(data[i+6])
				width = int(data[i+7])<<8 | int(data[i+8])
				return width, height, nil
			}
			return 0, 0, errors.New("short SOF")
		}
		i += 2 + segLen
	}
	return 0, 0, errors.New("no SOF marker")
}
