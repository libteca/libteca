package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrSocketSenderRevoked = errors.New("queued socket sender revoked")

type SocketCredential struct {
	Digest string
	UserID int64
}

func (d *DB) WithSocketAuthority(digest string, user int64, action func() error, senders ...SocketCredential) error {
	d.socketAuthority.RLock()
	defer d.socketAuthority.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	credentials := append([]SocketCredential{{Digest: digest, UserID: user}}, senders...)
	for index, credential := range credentials {
		var id int64
		err := d.QueryRowContext(ctx, `SELECT u.id FROM tokens t JOIN users u ON u.id = t.user_id WHERE t.value = ? AND t.user_id = ? AND t.revoked_at IS NULL`, credential.Digest, credential.UserID).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			if index > 0 {
				return ErrSocketSenderRevoked
			}
			return ErrNotFound
		}
		if err != nil {
			return err
		}
	}
	return action()
}
