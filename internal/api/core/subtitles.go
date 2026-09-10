package core

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/libteca/libteca/internal/auth"
)

var srtStamp = regexp.MustCompile(`(\d{1,2}:\d{2}:\d{2}),(\d{1,3})`)

func (a *API) subtitles(w http.ResponseWriter, r *http.Request) {
	fid := auth.Atoi64(r.PathValue("fileId"))
	f, err := a.DB.FileByID(fid)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "file not found"})
		return
	}
	side, ok := sidecarSRT(f.Path)
	if !ok {
		writeJSON(w, 404, map[string]string{"error": "no subtitles"})
		return
	}
	data, err := os.ReadFile(side)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "no subtitles"})
		return
	}
	w.Header().Set("Content-Type", "text/vtt")
	w.Header().Set("Cache-Control", "max-age=86400")
	w.WriteHeader(200)
	w.Write([]byte(srtToVTT(string(data))))
}

// sidecarSRT looks for <base>.srt and <base>.<2-letter>.srt next to the media
// file; the plain sidecar wins, else the first lang match. The media path comes
// only from the files table (PLAN §11: never client-supplied paths).
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
		if lang == "" && len(mid) == 2 && mid[0] >= 'a' && mid[0] <= 'z' && mid[1] >= 'a' && mid[1] <= 'z' {
			lang = filepath.Join(dir, e.Name())
		}
	}
	if lang != "" {
		return lang, true
	}
	return "", false
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
