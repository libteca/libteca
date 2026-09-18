package procfd

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ErrUnavailable means the installed ffmpeg/ffprobe cannot read a passed
// file descriptor as a protocol-whitelisted input. Every processor path
// fails closed on it rather than falling back to unconfined pathnames.
var ErrUnavailable = errors.New("descriptor input confinement unavailable")

const (
	formOption = iota
	formURL
	formDevFD
	probeTimeout = 10 * time.Second
)

type capability struct {
	once sync.Once
	form int
	err  error
}

var (
	ffmpegCap  capability
	ffprobeCap capability
)

// Args returns the argv fragment that makes bin read its input from the
// descriptor attached as ExtraFiles[0] (child fd 3) under a protocol
// whitelist. The whitelist always covers the descriptor protocol itself;
// extra names the additional protocols the command needs ("file" for local
// outputs, "pipe" for stdout).
func Args(bin string, extra ...string) ([]string, error) {
	switch bin {
	case "ffmpeg":
		return ffmpegCap.args(extra...)
	case "ffprobe":
		return ffprobeCap.args(extra...)
	}
	return nil, fmt.Errorf("procfd: unsupported binary %q", bin)
}

// Startup probes both binaries and logs the confinement posture. Failures
// are not fatal here: the processor paths refuse individually, fail-closed.
func Startup() {
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		if _, err := Args(bin); err != nil {
			slog.Warn("libteca: processor descriptor input unavailable; transcoding, probing and extraction will refuse",
				"binary", bin, "err", err)
			continue
		}
		slog.Info("libteca: processor descriptor input verified", "binary", bin)
	}
}

func (c *capability) args(extra ...string) ([]string, error) {
	bin := "ffmpeg"
	if c == &ffprobeCap {
		bin = "ffprobe"
	}
	c.once.Do(func() { c.probe(bin) })
	if c.err != nil {
		return nil, c.err
	}
	return c.argv(extra...), nil
}

func (c *capability) argv(extra ...string) []string {
	protos := []string{"fd"}
	input := "fd:"
	switch c.form {
	case formURL:
		input = "fd:3"
	case formDevFD:
		protos = []string{"file"}
		input = "/dev/fd/3"
	}
	for _, p := range extra {
		known := false
		for _, base := range protos {
			if p == base {
				known = true
				break
			}
		}
		if !known {
			protos = append(protos, p)
		}
	}
	args := []string{"-protocol_whitelist", strings.Join(protos, ",")}
	if c.form == formOption {
		args = append(args, "-fd", "3")
	}
	return append(args, "-i", input)
}

func (c *capability) probe(bin string) {
	for _, form := range []int{formOption, formURL, formDevFD} {
		if probeForm(bin, form) {
			c.form = form
			return
		}
	}
	c.err = ErrUnavailable
}

func probeForm(bin string, form int) bool {
	if _, err := exec.LookPath(bin); err != nil {
		return false
	}
	dir, err := os.MkdirTemp("", "libteca-procfd-*")
	if err != nil {
		return false
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "probe.wav")
	if err := os.WriteFile(path, probeWAV(), 0o600); err != nil {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	fake := capability{form: form}
	args := append(fake.argv(), "-v", "error")
	if bin == "ffmpeg" {
		args = append(args, "-f", "null", "-")
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.ExtraFiles = []*os.File{f}
	return cmd.Run() == nil && ctx.Err() == nil
}

func probeWAV() []byte {
	const samples = 800
	dataLen := samples * 2
	body := make([]byte, 44+dataLen)
	copy(body[0:], "RIFF")
	binary.LittleEndian.PutUint32(body[4:], uint32(36+dataLen))
	copy(body[8:], "WAVE")
	copy(body[12:], "fmt ")
	binary.LittleEndian.PutUint32(body[16:], 16)
	binary.LittleEndian.PutUint16(body[20:], 1)
	binary.LittleEndian.PutUint16(body[22:], 1)
	binary.LittleEndian.PutUint32(body[24:], 8000)
	binary.LittleEndian.PutUint32(body[28:], 16000)
	binary.LittleEndian.PutUint16(body[32:], 2)
	binary.LittleEndian.PutUint16(body[34:], 16)
	copy(body[36:], "data")
	binary.LittleEndian.PutUint32(body[40:], uint32(dataLen))
	return body
}
