package server

import (
	"log/slog"
	"net/http"

	"github.com/libteca/libteca/internal/api/abs"
	"github.com/libteca/libteca/internal/api/core"
	"github.com/libteca/libteca/internal/api/jellyfin"
	"github.com/libteca/libteca/internal/api/opds"
	"github.com/libteca/libteca/internal/api/subsonic"
	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/libteca/libteca/internal/transcode"
	"github.com/neutron-build/neutron/go/neutron"
)

type Server struct {
	DB      *store.DB
	Dir     string
	HWAccel string
	Core    *core.API
}

// New builds the server with its core API instance. The same instance is
// mounted by Handler and must be handed to watch.New so HTTP-triggered and
// watch-triggered scans share one in-process run guard.
func New(db *store.DB, dataDir string) *Server {
	return &Server{DB: db, Dir: dataDir, Core: core.New(db, dataDir)}
}

func (s *Server) Handler() http.Handler {
	app := neutron.New(
		neutron.WithOpenAPIInfo("libteca", "0.1.0"),
		neutron.WithLogger(slog.Default()),
		neutron.WithMiddleware(neutron.Recover()),
	)
	r := app.Router()

	authMW := auth.Middleware(s.DB)

	if s.Core == nil {
		s.Core = core.New(s.DB, s.Dir)
	}
	tm := transcode.New(s.Dir)
	if s.HWAccel != "" {
		if err := tm.SetHwAccel(s.HWAccel); err != nil {
			slog.Warn("libteca: --hwaccel ignored", "value", s.HWAccel, "err", err)
		}
	}

	c := s.Core
	c.TC = tm
	c.MountPublic(r.Group("/api/core"))
	c.Mount(r.Group("/api/core", authMW))

	jf := jellyfin.New(s.DB, s.Dir, tm)
	jf.Mount(r)

	a := abs.New(s.DB, s.Dir)
	r.HandleFunc("GET /s/{sid}/t/{index}", a.SessionTrack)
	r.HandleFunc("POST /login", a.Login)
	r.HandleFunc("GET /ping", a.Ping)
	r.HandleFunc("GET /healthcheck", a.Healthcheck)
	ag := r.Group("/api", authMW)
	a.Mount(ag)

	sub := subsonic.New(s.DB, s.Dir)
	sub.Mount(r)

	od := opds.New(s.DB, s.Dir)
	od.Mount(r)

	r.StaticFS("/", webFS())

	return app.Handler()
}
