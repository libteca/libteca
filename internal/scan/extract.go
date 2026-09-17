package scan

import (
	"context"
	"os/exec"
	"time"
)

func extractCmd(parent context.Context, src, dst string) *exec.Cmd {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-v", "quiet", "-i", src, "-an", "-map", "0:v:0", "-frames:v", "1", "-q:v", "3", dst)
	cmd.Cancel = func() error {
		cancel()
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
	return cmd
}
