package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron-go/neutron"
)

func newUsersEnv(t *testing.T) (*store.DB, string, string) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := auth.InitAdmin(db, "admin", "password123"); err != nil {
		t.Fatal(err)
	}
	a := New(db, t.TempDir())
	app := neutron.New()
	r := app.Router()
	a.MountPublic(r.Group("/api/core"))
	g := r.Group("/api/core", auth.Middleware(db))
	a.Mount(g)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	return db, srv.URL + "/api/core", loginRequest(t, srv.URL+"/api/core", "admin", "password123")
}

func loginRequest(t *testing.T, base, user, pass string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	resp, err := http.Post(base+"/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("login %s = %d, want 200", user, resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Token == "" {
		t.Fatalf("login %s returned no token", user)
	}
	return out.Token
}

func callJSON(t *testing.T, method, url, token string, body any) (int, any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func jsonRows(t *testing.T, v any) []map[string]any {
	t.Helper()
	rows, ok := v.([]any)
	if !ok {
		t.Fatalf("expected JSON array, got %T", v)
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			t.Fatalf("expected JSON object row, got %T", row)
		}
		out = append(out, m)
	}
	return out
}

func userIDByName(t *testing.T, db *store.DB, name string) int64 {
	t.Helper()
	u, err := db.UserByName(name)
	if err != nil {
		t.Fatalf("user %q: %v", name, err)
	}
	return u.ID
}

func TestUserLifecycle(t *testing.T) {
	db, base, adminToken := newUsersEnv(t)

	code, body := callJSON(t, "POST", base+"/users", adminToken, map[string]any{"name": "alice", "password": "password123"})
	if code != 201 {
		t.Fatalf("create user = %d %v, want 201", code, body)
	}
	aliceID := int64(body.(map[string]any)["id"].(float64))

	code, _ = callJSON(t, "POST", base+"/users", adminToken, map[string]any{"name": "ALICE", "password": "password123"})
	if code != 409 {
		t.Fatalf("duplicate name = %d, want 409", code)
	}
	code, _ = callJSON(t, "POST", base+"/users", adminToken, map[string]any{"name": "bob", "password": "short"})
	if code != 400 {
		t.Fatalf("short password = %d, want 400", code)
	}

	code, body = callJSON(t, "GET", base+"/users", adminToken, nil)
	if code != 200 {
		t.Fatalf("list users = %d, want 200", code)
	}
	rows := jsonRows(t, body)
	if len(rows) != 2 {
		t.Fatalf("users = %d rows, want 2", len(rows))
	}
	for _, row := range rows {
		if _, ok := row["passwordHash"]; ok {
			t.Fatalf("user row leaked passwordHash: %v", row)
		}
		if _, ok := row["password_hash"]; ok {
			t.Fatalf("user row leaked password_hash: %v", row)
		}
	}

	aliceToken := loginRequest(t, base, "alice", "password123")

	code, _ = callJSON(t, "POST", base+"/users", aliceToken, map[string]any{"name": "eve", "password": "password123"})
	if code != 403 {
		t.Fatalf("non-admin create user = %d, want 403", code)
	}
	code, _ = callJSON(t, "GET", base+"/users", aliceToken, nil)
	if code != 403 {
		t.Fatalf("non-admin list users = %d, want 403", code)
	}

	code, _ = callJSON(t, "POST", base+"/users/1/password", aliceToken, map[string]any{"password": "hijacked1"})
	if code != 403 {
		t.Fatalf("non-admin reset other password = %d, want 403", code)
	}
	code, _ = callJSON(t, "POST", fmt.Sprintf("%s/users/%d/password", base, aliceID), aliceToken, map[string]any{"password": "ownpass123"})
	if code != 200 {
		t.Fatalf("self password change = %d, want 200", code)
	}
	loginRequest(t, base, "alice", "ownpass123")

	code, _ = callJSON(t, "POST", fmt.Sprintf("%s/users/%d/password", base, aliceID), adminToken, map[string]any{"password": "resetpass9"})
	if code != 200 {
		t.Fatalf("admin reset password = %d, want 200", code)
	}
	if tok := loginRequest(t, base, "alice", "resetpass9"); tok == "" {
		t.Fatal("alice cannot login with reset password")
	}

	code, _ = callJSON(t, "DELETE", fmt.Sprintf("%s/users/%d", base, userIDByName(t, db, "admin")), adminToken, nil)
	if code != 400 {
		t.Fatalf("delete self = %d, want 400", code)
	}

	aliceToken = loginRequest(t, base, "alice", "resetpass9")
	code, _ = callJSON(t, "DELETE", fmt.Sprintf("%s/users/%d", base, aliceID), adminToken, nil)
	if code != 200 {
		t.Fatalf("delete user = %d, want 200", code)
	}
	code, _ = callJSON(t, "GET", base+"/me", aliceToken, nil)
	if code != 401 {
		t.Fatalf("deleted user token = %d, want 401", code)
	}
}

