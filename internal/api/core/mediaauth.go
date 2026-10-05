package core

import (
	"net/http"
	"strings"
)

// MediaRequest identifies the core-face read-only routes that media
// elements and EventSource reach without an Authorization header. The auth
// middleware accepts the libteca-media cookie only on these GET/HEAD
// routes and refuses query-token authentication on them, so no credential
// ever rides in a media URL (AUD-01). The cookie is not a write
// credential: every mutation, including the progress beacon and the HLS
// stop, requires the Authorization header.
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
