package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/meta"
	"github.com/libteca/libteca/internal/podcast"
	"github.com/libteca/libteca/internal/scan"
	"github.com/libteca/libteca/internal/server"
	"github.com/libteca/libteca/internal/store"
	"github.com/libteca/libteca/internal/watch"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "backup" {
		runBackup(os.Args[2:])
		return
	}

	data := flag.String("data", "./data", "data directory")
	port := flag.Int("port", 8096, "listen port")
	initAdmin := flag.String("init-admin", "", "create admin as name:password")
	scanOnly := flag.Bool("scan", false, "scan all libraries then exit")
	showVersion := flag.Bool("version", false, "print version and exit")
	watchEnabled := flag.Bool("watch", true, "watch libraries for changes and rescan (env LIBTECA_WATCH=false disables; LIBTECA_SWEEP=<seconds> sets the sweep interval, 0 disables)")
	hwaccel := flag.String("hwaccel", "", "video hwaccel: auto|none|videotoolbox|vaapi|nvenc|qsv (default: $LIBTECA_HWACCEL)")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	abs, err := filepath.Abs(*data)
	if err != nil {
		fatal(err)
	}
	for _, d := range []string{abs, filepath.Join(abs, "covers")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			fatal(err)
		}
	}

	db, err := store.Open(filepath.Join(abs, "libteca.db"))
	if err != nil {
		fatal(err)
	}
	defer db.Close()
	meta.SetKeyLookup(db.GetSetting)

	if *initAdmin != "" {
		name, pass, ok := cut(*initAdmin, ':')
		if !ok {
			fatal(fmt.Errorf("--init-admin wants name:password"))
		}
		if err := auth.InitAdmin(db, name, pass); err != nil {
			fatal(err)
		}
		fmt.Printf("admin %q ready\n", name)
	}

	if *scanOnly {
		n, err := scan.All(db, filepath.Join(abs, "covers"), func(p scan.Progress) {
			fmt.Printf("\rscan: %d seen, %d probed, %d added, %d updated   ", p.FilesSeen, p.FilesProbed, p.FilesAdded, p.FilesUpdated)
		})
		if err != nil {
			fatal(err)
		}
		fmt.Printf("\nscan complete: %d editions current\n", n)
		return
	}

	srv := server.New(db, abs)
	srv.HWAccel = *hwaccel

	podcasts := podcast.New(db, abs)
	srv.Core.Podcasts = podcasts
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go podcasts.Run(ctx)

	if v := os.Getenv("LIBTECA_WATCH"); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			*watchEnabled = parsed
		}
	}
	if *watchEnabled {
		sweep := 6 * time.Hour
		if v, err := strconv.Atoi(os.Getenv("LIBTECA_SWEEP")); err == nil && v >= 0 {
			if v == 0 {
				sweep = 0
			} else {
				sweep = time.Duration(v) * time.Second
			}
		}
		go watch.New(srv.Core, db, watch.Config{SweepEvery: sweep}).Run(ctx)
		fmt.Printf("libteca: watch enabled (debounce %s, sweep %s)\n", watch.DefaultDebounce, sweep)
	}

	h := &http.Server{
		Addr:              fmt.Sprintf(":%d", *port),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := h.Shutdown(shutdownCtx); err != nil {
			fmt.Fprintln(os.Stderr, "libteca: shutdown:", err)
		}
		srv.Close()
	}()
	fmt.Printf("libteca %s listening on :%d (data: %s)\n", version, *port, abs)
	if err := h.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "libteca:", err)
	os.Exit(1)
}

// runBackup implements `libteca backup [-data dir] [-keep n]`: a stop-free
// database snapshot (VACUUM INTO) into <data>/backups/libteca-<date>.db plus
// a copy of <data>/covers/, pruning to the newest n database backups.
func runBackup(args []string) {
	fs := flag.NewFlagSet("backup", flag.ExitOnError)
	data := fs.String("data", "./data", "data directory")
	keep := fs.Int("keep", 10, "database backups to keep")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: libteca backup [-data dir] [-keep n]")
		fmt.Fprintln(os.Stderr, "  snapshots the database (stop-free VACUUM INTO) and covers/ into <data>/backups/,")
		fmt.Fprintln(os.Stderr, "  keeping the newest n database backups (default 10).")
		fs.PrintDefaults()
	}
	fs.Parse(args)
	abs, err := filepath.Abs(*data)
	if err != nil {
		fatal(err)
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		fatal(err)
	}
	db, err := store.Open(filepath.Join(abs, "libteca.db"))
	if err != nil {
		fatal(err)
	}
	defer db.Close()
	path, err := db.Snapshot(filepath.Join(abs, "covers"), filepath.Join(abs, "backups"), *keep)
	if err != nil {
		fatal(err)
	}
	fmt.Println("backup written:", path)
}

func cut(s string, sep byte) (a, b string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}
