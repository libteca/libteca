package auth

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/libteca/libteca/internal/store"
)

func openTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func onlyUserID(t *testing.T, db *store.DB) int64 {
	t.Helper()
	var uid int64
	if err := db.QueryRow(`SELECT id FROM users`).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	return uid
}

func TestInitAdmin(t *testing.T) {
	db := openTestDB(t)

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
	var tokens int
	db.QueryRow(`SELECT COUNT(*) FROM tokens`).Scan(&tokens)
	if tokens != 0 {
		t.Fatalf("bootstrap must not mint tokens: %d rows", tokens)
	}
}

func TestInitAdminValidation(t *testing.T) {
	for _, tc := range []struct {
		name, user, pass string
	}{
		{"empty name", "", "password123"},
		{"whitespace name", "   ", "password123"},
		{"nul in name", "ad\x00min", "password123"},
		{"short password", "admin", "short"},
		{"empty password", "admin", ""},
		{"long password", "admin", strings.Repeat("x", 1025)},
	} {
		db := openTestDB(t)
		if err := InitAdmin(db, tc.user, tc.pass); err == nil {
			t.Fatalf("%s: expected validation error", tc.name)
		}
		var n int
		db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
		if n != 0 {
			t.Fatalf("%s: rejected input must not create a user", tc.name)
		}
	}
}

func TestInitAdminExistingNonAdmin(t *testing.T) {
	db := openTestDB(t)
	if err := InitAdmin(db, "admin", "password123"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE users SET is_admin = 0`); err != nil {
		t.Fatal(err)
	}
	if err := InitAdmin(db, "admin", "password123"); err == nil {
		t.Fatal("existing non-admin must not be promoted silently")
	}
}

func TestVerifyStrictFormat(t *testing.T) {
	encoded := Hash("password123")
	if !Verify("password123", encoded) {
		t.Fatal("hash emitted by Hash must verify")
	}
	salt := strings.Repeat("a", 32)
	key := strings.Repeat("b", 64)
	for _, bad := range []string{
		"",
		"argon2id$v=19$m=65536,t=2,p=1$" + salt + "$" + key,
		"$argon2i$v=19$m=65536,t=2,p=1$" + salt + "$" + key,
		"$argon2id$v=18$m=65536,t=2,p=1$" + salt + "$" + key,
		"$argon2id$v=19$m=65536,t=2,p=2$" + salt + "$" + key,
		"$argon2id$v=19$m=1,t=1,p=1$" + salt + "$" + key,
		"$argon2id$v=19$m=65536,t=2,p=1$" + strings.Repeat("a", 33) + "$" + key,
		"$argon2id$v=19$m=65536,t=2,p=1$" + salt + "$" + strings.Repeat("b", 63),
		"$argon2id$v=19$m=65536,t=2,p=1$zz$" + key,
		"$argon2id$v=19$m=65536,t=2,p=1$" + salt + "$zz",
		"$argon2id$v=19$m=65536,t=2,p=1$" + salt,
		"$argon2id$v=19$m=65536,t=2,p=1$" + salt + "$" + key + "$extra",
		encoded + "x",
	} {
		if Verify("password123", bad) {
			t.Fatalf("Verify accepted malformed hash %q", bad)
		}
	}
	if Verify(strings.Repeat("x", 1025), encoded) {
		t.Fatal("oversized password must not verify")
	}
}

func TestRevocationVisibleWithoutCache(t *testing.T) {
	db := openTestDB(t)
	if err := InitAdmin(db, "admin", "password123"); err != nil {
		t.Fatal(err)
	}
	uid := onlyUserID(t, db)
	token, err := IssueToken(db, uid, "test")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := UserForToken(db, token); !ok {
		t.Fatal("fresh token must authenticate")
	}
	if _, err := db.Exec(`UPDATE tokens SET revoked_at = 1 WHERE user_id = ?`, uid); err != nil {
		t.Fatal(err)
	}
	if _, ok := UserForToken(db, token); ok {
		t.Fatal("revoked token authenticated after revocation")
	}
}

func TestRotationRevokesAndConditionalIssuance(t *testing.T) {
	db := openTestDB(t)
	if err := InitAdmin(db, "admin", "password123"); err != nil {
		t.Fatal(err)
	}
	uid := onlyUserID(t, db)
	u, err := db.User(uid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := IssueTokenForPassword(db, uid, "test", u.PasswordHash); err != nil {
		t.Fatal(err)
	}
	if err := db.RotatePassword(uid, Hash("newpass123")); err != nil {
		t.Fatal(err)
	}
	var active int
	db.QueryRow(`SELECT COUNT(*) FROM tokens WHERE user_id = ? AND revoked_at IS NULL`, uid).Scan(&active)
	if active != 0 {
		t.Fatalf("rotation left %d active tokens", active)
	}
	if _, err := IssueTokenForPassword(db, uid, "test", u.PasswordHash); err != ErrCredentialsChanged {
		t.Fatalf("issuance against stale hash = %v, want ErrCredentialsChanged", err)
	}
}

func TestKDFBudgetExhaustion(t *testing.T) {
	for i := 0; i < cap(kdfSlots); i++ {
		kdfSlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(kdfSlots); i++ {
			<-kdfSlots
		}
	}()
	if _, err := VerifyRequest(t.Context(), "password123", Hash("password123")); err != ErrKDFBusy {
		t.Fatalf("VerifyRequest with full budget = %v, want ErrKDFBusy", err)
	}
	if _, err := HashRequest(t.Context(), "password123"); err != ErrKDFBusy {
		t.Fatalf("HashRequest with full budget = %v, want ErrKDFBusy", err)
	}
}

func TestIssueTokenFromParentRevocation(t *testing.T) {
	db := openTestDB(t)
	if err := InitAdmin(db, "admin", "password123"); err != nil {
		t.Fatal(err)
	}
	uid := onlyUserID(t, db)
	parent, err := IssueToken(db, uid, "parent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := IssueTokenFromParent(db, uid, "child", parent); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE tokens SET revoked_at = 1 WHERE value = ?`, store.TokenDigest(parent)); err != nil {
		t.Fatal(err)
	}
	if _, err := IssueTokenFromParent(db, uid, "child", parent); err != ErrCredentialsChanged {
		t.Fatalf("issuance from revoked parent = %v, want ErrCredentialsChanged", err)
	}
}
