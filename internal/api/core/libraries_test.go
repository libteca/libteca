package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
)

func deleteLib(t *testing.T, a *API, db *store.DB, token string, id int64) (int, map[string]any) {
	t.Helper()
	h := auth.Middleware(db)(http.HandlerFunc(a.deleteLibrary))
	sid := strconv.FormatInt(id, 10)
	req := httptest.NewRequest("DELETE", "/libraries/"+sid, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.SetPathValue("id", sid)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func TestDeleteLibraryHandler(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := auth.InitAdmin(db, "admin", "password123"); err != nil {
		t.Fatal(err)
	}
	admin, err := db.UserByName("admin")
	if err != nil {
		t.Fatal(err)
	}
	adminTok, err := auth.IssueToken(db, admin.ID, "t")
	if err != nil {
		t.Fatal(err)
	}
	aliceID, err := db.CreateUser("alice", "x", false)
	if err != nil {
		t.Fatal(err)
	}
	aliceTok, err := auth.IssueToken(db, aliceID, "t")
	if err != nil {
		t.Fatal(err)
	}
	a := New(db, t.TempDir())
	libID, err := db.AddLibrary("A", "audiobooks", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	code, body := deleteLib(t, a, db, aliceTok, libID)
	if code != 403 {
		t.Fatalf("non-admin = %d %v, want 403", code, body)
	}
	code, body = deleteLib(t, a, db, adminTok, libID)
	if code != 200 || body["ok"] != true {
		t.Fatalf("admin delete = %d %v, want 200 ok", code, body)
	}
	code, _ = deleteLib(t, a, db, adminTok, libID)
	if code != 404 {
		t.Fatalf("missing = %d, want 404", code)
	}
}
