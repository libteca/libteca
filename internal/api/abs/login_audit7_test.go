package abs_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoginBodyCap(t *testing.T) {
	a, _ := newLoginEnv(t)
	huge := strings.Repeat("A", 2<<20)
	req := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader([]byte(`{"username":"bob","password":"`+huge+`"}`)))
	rec := httptest.NewRecorder()
	a.Login(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized login = %d, want 413", rec.Code)
	}
}

func TestLoginAggregrateIPLockout(t *testing.T) {
	_, login := newLoginEnv(t)
	for i := 0; i < 30; i++ {
		rec := login("ghost-user-"+strings.Repeat("a", i+1), "wrongpass1")
		if rec.Code != 401 {
			t.Fatalf("failure %d = %d, want 401", i+1, rec.Code)
		}
	}
	rec := login("bob", "password123")
	if rec.Code != 429 {
		t.Fatalf("valid principal after 30 aggregate failures from one IP = %d, want 429", rec.Code)
	}
}
