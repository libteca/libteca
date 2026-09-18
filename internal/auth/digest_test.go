package auth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/store"
)

func insertLegacyToken(t *testing.T, db *store.DB, uid int64, value string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO tokens (user_id, label, value, created_at) VALUES (?,?,?,?)`,
		uid, "legacy", value, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyPlaintextTokenConvergesToDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := InitAdmin(db, "admin", "password123"); err != nil {
		t.Fatal(err)
	}
	uid := onlyUserID(t, db)
	legacy := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	insertLegacyToken(t, db, uid, legacy)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db2, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	if _, ok := UserForToken(db2, legacy); !ok {
		t.Fatal("legacy plaintext token must authenticate after digest convergence")
	}
	var stored string
	var digested int
	if err := db2.QueryRow(`SELECT value, digested FROM tokens WHERE label = 'legacy'`).Scan(&stored, &digested); err != nil {
		t.Fatal(err)
	}
	if stored != store.TokenDigest(legacy) {
		t.Fatal("stored value must be the sha256 digest of the legacy plaintext")
	}
	if stored == legacy {
		t.Fatal("plaintext survived convergence")
	}
	if digested != 1 {
		t.Fatalf("digested = %d, want 1", digested)
	}

	db3, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db3.Close()
	if _, ok := UserForToken(db3, legacy); !ok {
		t.Fatal("convergence must be idempotent: token broken by second startup")
	}
	var stored2 string
	if err := db3.QueryRow(`SELECT value FROM tokens WHERE label = 'legacy'`).Scan(&stored2); err != nil {
		t.Fatal(err)
	}
	if stored2 != store.TokenDigest(legacy) {
		t.Fatal("second startup re-hashed an already-converged row")
	}
}

func TestIssuedTokensStoredAsDigests(t *testing.T) {
	db := openTestDB(t)
	if err := InitAdmin(db, "admin", "password123"); err != nil {
		t.Fatal(err)
	}
	uid := onlyUserID(t, db)
	token, err := IssueToken(db, uid, "test")
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.QueryRow(`SELECT value FROM tokens WHERE label = 'test'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token {
		t.Fatal("plaintext token value stored at rest")
	}
	if stored != store.TokenDigest(token) {
		t.Fatal("stored value is not the sha256 digest of the issued token")
	}
	if _, ok := UserForToken(db, token); !ok {
		t.Fatal("issued token must authenticate via digest comparison")
	}
	var lastSeen *int64
	if err := db.QueryRow(`SELECT last_seen_at FROM tokens WHERE label = 'test'`).Scan(&lastSeen); err != nil {
		t.Fatal(err)
	}
	if lastSeen == nil {
		t.Fatal("digest-keyed lookup must still record last_seen")
	}
}

func TestRevokeByValueHashesPresentation(t *testing.T) {
	db := openTestDB(t)
	if err := InitAdmin(db, "admin", "password123"); err != nil {
		t.Fatal(err)
	}
	uid := onlyUserID(t, db)
	token, err := IssueToken(db, uid, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RevokeTokenByValue(token); err != nil {
		t.Fatal(err)
	}
	if _, ok := UserForToken(db, token); ok {
		t.Fatal("revoked token still authenticates")
	}
}
