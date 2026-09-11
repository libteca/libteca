package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func newBodyLimitServer(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	srv := New(db, dir)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestCoreBodyLimit(t *testing.T) {
	ts := newBodyLimitServer(t)

	big := `{"username":"` + strings.Repeat("a", 5<<20) + `","password":"x"}`
	resp, err := http.Post(ts.URL+"/api/core/login", "application/json", strings.NewReader(big))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != 400 {
		t.Fatalf("oversized login = %d, want 400", resp.StatusCode)
	}

	resp2, err := http.Post(ts.URL+"/api/core/login", "application/json", bytes.NewReader([]byte(`{"username":"x","password":"y"}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	body, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != 401 {
		t.Fatalf("normal login = %d %s, want 401", resp2.StatusCode, body)
	}

	var req bytes.Buffer
	fmt.Fprint(&req, `{"opml":"`)
	for i := 0; i < (5<<20)/64; i++ {
		req.WriteString(strings.Repeat("x", 64))
	}
	req.WriteString(`"}`)
	unauth, err := http.Post(ts.URL+"/api/core/podcasts/import-opml", "application/json", &req)
	if err != nil {
		t.Fatal(err)
	}
	defer unauth.Body.Close()
	io.Copy(io.Discard, unauth.Body)
	if unauth.StatusCode != 401 {
		t.Fatalf("oversized unauthenticated opml = %d, want 401 (auth checked before body)", unauth.StatusCode)
	}
}
