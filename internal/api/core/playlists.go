package core

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

// MountPlaylists registers the playlist endpoints. Add this one line to
// API.Mount in core.go:
//
//	a.MountPlaylists(r)
func (a *API) MountPlaylists(r *neutron.Router) {
	r.HandleFunc("GET /playlists", a.playlistsList)
	r.HandleFunc("POST /playlists", a.playlistCreate)
	r.HandleFunc("GET /playlists/{id}", a.playlistGet)
	r.HandleFunc("PATCH /playlists/{id}", a.playlistPatch)
	r.HandleFunc("DELETE /playlists/{id}", a.playlistDelete)
	r.HandleFunc("POST /playlists/{id}/items", a.playlistItemAdd)
	r.HandleFunc("DELETE /playlists/{id}/items/{editionId}", a.playlistItemRemove)
	r.HandleFunc("POST /playlists/{id}/reorder", a.playlistItemReorder)
}

// ownedPlaylist loads the playlist and enforces access: owner or admin.
// Non-owner access answers 404, same as unknown ids (no existence leak).
// nil means it already responded.
func (a *API) ownedPlaylist(w http.ResponseWriter, r *http.Request, id int64) *store.Playlist {
	p, err := a.DB.Playlist(id)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "playlist not found"})
		return nil
	}
	u, ok := a.currentUser(r)
	if !ok || (!u.IsAdmin && p.UserID != u.ID) {
		writeJSON(w, 404, map[string]string{"error": "playlist not found"})
		return nil
	}
	return p
}

func playlistJSON(p *store.Playlist) map[string]any {
	return map[string]any{
		"id": p.ID, "name": p.Name, "ownerId": p.UserID, "owner": p.Owner,
		"songCount": p.SongCount, "durationSecs": p.DurationSecs,
		"createdAtMs": p.CreatedAt, "updatedAtMs": p.UpdatedAt,
	}
}

func playlistItemRow(it store.PlaylistItem) map[string]any {
	return map[string]any{
		"editionId": it.EditionID, "position": it.Position, "title": it.Title,
		"format": it.Format, "durationSecs": it.DurationSecs,
		"workId": it.WorkID, "workTitle": it.WorkTitle, "workAuthor": it.WorkAuthor,
		"hasCover": it.CoverPath != nil && *it.CoverPath != "",
	}
}

func (a *API) playlistsList(w http.ResponseWriter, r *http.Request) {
	u, ok := a.currentUser(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	uid := u.ID
	if u.IsAdmin {
		uid = 0
	}
	list, err := a.DB.ListPlaylists(uid)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	out := make([]map[string]any, 0, len(list))
	for i := range list {
		out = append(out, playlistJSON(&list[i]))
	}
	writeJSON(w, 200, out)
}

func (a *API) playlistCreate(w http.ResponseWriter, r *http.Request) {
	u, ok := a.currentUser(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var body struct {
		Name       string  `json:"name"`
		EditionIDs []int64 `json:"editionIds"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeJSON(w, 400, map[string]string{"error": "name required"})
		return
	}
	id, err := a.DB.CreatePlaylist(u.ID, body.Name)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	for _, eid := range body.EditionIDs {
		if _, err := a.DB.AddPlaylistItem(id, eid); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeJSON(w, 404, map[string]string{"error": "edition not found"})
				return
			}
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
	}
	writeJSON(w, 201, map[string]any{"id": id})
}

func (a *API) playlistGet(w http.ResponseWriter, r *http.Request) {
	p := a.ownedPlaylist(w, r, auth.Atoi64(r.PathValue("id")))
	if p == nil {
		return
	}
	items, err := a.DB.PlaylistItems(p.ID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	out := playlistJSON(p)
	rows := make([]map[string]any, 0, len(items))
	for _, it := range items {
		rows = append(rows, playlistItemRow(it))
	}
	out["items"] = rows
	writeJSON(w, 200, out)
}

func (a *API) playlistPatch(w http.ResponseWriter, r *http.Request) {
	p := a.ownedPlaylist(w, r, auth.Atoi64(r.PathValue("id")))
	if p == nil {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeJSON(w, 400, map[string]string{"error": "name required"})
		return
	}
	if err := a.DB.RenamePlaylist(p.ID, body.Name); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *API) playlistDelete(w http.ResponseWriter, r *http.Request) {
	p := a.ownedPlaylist(w, r, auth.Atoi64(r.PathValue("id")))
	if p == nil {
		return
	}
	if err := a.DB.DeletePlaylist(p.ID); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *API) playlistItemAdd(w http.ResponseWriter, r *http.Request) {
	p := a.ownedPlaylist(w, r, auth.Atoi64(r.PathValue("id")))
	if p == nil {
		return
	}
	var body struct {
		EditionID int64 `json:"editionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.EditionID <= 0 {
		writeJSON(w, 400, map[string]string{"error": "editionId required"})
		return
	}
	added, err := a.DB.AddPlaylistItem(p.ID, body.EditionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 404, map[string]string{"error": "edition not found"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 201, map[string]any{"ok": true, "added": added})
}

func (a *API) playlistItemRemove(w http.ResponseWriter, r *http.Request) {
	p := a.ownedPlaylist(w, r, auth.Atoi64(r.PathValue("id")))
	if p == nil {
		return
	}
	eid := auth.Atoi64(r.PathValue("editionId"))
	if eid <= 0 {
		writeJSON(w, 400, map[string]string{"error": "editionId required"})
		return
	}
	if err := a.DB.RemovePlaylistItem(p.ID, eid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 404, map[string]string{"error": "item not found"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// playlistItemReorder moves an item to a 1-based position (matching the
// positions returned by GET /playlists/{id}).
func (a *API) playlistItemReorder(w http.ResponseWriter, r *http.Request) {
	p := a.ownedPlaylist(w, r, auth.Atoi64(r.PathValue("id")))
	if p == nil {
		return
	}
	var body struct {
		EditionID int64 `json:"editionId"`
		Position  int   `json:"position"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.EditionID <= 0 || body.Position < 1 {
		writeJSON(w, 400, map[string]string{"error": "editionId and position (1-based) required"})
		return
	}
	if err := a.DB.ReorderPlaylistItem(p.ID, body.EditionID, body.Position); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 404, map[string]string{"error": "item not found"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
