package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	ErrInvalidName        = errors.New("username must be valid UTF-8, 1-128 bytes, without control characters")
	ErrInvalidPassword    = errors.New("password must be 8 to 1024 bytes")
)

func ValidatePassword(password string) error {
	if len(password) < 8 || len(password) > 1024 {
		return ErrInvalidPassword
	}
	return nil
}

func ValidateCredentials(name, password string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 128 || !utf8.ValidString(name) {
		return "", ErrInvalidName
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrInvalidName
		}
	}
	if len(password) < 8 || len(password) > 1024 {
		return "", ErrInvalidPassword
	}
	return name, nil
}

var kdfSlots = make(chan struct{}, 2)

var readRandom = rand.Read

func Hash(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := readRandom(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, 2, 64*1024, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=2,p=1$%s$%s", hex.EncodeToString(salt), hex.EncodeToString(key)), nil
}

func MustHash(password string) string {
	encoded, err := Hash(password)
	if err != nil {
		panic(err)
	}
	return encoded
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
	return Hash(password)
}

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

// Fixed Argon2id string for the timing-equalization burn on unknown users.
// It verifies no real account: the password is a non-account constant and the
// salt is checked in, which is all a dummy needs (it is not a credential).
const dummyHash = "$argon2id$v=19$m=65536,t=2,p=1$30313233343536373839616263646566$ad2186370400ab3db07cb4cb49545647c6c93a728c6f80556ae93a34d7148753"

func DummyHash() string { return dummyHash }

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

func IssueToken(db *store.DB, userID int64, label string) (string, error) {
	value, err := newTokenValue()
	if err != nil {
		return "", err
	}
	_, err = db.Exec(`INSERT INTO tokens (user_id, label, value, digested, created_at) VALUES (?,?,?,1,?)`,
		userID, label, store.TokenDigest(value), time.Now().UnixMilli())
	return value, err
}

func IssueTokenForPassword(db *store.DB, userID int64, label, verifiedHash string) (string, error) {
	value, err := newTokenValue()
	if err != nil {
		return "", err
	}
	res, err := db.Exec(`INSERT INTO tokens (user_id, label, value, digested, created_at)
		SELECT id, ?, ?, 1, ? FROM users WHERE id = ? AND password_hash = ?`,
		label, store.TokenDigest(value), time.Now().UnixMilli(), userID, verifiedHash)
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

func IssueTokenFromParent(db *store.DB, userID int64, label, parent string) (string, error) {
	value, err := newTokenValue()
	if err != nil {
		return "", err
	}
	res, err := db.Exec(`INSERT INTO tokens (user_id, label, value, digested, created_at)
		SELECT user_id, ?, ?, 1, ? FROM tokens WHERE value = ? AND user_id = ? AND revoked_at IS NULL`,
		label, store.TokenDigest(value), time.Now().UnixMilli(), store.TokenDigest(parent), userID)
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

func LookupTokenUser(db *store.DB, value string) (*store.User, error) {
	if value == "" || len(value) > 256 {
		return nil, store.ErrNotFound
	}
	digest := store.TokenDigest(value)
	var u store.User
	var lastSeen sql.NullInt64
	err := db.QueryRow(`SELECT u.id, u.name, u.password_hash, u.is_admin, u.created_at, u.updated_at, t.last_seen_at
		FROM tokens t JOIN users u ON u.id = t.user_id
		WHERE t.value = ? AND t.revoked_at IS NULL`, digest).
		Scan(&u.ID, &u.Name, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt, &lastSeen)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	now := time.Now().UnixMilli()
	if !lastSeen.Valid || lastSeen.Int64 < now-60_000 {
		if _, err := db.Exec(`UPDATE tokens SET last_seen_at = ?
			WHERE value = ? AND revoked_at IS NULL
			AND (last_seen_at IS NULL OR last_seen_at < ?)`,
			now, digest, now-60_000); err != nil {
			slog.Warn("libteca: token activity update failed", "err", err)
		}
	}
	return &u, nil
}

func UserForToken(db *store.DB, value string) (*store.User, bool) {
	u, err := LookupTokenUser(db, value)
	return u, err == nil
}

func Middleware(db *store.DB) func(http.Handler) http.Handler {
	return middleware(db, nil)
}

// MiddlewareWithMediaCookie additionally accepts the libteca-media cookie on
// the caller-defined media routes and rejects query-token authentication
// there: media element and EventSource URLs carry no credential at all, and
// the cookie keeps the account bearer token out of URLs (AUD-01).
func MiddlewareWithMediaCookie(db *store.DB, mediaRoute func(*http.Request) bool) func(http.Handler) http.Handler {
	return middleware(db, mediaRoute)
}

const MediaCookieName = "libteca-media"

func SetMediaCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     MediaCookieName,
		Value:    value,
		Path:     "/api/core",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func ClearMediaCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     MediaCookieName,
		Value:    "",
		Path:     "/api/core",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func middleware(db *store.DB, mediaRoute func(*http.Request) bool) func(http.Handler) http.Handler {
	unauthorized := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			value := r.Header.Get("Authorization")
			value = strings.TrimPrefix(value, "Bearer ")
			media := mediaRoute != nil && mediaRoute(r)
			if value == "" {
				query := r.URL.Query().Get("token")
				if query != "" && media {
					unauthorized(w)
					return
				}
				value = query
			}
			if value == "" && media {
				if c, err := r.Cookie(MediaCookieName); err == nil {
					value = c.Value
				}
			}
			user, err := LookupTokenUser(db, value)
			if err != nil {
				if errors.Is(err, store.ErrNotFound) {
					unauthorized(w)
					return
				}
				slog.Error("libteca: token lookup failed", "err", err)
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte(`{"error":"authentication unavailable"}`))
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

func InitAdmin(db *store.DB, name, password string) error {
	name, err := ValidateCredentials(name, password)
	if err != nil {
		return err
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
	hash, err := Hash(password)
	if err != nil {
		return err
	}
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
