package core

import (
	"fmt"
	"testing"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
)

func TestLogoutRevokesCurrentToken(t *testing.T) {
	db, base, token := newUsersEnv(t)
	code, _ := callJSON(t, "POST", base+"/logout", token, nil)
	if code != 204 {
		t.Fatalf("logout = %d, want 204", code)
	}
	code, _ = callJSON(t, "GET", base+"/me", token, nil)
	if code != 401 {
		t.Fatalf("request after logout = %d, want 401", code)
	}
	other, err := auth.IssueToken(db, 1, "other-device")
	if err != nil {
		t.Fatal(err)
	}
	code, _ = callJSON(t, "GET", base+"/me", other, nil)
	if code != 200 {
		t.Fatalf("independent token after logout = %d, want 200", code)
	}
}

func TestUserCreateValidation(t *testing.T) {
	db, base, token := newUsersEnv(t)
	cases := []struct {
		name     string
		password string
	}{
		{"   ", "password123"},
		{"\x01control", "password123"},
		{string(make([]byte, 129)), "password123"},
		{"shortpw", "short"},
		{"longpw", string(make([]byte, 1025))},
	}
	for _, c := range cases {
		code, _ := callJSON(t, "POST", base+"/users", token, map[string]any{"name": c.name, "password": c.password})
		if code != 400 {
			t.Fatalf("create %q = %d, want 400", c.name, code)
		}
	}
	code, _ := callJSON(t, "POST", base+"/users", token, map[string]any{"name": "  padded  ", "password": "password123"})
	if code != 201 {
		t.Fatalf("trimmed valid create = %d, want 201", code)
	}
	if userIDByName(t, db, "padded") == 0 {
		t.Fatal("name was not trimmed before storage")
	}
}

func TestStaleSelfPasswordChangeRejected(t *testing.T) {
	db, base, adminToken := newUsersEnv(t)
	code, body := callJSON(t, "POST", base+"/users", adminToken, map[string]any{"name": "alice", "password": "password123"})
	if code != 201 {
		t.Fatalf("create alice = %d", code)
	}
	aliceID := int64(body.(map[string]any)["id"].(float64))
	alice, err := db.User(aliceID)
	if err != nil {
		t.Fatal(err)
	}
	staleHash := alice.PasswordHash
	if err := db.RotatePasswordChecked(aliceID, nil, auth.Hash("adminreset1")); err != nil {
		t.Fatal(err)
	}
	err = db.RotatePasswordChecked(aliceID, &staleHash, auth.Hash("stalechange1"))
	if err == nil || err.Error() != "credentials changed; authenticate again" {
		t.Fatalf("stale self-change = %v, want credentials-changed rejection", err)
	}
	fresh, err := db.User(aliceID)
	if err != nil {
		t.Fatal(err)
	}
	if !auth.Verify("adminreset1", fresh.PasswordHash) {
		t.Fatal("stale change overwrote the newer reset")
	}
	code, _ = callJSON(t, "POST", fmt.Sprintf("%s/users/%d/password", base, aliceID), adminToken, map[string]any{"password": "resetpass9"})
	if code != 200 {
		t.Fatalf("admin reset after CAS = %d, want 200", code)
	}
}

func TestRotationDropsSubsonicSecretAndSessions(t *testing.T) {
	db, _, _ := newUsersEnv(t)
	if err := db.SetSetting("subsonic.pw.1", "primary-password"); err != nil {
		t.Fatal(err)
	}
	now := nowMilli()
	libID, err := db.AddLibrary("lib", "audiobooks", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workID, err := db.UpsertWork(&store.Work{LibraryID: libID, Title: "w"})
	if err != nil {
		t.Fatal(err)
	}
	editionID, err := db.UpsertEdition(&store.Edition{WorkID: workID, Format: "mp3", Title: "e"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO playback_sessions (id, user_id, edition_id, started_at, updated_at, position_secs, time_listened_secs, device_info)
		VALUES ('s1', 1, ?, ?, ?, 0, 0, '{}')`, editionID, now, now); err != nil {
		t.Fatal(err)
	}
	if err := db.RotatePasswordChecked(1, nil, auth.Hash("newpassword1")); err != nil {
		t.Fatal(err)
	}
	if _, ok := db.GetSetting("subsonic.pw.1"); ok {
		t.Fatal("rotation left the legacy subsonic secret behind")
	}
	s, err := db.Session("s1")
	if err != nil {
		t.Fatal(err)
	}
	if s.ClosedAt == nil {
		t.Fatal("rotation left the playback session open")
	}
}
