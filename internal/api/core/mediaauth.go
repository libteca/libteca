package core

import (
	"net/http"
	"strings"
)

// MediaRequest identifies the core-face routes that media elements and
// EventSource reach without an Authorization header. The auth middleware
// accepts the libteca-media cookie only on these routes (read-only methods
// only) and refuses query-token authentication on them, so no credential
// ever rides in a media URL (AUD-01). The cookie is not a write credential
// here: mutations need ProgressBeaconRequest below or the bearer header.
func MediaRequest(r *http.Request) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	return isMediaPath(r.URL.Path)
}

// MediaMutationRequest identifies the cookie-authenticated unsafe methods
// the first-party web app issues from its reader/player pages, where
// sendBeacon and keepalive fetch carry no Authorization header: the reader
// progress beacon and the HLS session stop. The auth middleware consumes
// the media cookie for these only after the same-origin check, so
// SameSite=Strict is never the sole CSRF boundary (A12-01).
func MediaMutationRequest(r *http.Request) bool {
	p := strings.TrimPrefix(r.URL.Path, "/api/core")
	if len(p) == len(r.URL.Path) {
		return false
	}
	switch r.Method {
	case http.MethodPost:
		id := strings.TrimPrefix(p, "/progress/")
		return id != "" && !strings.Contains(id, "/")
	case http.MethodDelete:
		id := strings.TrimPrefix(p, "/hls/")
		return id != "" && !strings.Contains(id, "/")
	}
	return false
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
