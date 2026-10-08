package core

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
)

func (a *API) gamePlaytime(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string   `json:"sessionId"`
		Elapsed   *float64 `json:"elapsed"`
		Epoch     *int64   `json:"resetGeneration"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.Elapsed == nil || body.Epoch == nil {
		writeJSON(w, 400, map[string]string{"error": "sessionId, elapsed and resetGeneration are required"})
		return
	}
	if decoder.Decode(new(any)) != io.EOF {
		writeJSON(w, 400, map[string]string{"error": "invalid playtime report"})
		return
	}
	if strings.TrimSpace(body.SessionID) != body.SessionID || body.SessionID == "" || len(body.SessionID) > 128 || *body.Epoch < 0 || store.ValidPlaytime(*body.Elapsed) != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid playtime report"})
		return
	}
	result, err := a.DB.ReportGamePlaytime(auth.UserID(r), auth.Atoi64(r.PathValue("editionId")), *body.Epoch, body.SessionID, *body.Elapsed)
	if errors.Is(err, store.ErrPlaytimeConflict) {
		writeJSON(w, 409, map[string]string{"error": "playtime was reset"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "edition not found"})
		return
	}
	if errors.Is(err, store.ErrPlaytimeInvalid) {
		writeJSON(w, 400, map[string]string{"error": "invalid game playtime report"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "internal error"})
		return
	}
	writeJSON(w, 200, map[string]any{"position": result.EditionPositionSecs, "revision": result.Revision, "resetGeneration": result.ResetGeneration})
}
