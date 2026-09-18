package core

import (
	"mime"
	"net/http"
	"path/filepath"

	"github.com/libteca/libteca/internal/auth"
	"github.com/neutron-build/neutron/go/neutron"
)

// MountReading registers the reading endpoints. Add this one line to
// API.Mount in core.go:
//
//	a.MountReading(r)
func (a *API) MountReading(r *neutron.Router) {
	r.HandleFunc("GET /editions/{id}/download", a.editionDownload)
}

var downloadContentTypes = map[string]string{
	"epub": "application/epub+zip",
	"cbz":  "application/zip",
	"cbr":  "application/vnd.comicbook-rar",
	"pdf":  "application/pdf",
}

func (a *API) editionDownload(w http.ResponseWriter, r *http.Request) {
	id := auth.Atoi64(r.PathValue("id"))
	f, format, err := a.DB.EditionFile(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "edition not found"})
		return
	}
	root, rerr := a.confinedRoot(f.EditionID)
	if rerr != nil {
		writeJSON(w, 404, map[string]string{"error": "edition not found"})
		return
	}
	// Path safety (PLAN §11): the served path comes only from the files
	ct := downloadContentTypes[format]
	if ct == "" {
		ct = mime.TypeByExtension(filepath.Ext(f.Path))
	}
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	serveConfined(w, r, root, f.Path)
}
