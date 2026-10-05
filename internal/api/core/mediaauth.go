package core

import (
	"net/http"
	"strings"
)

// MediaRequest identifies the core-face routes that media elements,
// EventSource and sendBeacon reach without an Authorization header. The
// auth middleware accepts the libteca-media cookie only on these routes and
// refuses query-token authentication on them, so no credential ever rides in
// a media URL (AUD-01).
func MediaRequest(r *http.Request) bool {
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
