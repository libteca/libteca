package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/libteca/libteca/internal/transcode"
)

const (
	prebufferTargetSecs = 2.0
	prebufferSegments   = 2
	ladderSourceSecs    = 30
)

type rungResult struct {
	Sessions int     `json:"sessions"`
	MeanTTFS float64 `json:"mean_ttf_secs"`
	MaxTTFS  float64 `json:"max_ttf_secs"`
}

type transcodeResult struct {
	Meta meta `json:"meta"`

	Source   string       `json:"source"`
	Runs     int          `json:"runs"`
	LatencyS []float64    `json:"prebuffer_secs"`
	TTFS     []float64    `json:"first_segment_secs"`
	TTFSP50  float64      `json:"first_segment_p50_secs"`
	TTFSP95  float64      `json:"first_segment_p95_secs"`
	P50      float64      `json:"p50_secs"`
	P95      float64      `json:"p95_secs"`
	TargetS  float64      `json:"target_secs"`
	Ladder   []rungResult `json:"concurrency_ladder"`

	Verdicts []verdict `json:"verdicts"`
}

func runTranscode(args []string) error {
	fs := flag.NewFlagSet("transcode", flag.ExitOnError)
	runs := fs.Int("runs", 20, "sequential prebuffer runs")
	source := fs.String("source", "", "source mp4 (default: seeded 30s testsrc under <repo>/data/bench-seed/video-long)")
	fs.Parse(args)

	root, err := repoRoot()
	if err != nil {
		return err
	}
	if *source == "" {
		dir := filepath.Join(root, "data", "bench-seed", "video-long")
		if countExt(dir, ".mp4") == 0 {
			fmt.Printf("seeding 30s test source into %s...\n", dir)
			if err := seedExec(root, "video", dir, 1, "--duration", fmt.Sprintf("%d", ladderSourceSecs)); err != nil {
				return err
			}
		}
		*source, err = findFirst(dir, ".mp4")
		if err != nil {
			return err
		}
	}
	if _, err := os.Stat(*source); err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "libteca-bench-tx")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	m := transcode.New(tmp)

	lat := make([]float64, 0, *runs)
	ttfs := make([]float64, 0, *runs)
	for i := 0; i < *runs; i++ {
		id := fmt.Sprintf("bench-p%02d", i)
		start := time.Now()
		s, err := m.Get(id, 1, *source, 0, func() (*os.File, error) { return os.Open(*source) })
		if err != nil {
			return err
		}
		s.Prebuffer(context.Background(), 1, 15*time.Second)
		ttfs = append(ttfs, time.Since(start).Seconds())
		ready := s.Prebuffer(context.Background(), prebufferSegments, 15*time.Second)
		lat = append(lat, time.Since(start).Seconds())
		m.Close(id)
		if ready < prebufferSegments {
			fmt.Printf("  warn: run %d prebuffered %d/%d segments\n", i, ready, prebufferSegments)
		}
	}
	p50, p95 := percentiles(lat)
	ttfsP50, ttfsP95 := percentiles(ttfs)

	res := transcodeResult{
		Meta: newMeta(), Source: filepath.Base(*source), Runs: len(lat),
		LatencyS: lat, TTFS: ttfs, TTFSP50: ttfsP50, TTFSP95: ttfsP95,
		P50: p50, P95: p95, TargetS: prebufferTargetSecs,
	}
	res.Verdicts = []verdict{
		{
			Name: "first_segment_p95", Value: fmt.Sprintf("%.2fs", ttfsP95),
			Target: fmt.Sprintf("< %.1fs", prebufferTargetSecs), Pass: ttfsP95 < prebufferTargetSecs,
		},
		{
			Name: "prebuffer2_p95", Value: fmt.Sprintf("%.2fs", p95),
			Target: fmt.Sprintf("< %.1fs (informational)", prebufferTargetSecs), Pass: p95 < prebufferTargetSecs,
		},
	}

	ladder := []int{1, 2, 4, 8}
	for _, n := range ladder {
		r := ladderRung(m, *source, n)
		res.Ladder = append(res.Ladder, r)
	}

	rows := [][4]string{
		{"source", filepath.Base(*source), "-", "-"},
		{"runs", fmt.Sprintf("%d", len(lat)), "-", "-"},
		{"first_segment_p50", fmt.Sprintf("%.2fs", ttfsP50), "-", "-"},
		{"first_segment_p95", fmt.Sprintf("%.2fs", ttfsP95), fmt.Sprintf("< %.1fs", prebufferTargetSecs), mark(ttfsP95 < prebufferTargetSecs)},
		{"prebuffer2_p50", fmt.Sprintf("%.2fs", p50), "-", "-"},
		{"prebuffer2_p95", fmt.Sprintf("%.2fs", p95), "informational", mark(p95 < prebufferTargetSecs)},
	}
	for _, r := range res.Ladder {
		rows = append(rows, [4]string{
			fmt.Sprintf("ladder_%ds_mean_ttf", r.Sessions),
			fmt.Sprintf("%.2fs", r.MeanTTFS), "-", "-",
		})
		rows = append(rows, [4]string{
			fmt.Sprintf("ladder_%ds_max_ttf", r.Sessions),
			fmt.Sprintf("%.2fs", r.MaxTTFS), "-", "-",
		})
	}
	printTable("bench-transcode (in-process sessions, Prebuffer 2 segments)", rows, res.Verdicts)
	_, werr := writeResult("transcode", res)
	return werr
}

func ladderRung(m *transcode.Manager, source string, n int) rungResult {
	var wg sync.WaitGroup
	ttf := make([]float64, n)
	start := time.Now()
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("bench-l%d-%d", n, i)
		s, err := m.Get(id, int64(1000+i), source, 0, func() (*os.File, error) { return os.Open(source) })
		if err != nil {
			fmt.Fprintf(os.Stderr, "bench: ladder %d: %v\n", n, err)
			continue
		}
		wg.Add(1)
		go func(s *transcode.Session, i int) {
			defer wg.Done()
			s.Prebuffer(context.Background(), 1, 20*time.Second)
			ttf[i] = time.Since(start).Seconds()
		}(s, i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		m.Close(fmt.Sprintf("bench-l%d-%d", n, i))
	}
	var sum, max float64
	for _, v := range ttf {
		sum += v
		if v > max {
			max = v
		}
	}
	return rungResult{Sessions: n, MeanTTFS: sum / float64(n), MaxTTFS: max}
}

func percentiles(v []float64) (p50, p95 float64) {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	idx := func(p float64) int {
		i := int(p * float64(len(s)))
		if i >= len(s) {
			i = len(s) - 1
		}
		return i
	}
	return s[idx(0.50)], s[idx(0.95)]
}
