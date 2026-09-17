package store

import (
	"database/sql"
	"errors"
	"strconv"
)

// Token is a session/API token. Value is deliberately not part of this
// struct so token values can never leak into JSON responses by accident.
type Token struct {
	ID         int64
	UserID     int64
	Label      string
	CreatedAt  int64
	LastSeenAt *int64
	RevokedAt  *int64
}

func (d *DB) CreateUser(name, passwordHash string, isAdmin bool) (int64, error) {
	now := nowMilli()
	res, err := d.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES (?,?,?,?,?)`,
		name, passwordHash, isAdmin, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (d *DB) UpdateUserPassword(id int64, passwordHash string) error {
	_, err := d.Exec(`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		passwordHash, nowMilli(), id)
	return err
}

func (d *DB) RotatePassword(id int64, passwordHash string) error {
	return d.RotatePasswordChecked(id, nil, passwordHash)
}

var ErrCredentialsChanged = errors.New("credentials changed; authenticate again")

func (d *DB) RotatePasswordChecked(id int64, expected *string, passwordHash string) error {
	return d.Update(func(tx *Tx) error {
		now := nowMilli()
		var res sql.Result
		var err error
		if expected != nil {
			res, err = tx.Exec(`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ? AND password_hash = ?`,
				passwordHash, now, id, *expected)
		} else {
			res, err = tx.Exec(`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
				passwordHash, now, id)
		}
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			if expected != nil {
				return ErrCredentialsChanged
			}
			return ErrNotFound
		}
		if _, err := tx.Exec(`UPDATE tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, now, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE playback_sessions SET closed_at = ?, updated_at = ? WHERE user_id = ? AND closed_at IS NULL`, now, now, id); err != nil {
			return err
		}
		_, err = tx.Exec(`DELETE FROM settings WHERE key = ?`, "subsonic.pw."+strconv.FormatInt(id, 10))
		return err
	})
}

func (d *DB) RevokeTokenByValue(value string) error {
	if value == "" {
		return nil
	}
	_, err := d.Exec(`UPDATE tokens SET revoked_at = ? WHERE value = ? AND revoked_at IS NULL`, nowMilli(), value)
	return err
}

func (d *DB) CountAdmins() (int, error) {
	var n int
	err := d.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin = 1`).Scan(&n)
	return n, err
}

// ErrLastAdmin is returned when the deletion would leave no administrator.
var ErrLastAdmin = errors.New("cannot delete last admin")

// DeleteUserGuarded removes a user with the last-admin check and the deletion
// in one transaction: checking admin count separately from deleting let two
// concurrent admin deletions both pass and leave the install adminless.
func (d *DB) DeleteUserGuarded(id int64) ([]string, error) {
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var isAdmin bool
	err = tx.QueryRow(`SELECT is_admin FROM users WHERE id = ?`, id).Scan(&isAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if isAdmin {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM users WHERE is_admin = 1`).Scan(&n); err != nil {
			return nil, err
		}
		if n <= 1 {
			return nil, ErrLastAdmin
		}
	}

	rows, err := tx.Query(`SELECT value FROM tokens WHERE user_id = ?`, id)
	if err != nil {
		return nil, err
	}
	var values []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		values = append(values, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM playback_sessions WHERE user_id = ?`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM progress WHERE user_id = ?`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, "subsonic.pw."+strconv.FormatInt(id, 10)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM tokens WHERE user_id = ?`, id); err != nil {
		return nil, err
	}
	res, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return values, nil
}

// DeleteUser removes the user along with their tokens, progress and
// playback sessions (all foreign-keyed to users) and returns the deleted
// token values so callers can drop them from the auth cache. The last-admin
// guarantee is the caller's business; admins go through DeleteUserGuarded.
func (d *DB) DeleteUser(id int64) ([]string, error) {
	tx, err := d.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT value FROM tokens WHERE user_id = ?`, id)
	if err != nil {
		return nil, err
	}
	var values []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return nil, err
		}
		values = append(values, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM playback_sessions WHERE user_id = ?`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM progress WHERE user_id = ?`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, "subsonic.pw."+strconv.FormatInt(id, 10)); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM tokens WHERE user_id = ?`, id); err != nil {
		return nil, err
	}
	res, err := tx.Exec(`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return values, nil
}

func (d *DB) UserTokenValues(id int64) ([]string, error) {
	rows, err := d.Query(`SELECT value FROM tokens WHERE user_id = ? AND revoked_at IS NULL`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

func (d *DB) RevokeUserTokens(id int64) error {
	_, err := d.Exec(`UPDATE tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, nowMilli(), id)
	return err
}

// Tokens lists tokens; userID 0 means all users.
func (d *DB) Tokens(userID int64) ([]Token, error) {
	rows, err := d.Query(`SELECT id, user_id, label, created_at, last_seen_at, revoked_at
		FROM tokens WHERE (? = 0 OR user_id = ?) ORDER BY id`, userID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Token
	for rows.Next() {
		var t Token
		if err := rows.Scan(&t.ID, &t.UserID, &t.Label, &t.CreatedAt, &t.LastSeenAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (d *DB) TokenByID(id int64) (*Token, error) {
	var t Token
	err := d.QueryRow(`SELECT id, user_id, label, created_at, last_seen_at, revoked_at FROM tokens WHERE id = ?`, id).
		Scan(&t.ID, &t.UserID, &t.Label, &t.CreatedAt, &t.LastSeenAt, &t.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &t, err
}

// RevokeToken marks a token revoked and returns its value for cache
// invalidation. The value must never be sent to clients.
func (d *DB) RevokeToken(id int64) (string, error) {
	var value string
	err := d.QueryRow(`SELECT value FROM tokens WHERE id = ?`, id).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	_, err = d.Exec(`UPDATE tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`, nowMilli(), id)
	return value, err
}
