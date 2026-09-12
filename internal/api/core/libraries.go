package core

import (
	"errors"
	"net/http"

	"github.com/libteca/libteca/internal/auth"
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
		pods, err := a.DB.Podcasts()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "internal error"})
			return
		}
		for i := range pods {
			if pods[i].LibraryID != id {
				continue
			}
			if err := a.Podcasts.DeletePodcast(pods[i].ID); err != nil {
				writeJSON(w, 409, map[string]string{"error": "podcast refresh or download in progress"})
				return
			}
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
