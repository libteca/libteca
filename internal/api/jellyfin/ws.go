package jellyfin

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libteca/libteca/internal/auth"
)

// Minimal server-side RFC 6455 (websocket) for the Jellyfin /socket.
// Limits: text frames only (binary tolerated, ignored), no continuation or
// fragmented frames, max frame payload 1 MiB, clients must mask (RFC), server
// never masks. No subprotocol negotiation.

const (
	wsGUID         = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	wsMaxMessage   = 1 << 20
	wsPingEvery    = 30 * time.Second
	wsWriteTimeout = 10 * time.Second
	wsKeepAlive    = "1800" // corpus: ForceKeepAlive Data value/shape unverified against 10.10 traffic
)

const (
	opCont   = 0x0
	opText   = 0x1
	opBinary = 0x2
	opClose  = 0x8
	opPing   = 0x9
	opPong   = 0xA
)

type wsErr struct {
	code int
	msg  string
}

func (e *wsErr) Error() string { return e.msg }

func wsProtoErr(msg string) error { return &wsErr{code: 1002, msg: msg} }
func wsTooBig() error             { return &wsErr{code: 1009, msg: "frame too large"} }

func closePayload(code int) []byte {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], uint16(code))
	return b[:]
}

func unmask(p []byte, key [4]byte) {
	for i := range p {
		p[i] ^= key[i%4]
	}
}

func writeFrame(w io.Writer, op byte, payload []byte) error {
	hdr := []byte{0x80 | op}
	n := len(payload)
	switch {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n <= 0xFFFF:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 127)
		ext := make([]byte, 8)
		binary.BigEndian.PutUint64(ext, uint64(n))
		hdr = append(hdr, ext...)
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// readFrame reads one full frame. requireMask enforces RFC client masking
// (server side passes true; tests parsing server frames pass false).
func readFrame(br *bufio.Reader, requireMask bool) (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(br, h[:]); err != nil {
		return 0, nil, err
	}
	fin := h[0]&0x80 != 0
	if h[0]&0x70 != 0 {
		return 0, nil, wsProtoErr("rsv bits set")
	}
	op := h[0] & 0x0F
	switch op {
	case opText, opBinary, opClose, opPing, opPong:
	default:
		return 0, nil, wsProtoErr("unsupported opcode") // no continuation/fragmentation
	}
	if !fin {
		return 0, nil, wsProtoErr("fragmented frame")
	}
	masked := h[1]&0x80 != 0
	ln := uint64(h[1] & 0x7F)
	if ln == 126 {
		var ext [2]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			return 0, nil, err
		}
		ln = uint64(binary.BigEndian.Uint16(ext[:]))
	} else if ln == 127 {
		var ext [8]byte
		if _, err := io.ReadFull(br, ext[:]); err != nil {
			return 0, nil, err
		}
		ln = binary.BigEndian.Uint64(ext[:])
		if ln&0x8000000000000000 != 0 {
			return 0, nil, wsProtoErr("length high bit set")
		}
	}
	if op >= opClose && ln > 125 {
		return 0, nil, wsProtoErr("control frame too large")
	}
	if ln > wsMaxMessage {
		return 0, nil, wsTooBig()
	}
	if requireMask && !masked {
		return 0, nil, wsProtoErr("client frame not masked")
	}
	var key [4]byte
	if masked {
		if _, err := io.ReadFull(br, key[:]); err != nil {
			return 0, nil, err
		}
	}
	payload := make([]byte, ln)
	if _, err := io.ReadFull(br, payload); err != nil {
		return 0, nil, err
	}
	if masked {
		unmask(payload, key)
	}
	return op, payload, nil
}

func wsAcceptKey(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

type wsClient interface {
	enqueue(msg []byte) bool
	shutdown()
	device() string
	user() int64
}

type liveSession struct {
	DeviceID      string
	PlaySessionID string
	ItemID        string
	UserID        int64
	PosTicks      int64
	Paused        bool
	Client        string
	DeviceName    string
}

type hub struct {
	mu       sync.Mutex
	conns    map[wsClient]bool // bool = subscribed to Sessions
	sessions map[string]*liveSession
	seq      uint64
}

func newHub() *hub {
	return &hub{conns: map[wsClient]bool{}, sessions: map[string]*liveSession{}}
}

func (h *hub) register(c wsClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.seq++
	h.conns[c] = false
}

func (h *hub) unregister(c wsClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, c)
}

