package abs

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/store"
)

func (s *bodyTestStack) addEdition(t *testing.T, fileDuration float64) int64 {
	t.Helper()
	now := time.Now().UnixMilli()
	var libID int64
	if err := s.db.QueryRow(`SELECT w.library_id FROM works w JOIN editions e ON e.work_id = w.id WHERE e.id = ?`, s.edID).Scan(&libID); err != nil {
		t.Fatal(err)
	}
	res, err := s.db.Exec(`INSERT INTO works (library_id, title, created_at, updated_at) VALUES (?,?,?,?)`, libID, "Second", now, now)
	if err != nil {
		t.Fatal(err)
	}
	workID, _ := res.LastInsertId()
	res, err = s.db.Exec(`INSERT INTO editions (work_id, format, title, duration_secs, position, created_at) VALUES (?,?,?,?,1,?)`, workID, "audio", "Second", fileDuration, now)
	if err != nil {
		t.Fatal(err)
	}
	edID, _ := res.LastInsertId()
	if _, err := s.db.Exec(`INSERT INTO files (edition_id, path, seq, size_bytes, mtime_secs, duration_secs, chapters, embedded_meta, missing, probed_at)
		VALUES (?,?,1,1,?,?,'[]','{}',0,?)`, edID, filepath.Join(t.TempDir(), "f2.m4b"), now, fileDuration, now); err != nil {
		t.Fatal(err)
	}
	return edID
}

func (s *bodyTestStack) createSessionOn(t *testing.T, id string, edID int64) {
	t.Helper()
	now := time.Now().UnixMilli()
	sess := &store.Session{ID: id, UserID: 1, EditionID: edID, StartedAt: now, UpdatedAt: now, DeviceInfo: "{}"}
	if err := s.db.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
}

func (s *bodyTestStack) sessionOpen(t *testing.T, id string) bool {
	t.Helper()
	got, err := s.db.Session(id)
	if err != nil {
		t.Fatal(err)
	}
	return got.ClosedAt == nil
}

func TestSessionTimingValidatedAgainstServerDuration(t *testing.T) {
	for _, ep := range []struct{ name, suffix string }{
		{"sync", "sync"},
		{"close", "close"},
	} {
		for _, tc := range []struct {
			name         string
			body         string
			code         int
			wantPosition float64
			wantListened float64
		}{
			{"zero client duration beyond edition", `{"currentTime":70,"timeListened":2,"duration":0}`, 400, 0, 0},
			{"inflated client duration beyond edition", `{"currentTime":70,"timeListened":2,"duration":1000}`, 400, 0, 0},
			{"end position with zero client duration", `{"currentTime":60,"timeListened":2,"duration":0}`, 200, 60, 2},
			{"server bound overrides shorter client duration", `{"currentTime":64,"timeListened":2,"duration":59}`, 200, 64, 2},
		} {
			t.Run(ep.name+" "+tc.name, func(t *testing.T) {
				s := newBodyTestStack(t)
				s.createSession(t, "sid-timing")
				code, body := s.post(t, "/api/session/sid-timing/"+ep.suffix, "application/json", tc.body)
				if code != tc.code {
					t.Fatalf("%s = %d %s, want %d", tc.name, code, body, tc.code)
				}
				position, listened := s.sessionState(t, "sid-timing")
				if tc.code == 400 {
					if position != 0 || listened != 0 {
						t.Fatalf("rejected %s mutated session: position %v listened %v", ep.name, position, listened)
					}
					if n := s.progressCount(t); n != 0 {
						t.Fatalf("rejected %s wrote %d progress rows", ep.name, n)
					}
				} else if position != tc.wantPosition || listened != tc.wantListened {
					t.Fatalf("accepted %s position/listened = %v/%v, want %v/%v", ep.name, position, listened, tc.wantPosition, tc.wantListened)
				}
				wantOpen := tc.code == 400 || ep.name == "sync"
				if open := s.sessionOpen(t, "sid-timing"); open != wantOpen {
					t.Fatalf("session open after %s = %t, want %t", tc.name, open, wantOpen)
				}
			})
		}
	}
}

func TestSessionUnknownServerDurationFallsBackToClientBound(t *testing.T) {
	s := newBodyTestStack(t)
	ed := s.addEdition(t, 0)
	s.createSessionOn(t, "sid-unknown", ed)

	code, body := s.post(t, "/api/session/sid-unknown/sync", "application/json", `{"currentTime":100,"timeListened":1,"duration":200}`)
	if code != 200 {
		t.Fatalf("bounded position with unknown server duration = %d %s, want 200", code, body)
	}
	position, listened := s.sessionState(t, "sid-unknown")
	if position != 100 || listened != 1 {
		t.Fatalf("sync on unknown duration left position/listened %v/%v, want 100/1", position, listened)
	}

	code, body = s.post(t, "/api/session/sid-unknown/sync", "application/json", `{"currentTime":3000000,"timeListened":1,"duration":0}`)
	if code != 400 {
		t.Fatalf("position beyond the unknown-duration policy cap = %d %s, want 400", code, body)
	}
	position, listened = s.sessionState(t, "sid-unknown")
	if position != 100 || listened != 1 {
		t.Fatalf("rejected sync mutated session: position %v listened %v", position, listened)
	}
	if !s.sessionOpen(t, "sid-unknown") {
		t.Fatal("rejected sync must leave the session open")
	}
}

