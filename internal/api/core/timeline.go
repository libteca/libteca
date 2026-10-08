package core

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
)

func (a *API) editionTimeline(w http.ResponseWriter, r *http.Request) {
	timeline, err := a.DB.EditionTimeline(auth.Atoi64(r.PathValue("id")))
	if errors.Is(err, store.ErrTimelineUnavailable) {
		writeJSON(w, 409, map[string]string{"error": "timeline_unavailable"})
		return
	}
	if err != nil {
		writeEditionLookupError(w, err)
		return
	}
	writeJSON(w, 200, timeline)
}

func (a *API) selectedPlayback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Version    int      `json:"contractVersion"`
		RequestID  string   `json:"requestId"`
		Generation string   `json:"generation"`
		FileID     int64    `json:"fileId"`
		Offset     *float64 `json:"fileOffsetSecs"`
		Mode       string   `json:"mode"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || body.Version != 1 || body.RequestID == "" || len(body.RequestID) > 128 || body.Generation == "" || body.FileID <= 0 || body.Offset == nil || (body.Mode != "auto" && body.Mode != "hls") {
		writeJSON(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	timeline, err := a.DB.EditionTimeline(auth.Atoi64(r.PathValue("id")))
	if errors.Is(err, store.ErrTimelineUnavailable) {
		writeJSON(w, 409, map[string]string{"error": "timeline_unavailable"})
		return
	}
	if err != nil {
		writeEditionLookupError(w, err)
		return
	}
	if timeline.Generation != body.Generation {
		writeJSON(w, 409, map[string]string{"error": "generation_mismatch"})
		return
	}
	file, position, err := timeline.Select(body.FileID, *body.Offset)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "file_not_in_edition"})
		return
	}
	if err != nil {
		writeJSON(w, 422, map[string]string{"error": err.Error()})
		return
	}
	ed := &store.EditionView{Edition: timeline.Edition, Files: []store.FileRec{file.File}}
	if !streamable(ed) {
		writeJSON(w, 415, map[string]string{"error": "edition is not streamable audio/video"})
		return
	}
	response := map[string]any{"contractVersion": 1, "requestId": body.RequestID, "editionId": timeline.EditionID, "generation": timeline.Generation, "fileId": body.FileID, "fileOffsetSecs": *body.Offset, "editionPositionSecs": position, "mediaTimeOrigin": "file-start", "mediaStartSecs": 0, "mode": "direct"}
	if body.Mode == "hls" || !browserPlayable(ed) {
		if a.TC == nil {
			writeJSON(w, 503, map[string]string{"error": "transcode_unavailable"})
			return
		}
		sid, err := a.issueSelectedWebTicket(auth.UserID(r), timeline.EditionID, body.FileID, timeline.Generation)
		if err != nil {
			writeJSON(w, 503, map[string]string{"error": "session creation failed"})
			return
		}
		response["mode"] = "hls"
		response["sessionId"] = sid
	}
	writeJSON(w, 201, response)
}