func TestTokenLifecycle(t *testing.T) {
	_, base, adminToken := newUsersEnv(t)
	code, body := callJSON(t, "POST", base+"/users", adminToken, map[string]any{"name": "alice", "password": "password123"})
	if code != 201 {
		t.Fatalf("create user = %d, want 201", code)
	}
	aliceToken := loginRequest(t, base, "alice", "password123")

	code, body = callJSON(t, "POST", base+"/tokens", aliceToken, map[string]any{"label": "phone"})
	if code != 201 {
		t.Fatalf("issue token = %d, want 201", code)
	}
	phoneValue, _ := body.(map[string]any)["token"].(string)
	if phoneValue == "" {
		t.Fatal("issue token returned no value")
	}

	code, _ = callJSON(t, "GET", base+"/me", phoneValue, nil)
	if code != 200 {
		t.Fatalf("new token rejected before revoke = %d, want 200", code)
	}

	code, body = callJSON(t, "GET", base+"/tokens", aliceToken, nil)
	if code != 200 {
		t.Fatalf("list tokens = %d, want 200", code)
	}
	var phoneID float64
	for _, row := range jsonRows(t, body) {
		if _, ok := row["token"]; ok {
			t.Fatalf("token row leaked value: %v", row)
		}
		if row["label"] == "phone" {
			phoneID = row["id"].(float64)
		}
	}
	if phoneID == 0 {
		t.Fatalf("phone token missing from list: %v", body)
	}

	adminLoginID := -1
	code, body = callJSON(t, "GET", base+"/tokens", adminToken, nil)
	if code != 200 {
		t.Fatalf("admin list tokens = %d, want 200", code)
	}
	sawAdminToken := false
	for _, row := range jsonRows(t, body) {
		if row["userId"].(float64) == 1 {
			sawAdminToken = true
			adminLoginID = int(row["id"].(float64))
		}
	}
	if !sawAdminToken {
		t.Fatal("admin token list does not include other users' tokens")
	}

	code, _ = callJSON(t, "DELETE", fmt.Sprintf("%s/tokens/%d", base, adminLoginID), aliceToken, nil)
	if code != 403 {
		t.Fatalf("non-admin revoke other token = %d, want 403", code)
	}

	code, _ = callJSON(t, "DELETE", fmt.Sprintf("%s/tokens/%d", base, int64(phoneID)), aliceToken, nil)
	if code != 200 {
		t.Fatalf("revoke own token = %d, want 200", code)
	}
	code, _ = callJSON(t, "GET", base+"/me", phoneValue, nil)
	if code != 401 {
		t.Fatalf("revoked token accepted = %d, want 401", code)
	}

	code, body = callJSON(t, "GET", base+"/tokens", adminToken, nil)
	if code != 200 {
		t.Fatalf("admin list tokens = %d, want 200", code)
	}
	var aliceLoginID float64
	for _, row := range jsonRows(t, body) {
		if row["userId"].(float64) == 2 && row["revokedAtMs"] == nil {
			aliceLoginID = row["id"].(float64)
		}
	}
	if aliceLoginID == 0 {
		t.Fatalf("no active alice token left: %v", body)
	}
	code, _ = callJSON(t, "DELETE", fmt.Sprintf("%s/tokens/%d", base, int64(aliceLoginID)), adminToken, nil)
	if code != 200 {
		t.Fatalf("admin revoke other token = %d, want 200", code)
	}
	code, _ = callJSON(t, "GET", base+"/me", aliceToken, nil)
	if code != 401 {
		t.Fatalf("alice login token accepted after admin revoke = %d, want 401", code)
	}
}

func TestCannotDeleteLastAdmin(t *testing.T) {
	db, base, adminToken := newUsersEnv(t)
	adminID := userIDByName(t, db, "admin")

	code, body := callJSON(t, "DELETE", fmt.Sprintf("%s/users/%d", base, adminID), adminToken, nil)
	if code != 400 {
		t.Fatalf("delete last admin = %d %v, want 400", code, body)
	}

	code, _ = callJSON(t, "POST", base+"/users", adminToken, map[string]any{"name": "root2", "password": "password123", "isAdmin": true})
	if code != 201 {
		t.Fatalf("create second admin = %d, want 201", code)
	}
	root2Token := loginRequest(t, base, "root2", "password123")
	code, _ = callJSON(t, "DELETE", fmt.Sprintf("%s/users/%d", base, adminID), root2Token, nil)
	if code != 200 {
		t.Fatalf("delete non-last admin = %d, want 200", code)
	}
	code, _ = callJSON(t, "GET", base+"/me", adminToken, nil)
	if code != 401 {
		t.Fatalf("deleted admin token accepted = %d, want 401", code)
	}
}
