package jellyfin

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/libteca/libteca/internal/auth"
	"github.com/libteca/libteca/internal/store"
	"github.com/neutron-build/neutron/go/neutron"
)

func TestWriteFrameHelloVector(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, opText, []byte("Hello")); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x81, 0x05, 0x48, 0x65, 0x6c, 0x6c, 0x6f}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("got %x, want %x", buf.Bytes(), want)
	}
}

func TestReadFrameMaskedHelloVector(t *testing.T) {
	raw := []byte{0x81, 0x85, 0x37, 0xfa, 0x21, 0x3d, 0x7f, 0x9f, 0x4d, 0x51, 0x58}
	op, p, err := readFrame(bufio.NewReader(bytes.NewReader(raw)), true)
	if err != nil {
		t.Fatal(err)
	}
	if op != opText || string(p) != "Hello" {
		t.Fatalf("op=%d payload=%q", op, p)
	}
}

func TestReadFrameUnmaskedClientRejected(t *testing.T) {
	raw := []byte{0x81, 0x05, 0x48, 0x65, 0x6c, 0x6c, 0x6f}
	if _, _, err := readFrame(bufio.NewReader(bytes.NewReader(raw)), true); err == nil {
		t.Fatal("unmasked client frame accepted")
	}
}

func TestFrameLengthRoundtrip(t *testing.T) {
	for _, n := range []int{0, 125, 126, 65535, 65536, 100000} {
		payload := make([]byte, n)
		for i := range payload {
			payload[i] = byte(i)
		}
		var buf bytes.Buffer
		if err := writeFrame(&buf, opText, payload); err != nil {
			t.Fatal(err)
		}
		op, got, err := readFrame(bufio.NewReader(&buf), false)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		if op != opText || !bytes.Equal(got, payload) {
			t.Fatalf("n=%d roundtrip mismatch", n)
		}
	}
}

func TestReadFrameTooBig(t *testing.T) {
	raw := []byte{0x81, 0xff}
	ext := make([]byte, 8)
	binary.BigEndian.PutUint64(ext, wsMaxMessage+1)
	raw = append(append(raw, ext...), 1, 2, 3, 4)
	_, _, err := readFrame(bufio.NewReader(bytes.NewReader(raw)), true)
	var we *wsErr
	if !errors.As(err, &we) || we.code != 1009 {
		t.Fatalf("err = %v, want 1009", err)
	}
}

func TestControlFrameLimits(t *testing.T) {
	raw := []byte{0x89, 0xfe, 0x00, 0x7e}
	raw = append(raw, 1, 2, 3, 4)
	raw = append(raw, make([]byte, 126)...)
	if _, _, err := readFrame(bufio.NewReader(bytes.NewReader(raw)), true); err == nil {
		t.Fatal("oversize control frame accepted")
	}

	var buf bytes.Buffer
	if err := writeFrame(&buf, opClose, closePayload(1000)); err != nil {
		t.Fatal(err)
	}
	want := []byte{0x88, 0x02, 0x03, 0xe8}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("close frame = %x, want %x", buf.Bytes(), want)
	}
}

func TestReadFrameRejectsContinuation(t *testing.T) {
	raw := []byte{0x00, 0x81, 1, 2, 3, 4, 'a'}
	if _, _, err := readFrame(bufio.NewReader(bytes.NewReader(raw)), true); err == nil {
		t.Fatal("continuation frame accepted")
	}
}

type fakeClient struct {
	dev     string
	ch      chan []byte
	dropped bool
}

func (f *fakeClient) enqueue(msg []byte) bool {
	select {
	case f.ch <- msg:
		return true
	default:
		return false
	}
}

func (f *fakeClient) shutdown()      { f.dropped = true }
func (f *fakeClient) device() string { return f.dev }