func (h *hub) subscribe(c wsClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.conns[c]; ok {
		h.conns[c] = true
	}
}

func (h *hub) unsubscribe(c wsClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.conns[c]; ok {
		h.conns[c] = false
	}
}

func (h *hub) deliver(c wsClient, msg []byte) {
	if !c.enqueue(msg) {
		c.shutdown()
		delete(h.conns, c)
	}
}

func (h *hub) push(c wsClient, msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.conns[c]; ok {
		h.deliver(c, msg)
	}
}

func (h *hub) broadcast(msg []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c, sub := range h.conns {
		if sub {
			h.deliver(c, msg)
		}
	}
}

func (h *hub) sendTo(device string, msg []byte) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.conns {
		if c.device() == device {
			h.deliver(c, msg)
			return true
		}
	}
	return false
}

func (h *hub) update(s *liveSession) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessions[s.DeviceID] = s
}

func (h *hub) remove(device string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, device)
}

func (h *hub) snapshot() []*liveSession {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*liveSession, 0, len(h.sessions))
	for _, s := range h.sessions {
		out = append(out, s)
	}
	return out
}

// deviceOwnedBy reports whether the target device belongs to the sender's
// user. The sender's identity comes from its authenticated connection, not
// from playback state: sockets exist before any session is reported.
func (h *hub) deviceOwnedBy(device string, from wsClient) bool {
	senderUser := from.user()
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.sessions {
		if s.DeviceID == device {
			return s.UserID == senderUser
		}
	}
	return false
}

func (h *hub) deviceForPlaySession(psid string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.sessions {
		if s.PlaySessionID != "" && s.PlaySessionID == psid {
			return s.DeviceID
		}
	}
	return ""
}

func (h *hub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.conns {
		c.shutdown()
	}
	h.conns = map[wsClient]bool{}
}

type socketConn struct {
	uidv     int64
	dev      string
	conn     net.Conn
	br       *bufio.Reader
	w        *bufio.Writer
	send     chan []byte
	pong     chan []byte
	closeReq chan []byte
	done     chan struct{}
	once     sync.Once
}

func (c *socketConn) enqueue(msg []byte) bool {
	select {
	case c.send <- msg:
		return true
	default:
		return false
	}
}

func (c *socketConn) shutdown() {
	c.once.Do(func() {
		c.conn.Close()
		close(c.done)
	})
}

func (c *socketConn) device() string { return c.dev }
func (c *socketConn) user() int64   { return c.uidv }

func (c *socketConn) requestClose(code int) {
	select {
	case c.closeReq <- closePayload(code):
	default:
	}
}

func (c *socketConn) writeLoop() {
	ticker := time.NewTicker(wsPingEvery)
	defer ticker.Stop()
	for {
		var op byte
		var payload []byte
		quit := false
		select {
		case msg := <-c.send:
			op, payload = opText, msg
		case p := <-c.pong:
			op, payload = opPong, p
		case cl := <-c.closeReq:
			op, payload = opClose, cl
			quit = true
		case <-ticker.C:
			op, payload = opPing, nil // corpus: ping cadence unverified
		case <-c.done:
			op, payload = opClose, closePayload(1000)
			quit = true
		}
		c.conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
		err := writeFrame(c.w, op, payload)
		if err == nil {
			err = c.w.Flush()
		} else {
			c.w.Flush()
		}
		if err != nil || quit {
			c.conn.Close()
			return
		}
	}
}

func (c *socketConn) readLoop(a *API) {
	defer func() {
		a.hubv().unregister(c)
		c.shutdown()
	}()
	for {
		op, payload, err := readFrame(c.br, true)
		if err != nil {
			var we *wsErr
			if errors.As(err, &we) {
				c.requestClose(we.code)
			}
			return
		}
		switch op {
		case opText:
			a.handleText(c, payload)
		case opPing:
			select {
			case c.pong <- payload:
			default:
			}
		case opPong:
		case opBinary:
			slog.Debug("jellyfin ws: binary frame ignored")
		case opClose:
			code := 1000
			if len(payload) >= 2 {
				code = int(binary.BigEndian.Uint16(payload))
			}
			c.requestClose(code)
			return
		}
	}
}

