package core

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/resourcebudget"
	"github.com/libteca/libteca/internal/store"
	"github.com/libteca/libteca/internal/transcode"
	"github.com/libteca/libteca/internal/trickplay"
	"github.com/neutron-build/neutron/go/neutron"
)

var (
	reHLSURI  = regexp.MustCompile(`^(seg\d+\.ts|index\.m3u8)$`)
	reHLSFile = regexp.MustCompile(`^seg\d+\.ts$|^index\.m3u8$`)
	reHLSSID  = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

func (a *API) MountHLS(r *neutron.Router) {
	r.HandleFunc("GET /editions/{id}/playback", a.editionPlayback)
	r.HandleFunc("GET /editions/{id}/timeline", a.editionTimeline)
	r.HandleFunc("POST /editions/{id}/playback-sessions", a.selectedPlayback)
	r.HandleFunc("GET /editions/{id}/thumbs", a.editionThumbs)
	r.HandleFunc("GET /editions/{id}/thumbs/{file}", a.editionThumbTile)
	r.HandleFunc("GET /hls/{sid}/{file}", a.hlsFile)
	r.HandleFunc("DELETE /hls/{sid}", a.hlsStop)
}

const thumbsWidth = 320

func (a *API) trickplayer() *trickplay.Generator {
	a.tpMu.Lock()
	defer a.tpMu.Unlock()
	if a.tp == nil {
		a.tp = trickplay.New(a.DataDir)
	}
	return a.tp
}

func (a *API) SetTrickplayGenerator(g *trickplay.Generator) {
	a.tpMu.Lock()
	defer a.tpMu.Unlock()
	a.tp = g
}

func (a *API) editionThumbSource(id int64) (*store.EditionView, error) {
	ed, err := a.DB.EditionByID(id)
	if err != nil {
		return nil, err
	}
	if len(ed.Files) == 0 {
		return nil, store.ErrNotFound
	}
	if ed.Files[0].VideoCodec == nil || *ed.Files[0].VideoCodec == "" {
		return nil, store.ErrNotFound
	}
	return ed, nil
}

func (a *API) selectedThumbSource(r *http.Request) (*store.EditionView, error) {
	id := auth.Atoi64(r.PathValue("id"))
	timeline, err := a.DB.EditionTimeline(id)
	if err != nil {
		return nil, err
	}
	if expected := r.URL.Query().Get("generation"); expected != "" && expected != timeline.Generation {
		return nil, store.ErrTimelineUnavailable
	}
	file := timeline.Files[0]
	if raw := r.URL.Query().Get("fileId"); raw != "" {
		selected, _, err := timeline.Select(auth.Atoi64(raw), 0)
		if err != nil {
			return nil, err
		}
		file = *selected
	}
	if file.File.VideoCodec == nil || *file.File.VideoCodec == "" {
		return nil, store.ErrNotFound
	}
	return &store.EditionView{Edition: timeline.Edition, Generation: timeline.Generation, Files: []store.FileRec{file.File}}, nil
}

func writeEditionLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrTimelineUnavailable) {
		writeJSON(w, 409, map[string]string{"error": "timeline_unavailable"})
		return
	}
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	writeJSON(w, 500, map[string]string{"error": "internal error"})
}

