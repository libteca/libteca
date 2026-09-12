package core

import (
	"errors"
	"net/http"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/podcast"
	"github.com/libteca/libteca/internal/store"
)

func (a *API) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	id := auth.Atoi64(r.PathValue("id"))
	lib, err := a.DB.Library(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "library not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	if lib.Type == "podcasts" && a.Podcasts != nil {
		// All-or-nothing: the per-subscription loop deleted everything up to
		// the first busy one and THEN reported 409, permanently destroying
		// half a library behind a "try again" response.
		if err := a.Podcasts.DeleteLibraryPodcasts(id); err != nil {
			if errors.Is(err, podcast.ErrRefreshBusy) {
				writeJSON(w, 409, map[string]string{"error": "a podcast refresh or download is in progress; nothing was deleted"})
				return
			}
			writeJSON(w, 500, map[string]string{"error": "internal error"})
			return
		}
	}
	err = a.DB.DeleteLibrary(id)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "library not found"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
