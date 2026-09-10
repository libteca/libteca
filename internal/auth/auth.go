package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"

	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/libteca/libteca/internal/store"
)

type contextKey int

const userIDKey contextKey = iota

var cache sync.Map

func Hash(password string) string {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		panic(err)
	}
	key := argon2.IDKey([]byte(password), salt, 2, 64*1024, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=2,p=1$%s$%s", hex.EncodeToString(salt), hex.EncodeToString(key))
}

func Verify(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m uint32 = 65536
	var t uint32 = 2
	var p uint8 = 1
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := hex.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := hex.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func IssueToken(db *store.DB, userID int64, label string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	value := hex.EncodeToString(raw)
	_, err := db.Exec(`INSERT INTO tokens (user_id, label, value, created_at) VALUES (?,?,?,?)`,
		userID, label, value, time.Now().UnixMilli())
	return value, err
}

func UserForToken(db *store.DB, value string) (*store.User, bool) {
	if u, ok := cache.Load(value); ok {
		if user, ok := u.(*store.User); ok {
			return user, true
		}
	}
	var u store.User
	err := db.QueryRow(`SELECT u.id, u.name, u.password_hash, u.is_admin, u.created_at, u.updated_at
		FROM tokens t JOIN users u ON u.id = t.user_id
		WHERE t.value = ? AND t.revoked_at IS NULL`, value).
		Scan(&u.ID, &u.Name, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, false
	}
	cache.Store(value, &u)
	db.Exec(`UPDATE tokens SET last_seen_at = ? WHERE value = ?`, time.Now().UnixMilli(), value)
	return &u, true
}

// InvalidateToken drops a token from the lookup cache so a revoked or
// deleted token stops authenticating immediately.
func InvalidateToken(value string) {
	if value != "" {
		cache.Delete(value)
	}
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
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func UserID(r *http.Request) int64 {
	if v, ok := r.Context().Value(userIDKey).(int64); ok {
		return v
	}
	return 0
}

func InitAdmin(db *store.DB, name, password string) error {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("users already exist; --init-admin only works on first run")
	}
	now := time.Now().UnixMilli()
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES (?,?,1,?,?)`,
		name, Hash(password), now, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	_, err = db.Exec(`INSERT INTO tokens (user_id, label, value, created_at) VALUES (?,?,?,?)`,
		id, "bootstrap", hex.EncodeToString(mustRandom(32)), now)
	return err
}

func mustRandom(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func Atoi64(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}
