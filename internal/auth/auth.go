package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/libteca/libteca/internal/store"
)

type contextKey int

const (
	userIDKey contextKey = iota
	tokenKey
)

var (
	ErrKDFBusy            = errors.New("password verification capacity exhausted")
	ErrBadCredentials     = errors.New("invalid credentials")
	ErrCredentialsChanged = errors.New("credentials changed; authenticate again")
)

// kdfSlots bounds concurrent Argon2 derivations process-wide: each one costs
// 64 MiB of working memory, and per-principal limiters do not cap simultaneous
// memory-hard work across different buckets.
var kdfSlots = make(chan struct{}, 2)

func Hash(password string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	key := argon2.IDKey([]byte(password), salt, 2, 64*1024, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=2,p=1$%s$%s", hex.EncodeToString(salt), hex.EncodeToString(key))
}

func HashRequest(ctx context.Context, password string) (string, error) {
	if len(password) > 1024 {
		return "", fmt.Errorf("password too long")
	}
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	select {
	case kdfSlots <- struct{}{}:
		defer func() { <-kdfSlots }()
	default:
		return "", ErrKDFBusy
	}
	return Hash(password), nil
}

// Verify accepts exactly the one parameter set Hash emits. Stored hashes come
// from the database, not the requester, but a corrupted or imported row must
// not be able to request arbitrary Argon2 work or panic the decoder.
func Verify(password, encoded string) bool {
	if len(password) > 1024 || len(encoded) > 256 {
		return false
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" ||
		parts[2] != "v=19" || parts[3] != "m=65536,t=2,p=1" {
		return false
	}
	salt, err := hex.DecodeString(parts[4])
	if err != nil || len(salt) != 16 {
		return false
	}
	want, err := hex.DecodeString(parts[5])
	if err != nil || len(want) != 32 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, 2, 64*1024, 1, 32)
	return subtle.ConstantTimeCompare(got, want) == 1
}

func VerifyRequest(ctx context.Context, password, encoded string) (bool, error) {
	if len(password) > 1024 {
		return false, nil
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	default:
	}
	select {
	case kdfSlots <- struct{}{}:
		defer func() { <-kdfSlots }()
	default:
		return false, ErrKDFBusy
	}
	return Verify(password, encoded), nil
}

var dummyHash = Hash("libteca-non-account-dummy-password")

func DummyHash() string { return dummyHash }

// CheckPassword is the shared request-time credential check: unknown users
// and wrong passwords cost the same KDF work and produce the same error, so
// the faces cannot be probed for valid usernames by response time.
func CheckPassword(ctx context.Context, db *store.DB, name, password string) (*store.User, error) {
	u, lookupErr := db.UserByName(name)
	if lookupErr != nil && !errors.Is(lookupErr, store.ErrNotFound) {
		return nil, lookupErr
	}
	encoded := dummyHash
	if lookupErr == nil {
		encoded = u.PasswordHash
	}
	valid, err := VerifyRequest(ctx, password, encoded)
	if err != nil {
		return nil, err
	}
	if lookupErr != nil || !valid {
		return nil, ErrBadCredentials
	}
	return u, nil
}

func newTokenValue() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// IssueToken mints an unconditional token. Request handlers must use
// IssueTokenForPassword or IssueTokenFromParent so issuance cannot outlive
// the credential that authorized it; this path remains for tooling and test
// fixtures that seed tokens directly.
func IssueToken(db *store.DB, userID int64, label string) (string, error) {
	value, err := newTokenValue()
	if err != nil {
		return "", err
	}
	_, err = db.Exec(`INSERT INTO tokens (user_id, label, value, created_at) VALUES (?,?,?,?)`,
		userID, label, value, time.Now().UnixMilli())
	return value, err
}

// IssueTokenForPassword mints a token only when the verified hash is still
// the user's current hash: a password rotation that races the login must not
// leave an old-password-authenticated token active.
func IssueTokenForPassword(db *store.DB, userID int64, label, verifiedHash string) (string, error) {
	value, err := newTokenValue()
	if err != nil {
		return "", err
	}
	res, err := db.Exec(`INSERT INTO tokens (user_id, label, value, created_at)
		SELECT id, ?, ?, ? FROM users WHERE id = ? AND password_hash = ?`,
		label, value, time.Now().UnixMilli(), userID, verifiedHash)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", ErrCredentialsChanged
	}
	return value, nil
}