func TestHubBroadcastSubscribedOnly(t *testing.T) {
	h := newHub()
	a := &fakeClient{ch: make(chan []byte, 4)}
	b := &fakeClient{ch: make(chan []byte, 4)}
	h.register(a)
	h.register(b)
	h.subscribe(a)
	h.broadcast([]byte("x"))
	select {
	case m := <-a.ch:
		if string(m) != "x" {
			t.Fatalf("got %q", m)
		}
	default:
		t.Fatal("subscribed client missed broadcast")
	}
	select {
	case <-b.ch:
		t.Fatal("unsubscribed client received broadcast")
	default:
	}
}

func TestHubSlowConsumerDropped(t *testing.T) {
	h := newHub()
	f := &fakeClient{dev: "d", ch: make(chan []byte, 1)}
	f.ch <- []byte("full")
	h.register(f)
	h.push(f, []byte("m"))
	if !f.dropped {
		t.Fatal("slow consumer not dropped")
	}
	h.register(&fakeClient{ch: make(chan []byte, 1)})
	if got := len(h.conns); got != 1 {
		t.Fatalf("conns = %d, want 1", got)
	}
}

func TestHubSendToDevice(t *testing.T) {
	h := newHub()
	a := &fakeClient{dev: "a", ch: make(chan []byte, 4)}
	b := &fakeClient{dev: "b", ch: make(chan []byte, 4)}
	h.register(a)
	h.register(b)
	if !h.sendTo("b", []byte("m")) {
		t.Fatal("sendTo missed device b")
	}
	select {
	case m := <-b.ch:
		if string(m) != "m" {
			t.Fatalf("b got %q", m)
		}
	default:
		t.Fatal("b channel empty")
	}
	select {
	case <-a.ch:
		t.Fatal("a received b's message")
	default:
	}
	if h.sendTo("nope", []byte("m")) {
		t.Fatal("sendTo to unknown device succeeded")
	}
}

func TestHubUnregisterAndSubscribeGone(t *testing.T) {
	h := newHub()
	a := &fakeClient{ch: make(chan []byte, 4)}
	h.register(a)
	h.subscribe(a)
	h.unregister(a)
	h.subscribe(a)
	h.broadcast([]byte("x"))
	select {
	case <-a.ch:
		t.Fatal("unregistered client received broadcast")
	default:
	}
}

func TestHubSessionsLifecycle(t *testing.T) {
	h := newHub()
	h.update(&liveSession{DeviceID: "tv", PlaySessionID: "ps-1", ItemID: "e1"})
	if d := h.deviceForPlaySession("ps-1"); d != "tv" {
		t.Fatalf("deviceForPlaySession = %q", d)
	}
	if d := h.deviceForPlaySession("other"); d != "" {
		t.Fatalf("unknown psid matched %q", d)
	}
	snap := h.snapshot()
	if len(snap) != 1 || snap[0].DeviceID != "tv" {
		t.Fatalf("snapshot = %+v", snap)
	}
	h.remove("tv")
	if snap := h.snapshot(); len(snap) != 0 {
		t.Fatalf("snapshot after remove = %+v", snap)
	}
}

type testWSClient struct {
	conn net.Conn
	br   *bufio.Reader
}

func wsDialStatus(t *testing.T, url, apiKey, deviceID string) (*testWSClient, string) {
	t.Helper()
	host := strings.TrimPrefix(url, "http://")
	conn, err := net.DialTimeout("tcp", host, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	c := &testWSClient{conn: conn, br: bufio.NewReader(conn)}
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	var sb strings.Builder
	sb.WriteString("GET /socket?api_key=" + apiKey + " HTTP/1.1\r\n")
	sb.WriteString("Host: " + host + "\r\n")
	sb.WriteString("Upgrade: websocket\r\nConnection: Upgrade\r\n")
	sb.WriteString("Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n")
	if deviceID != "" {
		sb.WriteString(`X-Emby-Authorization: MediaBrowser Client="tclient", Device="tbox", DeviceId="` + deviceID + `", Version="1.0"` + "\r\n")
	}
	sb.WriteString("\r\n")
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte(sb.String())); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	status, err := c.br.ReadString('\n')
	if err != nil {
		conn.Close()
		t.Fatal(err)
	}
	return c, status
}