func (a *API) handleText(c *socketConn, payload []byte) {
	var msg struct {
		MessageType string          `json:"MessageType"`
		Data        json.RawMessage `json:"Data"` // corpus: envelope shape {MessageType, Data} unverified against 10.10 traffic
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		slog.Debug("jellyfin ws: bad json", "err", err)
		return
	}
	switch msg.MessageType {
	case "KeepAlive":
	case "SessionsStart":
		a.hubv().subscribe(c)
		a.hubv().push(c, a.sessionsMessage())
	case "SessionsStop": // corpus: message name unverified
		a.hubv().unsubscribe(c)
	case "Play", "Playstate":
		a.forwardCommand(c, msg.MessageType, msg.Data)
	default:
		slog.Debug("jellyfin ws: ignored message", "type", msg.MessageType)
	}
}

func (a *API) forwardCommand(from wsClient, typ string, raw json.RawMessage) {
	if len(raw) == 0 {
		return
	}
	var t struct {
		DeviceId      string `json:"DeviceId"`
		PlaySessionId string `json:"PlaySessionId"` // corpus: targeting fields unverified
	}
	json.Unmarshal(raw, &t)
	device := t.DeviceId
	if device == "" && t.PlaySessionId != "" {
		device = a.hubv().deviceForPlaySession(t.PlaySessionId)
	}
	if device == "" || device == from.device() {
		return
	}
	// Remote-controlling another user's device is admin territory: the
	// session table is global, so without this check any authenticated
	// user could drive anyone's player. The sender's own sessions define
	// what it may target.
	if !a.hubv().deviceOwnedBy(device, from) {
		return
	}
	out, err := json.Marshal(struct {
		MessageType string          `json:"MessageType"`
		Data        json.RawMessage `json:"Data"`
	}{typ, raw})
	if err != nil {
		return
	}
	a.hubv().sendTo(device, out)
}

type wsPlayState struct {
	PositionTicks int64 `json:"PositionTicks"`
	IsPaused      bool  `json:"IsPaused"`
}

type wsSessionDTO struct {
	Id             string         `json:"Id"`
	UserId         string         `json:"UserId"`
	UserName       string         `json:"UserName,omitempty"`
	Client         string         `json:"Client,omitempty"`
	DeviceName     string         `json:"DeviceName,omitempty"`
	PlaySessionId  string         `json:"PlaySessionId,omitempty"`
	NowPlayingItem map[string]any `json:"NowPlayingItem,omitempty"`
	PlayState      *wsPlayState   `json:"PlayState,omitempty"`
} // corpus: session DTO field set trimmed vs real 10.10 /Sessions shape

func (a *API) sessionDTOs() []wsSessionDTO {
	sessions := a.hubv().snapshot()
	dtos := make([]wsSessionDTO, 0, len(sessions))
	for _, s := range sessions {
		dto := wsSessionDTO{
			Id:            s.DeviceID,
			UserId:        strconv.FormatInt(s.UserID, 10),
			Client:        s.Client,
			DeviceName:    s.DeviceName,
			PlaySessionId: s.PlaySessionID,
		}
		if a.DB != nil {
			var name string
			a.DB.QueryRow(`SELECT name FROM users WHERE id = ?`, s.UserID).Scan(&name)
			dto.UserName = name
		}
		if s.ItemID != "" {
			if it, ok := a.detailFor(s.UserID, s.ItemID); ok {
				delete(it, "__sort")
				delete(it, "__created")
				dto.NowPlayingItem = it
			}
		}
		dto.PlayState = &wsPlayState{PositionTicks: s.PosTicks, IsPaused: s.Paused}
		dtos = append(dtos, dto)
	}
	return dtos
}

func (a *API) sessionsMessage() []byte {
	out, _ := json.Marshal(struct {
		MessageType string `json:"MessageType"`
		Data        any    `json:"Data"`
	}{"Sessions", a.sessionDTOs()})
	return out
}

type wsClientInfo struct {
	deviceID   string
	client     string
	deviceName string
}

