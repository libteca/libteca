package abs_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/api/abs"
	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
)

func newLoginEnv(t *testing.T) (*abs.API, func(string, string) *httptest.ResponseRecorder) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Now().UnixMilli()
	if _, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES (?,?,1,?,?)`,
		"bob", auth.Hash("password123"), now, now); err != nil {
		t.Fatal(err)
	}
	a := abs.New(db, dir)
	a.LoginLimiter = auth.NewLimiter()
	login := func(user, pass string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
		req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		a.Login(rec, req)
		return rec
	}
	return a, login
}

func TestLoginRateLimit(t *testing.T) {
	_, login := newLoginEnv(t)
	for i := 0; i < 5; i++ {
		if rec := login("bob", "wrongpass1"); rec.Code != 401 {
			t.Fatalf("failure %d = %d, want 401", i+1, rec.Code)
		}
	}
	rec := login("bob", "wrongpass1")
	if rec.Code != 429 {
		t.Fatalf("locked login = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header")
	}
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Error == "" {
		t.Fatalf("bad error body: %s", rec.Body.String())
	}
	if rec := login("bob", "password123"); rec.Code != 429 {
		t.Fatalf("valid credentials while locked = %d, want 429", rec.Code)
	}
}

func TestLoginSuccessResetsFailures(t *testing.T) {
	_, login := newLoginEnv(t)
	for i := 0; i < 4; i++ {
		if rec := login("bob", "wrongpass1"); rec.Code != 401 {
			t.Fatalf("failure %d = %d, want 401", i+1, rec.Code)
		}
	}
	if rec := login("bob", "password123"); rec.Code != 200 {
		t.Fatalf("valid login = %d, want 200", rec.Code)
	}
	for i := 0; i < 4; i++ {
		if rec := login("bob", "wrongpass1"); rec.Code != 401 {
			t.Fatalf("failure %d = %d, want 401", i+1, rec.Code)
		}
	}
	if rec := login("bob", "password123"); rec.Code != 200 {
		t.Fatalf("valid login after reset = %d, want 200", rec.Code)
	}
}