func wsDial(t *testing.T, url, apiKey, deviceID string) *testWSClient {
	t.Helper()
	c, status := wsDialStatus(t, url, apiKey, deviceID)
	if !strings.Contains(status, "101") {
		c.conn.Close()
		t.Fatalf("upgrade status %q", status)
	}
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	accept := ""
	for {
		line, err := c.br.ReadString('\n')
		if err != nil {
			c.conn.Close()
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
		if i := strings.Index(line, ":"); i > 0 && strings.EqualFold(strings.TrimSpace(line[:i]), "Sec-WebSocket-Accept") {
			accept = strings.TrimSpace(line[i+1:])
		}
	}
	if accept != wsAcceptKey(key) {
		c.conn.Close()
		t.Fatalf("Sec-WebSocket-Accept = %q", accept)
	}
	c.conn.SetDeadline(time.Time{})
	return c
}

func (c *testWSClient) send(op byte, payload []byte) {
	key := [4]byte{0x11, 0x22, 0x33, 0x44}
	masked := append([]byte(nil), payload...)
	unmask(masked, key)
	frame := []byte{0x80 | op, 0x80 | byte(len(payload))}
	frame = append(frame, key[:]...)
	frame = append(frame, masked...)
	c.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	c.conn.Write(frame)
}

func (c *testWSClient) sendText(s string) { c.send(opText, []byte(s)) }

func (c *testWSClient) readText(t *testing.T) string {
	t.Helper()
	for {
		c.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		op, p, err := readFrame(c.br, false)
		if err != nil {
			t.Fatalf("ws read: %v", err)
		}
		switch op {
		case opText:
			return string(p)
		case opPing, opPong:
		case opClose:
			t.Fatalf("unexpected close frame: %x", p)
		default:
			t.Fatalf("unexpected opcode %d", op)
		}
	}
}

func (c *testWSClient) readMessage(t *testing.T) (string, json.RawMessage) {
	t.Helper()
	var m struct {
		MessageType string          `json:"MessageType"`
		Data        json.RawMessage `json:"Data"`
	}
	if err := json.Unmarshal([]byte(c.readText(t)), &m); err != nil {
		t.Fatalf("bad message: %v", err)
	}
	return m.MessageType, m.Data
}

func wsTestStack(t *testing.T) (*httptest.Server, *API, string, int64, string) {
	t.Helper()
	socketHub = newHub()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "ws.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	now := time.Now().UnixMilli()
	res, err := db.Exec(`INSERT INTO users (name, password_hash, is_admin, created_at, updated_at) VALUES ('u','x',1,?,?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	uid, _ := res.LastInsertId()
	token, err := auth.IssueToken(db, uid, "ws-test")
	if err != nil {
		t.Fatal(err)
	}
	libRoot := filepath.Join(dir, "tv")
	lib, err := db.AddLibrary("TV", "tv", libRoot)
	if err != nil {
		t.Fatal(err)
	}
	work, err := db.UpsertWork(&store.Work{LibraryID: lib, Title: "Show"})
	if err != nil {
		t.Fatal(err)
	}
	dur := 1800.0
	season, ep := 1, 1
	ed, err := db.UpsertEdition(&store.Edition{
		WorkID: work, Format: "video", Title: "Pilot", DurationSecs: &dur,
		SeasonNum: &season, EpisodeNum: &ep,
	})
	if err != nil {
		t.Fatal(err)
	}
	h264, mkv := "h264", "mkv"
	if err := db.UpsertFile(&store.FileRec{
		EditionID: ed, Path: filepath.Join(libRoot, "pilot.mkv"), Seq: 1,
		SizeBytes: 4, VideoCodec: &h264, Container: &mkv,
		DurationSecs: dur, Chapters: "[]",
	}); err != nil {
		t.Fatal(err)
	}
	r := neutron.New().Router()
	jf := New(db, dir, nil)
	r.HandleFunc("GET /socket", jf.handleSocket)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, jf, token, uid, "e" + strconv.FormatInt(ed, 10)
}

func playbackRequest(t *testing.T, path, deviceID string, uid int64) *http.Request {
	t.Helper()
	req := httptest.NewRequest("POST", path, nil)
	req.Header.Set("X-Emby-Authorization", `MediaBrowser Client="JMP", Device="Living Room TV", DeviceId="`+deviceID+`", Version="10.10"`)
	return req.WithContext(withUser(req, uid))
}

func TestSocketAuthRejected(t *testing.T) {
	srv, _, _, _, _ := wsTestStack(t)
	c, status := wsDialStatus(t, srv.URL, "bad-token", "dev")
	defer c.conn.Close()
	if !strings.Contains(status, "401") {
		t.Fatalf("status %q, want 401", status)
	}
}

func TestSocketEndToEnd(t *testing.T) {
	srv, jf, token, uid, itemID := wsTestStack(t)

	tv := wsDial(t, srv.URL, token, "tv-dev")
	t.Cleanup(func() { tv.conn.Close() })
	remote := wsDial(t, srv.URL, token, "remote-dev")
	t.Cleanup(func() { remote.conn.Close() })

	typ, data := tv.readMessage(t)
	if typ != "ForceKeepAlive" || string(data) != `"1800"` {
		t.Fatalf("first message = %s %s", typ, data)
	}
	if typ, _ := remote.readMessage(t); typ != "ForceKeepAlive" {
		t.Fatal("remote missing ForceKeepAlive")
	}

	remote.sendText(`{"MessageType":"SessionsStart"}`)
	typ, data = remote.readMessage(t)
	if typ != "Sessions" || string(data) != "[]" {
		t.Fatalf("snapshot = %s %s", typ, data)
	}

	jf.ReportPlayback(playbackRequest(t, "/Sessions/Playing/Progress", "tv-dev", uid), itemID, "ps-1", 123450000, true)
	typ, data = remote.readMessage(t)
	if typ != "Sessions" {
		t.Fatalf("got %s", typ)
	}
	var sessions []wsSessionDTO
	if err := json.Unmarshal(data, &sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %+v", sessions)
	}
	s := sessions[0]
	if s.Id != "tv-dev" || s.UserId != strconv.FormatInt(uid, 10) || s.UserName != "u" ||
		s.Client != "JMP" || s.DeviceName != "Living Room TV" || s.PlaySessionId != "ps-1" {
		t.Fatalf("session = %+v", s)
	}
	if s.PlayState == nil || s.PlayState.PositionTicks != 123450000 || !s.PlayState.IsPaused {
		t.Fatalf("playState = %+v", s.PlayState)
	}
	if s.NowPlayingItem == nil || s.NowPlayingItem["Id"] != itemID || s.NowPlayingItem["Name"] != "Pilot" {
		t.Fatalf("nowPlaying = %+v", s.NowPlayingItem)
	}

	remote.sendText(`{"MessageType":"Playstate","Data":{"Command":"Pause","DeviceId":"tv-dev","SeekPositionTicks":555}}`)
	typ, data = tv.readMessage(t)
	if typ != "Playstate" || !strings.Contains(string(data), `"Command":"Pause"`) {
		t.Fatalf("passthrough = %s %s", typ, data)
	}

	remote.sendText(`{"MessageType":"Play","Data":{"PlayCommand":"PlayNow","ItemIds":["` + itemID + `"],"PlaySessionId":"ps-1"}}`)
	typ, data = tv.readMessage(t)
	if typ != "Play" || !strings.Contains(string(data), `"PlaySessionId":"ps-1"`) {
		t.Fatalf("play forward = %s %s", typ, data)
	}

	remote.sendText(`{"MessageType":"KeepAlive"}`)
	jf.ReportPlayback(playbackRequest(t, "/Sessions/Playing/Stopped", "tv-dev", uid), itemID, "ps-1", 0, false)
	typ, data = remote.readMessage(t)
	if typ != "Sessions" || string(data) != "[]" {
		t.Fatalf("after stop = %s %s", typ, data)
	}
}