var (
	reWSDeviceID   = regexp.MustCompile(`DeviceId="([^"]+)"`)
	reWSClientName = regexp.MustCompile(`Client="([^"]+)"`)
	reWSDeviceName = regexp.MustCompile(`Device="([^"]+)"`)
	reWSAuthToken  = regexp.MustCompile(`Token="([^"]+)"`)
)

func clientInfo(r *http.Request) wsClientInfo {
	authz := r.Header.Get("X-Emby-Authorization")
	info := wsClientInfo{client: "Jellyfin Client"}
	if m := reWSDeviceID.FindStringSubmatch(authz); m != nil {
		info.deviceID = m[1]
	} else if q := r.URL.Query().Get("DeviceId"); q != "" {
		info.deviceID = q
	}
	if m := reWSClientName.FindStringSubmatch(authz); m != nil {
		info.client = m[1]
	}
	if m := reWSDeviceName.FindStringSubmatch(authz); m != nil {
		info.deviceName = m[1]
	}
	if info.deviceID == "" {
		info.deviceID = "dev-" + strconv.FormatInt(uid(r), 10)
	}
	return info
}

// ReportPlayback records playback state from the /Sessions/Playing* handlers
// and pushes a Sessions message to subscribed sockets. Playing vs Progress vs
// Stopped is derived from the request path; Stopped clears the session.
func (a *API) ReportPlayback(r *http.Request, itemID, playSessionID string, posTicks int64, paused bool) {
	info := clientInfo(r)
	if strings.HasSuffix(r.URL.Path, "Stopped") {
		a.hubv().remove(info.deviceID)
	} else {
		a.hubv().update(&liveSession{
			DeviceID:      info.deviceID,
			PlaySessionID: playSessionID,
			ItemID:        itemID,
			UserID:        uid(r),
			PosTicks:      posTicks,
			Paused:        paused,
			Client:        info.client,
			DeviceName:    info.deviceName,
		})
	}
	a.hubv().broadcast(a.sessionsMessage())
}

// CloseSockets drops every live /socket connection (graceful shutdown hook).
func (a *API) CloseSockets() {
	a.hubv().closeAll()
}

func headerHasToken(v, token string) bool {
	for _, part := range strings.Split(v, ",") {
		if strings.EqualFold(strings.TrimSpace(part), token) {
			return true
		}
	}
	return false
}

// handleSocket serves GET /socket (route registered via the reported Mount
// addition in jellyfin.go; not inside the jfAuth group because browsers and
// TV clients authenticate via query param here).
func (a *API) handleSocket(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("api_key")
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	if token == "" {
		token = r.Header.Get("X-Emby-Token")
	}
	if token == "" {
		if m := reWSAuthToken.FindStringSubmatch(r.Header.Get("Authorization")); m != nil {
			token = m[1]
		}
	}
	if token == "" {
		if m := reWSAuthToken.FindStringSubmatch(r.Header.Get("X-Emby-Authorization")); m != nil {
			token = m[1]
		}
	}
	user, ok := auth.UserForToken(a.DB, token)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"unauthorized"}`))
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") ||
		!headerHasToken(r.Header.Get("Connection"), "upgrade") {
		http.Error(w, "not a websocket", 400)
		return
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		http.Error(w, "unsupported websocket version", 400)
		return
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	raw, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(raw) != 16 {
		http.Error(w, "bad websocket key", 400)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijack unsupported", 500)
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " +
		wsAcceptKey(key) + "\r\n\r\n"
	conn.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	if _, err := brw.Writer.WriteString(resp); err != nil {
		conn.Close()
		return
	}
	if err := brw.Writer.Flush(); err != nil {
		conn.Close()
		return
	}
	conn.SetDeadline(time.Time{})

	info := clientInfo(r)
	c := &socketConn{
		uidv:     user.ID,
		dev:      info.deviceID,
		conn:     conn,
		br:       brw.Reader,
		w:        brw.Writer,
		send:     make(chan []byte, 32),
		pong:     make(chan []byte, 8),
		closeReq: make(chan []byte, 1),
		done:     make(chan struct{}),
	}
	a.hubv().register(c)
	force, _ := json.Marshal(struct {
		MessageType string `json:"MessageType"`
		Data        any    `json:"Data"`
	}{"ForceKeepAlive", wsKeepAlive})
	c.enqueue(force)
	go c.writeLoop()
	c.readLoop(a)
}
