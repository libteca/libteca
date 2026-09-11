package core

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/libteca/libteca/internal/auth"
)

var srtStamp = regexp.MustCompile(`(\d{1,2}:\d{2}:\d{2}),(\d{1,3})`)

var (
	ffmpegLookPath = exec.LookPath
	ffmpegRun      = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		return exec.CommandContext(ctx, name, args...).Output()
	}
)

func (a *API) subtitles(w http.ResponseWriter, r *http.Request) {
	fid := auth.Atoi64(r.PathValue("fileId"))
	f, err := a.DB.FileByID(fid)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "file not found"})
		return
	}
	side, ok := sidecarSRT(f.Path)
	if ok {
		data, err := os.ReadFile(side)
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "no subtitles"})
			return
		}
		writeVTT(w, []byte(srtToVTT(string(data))))
		return
	}
	data, err := embeddedVTT(r.Context(), f.Path)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "no subtitles"})
		return
	}
	writeVTT(w, data)
}

func writeVTT(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "text/vtt")
	w.Header().Set("Cache-Control", "max-age=86400")
	w.WriteHeader(200)
	w.Write(data)
}

// sidecarSRT looks for <base>.srt and <base>.<lang>[.flag].srt next to the
// media file; the plain sidecar wins, else the first lang match. The media
// path comes only from the files table (PLAN §11: never client-supplied paths).
func sidecarSRT(media string) (string, bool) {
	dir := filepath.Dir(media)
	base := strings.ToLower(strings.TrimSuffix(filepath.Base(media), filepath.Ext(media)))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	lang := ""
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := strings.ToLower(e.Name())
		if !strings.HasSuffix(name, ".srt") || !strings.HasPrefix(name, base+".") {
			continue
		}
		if name == base+".srt" {
			return filepath.Join(dir, e.Name()), true
		}
		mid := name[len(base)+1 : len(name)-len(".srt")]
		ok := len(mid) > 0
		for _, r := range mid {
			if !(r >= 'a' && r <= 'z' || r == '.') {
				ok = false
				break
			}
		}
		if lang == "" && ok {
			lang = filepath.Join(dir, e.Name())
		}
	}
	if lang != "" {
		return lang, true
	}
	return "", false
}

func libtecaVTT(media string) string {
	return strings.TrimSuffix(media, filepath.Ext(media)) + ".libteca.vtt"
}

func cachedVTT(media string) ([]byte, bool) {
	cache := libtecaVTT(media)
	ci, err := os.Stat(cache)
	if err != nil {
		return nil, false
	}
	si, err := os.Stat(media)
	if err != nil || ci.ModTime().Before(si.ModTime()) {
		return nil, false
	}
	data, err := os.ReadFile(cache)
	if err != nil {
		return nil, false
	}
	return data, true
}

func extractEmbedded(ctx context.Context, src string) ([]byte, error) {
	if _, err := ffmpegLookPath("ffmpeg"); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return ffmpegRun(ctx, "ffmpeg", "-i", src, "-map", "0:s:0", "-f", "webvtt", "-")
}

func embeddedVTT(ctx context.Context, media string) ([]byte, error) {
	if data, ok := cachedVTT(media); ok {
		return data, nil
	}
	data, err := extractEmbedded(ctx, media)
	if err != nil {
		return nil, err
	}
	_ = os.WriteFile(libtecaVTT(media), data, 0o644)
	return data, nil
}

func srtToVTT(s string) string {
	s = strings.TrimPrefix(s, "\uFEFF")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var out []string
	out = append(out, "WEBVTT", "")
	var cue []string
	flush := func() {
		if len(cue) == 0 {
			return
		}
		start := 0
		if len(cue) > 1 && isDigits(cue[0]) && strings.Contains(cue[1], "-->") {
			start = 1
		}
		for _, line := range cue[start:] {
			if strings.Contains(line, "-->") {
				line = srtStamp.ReplaceAllString(line, "$1.$2")
			}
			out = append(out, line)
		}
		out = append(out, "")
		cue = cue[:0]
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			flush()
			continue
		}
		cue = append(cue, line)
	}
	flush()
	return strings.Join(out, "\n")
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
