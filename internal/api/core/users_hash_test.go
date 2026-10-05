package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/libteca/libteca/internal/auth"
)

func postUsersRaw(t *testing.T, base, token string, body any) *http.Response {
	t.Helper()
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, base+"/users", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestUserCreateHashErrorClassification(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		want       int
		retryAfter bool
	}{
		{"kdf busy", auth.ErrKDFBusy, http.StatusTooManyRequests, true},
		{"entropy failure", errors.New("random source unavailable"), http.StatusInternalServerError, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, base, adminToken := newUsersEnv(t)
			orig := hashRequest
			hashRequest = func(context.Context, string) (string, error) { return "", tc.err }
			defer func() { hashRequest = orig }()

			resp := postUsersRaw(t, base, adminToken, map[string]string{"name": "hal", "password": "password123"})
			if resp.StatusCode != tc.want {
				t.Fatalf("create user = %d, want %d", resp.StatusCode, tc.want)
			}
			_, hasRetry := resp.Header["Retry-After"]
			if hasRetry != tc.retryAfter {
				t.Fatalf("Retry-After present = %v, want %v", hasRetry, tc.retryAfter)
			}
			if _, err := db.UserByName("hal"); err == nil {
				t.Fatal("failed hash must not create the user")
			}
		})
	}
}

func TestUserSetPasswordHashErrorClassification(t *testing.T) {
	db, base, adminToken := newUsersEnv(t)
	adminID := userIDByName(t, db, "admin")
	before, err := db.User(adminID)
	if err != nil {
		t.Fatal(err)
	}

	orig := hashRequest
	hashRequest = func(context.Context, string) (string, error) { return "", errors.New("random source unavailable") }
	defer func() { hashRequest = orig }()

	code, out := callJSON(t, "POST", fmt.Sprintf("%s/users/%d/password", base, adminID), adminToken, map[string]string{"password": "newpass123"})
	if code != http.StatusInternalServerError {
		t.Fatalf("password reset with entropy failure = %d %v, want 500", code, out)
	}
	after, err := db.User(adminID)
	if err != nil {
		t.Fatal(err)
	}
	if after.PasswordHash != before.PasswordHash {
		t.Fatal("failed hash must not rotate the password")
	}
}
