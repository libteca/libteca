package opds

import (
	"net/http"

	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-dev/neutron-go/neutron"
)

type API struct {
	DB  *store.DB
	Dir string
}

func New(db *store.DB, dir string) *API {
	return &API{DB: db, Dir: dir}
}

func (a *API) Mount(r *neutron.Router) {
	r.HandleFunc("GET /opds", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not implemented", http.StatusNotImplemented)
	})
}
