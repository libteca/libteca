package core

import (
	"net/http"
	"strings"
)

func MediaRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return isMediaPath(r.URL.Path)
}

func isMediaPath(path string) bool {
	p := strings.TrimPrefix(path, "/api/core")
	if len(p) == len(path) {
		return false
	}
	switch {
	case strings.HasPrefix(p, "/stream/"),
		strings.HasPrefix(p, "/covers/"),
		strings.HasPrefix(p, "/subtitles/"),
		strings.HasPrefix(p, "/hls/"),
		strings.HasPrefix(p, "/progress/"),
		p == "/podcasts/export-opml",
		strings.HasPrefix(p, "/podcasts/episodes/") && strings.HasSuffix(p, "/stream"):
		return true
	case strings.HasPrefix(p, "/editions/"):
		return strings.HasSuffix(p, "/download") || strings.Contains(p, "/thumbs")
	case strings.HasPrefix(p, "/libraries/"):
		return strings.HasSuffix(p, "/scan/events") || strings.HasSuffix(p, "/refresh-meta/events")
	}
	return false
}
