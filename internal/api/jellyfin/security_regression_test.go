package jellyfin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/api/core"
	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

func securityHTTP(t *testing.T, server, token, method, path, device, body string, want int) {
	t.Helper()
	req, err := http.NewRequest(method, server+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Emby-Token", token)
	}
	req.Header.Set("X-Emby-Authorization", `MediaBrowser DeviceId="`+device+`"`)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		t.Fatalf("%s %s: %d %s; want %d", method, path, resp.StatusCode, bytes, want)
	}
}

func securityServer(t *testing.T) (*httptest.Server, *API, string, int64, string) {
	t.Helper()
	old, jf, token, user, item := wsTestStack(t)
	old.Close()
	r := neutron.New().Router()
	jf.Mount(r)
	c := core.New(jf.DB, jf.Dir)
	c.Mount(r.Group("/api/core", auth.MiddlewareWithMediaCookie(jf.DB, core.MediaRequest)))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	t.Cleanup(jf.CloseSockets)
	return srv, jf, token, user, item
}

func securityUser(t *testing.T, jf *API, name string) (int64, string) {
	t.Helper()
	id, err := jf.DB.CreateUser(name, "test-unused-hash", false)
	if err != nil {
		t.Fatal(err)
	}
	token, err := auth.IssueToken(jf.DB, id, name)
	if err != nil {
		t.Fatal(err)
	}
	return id, token
}

func securitySocket(t *testing.T, srv *httptest.Server, token, device string) *testWSClient {
	t.Helper()
	c := wsDial(t, srv.URL, token, device)
	t.Cleanup(func() { c.conn.Close() })
	if typ, _ := c.readMessage(t); typ != "ForceKeepAlive" {
		t.Fatal(typ)
	}
	return c
}

func expectOwnerSessions(t *testing.T, c *testWSClient, user int64, count int) []wsSessionDTO {
	t.Helper()
	typ, data := c.readMessage(t)
	var sessions []wsSessionDTO
	if typ != "Sessions" || json.Unmarshal(data, &sessions) != nil {
		t.Fatalf("%s %s", typ, data)
	}
	if len(sessions) != count {
		t.Fatalf("sessions=%s; want %d", data, count)
	}
	for _, session := range sessions {
		if session.UserId != strconv.FormatInt(user, 10) {
			t.Fatalf("foreign session: %s", data)
		}
	}
	return sessions
}

func TestSecurityLiveSessionsOwnersHTTP(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprint("sameDevice=", shared), func(t *testing.T) {
			srv, jf, tokenA, userA, item := securityServer(t)
			userB, tokenB := securityUser(t, jf, "b")
			deviceA, deviceB := "a-tv", "b-tv"
			if shared {
				deviceB = deviceA
			}
			a := securitySocket(t, srv, tokenA, "a-remote")
			b := securitySocket(t, srv, tokenB, "b-remote")
			a.sendText(`{"MessageType":"SessionsStart"}`)
			b.sendText(`{"MessageType":"SessionsStart"}`)
			expectOwnerSessions(t, a, userA, 0)
			expectOwnerSessions(t, b, userB, 0)
			play := func(token, path, device string, position int) {
				securityHTTP(t, srv.URL, token, "POST", "/Sessions/Playing"+path, device, fmt.Sprintf(`{"ItemId":%q,"PositionTicks":%d,"PlaySessionId":"same-ps"}`, item, position), 200)
			}
			play(tokenA, "", deviceA, 10)
			expectOwnerSessions(t, a, userA, 1)
			expectOwnerSessions(t, b, userB, 0)
			late := securitySocket(t, srv, tokenB, "late")
			late.sendText(`{"MessageType":"SessionsStart"}`)
			expectOwnerSessions(t, late, userB, 0)
			play(tokenB, "/Progress", deviceB, 20)
			sessionsA := expectOwnerSessions(t, a, userA, 1)
			expectOwnerSessions(t, b, userB, 1)
			expectOwnerSessions(t, late, userB, 1)
			if sessionsA[0].PlayState.PositionTicks != 10 {
				t.Fatal("B overwrote A")
			}
			play(tokenB, "/Stopped", deviceB, 30)
			expectOwnerSessions(t, a, userA, 1)
			expectOwnerSessions(t, b, userB, 0)
			expectOwnerSessions(t, late, userB, 0)
			play(tokenA, "/Progress", deviceA, 40)
			expectOwnerSessions(t, a, userA, 1)
			expectOwnerSessions(t, b, userB, 0)
			expectOwnerSessions(t, late, userB, 0)
			play(tokenA, "/Stopped", deviceA, 50)
			expectOwnerSessions(t, a, userA, 0)
			expectOwnerSessions(t, b, userB, 0)
			expectOwnerSessions(t, late, userB, 0)
		})
	}
}

