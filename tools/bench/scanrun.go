package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/libteca/libteca/internal/scan"
	"github.com/libteca/libteca/internal/store"
)

const (
	coldScanTargetSecs = 600.0
	warmScanTargetSecs = 30.0
)

type scanResult struct {
	Meta meta `json:"meta"`

	Files       int     `json:"files"`
	SeedDir     string  `json:"seed_dir"`
	ColdSecs    float64 `json:"cold_secs"`
	ColdPerSec  float64 `json:"cold_files_per_sec"`
	WarmSecs    float64 `json:"warm_secs"`
	WarmPerSec  float64 `json:"warm_files_per_sec"`
	WarmProbes  int     `json:"warm_probes"`
	ColdAdded   int     `json:"cold_files_added"`
	WarmUpdated int     `json:"warm_files_updated"`

	ColdTargetSecs float64   `json:"cold_target_secs"`
	WarmTargetSecs float64   `json:"warm_target_secs"`
	Verdicts       []verdict `json:"verdicts"`
}

func runScan(args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	count := fs.Int("count", 10000, "audio files to scan")
	quick := fs.Bool("quick", false, "scan 2000 files instead of --count")
	seedDir := fs.String("seed-dir", "", "seed cache dir (default <repo>/data/bench-seed/audio)")
	reseed := fs.Bool("reseed", false, "regenerate missing seed files even if some exist")
	fs.Parse(args)

	if *quick {
		*count = 2000
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	if *seedDir == "" {
		*seedDir = filepath.Join(root, "data", "bench-seed", "audio")
	}
	have := countExt(*seedDir, ".mp3")
	if *reseed || have < *count {
		fmt.Printf("seeding %d audio files into %s (have %d)...\n", *count, *seedDir, have)
		if err := seedExec(root, "audio", *seedDir, *count); err != nil {
			return err
		}
	}

	tmp, err := os.MkdirTemp("", "libteca-bench-scan")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	db, err := store.Open(filepath.Join(tmp, "libteca.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	libID, err := db.AddLibrary("bench", "audiobooks", *seedDir)
	if err != nil {
		return err
	}
	lib := &store.Library{ID: libID, Name: "bench", Type: "audiobooks", Path: *seedDir}
	covers := filepath.Join(tmp, "covers")

	cold := scanRun(db, lib, covers)
	warm := scanRun(db, lib, covers)

	res := scanResult{
		Meta: newMeta(), Files: warm.seen, SeedDir: *seedDir,
		ColdSecs: cold.secs, ColdAdded: cold.added,
		WarmSecs: warm.secs, WarmProbes: warm.probes, WarmUpdated: warm.updated,
		ColdTargetSecs: coldScanTargetSecs, WarmTargetSecs: warmScanTargetSecs,
	}
	res.ColdPerSec = float64(res.Files) / cold.secs
	res.WarmPerSec = float64(res.Files) / warm.secs
	res.Verdicts = []verdict{
		{
			Name: "cold_scan", Value: fmt.Sprintf("%.1fs (%.0f files/s)", cold.secs, res.ColdPerSec),
			Target: fmt.Sprintf("< %.0fs", coldScanTargetSecs), Pass: cold.secs < coldScanTargetSecs,
		},
		{
			Name: "warm_rescan", Value: fmt.Sprintf("%.1fs, %d probes", warm.secs, warm.probes),
			Target: "near-instant (few probes)", Pass: warm.probes == 0 || warm.secs < 1.0,
		},
	}

	rows := [][4]string{
		{"files", fmt.Sprintf("%d", res.Files), "-", "-"},
		{"cold_scan", fmt.Sprintf("%.2fs", cold.secs), fmt.Sprintf("< %.0fs", coldScanTargetSecs), mark(cold.secs < coldScanTargetSecs)},
		{"cold_rate", fmt.Sprintf("%.0f files/s", res.ColdPerSec), "-", "-"},
		{"cold_added", fmt.Sprintf("%d", cold.added), "-", "-"},
		{"warm_rescan", fmt.Sprintf("%.2fs", warm.secs), "near-instant", mark(warm.secs < warmScanTargetSecs)},
		{"warm_probes", fmt.Sprintf("%d", warm.probes), "0 (skip unprobed)", mark(warm.probes == 0)},
		{"warm_updated", fmt.Sprintf("%d", warm.updated), "-", "-"},
	}
	printTable("bench-scan (in-process scan.Library, fresh store)", rows, res.Verdicts)
	if warm.probes > 0 {
		fmt.Printf("  note: warm re-scan re-probed %d files (mtime/size skip not in scan path yet; SPEC 3.7)\n", warm.probes)
	}
	_, werr := writeResult("scan", res)
	return werr
}

type scanStats struct {
	secs    float64
	probes  int
	added   int
	updated int
	seen    int
}

func scanRun(db *store.DB, lib *store.Library, covers string) scanStats {
	var final scan.Progress
	start := time.Now()
	_, err := scan.Library(db, lib, covers, func(p scan.Progress) { final = p })
	if err != nil {
		fmt.Fprintf(os.Stderr, "bench: scan error: %v\n", err)
	}
	return scanStats{
		secs:    time.Since(start).Seconds(),
		probes:  final.FilesProbed,
		added:   final.FilesAdded,
		updated: final.FilesUpdated,
		seen:    final.FilesSeen,
	}
}
