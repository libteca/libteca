package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSocketAuthorityRevocationFence(t *testing.T) {
	for _, action := range []string{"logout", "token", "all-tokens", "password", "delete", "guarded-delete"} {
		t.Run(action, func(t *testing.T) {
			db, err := Open(filepath.Join(t.TempDir(), "authority.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			user, err := db.CreateUser("u", "hash", false)
			if err != nil {
				t.Fatal(err)
			}
			digest := TokenDigest("credential")
			res, err := db.Exec(`INSERT INTO tokens(user_id,label,value,digested,created_at) VALUES(?,?,?,1,?)`, user, "test", digest, time.Now().UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			token, _ := res.LastInsertId()
			if err := db.WithSocketAuthority(digest, user+1, func() error { t.Error("wrong owner admitted"); return nil }); !errors.Is(err, ErrNotFound) {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			admitted := make(chan error, 1)
			go func() {
				admitted <- db.WithSocketAuthority(digest, user, func() error { close(entered); <-release; return nil })
			}()
			<-entered
			completed := make(chan error, 1)
			started := make(chan struct{})
			go func() {
				close(started)
				switch action {
				case "logout":
					completed <- db.RevokeTokenByValue("credential")
				case "token":
					_, err := db.RevokeToken(token)
					completed <- err
				case "all-tokens":
					completed <- db.RevokeUserTokens(user)
				case "password":
					completed <- db.RotatePassword(user, "new-hash")
				case "delete":
					_, err := db.DeleteUser(user)
					completed <- err
				case "guarded-delete":
					_, err := db.DeleteUserGuarded(user)
					completed <- err
				}
			}()
			<-started
			select {
			case err := <-completed:
				close(release)
				t.Fatalf("revocation overtook admitted authority: %v", err)
			case <-time.After(20 * time.Millisecond):
			}
			close(release)
			if err := <-admitted; err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-completed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("revocation did not finish")
			}
			called := false
			err = db.WithSocketAuthority(digest, user, func() error { called = true; return nil })
			if !errors.Is(err, ErrNotFound) || called {
				t.Fatalf("revoked authority: %v called=%v", err, called)
			}
		})
	}
}