func TestSecuritySocketExplicitRevocationHTTP(t *testing.T) {
	for _, action := range []string{"logout", "password", "admin-revoke", "delete"} {
		t.Run(action, func(t *testing.T) {
			srv, jf, admin, _, item := securityServer(t)
			user, token := securityUser(t, jf, "revokee")
			_, otherToken := securityUser(t, jf, "other")
			independentlyIssued, err := auth.IssueToken(jf.DB, user, "other-token")
			if err != nil {
				t.Fatal(err)
			}
			source := securitySocket(t, srv, token, "remote")
			target := securitySocket(t, srv, independentlyIssued, "tv")
			other := securitySocket(t, srv, otherToken, "tv")
			source.sendText(`{"MessageType":"SessionsStart"}`)
			expectOwnerSessions(t, source, user, 0)
			source.sendText(`{"MessageType":"Playstate","Data":{"DeviceId":"tv","Command":"Pause"}}`)
			if typ, _ := target.readMessage(t); typ != "Playstate" {
				t.Fatal(typ)
			}
			switch action {
			case "logout":
				securityHTTP(t, srv.URL, token, "POST", "/api/core/logout", "", "", 204)
			case "password":
				securityHTTP(t, srv.URL, admin, "POST", fmt.Sprintf("/api/core/users/%d/password", user), "", `{"password":"new-password"}`, 200)
			case "admin-revoke":
				tokens, err := jf.DB.Tokens(user)
				if err != nil {
					t.Fatal(err)
				}
				securityHTTP(t, srv.URL, admin, "DELETE", fmt.Sprintf("/api/core/tokens/%d", tokens[0].ID), "", "", 200)
			case "delete":
				securityHTTP(t, srv.URL, admin, "DELETE", fmt.Sprintf("/api/core/users/%d", user), "", "", 200)
			}
			securityHTTP(t, srv.URL, token, "GET", "/Sessions", "", "", 401)
			source.sendText(`{"MessageType":"Playstate","Data":{"DeviceId":"tv","Command":"Unpause"}}`)
			source.sendText(`{"MessageType":"SessionsStart"}`)
			if action == "logout" || action == "admin-revoke" {
				securityHTTP(t, srv.URL, independentlyIssued, "POST", "/Sessions/Playing", "tv", fmt.Sprintf(`{"ItemId":%q,"PositionTicks":12}`, item), 200)
			} else {
				securityHTTP(t, srv.URL, independentlyIssued, "GET", "/Sessions", "", "", 401)
			}
			source.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
			op, payload, err := readFrame(source.br, false)
			if err == nil && op != opClose {
				t.Fatalf("revoked socket received %d %s", op, payload)
			}
			if err != nil {
				if timeout, ok := err.(interface{ Timeout() bool }); ok && timeout.Timeout() {
					t.Fatal("revoked socket remained open")
				}
			}
			for _, c := range []*testWSClient{target, other} {
				c.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				op, payload, err := readFrame(c.br, false)
				if err == nil && op == opText {
					t.Fatalf("revoked command delivered: %s", payload)
				}
			}
		})
	}
}

