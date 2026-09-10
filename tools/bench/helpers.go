package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
)

func buildPkg(root, pkg, out string) error {
	cmd := exec.Command("go", "build", "-o", out, pkg)
	cmd.Dir = root
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// rssKB returns resident set size in KB via ps (darwin + linux).
func rssKB(pid int) (int64, error) {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return 0, err
	}
	return v, nil
}

// seedExec builds tools/seed and runs it to guarantee >= want media files
// under dir. Seed is idempotent, so an existing cache is reused.
func seedExec(root, kind, dir string, want int, extra ...string) error {
	switch kind {
	case "audio", "video":
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			return fmt.Errorf("ffmpeg not in PATH: %w", err)
		}
	}
	bin := filepath.Join(os.TempDir(), "libteca-seed-bench")
	if err := buildPkg(root, "./tools/seed", bin); err != nil {
		return fmt.Errorf("build tools/seed: %w", err)
	}
	args := []string{"--kind", kind, "--out", dir, "--count", strconv.Itoa(want)}
	args = append(args, extra...)
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func countExt(dir, ext string) int {
	n := 0
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ext) {
			n++
		}
		return nil
	})
	return n
}

func findFirst(dir, ext string) (string, error) {
	var found string
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.EqualFold(filepath.Ext(path), ext) && found == "" {
			found = path
		}
		return nil
	})
	if found == "" {
		return "", fmt.Errorf("no %s file under %s", ext, dir)
	}
	return found, nil
}

func printTable(title string, rows [][4]string, verdicts []verdict) {
	fmt.Printf("\n%s\n", title)
	w := tabwriter.NewWriter(os.Stdout, 2, 4, 2, ' ', 0)
	for _, r := range rows {
		fmt.Fprintf(w, "  %s:\t%s\t(target %s)\t%s\n", r[0], r[1], r[2], r[3])
	}
	w.Flush()
	if len(verdicts) > 0 {
		pass := true
		for _, v := range verdicts {
			if !v.Pass {
				pass = false
			}
		}
		status := "PASS"
		if !pass {
			status = "FAIL"
		}
		fmt.Printf("  verdict: %s\n", status)
	}
}
