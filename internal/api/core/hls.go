package core

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libteca/libteca/internal/auth"
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
	r.HandleFunc("GET /editions/{id}/thumbs", a.editionThumbs)
	r.HandleFunc("GET /editions/{id}/thumbs/{file}", a.editionThumbTile)
	r.HandleFunc("GET /hls/{sid}/{file}", a.hlsFile)
	r.HandleFunc("DELETE /hls/{sid}", a.hlsStop)
}

const thumbsWidth = 320

var (
	tpMu  sync.Mutex
	tpGen = map[*API]*trickplay.Generator{}
)

func (a *API) trickplayer() *trickplay.Generator {
	tpMu.Lock()
	defer tpMu.Unlock()
	g := tpGen[a]
	if g == nil {
		g = trickplay.New(a.DataDir)
		tpGen[a] = g
	}
	return g
}

func (a *API) editionThumbSource(id int64) (*store.EditionView, error) {
	ed, err := a.DB.EditionByID(id)
	if err != nil || len(ed.Files) == 0 {
		return nil, store.ErrNotFound
	}
	if ed.Files[0].VideoCodec == nil || *ed.Files[0].VideoCodec == "" {
		return nil, store.ErrNotFound
	}
	return ed, nil
}

func (a *API) editionThumbs(w http.ResponseWriter, r *http.Request) {
	ed, err := a.editionThumbSource(auth.Atoi64(r.PathValue("id")))
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	itemID := "e" + strconv.FormatInt(ed.ID, 10)
	m, err := a.trickplayer().Manifest(r.Context(), itemID, ed.Files[0].Path, thumbsWidth)
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
	ed, err := a.editionThumbSource(auth.Atoi64(r.PathValue("id")))
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	index, ok := trickplay.ParseTileName(r.PathValue("file"))
	if !ok {
		writeJSON(w, 400, map[string]string{"error": "bad tile"})
		return
	}
	itemID := "e" + strconv.FormatInt(ed.ID, 10)
	path, err := a.trickplayer().Tile(r.Context(), itemID, ed.Files[0].Path, thumbsWidth, index)
	if errors.Is(err, trickplay.ErrNotFound) {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	if errors.Is(err, trickplay.ErrNoFFmpeg) {
		w.Header().Set("Retry-After", "120")
		writeJSON(w, 503, map[string]string{"error": "ffmpeg unavailable"})
		return
	}
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "tile generation failed"})
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=86400")
	serveFile(w, r, path)
}

func webSessionID(editionID int64) (string, error) {
	return transcode.NewSessionID("web-", editionID)
}

func parseWebSessionID(s string) (int64, bool) {
	if !strings.HasPrefix(s, "web-") {
		return 0, false
	}
	rest := strings.TrimPrefix(s, "web-")
	idText, _, hasSuffix := strings.Cut(rest, "-")
	// Suffixed ids are the unique-per-playback shape; a bare "web-<id>" is
	// the legacy shape callers still send and sessions are in-memory only,
	// so both parse and route by their edition.
	if !hasSuffix {
		idText = rest
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	return id, err == nil && id > 0
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
	return (vcodec == "h264" || vcodec == "vp9" || vcodec == "av1") && audioOK && containerOK
}

func (a *API) editionPlayback(w http.ResponseWriter, r *http.Request) {
	id := auth.Atoi64(r.PathValue("id"))
	ed, err := a.DB.EditionByID(id)
	if err != nil || len(ed.Files) == 0 {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	fileID := ed.Files[0].ID
	if browserPlayable(ed) {
		writeJSON(w, 200, map[string]any{"mode": "direct", "fileId": fileID})
		return
	}
	if a.TC == nil {
		writeJSON(w, 503, map[string]string{"error": "transcode unavailable"})
		return
	}
	sid, err := webSessionID(ed.ID)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "session creation failed"})
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
	eid, ok := parseWebSessionID(sid)
	if !ok {
		writeJSON(w, 400, map[string]string{"error": "bad session id"})
		return
	}
	ed, err := a.DB.EditionByID(eid)
	if err != nil || len(ed.Files) == 0 {
		writeJSON(w, 404, map[string]string{"error": "edition not found"})
		return
	}
	start := 0.0
	if s := r.URL.Query().Get("start"); s != "" {
		if v, err := strconv.ParseFloat(s, 64); err == nil && v > 0 {
			start = v
		}
	}
	s, err := a.TC.Get(sid, ed.ID, ed.Files[0].Path, start)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "transcode failed"})
		return
	}
	if strings.HasSuffix(file, ".m3u8") {
		s.Prebuffer(r.Context(), 2, 10*time.Second)
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
		tok := r.URL.Query().Get("token")
		if tok == "" {
			tok = auth.Token(r)
		}
		w.Write(rewriteHLSPlaylist(data, sid, tok))
		return
	}
	if !a.TC.WaitForSegmentFile(r.Context(), sid, file, 10*time.Second) {
		writeJSON(w, 404, map[string]string{"error": "segment not found"})
		return
	}
	path := filepath.Join(a.DataDir, "transcode", sid, file)
	f, err := os.Open(path)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "segment not found"})
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	http.ServeContent(w, r, file, fi.ModTime(), f)
}
