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
	"syscall"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/podcast"
	"github.com/libteca/libteca/internal/scan"
	"github.com/libteca/libteca/internal/server"
	"github.com/libteca/libteca/internal/store"
)

var version = "dev"

func main() {
	data := flag.String("data", "./data", "data directory")
	port := flag.Int("port", 8096, "listen port")
	initAdmin := flag.String("init-admin", "", "create admin as name:password")
	scanOnly := flag.Bool("scan", false, "scan all libraries then exit")
	hwaccel := flag.String("hwaccel", "", "video hwaccel: auto|none|videotoolbox|vaapi|nvenc|qsv (default: $LIBTECA_HWACCEL)")
	flag.Parse()

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

	if *initAdmin != "" {
		name, pass, ok := cut(*initAdmin, ':')
		if !ok {
			fatal(fmt.Errorf("--init-admin wants name:password"))
		}
		if err := auth.InitAdmin(db, name, pass); err != nil {
			fatal(err)
		}
		fmt.Printf("admin %q created\n", name)
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go podcasts.Run(ctx)

	h := &http.Server{
		Addr:              fmt.Sprintf(":%d", *port),
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	go func() {
		<-ctx.Done()
		h.Shutdown(context.Background())
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

func cut(s string, sep byte) (a, b string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return "", "", false
}
