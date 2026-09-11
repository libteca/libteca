package jellyfin

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

func newAuthEnv(t *testing.T) (*testEnv, func(string, string) *httptest.ResponseRecorder) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Now().UnixMilli()
	if _, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES (?,?,1,?,?)`,
		"bob", auth.Hash("password123"), now, now); err != nil {
		t.Fatal(err)
	}
	r := neutron.New().Router()
	jf := New(db, t.TempDir(), nil)
	jf.LoginLimiter = auth.NewLimiter()
	jf.Mount(r)
	e := &testEnv{db: db, h: r, jf: jf}
	authPost := func(user, pass string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/Users/AuthenticateByName",
			strings.NewReader(`{"Username":"`+user+`","Pw":"`+pass+`"}`))
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		return rec
	}
	return e, authPost
}

func TestAuthenticateByNameRateLimit(t *testing.T) {
	_, authPost := newAuthEnv(t)
	for i := 0; i < 5; i++ {
		if rec := authPost("bob", "wrongpass1"); rec.Code != 401 {
			t.Fatalf("failure %d = %d, want 401", i+1, rec.Code)
		}
	}
	rec := authPost("bob", "wrongpass1")
	if rec.Code != 429 {
		t.Fatalf("locked auth = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header")
	}
	if rec := authPost("bob", "password123"); rec.Code != 429 {
		t.Fatalf("valid credentials while locked = %d, want 429", rec.Code)
	}
}

func TestAuthenticateByNameSuccessWithinLimit(t *testing.T) {
	_, authPost := newAuthEnv(t)
	for i := 0; i < 4; i++ {
		if rec := authPost("bob", "wrongpass1"); rec.Code != 401 {
			t.Fatalf("failure %d = %d, want 401", i+1, rec.Code)
		}
	}
	rec := authPost("bob", "password123")
	if rec.Code != 200 {
		t.Fatalf("valid auth within limit = %d, want 200", rec.Code)
	}
}
