package core

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

// MountLinking registers the work/edition linking endpoints. Add this one
// line to API.Mount in core.go:
//
//	a.MountLinking(r)
func (a *API) MountLinking(r *neutron.Router) {
	r.HandleFunc("POST /editions/{id}/move", a.editionMove)
	r.HandleFunc("POST /editions/{id}/split", a.editionSplit)
	r.HandleFunc("POST /works/{id}/merge", a.workMerge)
}

// editionMove: POST /api/core/editions/{id}/move
// {workId | newTitle, newAuthor?, libraryId?}. With workId the edition joins
// that work. With newTitle the target work is matched (or created) using the
// library/title/author unique index; libraryId defaults to the source work's
// library. Moving to a different library is allowed and applies to ALL
// editions of the target work (works own the library, editions do not).
func (a *API) editionMove(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	eid := auth.Atoi64(r.PathValue("id"))
	e, err := a.DB.EditionRow(eid)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "edition not found"})
		return
	}
	var body struct {
		WorkID    int64   `json:"workId"`
		NewTitle  string  `json:"newTitle"`
		NewAuthor *string `json:"newAuthor"`
		LibraryID int64   `json:"libraryId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	src, err := a.DB.WorkByID(e.WorkID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}

	targetID := body.WorkID
	targetLib := int64(0)
	created := false
	switch {
	case body.WorkID != 0:
		tw, err := a.DB.WorkByID(body.WorkID)
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "work not found"})
			return
		}
		targetLib = tw.LibraryID
	case strings.TrimSpace(body.NewTitle) != "":
		targetLib = src.LibraryID
		if body.LibraryID != 0 {
			if _, err := a.DB.Library(body.LibraryID); err != nil {
				writeJSON(w, 404, map[string]string{"error": "library not found"})
				return
			}
			targetLib = body.LibraryID
		}
		nw := &store.Work{LibraryID: targetLib, Title: strings.TrimSpace(body.NewTitle), Author: body.NewAuthor}
		id, err := a.DB.EnsureWorkInLibrary(nw)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "internal error"})
			return
		}
		targetID = id
		created = nw.Created
	default:
		writeJSON(w, 400, map[string]string{"error": "workId or newTitle required"})
		return
	}
	res, err := a.DB.MoveEditionToWork(eid, targetID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 404, map[string]string{"error": "edition not found"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	resp := map[string]any{
		"ok": true, "workId": res.TargetWorkID, "created": created,
		"sourceWorkId": res.SourceWorkID, "sourceDeleted": res.SourceDeleted,
	}
	if targetLib != src.LibraryID {
		resp["warning"] = "target work is in a different library; a work's library applies to all of its editions"
	}
	writeJSON(w, 200, resp)
}

// editionSplit: POST /api/core/editions/{id}/split {title?, author?} — moves
// the edition into its own work (title defaults to the edition title, author
// to the source work's author). If the source work is left empty it is
// deleted.
func (a *API) editionSplit(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	eid := auth.Atoi64(r.PathValue("id"))
	var body struct {
		Title  string  `json:"title"`
		Author *string `json:"author"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad request"})
		return
	}
	res, err := a.DB.SplitEditionToNewWork(eid, strings.TrimSpace(body.Title), body.Author)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 404, map[string]string{"error": "edition not found"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"ok": true, "workId": res.TargetWorkID, "created": res.Created,
		"sourceWorkId": res.SourceWorkID, "sourceDeleted": res.SourceDeleted,
	})
}

// workMerge: POST /api/core/works/{id}/merge {intoWorkId} (admin) — moves all
// editions of this work into the target and deletes this work. The target
// keeps its cover.
func (a *API) workMerge(w http.ResponseWriter, r *http.Request) {
	if !a.requireAdmin(w, r) {
		return
	}
	id := auth.Atoi64(r.PathValue("id"))
	var body struct {
		IntoWorkID int64 `json:"intoWorkId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.IntoWorkID == 0 {
		writeJSON(w, 400, map[string]string{"error": "intoWorkId required"})
		return
	}
	if id == body.IntoWorkID {
		writeJSON(w, 400, map[string]string{"error": "cannot merge a work into itself"})
		return
	}
	if err := a.DB.MergeWorks(id, body.IntoWorkID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 404, map[string]string{"error": "work not found"})
			return
		}
		if errors.Is(err, store.ErrSameWork) {
			writeJSON(w, 400, map[string]string{"error": "cannot merge a work into itself"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "intoWorkId": body.IntoWorkID})
}
