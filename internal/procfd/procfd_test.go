package procfd

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func hasFFmpegTools() bool {
	_, errFF := exec.LookPath("ffmpeg")
	_, errFP := exec.LookPath("ffprobe")
	return errFF == nil && errFP == nil
}

func TestArgsShapeCarriesWhitelistAndDescriptorInput(t *testing.T) {
	if !hasFFmpegTools() {
		t.Skip("ffmpeg/ffprobe not installed")
	}
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		args, err := Args(bin, "file")
		if err != nil {
			t.Fatalf("%s: %v", bin, err)
		}
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "-protocol_whitelist fd,file") {
			t.Fatalf("%s args %q must whitelist exactly fd,file", bin, joined)
		}
		if strings.Contains(joined, "fd,file,file") {
			t.Fatalf("%s args %q must dedupe protocols", bin, joined)
		}
		if !strings.Contains(joined, "-fd 3 -i fd:") && !strings.Contains(joined, "-i fd:3") && !strings.Contains(joined, "-i /dev/fd/3") {
			t.Fatalf("%s args %q must read the input from descriptor 3", bin, joined)
		}
	}
}

func TestProbeFailClosedWithoutBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		c := &capability{}
		c.probe(bin)
		if c.err != ErrUnavailable {
			t.Fatalf("%s probe err = %v, want ErrUnavailable", bin, c.err)
		}
	}
}

func TestArgsUnknownBinary(t *testing.T) {
	if _, err := Args("vlc"); err == nil {
		t.Fatal("unknown binaries must be refused")
	}
}

func TestProbeFormRejectsMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if probeForm("ffmpeg", formOption) {
		t.Fatal("probe must not succeed without the binary")
	}
}

func TestDescriptorInputDecodesMedia(t *testing.T) {
	if !hasFFmpegTools() {
		t.Skip("ffmpeg/ffprobe not installed")
	}
	dir := t.TempDir()
	wav := filepath.Join(dir, "in.wav")
	if err := os.WriteFile(wav, probeWAV(), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(wav)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	in, err := Args("ffprobe")
	if err != nil {
		t.Fatal(err)
	}
	args := append(append([]string{"-v", "error"}, in...), "-show_entries", "format=format_name", "-of", "csv=p=0")
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	cmd.ExtraFiles = []*os.File{f}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ffprobe over descriptor: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "wav") {
		t.Fatalf("format = %q, want wav", out)
	}
}
