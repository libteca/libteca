package scan

import (
	"context"
	"os"
	"os/exec"
	"time"

	"github.com/libteca/libteca/internal/procfd"
)

func extractCmd(parent context.Context, src *os.File, dst string) *exec.Cmd {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil
	}
	inArgs, err := procfd.Args("ffmpeg", "file")
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	args := []string{"-y", "-v", "quiet"}
	args = append(args, inArgs...)
	args = append(args, "-an", "-map", "0:v:0", "-frames:v", "1", "-q:v", "3", dst)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.ExtraFiles = []*os.File{src}
	cmd.Cancel = func() error {
		cancel()
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	return cmd
}