func (a *API) editionThumbs(w http.ResponseWriter, r *http.Request) {
	ed, err := a.selectedThumbSource(r)
	if err != nil {
		writeEditionLookupError(w, err)
		return
	}
	generation := ed.Generation
	itemID := "f" + strconv.FormatInt(ed.Files[0].ID, 10) + "-" + strings.ReplaceAll(generation, ":", "-")
	open, oerr := a.openEditionFile(ed)
	if oerr != nil {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	m, err := a.trickplayer().Manifest(r.Context(), itemID, open, thumbsWidth)
	if errors.Is(err, trickplay.ErrNoFFmpeg) {
		w.Header().Set("Retry-After", "120")
		writeJSON(w, 503, map[string]string{"error": "ffmpeg unavailable"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "thumbs unavailable"})
		return
	}
	writeJSON(w, 200, m)
}

func (a *API) editionThumbTile(w http.ResponseWriter, r *http.Request) {
	ed, err := a.selectedThumbSource(r)
	if err != nil {
		writeEditionLookupError(w, err)
		return
	}
	index, ok := trickplay.ParseTileName(r.PathValue("file"))
	if !ok {
		writeJSON(w, 400, map[string]string{"error": "bad tile"})
		return
	}
	generation := ed.Generation
	itemID := "f" + strconv.FormatInt(ed.Files[0].ID, 10) + "-" + strings.ReplaceAll(generation, ":", "-")
	open, oerr := a.openEditionFile(ed)
	if oerr != nil {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	path, err := a.trickplayer().Tile(r.Context(), itemID, open, thumbsWidth, index)
	if errors.Is(err, trickplay.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	if errors.Is(err, trickplay.ErrNoFFmpeg) {
		w.Header().Set("Retry-After", "120")
		writeJSON(w, 503, map[string]string{"error": "ffmpeg unavailable"})
		return
	}
	if errors.Is(err, trickplay.ErrBusy) || errors.Is(err, resourcebudget.ErrLimit) {
		w.Header().Set("Retry-After", "10")
		writeJSON(w, 503, map[string]string{"error": "trickplay capacity exhausted"})
		return
	}
	if errors.Is(err, trickplay.ErrBadWidth) || errors.Is(err, trickplay.ErrBadItemID) {
		writeJSON(w, 400, map[string]string{"error": "bad trickplay request"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "tile generation failed"})
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400")
	serveFile(w, r, path)
}

func webSessionID(editionID int64) (string, error) {
	return transcode.NewSessionID("web-", editionID)
}

type webTicket struct {
	userID     int64
	editionID  int64
	fileID     int64
	generation string
	expires    time.Time
}

const (
	webTicketTTL  = 10 * time.Minute
	webTicketsMax = 256
)

func (a *API) issueWebTicket(userID, editionID int64) (string, error) {
	return a.issueSelectedWebTicket(userID, editionID, 0, "")
}

func (a *API) issueSelectedWebTicket(userID, editionID, fileID int64, generation string) (string, error) {
	sid, err := webSessionID(editionID)
	if err != nil {
		return "", err
	}
	a.ticketMu.Lock()
	defer a.ticketMu.Unlock()
	if a.tickets == nil {
		a.tickets = make(map[string]webTicket)
	}
	now := time.Now()
	for key, ticket := range a.tickets {
		if !now.Before(ticket.expires) {
			delete(a.tickets, key)
		}
	}
	if len(a.tickets) >= webTicketsMax {
		return "", fmt.Errorf("playback ticket capacity exhausted")
	}
	a.tickets[sid] = webTicket{userID: userID, editionID: editionID, fileID: fileID, generation: generation, expires: now.Add(webTicketTTL)}
	return sid, nil
}

func (a *API) checkWebTicket(sid string, userID int64, remove bool) (int64, bool) {
	a.ticketMu.Lock()
	defer a.ticketMu.Unlock()
	ticket, ok := a.tickets[sid]
	if !ok || ticket.userID != userID {
		return 0, false
	}
	if !time.Now().Before(ticket.expires) {
		delete(a.tickets, sid)
		return 0, false
	}
	if remove {
		delete(a.tickets, sid)
	} else {
		ticket.expires = time.Now().Add(webTicketTTL)
		a.tickets[sid] = ticket
	}
	return ticket.editionID, true
}

func browserPlayable(ed *store.EditionView) bool {
	if len(ed.Files) == 0 {
		return false
	}
	f := ed.Files[0]
	vcodec := ""
	container := ""
	if f.VideoCodec != nil {
		vcodec = *f.VideoCodec
	}
	if f.Container != nil {
		container = *f.Container
	}
	acodec := ""
	if f.Codec != nil {
		acodec = *f.Codec
	}
	audioOK := acodec == "" || acodec == "aac" || acodec == "mp3" || acodec == "flac" || acodec == "opus" || acodec == "vorbis"
	first := strings.Split(container, ",")[0]
	containerOK := first == "mp4" || first == "mov" || first == "m4v" ||
		(strings.Contains(container, "webm") && (vcodec == "vp9" || vcodec == "vp8" || vcodec == "av1"))
	if vcodec == "" {
		return acodec != "" && audioOK
	}
	if vcodec == "vp8" {
		return strings.Contains(container, "webm") && audioOK
	}
	return (vcodec == "h264" || vcodec == "vp9" || vcodec == "av1") && audioOK && containerOK
}

func streamable(ed *store.EditionView) bool {
	if len(ed.Files) == 0 || strings.HasPrefix(ed.Format, "game-") {
		return false
	}
	f := ed.Files[0]
	hasVideo := f.VideoCodec != nil && *f.VideoCodec != ""
	hasAudio := f.Codec != nil && *f.Codec != ""
	return hasVideo || hasAudio
}

func (a *API) editionPlayback(w http.ResponseWriter, r *http.Request) {
	id := auth.Atoi64(r.PathValue("id"))
	ed, err := a.DB.EditionByID(id)
	if err != nil {
		writeEditionLookupError(w, err)
		return
	}
	if len(ed.Files) == 0 {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	fileID := ed.Files[0].ID
	if browserPlayable(ed) {
		writeJSON(w, 200, map[string]any{"mode": "direct", "fileId": fileID})
		return
	}
	if !streamable(ed) {
		writeJSON(w, 415, map[string]string{"error": "edition is not streamable audio/video"})
		return
	}
	if a.TC == nil {
		writeJSON(w, 503, map[string]string{"error": "transcode unavailable"})
		return
	}
	sid, err := a.issueWebTicket(auth.UserID(r), ed.ID)
	if err != nil {
		w.Header().Set("Retry-After", "5")
		writeJSON(w, 503, map[string]string{"error": "session creation failed"})
		return
	}
	writeJSON(w, 200, map[string]any{
		"mode": "hls", "fileId": fileID, "sessionId": sid,
	})
}

func rewriteHLSPlaylist(playlist []byte, sid, token string) []byte {
	prefix := "/api/core/hls/" + sid + "/"
	q := ""
	if token != "" {
		q = "?token=" + url.QueryEscape(token)
	}
	lines := strings.Split(string(playlist), "\n")
	for i, line := range lines {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		file, _, _ := strings.Cut(s, "?")
		if reHLSURI.MatchString(file) {
			lines[i] = prefix + file + q
		}
	}
	return []byte(strings.Join(lines, "\n"))
}

func (a *API) hlsStop(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	if !reHLSSID.MatchString(sid) || !strings.HasPrefix(sid, "web-") {
		writeJSON(w, 400, map[string]string{"error": "bad session id"})
		return
	}
	if _, ok := a.checkWebTicket(sid, auth.UserID(r), true); !ok {
		writeJSON(w, 404, map[string]string{"error": "session not found"})
		return
	}
	if a.TC != nil {
		a.TC.Close(sid)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (a *API) hlsFile(w http.ResponseWriter, r *http.Request) {
	sid := r.PathValue("sid")
	file := r.PathValue("file")
	if !reHLSSID.MatchString(sid) || !strings.HasPrefix(sid, "web-") {
		writeJSON(w, 400, map[string]string{"error": "bad session id"})
		return
	}
	if !reHLSFile.MatchString(file) {
		writeJSON(w, 400, map[string]string{"error": "bad file"})
		return
	}
	if a.TC == nil {
		writeJSON(w, 503, map[string]string{"error": "transcode unavailable"})
		return
	}
	eid, ok := a.checkWebTicket(sid, auth.UserID(r), false)
	if !ok {
		writeJSON(w, 404, map[string]string{"error": "session not found"})
		return
	}
	ed, err := a.DB.EditionByID(eid)
	if err != nil {
		writeEditionLookupError(w, err)
		return
	}
	a.ticketMu.Lock()
	ticket := a.tickets[sid]
	a.ticketMu.Unlock()
	if ticket.fileID != 0 {
		current, gerr := a.DB.TimelineGeneration(eid)
		if gerr != nil || current != ticket.generation {
			a.checkWebTicket(sid, auth.UserID(r), true)
			a.TC.Close(sid)
			writeJSON(w, 409, map[string]string{"error": "generation_mismatch"})
			return
		}
		var selected *store.FileRec
		for i := range ed.Files {
			if ed.Files[i].ID == ticket.fileID {
				selected = &ed.Files[i]
				break
			}
		}
		if selected == nil {
			writeJSON(w, 404, map[string]string{"error": "file_not_in_edition"})
			return
		}
		ed.Files = []store.FileRec{*selected}
		ed.CumDurations = []float64{selected.DurationSecs}
	}
	if len(ed.Files) == 0 || !streamable(ed) {
		writeJSON(w, 404, map[string]string{"error": "edition not found"})
		return
	}
	if !strings.HasSuffix(file, ".m3u8") {
		if _, live := a.TC.Existing(sid, ed.ID); !live {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusGone, map[string]string{"error": "playback session expired"})
			return
		}
		if !a.TC.WaitForSegmentFile(r.Context(), sid, file, 10*time.Second) {
			if current, ok := a.TC.Existing(sid, ed.ID); ok && errors.Is(current.ResourceError(), resourcebudget.ErrLimit) {
				w.Header().Set("Retry-After", "5")
				writeJSON(w, 503, map[string]string{"error": "transcode resource limit reached"})
				return
			}
			writeJSON(w, 404, map[string]string{"error": "segment not found"})
			return
		}
		serveFile(w, r, filepath.Join(a.DataDir, "transcode", sid, file))
		return
	}
	start := 0.0
	if raw := r.URL.Query().Get("start"); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			writeJSON(w, 400, map[string]string{"error": "invalid start position"})
			return
		}
		if duration := ed.TotalDuration(); duration > 0 && value > duration {
			writeJSON(w, 400, map[string]string{"error": "start exceeds duration"})
			return
		}
		start = value
	}
	open, oerr := a.openEditionFile(ed)
	if oerr != nil {
		writeJSON(w, 404, map[string]string{"error": "edition not found"})
		return
	}
	s, err := a.TC.Get(sid, ed.ID, ed.Files[0].Path, start, open)
	if err != nil {
		if errors.Is(err, transcode.ErrSessionParams) {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, http.StatusGone, map[string]string{"error": "playback session expired"})
			return
		}
		if errors.Is(err, transcode.ErrCapacity) || errors.Is(err, resourcebudget.ErrLimit) {
			w.Header().Set("Retry-After", "5")
			writeJSON(w, 503, map[string]string{"error": "transcode capacity exhausted"})
			return
		}
		if errors.Is(err, transcode.ErrClosed) {
			writeJSON(w, 503, map[string]string{"error": "transcode unavailable"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": "transcode failed"})
		return
	}
	s.Prebuffer(r.Context(), 2, 10*time.Second)
	if errors.Is(s.ResourceError(), resourcebudget.ErrLimit) {
		w.Header().Set("Retry-After", "5")
		writeJSON(w, 503, map[string]string{"error": "transcode resource limit reached"})
		return
	}
	wait := time.NewTicker(200 * time.Millisecond)
	defer wait.Stop()
	for i := 0; i < 100; i++ {
		if fi, err := os.Stat(s.Playlist()); err == nil && fi.Size() > 0 {
			break
		}
		select {
		case <-r.Context().Done():
			writeJSON(w, 499, map[string]string{"error": "client closed request"})
			return
		case <-wait.C:
		}
	}
	data, err := os.ReadFile(s.Playlist())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "no playlist"})
		return
	}
	s.Touch()
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Write(rewriteHLSPlaylist(data, sid, ""))
}
