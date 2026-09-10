package auth

import (
	"path/filepath"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func TestInitAdmin(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	if err := InitAdmin(db, "admin", "password123"); err != nil {
		t.Fatal(err)
	}
	if err := InitAdmin(db, "admin", "password123"); err != nil {
		t.Fatalf("re-run with same password: %v", err)
	}
	if err := InitAdmin(db, "admin", "wrong-password"); err == nil {
		t.Fatal("expected error on wrong password")
	}
	if err := InitAdmin(db, "other", "password123"); err == nil {
		t.Fatal("expected error creating a second admin")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("users = %d, want 1", n)
	}
}