// IssueTokenFromParent mints a token only while the authorizing token is
// still active: a revocation that races the mint must not spawn a successor.
func IssueTokenFromParent(db *store.DB, userID int64, label, parent string) (string, error) {
	value, err := newTokenValue()
	if err != nil {
		return "", err
	}
	res, err := db.Exec(`INSERT INTO tokens (user_id, label, value, created_at)
		SELECT user_id, ?, ?, ? FROM tokens WHERE value = ? AND user_id = ? AND revoked_at IS NULL`,
		label, value, time.Now().UnixMilli(), parent, userID)
	if err != nil {
		return "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", err
	}
	if n != 1 {
		return "", ErrCredentialsChanged
	}
	return value, nil
}

// UserForToken resolves the bearer token against the database on every
// request. A positive cache could resurrect a revoked credential when
// revocation races a lookup, never consulted revoked_at on hits, and shared
// results across DB instances in one process.
func UserForToken(db *store.DB, value string) (*store.User, bool) {
	if value == "" || len(value) > 256 {
		return nil, false
	}
	var u store.User
	err := db.QueryRow(`SELECT u.id, u.name, u.password_hash, u.is_admin, u.created_at, u.updated_at
		FROM tokens t JOIN users u ON u.id = t.user_id
		WHERE t.value = ? AND t.revoked_at IS NULL`, value).
		Scan(&u.ID, &u.Name, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, false
	}
	now := time.Now().UnixMilli()
	if _, err := db.Exec(`UPDATE tokens SET last_seen_at = ?
		WHERE value = ? AND revoked_at IS NULL AND (last_seen_at IS NULL OR last_seen_at < ?)`,
		now, value, now-60_000); err != nil {
		slog.Warn("libteca: token activity update failed", "err", err)
	}
	return &u, true
}

func Middleware(db *store.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			value := r.Header.Get("Authorization")
			value = strings.TrimPrefix(value, "Bearer ")
			if value == "" {
				value = r.URL.Query().Get("token")
			}
			user, ok := UserForToken(db, value)
			if !ok {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"unauthorized"}`))
				return
			}
			ctx := context.WithValue(r.Context(), userIDKey, user.ID)
			ctx = context.WithValue(ctx, tokenKey, value)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func WithUser(r *http.Request, id int64) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userIDKey, id))
}

func WithToken(r *http.Request, token string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), tokenKey, token))
}

func UserID(r *http.Request) int64 {
	if v, ok := r.Context().Value(userIDKey).(int64); ok {
		return v
	}
	return 0
}

func Token(r *http.Request) string {
	if v, ok := r.Context().Value(tokenKey).(string); ok {
		return v
	}
	return ""
}

// InitAdmin validates its inputs like the web user-management path and makes
// the empty-install check plus the insert atomic: the old sequence could
// create the account, fail on the token insert, and report an error while
// the admin silently existed.
func InitAdmin(db *store.DB, name, password string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || strings.ContainsRune(name, '\x00') {
		return fmt.Errorf("invalid admin name")
	}
	if len(password) < 8 || len(password) > 1024 {
		return fmt.Errorf("password must be 8 to 1024 bytes")
	}
	u, err := db.UserByName(name)
	if err == nil {
		if !u.IsAdmin {
			return fmt.Errorf("user %q exists and is not an administrator", name)
		}
		if !Verify(password, u.PasswordHash) {
			return fmt.Errorf("user %q exists with a different password", name)
		}
		return nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	hash := Hash(password)
	return db.Update(func(tx *store.Tx) error {
		var count int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return fmt.Errorf("users already exist; refusing to create a second admin")
		}
		now := time.Now().UnixMilli()
		_, err := tx.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES (?,?,1,?,?)`,
			name, hash, now, now)
		return err
	})
}

func Atoi64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
