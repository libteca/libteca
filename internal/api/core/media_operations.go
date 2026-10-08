package core

import (
	"encoding/json"
	"errors"
	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
	"io"
	"net/http"
)

func (a *API) mountMediaOperations(r *neutron.Router) {
	r.HandleFunc("GET /media-progress/{kind}/{id}", a.mediaProgressSnapshot)
	r.HandleFunc("POST /media-operations", a.mediaOperationApply)
	r.HandleFunc("GET /media-operations/{operationId}", a.mediaOperationReceipt)
}
func (a *API) mediaProgressSnapshot(w http.ResponseWriter, r *http.Request) {
	s, e := a.DB.MediaSnapshot(auth.UserID(r), r.PathValue("kind"), auth.Atoi64(r.PathValue("id")))
	if e != nil {
		mediaOperationError(w, e)
		return
	}
	writeJSON(w, 200, s)
}
func (a *API) mediaOperationApply(w http.ResponseWriter, r *http.Request) {
	var o store.MediaOperation
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	dec.DisallowUnknownFields()
	if dec.Decode(&o) != nil || dec.Decode(new(any)) != io.EOF {
		mediaOperationError(w, store.ErrMediaEnvelope)
		return
	}
	result, e := a.DB.ApplyMediaOperation(auth.UserID(r), o)
	if e != nil {
		mediaOperationError(w, e)
		return
	}
	writeJSON(w, 200, result)
}
func (a *API) mediaOperationReceipt(w http.ResponseWriter, r *http.Request) {
	result, e := a.DB.MediaReceipt(auth.UserID(r), r.PathValue("operationId"))
	if e != nil {
		mediaOperationError(w, e)
		return
	}
	writeJSON(w, 200, result)
}
func mediaOperationError(w http.ResponseWriter, e error) {
	status := 500
	message := "media progress unavailable"
	if errors.Is(e, store.ErrMediaConflict) {
		status = 409
		message = "media progress changed; review pending progress"
	}
	if errors.Is(e, store.ErrNotFound) {
		status = 404
		message = "media target or receipt not found"
	}
	if errors.Is(e, store.ErrMediaEnvelope) {
		status = 400
		message = "invalid media operation"
	}
	writeJSON(w, status, map[string]any{"error": message})
}

func attachMediaResume(m map[string]any, resume *store.MediaResume) {
	m["resumeConflict"] = resume.Conflict
	if resume.FileID != nil {
		m["resumeFileId"] = *resume.FileID
		m["resumeFileOffset"] = resume.FileOffset
	}
	if resume.SavedGeneration != nil {
		m["progressGeneration"] = *resume.SavedGeneration
	}
	if resume.Position != nil {
		m["position"] = *resume.Position
	}
	if resume.Conflict {
		m["position"] = 0
		m["isFinished"] = false
	}
}