func TestSecurityPodcastPhysicalRootsHTTP(t *testing.T) {
	for _, mode := range []string{"native", "foreign", "outside", "final-symlink", "ancestor-symlink", "internal-symlink", "directory", "missing-episode"} {
		t.Run(mode, func(t *testing.T) {
			srv, jf, token, _, _ := securityServer(t)
			dir := filepath.Join(jf.Dir, "podcasts")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(jf.Dir, "outside.mp3")
			if err := os.WriteFile(outside, []byte("outside-secret"), 0o600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "episode.mp3")
			if err := os.WriteFile(path, []byte("episode-bytes"), 0o600); err != nil {
				t.Fatal(err)
			}
			lib, err := jf.DB.AddLibrary("Podcasts", "podcasts", dir)
			if err != nil {
				t.Fatal(err)
			}
			var source any
			var mutate func() error
			switch mode {
			case "foreign":
				root := filepath.Join(jf.Dir, "foreign")
				if err := os.MkdirAll(root, 0o700); err != nil {
					t.Fatal(err)
				}
				id, err := jf.DB.AddLibrary("Foreign", "podcasts", root)
				if err != nil {
					t.Fatal(err)
				}
				source = id
				path = filepath.Join(root, "foreign.mp3")
				if err := os.WriteFile(path, []byte("episode-bytes"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "outside":
				path = outside
			case "final-symlink", "internal-symlink":
				target := outside
				if mode == "internal-symlink" {
					target = filepath.Join(dir, "target.mp3")
					if err := os.WriteFile(target, []byte("episode-bytes"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				mutate = func() error {
					if err := os.Remove(path); err != nil {
						return err
					}
					return os.Symlink(target, path)
				}
			case "ancestor-symlink":
				mutate = func() error { return os.Symlink(jf.Dir, filepath.Join(dir, "alias")) }
				path = filepath.Join(dir, "alias", "outside.mp3")
			case "directory":
				path = dir
			}
			now := time.Now().UnixMilli()
			res, err := jf.DB.Exec(`INSERT INTO podcasts(library_id,feed_url,title,created_at) VALUES(?,?,?,?)`, lib, "https://example.test/feed", "pod", now)
			if err != nil {
				t.Fatal(err)
			}
			pod, _ := res.LastInsertId()
			res, err = jf.DB.Exec(`INSERT INTO files(path,seq,size_bytes,mtime_secs,source_library_id,duration_secs,probed_at) VALUES(?,1,13,?,?,1,0)`, path, now/1000, source)
			if err != nil {
				t.Fatal(err)
			}
			file, _ := res.LastInsertId()
			res, err = jf.DB.Exec(`INSERT INTO podcast_episodes(podcast_id,guid,title,enclosure_url,file_id,created_at) VALUES(?,?,?,?,?,?)`, pod, mode, "ep", "https://example.test/ep", file, now)
			if err != nil {
				t.Fatal(err)
			}
			ep, _ := res.LastInsertId()
			if mutate != nil {
				if err := mutate(); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing-episode" {
				ep += 1000
			}
			req, err := http.NewRequest("GET", fmt.Sprintf("%s/Audio/podcast/pe%d/stream", srv.URL, ep), nil)
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("X-Emby-Token", token)
			req.Header.Set("Range", "bytes=0-6")
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "native" || mode == "foreign" {
				if resp.StatusCode != 206 || string(data) != "episode" {
					t.Fatalf("%d %q", resp.StatusCode, data)
				}
			} else if resp.StatusCode != 404 || strings.Contains(string(data), "outside-secret") {
				t.Fatalf("%d %q", resp.StatusCode, data)
			}
		})
	}
}

func TestSecurityCoreExplicitQueryWriteCompatibility(t *testing.T) {
	srv, _, token, _, _ := securityServer(t)
	securityHTTP(t, srv.URL, "", "POST", "/api/core/tokens?token="+token, "", `{"label":"query-compatible"}`, 201)
	securityHTTP(t, srv.URL, "", "GET", "/api/core/progress/1?token="+token, "", "", 401)
	req, err := http.NewRequest("POST", srv.URL+"/api/core/tokens", strings.NewReader(`{"label":"cookie-only"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: auth.MediaCookieName, Value: token})
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("cookie-only mutation accepted: %d", resp.StatusCode)
	}
}

func TestSecurityPlaySessionLookupAuthenticatedOwnerHTTP(t *testing.T) {
	srv, jf, tokenA, _, item := securityServer(t)
	_, tokenB := securityUser(t, jf, "b")
	remote := securitySocket(t, srv, tokenA, "remote")
	a := securitySocket(t, srv, tokenA, "a-tv")
	b := securitySocket(t, srv, tokenB, "b-tv")
	for _, entry := range []struct{ token, device string }{{tokenA, "a-tv"}, {tokenB, "b-tv"}} {
		securityHTTP(t, srv.URL, entry.token, "POST", "/Sessions/Playing", entry.device, fmt.Sprintf(`{"ItemId":%q,"PositionTicks":1,"PlaySessionId":"shared-ps"}`, item), 200)
	}
	remote.sendText(`{"MessageType":"Playstate","Data":{"PlaySessionId":"shared-ps","Command":"Pause"}}`)
	if typ, _ := a.readMessage(t); typ != "Playstate" {
		t.Fatal(typ)
	}
	b.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	op, payload, err := readFrame(b.br, false)
	if err == nil && op == opText {
		t.Fatalf("foreign play-session target received command: %s", payload)
	}
}

func TestSecurityQueuedCommandSenderFenceHTTP(t *testing.T) {
	srv, jf, _, _, _ := securityServer(t)
	user, sourceToken := securityUser(t, jf, "queued-sender")
	targetToken, err := auth.IssueToken(jf.DB, user, "independent-target")
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	target := &socketConn{
		uidv: user, conn: server, w: bufio.NewWriter(server),
		send: make(chan socketMessage, 4), pong: make(chan []byte, 1), closeReq: make(chan []byte, 1), done: make(chan struct{}),
		authority: func(action func() error, senders ...store.SocketCredential) error {
			return jf.DB.WithSocketAuthority(store.TokenDigest(targetToken), user, action, senders...)
		},
	}
	t.Cleanup(target.shutdown)
	t.Cleanup(func() { client.Close() })
	if !target.enqueueMessage([]byte(`{"MessageType":"Playstate"}`), store.SocketCredential{Digest: store.TokenDigest(sourceToken), UserID: user}) {
		t.Fatal("queue admission failed")
	}
	securityHTTP(t, srv.URL, sourceToken, "POST", "/api/core/logout", "", "", 204)
	go target.writeLoop()
	if !target.enqueue([]byte(`{"MessageType":"StillAuthorized"}`)) {
		t.Fatal("target admission failed")
	}
	client.SetReadDeadline(time.Now().Add(3 * time.Second))
	op, data, err := readFrame(bufio.NewReader(client), false)
	if err != nil || op != opText || string(data) != `{"MessageType":"StillAuthorized"}` {
		t.Fatalf("revoked queued command escaped or valid target lost authority: %d %s %v", op, data, err)
	}
}

func TestSecuritySessionsStartBoundedWhileSocketWorkStalls(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ws.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Now().UnixMilli()
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('u','x',1,?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	user, _ := res.LastInsertId()
	token, err := auth.IssueToken(db, user, "stall")
	if err != nil {
		t.Fatal(err)
	}
	jf := New(db, dir, nil)
	r := neutron.New().Router()
	r.HandleFunc("GET /socket", jf.handleSocket)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	db2, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db2.Close() })
	c := securitySocket(t, srv, token, "stall-tv")
	jf.hubv().update(&liveSession{DeviceID: "stall-tv", UserID: user, ItemID: "e1", PlaySessionID: "ps-stall"})
	db.SetMaxOpenConns(1)
	held, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	c.sendText(`{"MessageType":"SessionsStart"}`)
	start := time.Now()
	if err := db2.RevokeTokenByValue(token); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("revocation blocked %s behind stalled socket work", elapsed)
	}
	c.conn.SetReadDeadline(time.Now().Add(9 * time.Second))
	closed := false
	sawSessions := false
	for !closed {
		op, payload, err := readFrame(c.br, false)
		if err != nil {
			closed = true
			break
		}
		switch op {
		case opText:
			if strings.Contains(string(payload), `"Sessions"`) {
				sawSessions = true
			}
		case opPing, opPong:
		case opClose:
			closed = true
		}
	}
	if !closed {
		t.Fatal("socket still delivering under stalled work after revocation")
	}
	if sawSessions {
		t.Fatal("session snapshot delivered after revocation")
	}
}

func TestSecuritySessionDetailContextBoundsAndCancels(t *testing.T) {
	srv, jf, token, user, item := securityServer(t)
	_ = securitySocket(t, srv, token, "ctx-tv")
	jf.hubv().update(&liveSession{DeviceID: "ctx-tv", UserID: user, ItemID: item, PlaySessionID: "ps-ctx"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := jf.sessionsMessageFor(ctx, user)
	if strings.Contains(string(cancelled), "ctx-tv") {
		t.Fatalf("cancelled detail context still produced sessions: %s", cancelled)
	}
	fresh := jf.sessionsMessageFor(context.Background(), user)
	if !strings.Contains(string(fresh), "ctx-tv") {
		t.Fatalf("fresh detail context lost owner sessions: %s", fresh)
	}
	if !strings.Contains(string(fresh), `"NowPlayingItem"`) {
		t.Fatalf("fresh detail context lost now-playing detail: %s", fresh)
	}
	sc := &socketConn{done: make(chan struct{})}
	dctx, dcancel := sc.detailContext()
	close(sc.done)
	select {
	case <-dctx.Done():
	case <-time.After(time.Second):
		t.Fatal("socket shutdown must cancel the detail context")
	}
	dcancel()
}
