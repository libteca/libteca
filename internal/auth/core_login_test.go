package auth_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/libteca/libteca/internal/api/core"
	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

func newCoreLoginEnv(t *testing.T) string {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := auth.InitAdmin(db, "admin", "password123"); err != nil {
		t.Fatal(err)
	}
	c := core.New(db, t.TempDir())
	c.LoginLimiter = auth.NewLimiter()
	app := neutron.New()
	r := app.Router()
	c.MountPublic(r.Group("/api/core"))
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return srv.URL + "/api/core"
}

func coreLogin(t *testing.T, base, user, pass string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	resp, err := http.Post(base+"/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestCoreLoginRateLimited(t *testing.T) {
	base := newCoreLoginEnv(t)
	for i := 0; i < 5; i++ {
		if resp := coreLogin(t, base, "admin", "wrongpass1"); resp.StatusCode != 401 {
			t.Fatalf("failure %d = %d, want 401", i+1, resp.StatusCode)
		}
	}
	resp := coreLogin(t, base, "admin", "wrongpass1")
	if resp.StatusCode != 429 {
		t.Fatalf("locked login = %d, want 429", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("missing Retry-After header")
	}
	var out struct {
		Error string `json:"error"`
	}
	body, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(body, &out); err != nil || out.Error != "too many attempts, try again later" {
		t.Fatalf("error body = %s", body)
	}
	if resp := coreLogin(t, base, "admin", "password123"); resp.StatusCode != 429 {
		t.Fatalf("valid credentials while locked = %d, want 429", resp.StatusCode)
	}
}

func TestCoreLoginSuccessResetsFailures(t *testing.T) {
	base := newCoreLoginEnv(t)
	for i := 0; i < 4; i++ {
		if resp := coreLogin(t, base, "admin", "wrongpass1"); resp.StatusCode != 401 {
			t.Fatalf("failure %d = %d, want 401", i+1, resp.StatusCode)
		}
	}
	if resp := coreLogin(t, base, "admin", "password123"); resp.StatusCode != 200 {
		t.Fatalf("valid login = %d, want 200", resp.StatusCode)
	}
	for i := 0; i < 4; i++ {
		if resp := coreLogin(t, base, "admin", "wrongpass1"); resp.StatusCode != 401 {
			t.Fatalf("failure %d = %d, want 401", i+1, resp.StatusCode)
		}
	}
	if resp := coreLogin(t, base, "admin", "password123"); resp.StatusCode != 200 {
		t.Fatalf("valid login after reset = %d, want 200", resp.StatusCode)
	}
}
