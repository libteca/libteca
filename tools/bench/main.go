package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const moduleIdent = "module github.com/libteca/libteca"

// Standalone bench entry (not a cmd/libteca subcommand): cmd/libteca is
// owned elsewhere this wave. Run as `go run ./tools/bench <idle|scan|transcode>`.

func usage() {
	fmt.Fprintln(os.Stderr, "usage: bench <idle|scan|transcode> [flags]")
	fmt.Fprintln(os.Stderr, "  idle        --samples N (default 10)")
	fmt.Fprintln(os.Stderr, "  scan        --count N (default 10000) --quick (=2000) --seed-dir DIR --reseed")
	fmt.Fprintln(os.Stderr, "  transcode   --runs N (default 20) --source FILE.mp4")
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "idle":
		err = runIdle(os.Args[2:])
	case "scan":
		err = runScan(os.Args[2:])
	case "transcode":
		err = runTranscode(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "bench:", err)
		os.Exit(1)
	}
}

type meta struct {
	Date      string `json:"date"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	GoVersion string `json:"go_version"`
	NumCPU    int    `json:"num_cpu"`
	Hostname  string `json:"hostname,omitempty"`
}

func newMeta() meta {
	host, _ := os.Hostname()
	return meta{
		Date: time.Now().Format(time.RFC3339), GOOS: runtime.GOOS,
		GOARCH: runtime.GOARCH, GoVersion: runtime.Version(),
		NumCPU: runtime.NumCPU(), Hostname: host,
	}
}

type verdict struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Target string `json:"target"`
	Pass   bool   `json:"pass"`
}

func writeResult(kind string, v any) (string, error) {
	root, err := repoRoot()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "bench", "results")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, time.Now().Format("20060102-150405")+"-"+kind+".json")
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	fmt.Printf("  results: %s\n", path)
	return path, nil
}

func mark(pass bool) string {
	if pass {
		return "PASS"
	}
	return "FAIL"
}

func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		mod, err := os.ReadFile(filepath.Join(wd, "go.mod"))
		if err == nil && strings.HasPrefix(string(mod), moduleIdent) {
			return wd, nil
		}
		parent := filepath.Dir(wd)
		if parent == wd {
			return "", fmt.Errorf("not inside the libteca module (no go.mod with %q)", moduleIdent)
		}
		wd = parent
	}
}
