package server

import (
	"log/slog"
	"net/http"

	"github.com/libteca/libteca/internal/api/abs"
	"github.com/libteca/libteca/internal/api/core"
	"github.com/libteca/libteca/internal/api/jellyfin"
	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/libteca/libteca/internal/transcode"
	"github.com/neutron-dev/neutron-go/neutron"
)

type Server struct {
	DB  *store.DB
	Dir string
}

func New(db *store.DB, dataDir string) *Server {
	return &Server{DB: db, Dir: dataDir}
}

func (s *Server) Handler() http.Handler {
	app := neutron.New(
		neutron.WithOpenAPIInfo("libteca", "0.1.0"),
		neutron.WithLogger(slog.Default()),
		neutron.WithMiddleware(neutron.Recover()),
	)
	r := app.Router()

	authMW := auth.Middleware(s.DB)

	c := core.New(s.DB, s.Dir)
	c.MountPublic(r.Group("/api/core"))
	c.Mount(r.Group("/api/core", authMW))

	jf := jellyfin.New(s.DB, s.Dir, transcode.New(s.Dir))
	jf.Mount(r)

	a := abs.New(s.DB, s.Dir)
	r.HandleFunc("GET /s/{sid}/t/{index}", a.SessionTrack)
	r.HandleFunc("POST /login", a.Login)
	r.HandleFunc("GET /ping", a.Ping)
	r.HandleFunc("GET /healthcheck", a.Healthcheck)
	ag := r.Group("/api", authMW)
	a.Mount(ag)

	r.StaticFS("/", webFS())

	return app.Handler()
}
