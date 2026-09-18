package podcast

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func msPtr(ms int64) *int64 {
	if ms == 0 {
		return nil
	}
	return &ms
}

func fltPtr(f float64) *float64 {
	if f == 0 {
		return nil
	}
	return &f
}

func int64Ptr(v int64) *int64 {
	if v == 0 {
		return nil
	}
	return &v
}

// nilOrEmpty maps "" to NULL for the validator columns.
func nilOrEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func derefFlt(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func mkdir(path string) error {
	return os.MkdirAll(path, 0o700)
}

func writeFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644)
}

func osRemove(path string) {
	os.Remove(path)
}

func newGetRequest(ctx context.Context, url string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	return req, nil
}

// normalizeFeedURL canonicalizes a feed URL for storage and duplicate
func normalizeFeedURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return raw
	}
	u.Fragment = ""
	u.RawFragment = ""
	u.Path = strings.TrimRight(u.Path, "/")
	return u.String()
}
