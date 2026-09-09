package scan

import (
	"os/exec"
	"runtime"
)

func extractCmd(src, dst string) *exec.Cmd {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil
	}
	_ = runtime.NumCPU()
	return exec.Command("ffmpeg", "-y", "-v", "quiet", "-i", src, "-an", "-map", "0:v:0", "-frames:v", "1", "-q:v", "3", dst)
}
