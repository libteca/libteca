package opds

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

func newLimitEnv(t *testing.T) func(string, string) *httptest.ResponseRecorder {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Now().UnixMilli()
	if _, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES (?,?,1,?,?)`,
		"limiter-user", auth.Hash("password123"), now, now); err != nil {
		t.Fatal(err)
	}
	r := neutron.New().Router()
	a := New(db, t.TempDir())
	a.LoginLimiter = auth.NewLimiter()
	a.Mount(r)
	return func(user, pass string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/opds", nil)
		req.SetBasicAuth(user, pass)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}
}

func TestBasicAuthRateLimit(t *testing.T) {
	get := newLimitEnv(t)
	for i := 0; i < 5; i++ {
		if rec := get("limiter-user", "wrongpass1"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d, want 401", i+1, rec.Code)
		}
	}
	rec := get("limiter-user", "wrongpass1")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("locked auth = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header")
	}
	if rec := get("limiter-user", "password123"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("valid credentials while locked = %d, want 429", rec.Code)
	}
}

func TestBasicAuthSuccessWithinLimit(t *testing.T) {
	get := newLimitEnv(t)
	for i := 0; i < 4; i++ {
		if rec := get("limiter-user", "wrongpass1"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d = %d, want 401", i+1, rec.Code)
		}
	}
	if rec := get("limiter-user", "password123"); rec.Code != http.StatusOK {
		t.Fatalf("valid auth within limit = %d, want 200", rec.Code)
	}
}

func TestInvalidateUserDropsCachedBasic(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Now().UnixMilli()
	if _, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES (?,?,1,?,?)`,
		"cache-user", auth.Hash("oldpass123"), now, now); err != nil {
		t.Fatal(err)
	}
	r := neutron.New().Router()
	a := New(db, t.TempDir())
	a.LoginLimiter = auth.NewLimiter()
	a.Mount(r)
	get := func(user, pass string) int {
		req := httptest.NewRequest("GET", "/opds", nil)
		req.SetBasicAuth(user, pass)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := get("cache-user", "oldpass123"); code != http.StatusOK {
		t.Fatalf("initial auth = %d, want 200", code)
	}
	u, err := db.UserByName("cache-user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, auth.Hash("newpass123"), u.ID); err != nil {
		t.Fatal(err)
	}
	InvalidateUser(u.ID)
	if code := get("cache-user", "oldpass123"); code != http.StatusUnauthorized {
		t.Fatalf("old password after InvalidateUser = %d, want 401", code)
	}
	if code := get("cache-user", "newpass123"); code != http.StatusOK {
		t.Fatalf("new password after InvalidateUser = %d, want 200", code)
	}
}

func TestInternalErrorsDoNotLeakErrText(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Now().UnixMilli()
	if _, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES (?,?,1,?,?)`,
		"leak-user", auth.Hash("password123"), now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE libraries`); err != nil {
		t.Fatal(err)
	}
	r := neutron.New().Router()
	New(db, t.TempDir()).Mount(r)
	req := httptest.NewRequest("GET", "/opds", nil)
	req.SetBasicAuth("leak-user", "password123")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("root feed with store failure = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "no such table") || strings.Contains(body, "sqlite") {
		t.Fatalf("internal error text leaked to client: %s", body)
	}
	if !strings.Contains(body, "internal error") {
		t.Fatalf("body = %s, want static message", body)
	}
}