func TestSessionTimeListenedBounds(t *testing.T) {
	for _, ep := range []struct{ name, suffix string }{
		{"sync", "sync"},
		{"close", "close"},
	} {
		for _, listened := range []string{"-1", "1e300", "2592001"} {
			t.Run(ep.name+" timeListened "+listened, func(t *testing.T) {
				s := newBodyTestStack(t)
				s.createSession(t, "sid-listened")
				code, body := s.post(t, "/api/session/sid-listened/"+ep.suffix, "application/json",
					fmt.Sprintf(`{"currentTime":30,"timeListened":%s,"duration":60}`, listened))
				if code != 400 {
					t.Fatalf("%s timeListened %s = %d %s, want 400", ep.name, listened, code, body)
				}
				position, got := s.sessionState(t, "sid-listened")
				if position != 0 || got != 0 {
					t.Fatalf("rejected %s mutated session: position %v listened %v", ep.name, position, got)
				}
				if n := s.progressCount(t); n != 0 {
					t.Fatalf("rejected %s wrote %d progress rows", ep.name, n)
				}
				if !s.sessionOpen(t, "sid-listened") {
					t.Fatalf("rejected %s must leave the session open", ep.name)
				}
			})
		}
		t.Run(ep.name+" normal delta", func(t *testing.T) {
			s := newBodyTestStack(t)
			s.createSession(t, "sid-listened-ok")
			code, body := s.post(t, "/api/session/sid-listened-ok/"+ep.suffix, "application/json", `{"currentTime":30,"timeListened":15,"duration":60}`)
			if code != 200 {
				t.Fatalf("%s with normal delta = %d %s, want 200", ep.name, code, body)
			}
			position, listened := s.sessionState(t, "sid-listened-ok")
			if position != 30 || listened != 15 {
				t.Fatalf("%s position/listened = %v/%v, want 30/15", ep.name, position, listened)
			}
		})
	}
}

func TestPostProgressValidatesAgainstServerDurationAndListened(t *testing.T) {
	s := newBodyTestStack(t)
	code, body := s.post(t, fmt.Sprintf("/api/me/progress/%d", s.edID), "application/json", `{"currentTime":70,"timeListened":1,"duration":1000}`)
	if code != 400 {
		t.Fatalf("postProgress with inflated duration = %d %s, want 400", code, body)
	}
	if n := s.progressCount(t); n != 0 {
		t.Fatalf("rejected postProgress wrote %d rows", n)
	}
	code, body = s.post(t, fmt.Sprintf("/api/me/progress/%d", s.edID), "application/json", `{"currentTime":30,"timeListened":1e300,"duration":60}`)
	if code != 400 {
		t.Fatalf("postProgress with oversized timeListened = %d %s, want 400", code, body)
	}
	if n := s.progressCount(t); n != 0 {
		t.Fatalf("rejected postProgress wrote %d rows", n)
	}
	code, body = s.post(t, fmt.Sprintf("/api/me/progress/%d", s.edID), "application/json", `{"currentTime":30,"timeListened":10,"duration":60}`)
	if code != 200 {
		t.Fatalf("valid postProgress = %d %s, want 200", code, body)
	}
}

func TestStoreHelpersRejectInvalidListenedDeltas(t *testing.T) {
	s := newBodyTestStack(t)
	s.createSession(t, "sid-store")
	sess, err := s.db.Session("sid-store")
	if err != nil {
		t.Fatal(err)
	}
	dur := 60.0
	p := &store.Progress{UserID: 1, EditionID: s.edID, EditionPositionSecs: 30, DurationSecs: &dur}

	if err := s.db.UpdateSessionWithProgress(sess, p, 1e300); err == nil {
		t.Fatal("UpdateSessionWithProgress must reject an oversized delta")
	}
	if err := s.db.CloseSessionWithProgress(sess, p, -1); err == nil {
		t.Fatal("CloseSessionWithProgress must reject a negative delta")
	}
	if err := s.db.CloseSession("sid-store", 0, 2592001); err == nil {
		t.Fatal("CloseSession must reject a delta beyond the policy cap")
	}
	position, listened := s.sessionState(t, "sid-store")
	if position != 0 || listened != 0 {
		t.Fatalf("rejected store deltas mutated session: position %v listened %v", position, listened)
	}
	if !s.sessionOpen(t, "sid-store") {
		t.Fatal("rejected store deltas must leave the session open")
	}
}
