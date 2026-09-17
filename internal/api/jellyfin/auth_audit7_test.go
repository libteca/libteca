package jellyfin

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/auth"
)

func TestAuthenticateMalformedBody(t *testing.T) {
	e, _ := newAuthEnv(t)
	req := httptest.NewRequest("POST", "/Users/AuthenticateByName", strings.NewReader(`{"Username":"bob",`))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Fatalf("malformed body = %d, want 400", rec.Code)
	}
}

func TestAuthenticateOversizedBody(t *testing.T) {
	e, _ := newAuthEnv(t)
	body := `{"Username":"bob","Pw":"` + strings.Repeat("A", 2<<20) + `"}`
	req := httptest.NewRequest("POST", "/Users/AuthenticateByName", strings.NewReader(body))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 413 {
		t.Fatalf("oversized body = %d, want 413", rec.Code)
	}
}

func TestSessionStoppedSingleDocument(t *testing.T) {
	e, _ := newAuthEnv(t)
	token, err := auth.IssueToken(e.db, 1, "test")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/Sessions/Playing/Stopped?PlaySessionId=u99-foreign", strings.NewReader(`{"ItemId":"e1"}`))
	req.Header.Set("X-Emby-Token", token)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("stopped = %d, want 200", rec.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("response is not a single JSON document: %q (%v)", rec.Body.String(), err)
	}
}
