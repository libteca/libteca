package procfd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/mediafs"
)

func TestSecondaryInputsRefusedBySupportedDescriptorForms(t *testing.T) {
	if !hasFFmpegTools() {
		t.Skip("ffmpeg/ffprobe not installed")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "library")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "outside.wav")
	if err := os.WriteFile(outside, probeWAV(), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, bin := range []string{"ffmpeg", "ffprobe"} {
		for _, form := range []int{formOption, formURL, formDevFD} {
			t.Run(fmt.Sprintf("%s/form%d", bin, form), func(t *testing.T) {
				if !probeForm(bin, form) {
					t.Skip("descriptor form unavailable in installed binary")
				}
				for _, extra := range [][]string{nil, {"file"}, {"pipe"}} {
					for _, format := range []string{"concat", "hls"} {
						t.Run(format+strings.Join(extra, ","), func(t *testing.T) {
							contents := "ffconcat version 1.0\nfile 'file://" + outside + "'\n"
							if format == "hls" {
								contents = "#EXTM3U\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:0.1,\nfile://" + outside + "\n#EXT-X-ENDLIST\n"
							}
							path := filepath.Join(root, format+".mp4")
							if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
								t.Fatal(err)
							}
							f, err := mediafs.Open(root, path)
							if err != nil {
								t.Fatal(err)
							}
							defer f.Close()
							c := capability{form: form}
							input := c.argv()
							at := 0
							for input[at] != "-i" && input[at] != "-fd" {
								at++
							}
							if format == "concat" {
								descriptorProtocol := "fd,file"
								if form == formDevFD {
									descriptorProtocol = "file"
								}
								baseline := append([]string{"-v", "error", "-protocol_whitelist", descriptorProtocol, "-safe", "0"}, input[at:]...)
								if bin == "ffmpeg" {
									baseline = append(baseline, "-f", "null", "-")
								} else {
									baseline = append(baseline, "-show_entries", "format=format_name", "-of", "csv=p=0")
								}
								ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
								cmd := exec.CommandContext(ctx, bin, baseline...)
								cmd.ExtraFiles = []*os.File{f}
								out, err := cmd.CombinedOutput()
								cancel()
								if err != nil {
									t.Fatalf("concat positive control failed: %v %s", err, out)
								}
								if _, err := f.Seek(0, 0); err != nil {
									t.Fatal(err)
								}
								if form != formDevFD {
									confined := append([]string{"-v", "error", "-protocol_whitelist", "fd", "-safe", "0"}, input[at:]...)
									if bin == "ffmpeg" {
										confined = append(confined, "-f", "null", "-")
									} else {
										confined = append(confined, "-show_entries", "format=format_name", "-of", "csv=p=0")
									}
									cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
									ccmd := exec.CommandContext(cctx, bin, confined...)
									ccmd.ExtraFiles = []*os.File{f}
									cout, cerr := ccmd.CombinedOutput()
									ccancel()
									if cerr == nil || !strings.Contains(string(cout), "not on whitelist") {
										t.Fatalf("concat secondary open not refused at protocol admission: %v %s", cerr, cout)
									}
									if _, err := f.Seek(0, 0); err != nil {
										t.Fatal(err)
									}
								}
							}
							args := append([]string{"-v", "error"}, c.argv(extra...)...)
							if bin == "ffmpeg" {
								args = append(args, "-f", "null", "-")
							} else {
								args = append(args, "-show_entries", "format=format_name", "-of", "csv=p=0")
							}
							ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
							defer cancel()
							cmd := exec.CommandContext(ctx, bin, args...)
							cmd.ExtraFiles = []*os.File{f}
							out, err := cmd.CombinedOutput()
							if ctx.Err() != nil {
								t.Fatalf("timed out: %s", out)
							}
							if err == nil {
								t.Fatalf("nested %s admitted through production argv: %s", format, out)
							}
							refused := strings.Contains(string(out), "not on whitelist") ||
								strings.Contains(string(out), "Format not on whitelist") ||
								strings.Contains(string(out), "Invalid data found") ||
								strings.Contains(string(out), "Not detecting")
							if !refused {
								t.Fatalf("nested %s not refused at admission: %v %s", format, err, out)
							}
						})
					}
				}
			})
		}
	}
}

func TestInputBoundariesRetainFileAndPipeOutputs(t *testing.T) {
	if !hasFFmpegTools() {
		t.Skip("ffmpeg/ffprobe not installed")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "in.wav")
	if err := os.WriteFile(path, probeWAV(), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, form := range []int{formOption, formURL, formDevFD} {
		t.Run(fmt.Sprint(form), func(t *testing.T) {
			if !probeForm("ffmpeg", form) {
				t.Skip("descriptor form unavailable in installed binary")
			}
			for _, output := range []string{filepath.Join(dir, "out.wav"), "pipe:1"} {
				f, err := os.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				c := capability{form: form}
				args := append([]string{"-y", "-v", "error"}, c.argv("file", "pipe")...)
				args = append(args, "-f", "wav", output)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				cmd := exec.CommandContext(ctx, "ffmpeg", args...)
				cmd.ExtraFiles = []*os.File{f}
				bytes, err := cmd.CombinedOutput()
				cancel()
				f.Close()
				if err != nil {
					t.Fatalf("output %s: %v %s", output, err, bytes)
				}
				if output == "pipe:1" && !strings.Contains(string(bytes), "WAVE") {
					t.Fatal("missing pipe output")
				}
			}
		})
	}
}
