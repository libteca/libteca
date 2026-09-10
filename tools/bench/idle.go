package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const idleTargetMB = 100.0

type idleResult struct {
	Meta     meta      `json:"meta"`
	Samples  []float64 `json:"rss_samples_mb"`
	MeanMB   float64   `json:"rss_mean_mb"`
	MaxMB    float64   `json:"rss_max_mb"`
	TargetMB float64   `json:"target_mb"`
	Pass     bool      `json:"pass"`
	Verdicts []verdict `json:"verdicts"`
}

func runIdle(args []string) error {
	fs := flag.NewFlagSet("idle", flag.ExitOnError)
	samples := fs.Int("samples", 10, "RSS samples, 1s apart")
	fs.Parse(args)

	root, err := repoRoot()
	if err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "libteca-bench-idle")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	bin := filepath.Join(tmp, "libteca")
	if err := buildPkg(root, "./cmd/libteca", bin); err != nil {
		return fmt.Errorf("build cmd/libteca: %w", err)
	}
	port, err := freePort()
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "--data", filepath.Join(tmp, "data"), "--port", strconv.Itoa(port))
	var log strings.Builder
	cmd.Stdout, cmd.Stderr = &log, &log
	if err := cmd.Start(); err != nil {
		return err
	}
	defer func() {
		cmd.Process.Signal(syscall.SIGTERM)
		done := make(chan struct{})
		go func() { cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	}()

	if err := waitHTTP(port, 15*time.Second); err != nil {
		os.Stderr.WriteString(log.String())
		return fmt.Errorf("server never became ready: %w", err)
	}
	time.Sleep(2 * time.Second)

	rss := make([]float64, 0, *samples)
	for i := 0; i < *samples; i++ {
		kb, err := rssKB(cmd.Process.Pid)
		if err != nil {
			return err
		}
		rss = append(rss, float64(kb)/1024)
		if i < *samples-1 {
			time.Sleep(1 * time.Second)
		}
	}
	var sum, max float64
	for _, v := range rss {
		sum += v
		if v > max {
			max = v
		}
	}
	mean := sum / float64(len(rss))

	res := idleResult{
		Meta: newMeta(), Samples: rss, MeanMB: mean, MaxMB: max,
		TargetMB: idleTargetMB, Pass: max < idleTargetMB,
	}
	res.Verdicts = []verdict{{
		Name: "idle_rss_max", Value: fmt.Sprintf("%.1f MB", max),
		Target: fmt.Sprintf("< %.0f MB", idleTargetMB), Pass: max < idleTargetMB,
	}}

	rows := [][4]string{
		{"samples", strconv.Itoa(len(rss)), "-", "-"},
		{"rss_mean", fmt.Sprintf("%.1f MB", mean), fmt.Sprintf("< %.0f MB", idleTargetMB), mark(mean < idleTargetMB)},
		{"rss_max", fmt.Sprintf("%.1f MB", max), fmt.Sprintf("< %.0f MB", idleTargetMB), mark(max < idleTargetMB)},
	}
	printTable("bench-idle (real server binary, empty data dir)", rows, res.Verdicts)
	_, werr := writeResult("idle", res)
	return werr
}

func waitHTTP(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			resp, err := http.Get(fmt.Sprintf("http://%s/healthcheck", addr))
			if err == nil {
				resp.Body.Close()
			}
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("timeout after %s", timeout)
}
