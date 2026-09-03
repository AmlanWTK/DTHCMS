// Package ws is a server-side implementation of the WebSocket protocol, RFC 6455.
//
// # Why this exists
//
// The implementation plan names coder/websocket. No module proxy is reachable from this
// environment, so no dependency can be added, and the realtime gateway is a named blueprint
// requirement (§4.1: "the junior doctor's screen updates instantly — no refresh"). The
// choice was between not building the gateway and writing the protocol. ADR-0018 records it.
//
// The scope is deliberately the server half of the protocol and nothing more: accept a
// handshake, read masked client frames, write unmasked server frames, answer pings, close
// politely. No client, no extensions, no permessage-deflate, no subprotocol negotiation
// beyond echoing one the caller allows. Everything RFC 6455 requires of a *server* is here;
// everything it does not is not.
//
// # What the RFC requires, and where each requirement lives
//
//	§4.2.2  handshake: Upgrade, Connection, Version 13, Sec-WebSocket-Accept   Accept
//	§5.1    a client frame is masked; a server frame is not                    Conn.read/write
//	§5.2    frame layout, 7/16/64-bit lengths, RSV must be zero                readFrame
//	§5.4    fragmentation: one message across continuation frames              Conn.Read
//	§5.5    control frames: ≤125 bytes, never fragmented                       readFrame
//	§5.5.1  close: echo the code, then stop                                    Conn.handleControl
//	§5.5.2  ping: answer with a pong carrying the same payload                 Conn.handleControl
//	§5.6    a text message is valid UTF-8                                      Conn.Read
//	§7.4.1  close codes, and which may appear on the wire                      CloseStatus
//
// Autobahn's test suite is the usual proof of a WebSocket implementation and cannot run
// here. What runs instead is ws_test.go — the frame layer against hand-written bytes — and
// a real Chromium client driving the gateway end to end (realtime_browser_test.go), which is
// the only client this system actually has to work with.
package ws

import (
	"bufio"
	"crypto/sha1" //nolint:gosec // RFC 6455 §1.3 specifies SHA-1 for the handshake accept value
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// magic is the GUID from RFC 6455 §1.3. It exists so that a server which merely echoes the
// key cannot be mistaken for one that implements the protocol.
const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Opcode is a frame's type (§5.2).
type Opcode byte

const (
	opContinuation Opcode = 0x0
	OpText         Opcode = 0x1
	OpBinary       Opcode = 0x2
	opClose        Opcode = 0x8
	opPing         Opcode = 0x9
	opPong         Opcode = 0xA
)

func (o Opcode) isControl() bool { return o&0x8 != 0 }

// CloseStatus is a close code (§7.4.1).
type CloseStatus uint16

const (
	StatusNormalClosure   CloseStatus = 1000
	StatusGoingAway       CloseStatus = 1001
	StatusProtocolError   CloseStatus = 1002
	StatusUnsupportedData CloseStatus = 1003
	// StatusNoStatusReceived and StatusAbnormalClosure are what a *reader* reports; §7.4.1
	// forbids sending them on the wire.
	StatusNoStatusReceived  CloseStatus = 1005
	StatusAbnormalClosure   CloseStatus = 1006
	StatusInvalidPayload    CloseStatus = 1007
	StatusPolicyViolation   CloseStatus = 1008
	StatusMessageTooBig     CloseStatus = 1009
	StatusInternalError     CloseStatus = 1011
	StatusServiceRestarting CloseStatus = 1012
	StatusTryAgainLater     CloseStatus = 1013
)

// sendable reports whether a code may be put on the wire (§7.4.1). 1005 and 1006 are
// reserved for the local endpoint's own reporting; 0–999 are not WebSocket codes at all.
func (c CloseStatus) sendable() bool {
	switch {
	case c == StatusNoStatusReceived || c == StatusAbnormalClosure:
		return false
	case c < 1000 || c > 4999:
		return false
	default:
		return true
	}
}

// CloseError is what Read returns when the peer closed. A close is an outcome, not a
// failure: the caller distinguishes it with errors.As.
type CloseError struct {
	Status CloseStatus
	Reason string
}

func (e CloseError) Error() string {
	if e.Reason == "" {
		return fmt.Sprintf("websocket closed: %d", e.Status)
	}
	return fmt.Sprintf("websocket closed: %d %s", e.Status, e.Reason)
}

// CloseStatusOf returns the close code an error carries, or StatusAbnormalClosure for an
// error that is not a close at all.
func CloseStatusOf(err error) CloseStatus {
	var closed CloseError
	if errors.As(err, &closed) {
		return closed.Status
	}
	return StatusAbnormalClosure
}

var (
	// ErrProtocol is a frame the peer should not have sent. The connection is closed with
	// 1002 and there is nothing to negotiate: a client that sends an unmasked frame or a
	// reserved bit is not a client this server can safely continue with.
	ErrProtocol = errors.New("websocket: protocol error")
	// ErrTooLarge is a message past the configured limit.
	ErrTooLarge = errors.New("websocket: message too large")
	// ErrClosed is a use of a connection that has already been closed.
	ErrClosed = errors.New("websocket: connection closed")
)

// AcceptOptions configures the handshake.
type AcceptOptions struct {
	// Subprotocols the server understands, most preferred first. The first one the client
	// also offers is selected; when the client offers none, none is selected.
	Subprotocols []string
	// OriginPatterns are the origins a browser may connect from. Empty means same-origin
	// only, which is the safe default: a WebSocket handshake is not subject to the
	// same-origin policy and carries cookies, so a gateway that accepts any Origin is a
	// cross-site request forgery hole with a long-lived connection attached.
	OriginPatterns []string
	// MaxMessage is the largest message Read will assemble. Zero means 1 MiB.
	MaxMessage int64
	// ReadTimeout bounds one read, and so is the deadline a missing heartbeat trips.
	ReadTimeout time.Duration
	// WriteTimeout bounds one write. A client that has stopped reading must not be able to
	// block a server goroutine for ever; that is what backpressure handling is for, and
	// this is the last line of it.
	WriteTimeout time.Duration
}

func (o *AcceptOptions) defaults() {
	if o.MaxMessage <= 0 {
		o.MaxMessage = 1 << 20
	}
	if o.ReadTimeout <= 0 {
		o.ReadTimeout = 70 * time.Second
	}
	if o.WriteTimeout <= 0 {
		o.WriteTimeout = 10 * time.Second
	}
}

// Accept completes the handshake and takes the connection over (§4.2.2).
//
// It writes the 101 response itself rather than through the ResponseWriter, because the
// connection is hijacked first: once hijacked, the writer belongs to the caller and the
// server will not add headers, a Date, or a chunked encoding to what is no longer HTTP.
func Accept(w http.ResponseWriter, r *http.Request, opts AcceptOptions) (*Conn, error) {
	opts.defaults()

	if err := checkHandshake(r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil, err
	}
	if err := checkOrigin(r, opts.OriginPatterns); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return nil, err
	}
	subprotocol := selectSubprotocol(r, opts.Subprotocols)

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "this server cannot upgrade the connection", http.StatusInternalServerError)
		return nil, errors.New("ws: the ResponseWriter cannot be hijacked")
	}
	conn, buffered, err := hijacker.Hijack()
	if err != nil {
		return nil, fmt.Errorf("ws: hijacking: %w", err)
	}

	response := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + acceptKey(r.Header.Get("Sec-WebSocket-Key")) + "\r\n"
	if subprotocol != "" {
		response += "Sec-WebSocket-Protocol: " + subprotocol + "\r\n"
	}
	response += "\r\n"

	_ = conn.SetWriteDeadline(time.Now().Add(opts.WriteTimeout))
	if _, err := buffered.WriteString(response); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ws: writing the handshake: %w", err)
	}
	if err := buffered.Flush(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ws: flushing the handshake: %w", err)
	}
	_ = conn.SetWriteDeadline(time.Time{})

	return &Conn{
		conn: conn, reader: buffered.Reader, writer: bufio.NewWriter(conn),
		subprotocol: subprotocol, opts: opts,
	}, nil
}

// checkHandshake is §4.2.1: what makes a request a WebSocket handshake rather than a GET.
func checkHandshake(r *http.Request) error {
	if r.Method != http.MethodGet {
		return errors.New("a WebSocket handshake is a GET")
	}
	if !headerContainsToken(r.Header, "Connection", "upgrade") {
		return errors.New("the Connection header must contain 'upgrade'")
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return errors.New("the Upgrade header must be 'websocket'")
	}
	if strings.TrimSpace(r.Header.Get("Sec-WebSocket-Version")) != "13" {
		// §4.4: a version this server does not speak is answered by naming the one it does.
		return errors.New("only WebSocket version 13 is supported")
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if decoded, err := base64.StdEncoding.DecodeString(key); err != nil || len(decoded) != 16 {
		return errors.New("Sec-WebSocket-Key must be sixteen base64-encoded bytes")
	}
	return nil
}

// checkOrigin is the cross-site guard. A WebSocket handshake is exempt from the same-origin
// policy and carries the browser's cookies, so a gateway that does not check Origin is
// reachable from any page the user happens to be reading.
func checkOrigin(r *http.Request, patterns []string) error {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		// Not a browser: a native client, which sends no Origin and is authenticated by the
		// same session and device proof as every other request it makes.
		return nil
	}
	host := r.Host
	if strings.EqualFold(stripScheme(origin), host) {
		return nil
	}
	for _, pattern := range patterns {
		if strings.EqualFold(pattern, origin) || strings.EqualFold(pattern, stripScheme(origin)) {
			return nil
		}
	}
	return fmt.Errorf("origin %q may not open a connection here", origin)
}

func stripScheme(origin string) string {
	if i := strings.Index(origin, "://"); i >= 0 {
		return origin[i+3:]
	}
	return origin
}

func selectSubprotocol(r *http.Request, offered []string) string {
	requested := splitTokens(r.Header.Get("Sec-WebSocket-Protocol"))
	for _, want := range offered {
		for _, got := range requested {
			if strings.EqualFold(want, got) {
				return want
			}
		}
	}
	return ""
}

func splitTokens(value string) []string {
	var out []string
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func headerContainsToken(h http.Header, name, token string) bool {
	for _, value := range h.Values(name) {
		for _, part := range splitTokens(value) {
			if strings.EqualFold(part, token) {
				return true
			}
		}
	}
	return false
}

// acceptKey is §1.3: SHA-1 of the client's key concatenated with the GUID, base64. SHA-1 is
// specified by the RFC and is not doing security work here — it exists so that a proxy which
// does not understand WebSocket cannot accidentally produce a valid-looking response.
func acceptKey(key string) string {
	h := sha1.New() //nolint:gosec // RFC 6455 §1.3
	h.Write([]byte(strings.TrimSpace(key)))
	h.Write([]byte(magic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// Conn is one WebSocket connection.
//
// Reads happen on one goroutine and writes on another — that is how a gateway is written —
// so writes are serialised by a mutex and reads are not. Calling Read from two goroutines is
// a programming error the protocol cannot recover from, and is not defended against.
type Conn struct {
	conn        net.Conn
	reader      *bufio.Reader
	writer      *bufio.Writer
	subprotocol string
	opts        AcceptOptions

	// client is true for a connection opened by Dial. It changes exactly two things, both
	// required by §5.1: this endpoint masks what it sends, and does not require what it
	// receives to be masked.
	client bool

	writeMu sync.Mutex
	closeMu sync.Mutex
	closed  bool
	// sentClose records that this endpoint has sent a close frame, so the close handshake
	// is not started twice.
	sentClose bool
}

// Subprotocol is the negotiated subprotocol, or "".
func (c *Conn) Subprotocol() string { return c.subprotocol }

// RemoteAddr is the peer's address, taken from the socket. Never from a header: an
// X-Forwarded-For a client controls is a client naming its own address.
func (c *Conn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

// Read returns the next complete message, reassembling continuation frames (§5.4) and
// answering control frames as they arrive.
//
// A close from the peer is returned as a CloseError, which is an outcome rather than a
// failure: a client that navigated away has done nothing wrong.
func (c *Conn) Read() (Opcode, []byte, error) {
	var (
		message    []byte
		messageOp  Opcode
		assembling bool
	)
	for {
		if err := c.conn.SetReadDeadline(time.Now().Add(c.opts.ReadTimeout)); err != nil {
			return 0, nil, err
		}
		frame, err := readFrame(c.reader, c.opts.MaxMessage, !c.client)
		if err != nil {
			return 0, nil, err
		}

		if frame.opcode.isControl() {
			// §5.5: a control frame may arrive between the fragments of a message and must
			// be handled without disturbing the assembly.
			if err := c.handleControl(frame); err != nil {
				return 0, nil, err
			}
			continue
		}

		switch {
		case frame.opcode == opContinuation:
			if !assembling {
				return 0, nil, fmt.Errorf("%w: a continuation frame with nothing to continue", ErrProtocol)
			}
		case assembling:
			return 0, nil, fmt.Errorf("%w: a new message began before the last one finished", ErrProtocol)
		default:
			messageOp = frame.opcode
			assembling = true
		}

		if int64(len(message)+len(frame.payload)) > c.opts.MaxMessage {
			return 0, nil, ErrTooLarge
		}
		message = append(message, frame.payload...)

		if frame.fin {
			// §5.6: a text message must be valid UTF-8, and an invalid one is 1007 rather
			// than a byte sequence handed to the application to misinterpret.
			if messageOp == OpText && !utf8.Valid(message) {
				return 0, nil, fmt.Errorf("%w: a text message that is not valid UTF-8", errInvalidPayload)
			}
			return messageOp, message, nil
		}
	}
}

var errInvalidPayload = errors.New("websocket: invalid payload")

// handleControl answers a ping, absorbs a pong, and turns a close into a CloseError after
// completing the handshake (§5.5).
func (c *Conn) handleControl(f frame) error {
	switch f.opcode {
	case opPing:
		return c.write(opPong, f.payload)
	case opPong:
		return nil
	case opClose:
		status, reason, err := parseClose(f.payload)
		if err != nil {
			_ = c.closeWith(StatusProtocolError, "malformed close frame")
			return err
		}
		// §5.5.1: echo the code back, then the connection is done.
		echo := status
		if !echo.sendable() {
			echo = StatusNormalClosure
		}
		_ = c.closeWith(echo, "")
		return CloseError{Status: status, Reason: reason}
	default:
		return fmt.Errorf("%w: unknown control opcode %#x", ErrProtocol, byte(f.opcode))
	}
}

func parseClose(payload []byte) (CloseStatus, string, error) {
	switch {
	case len(payload) == 0:
		// §5.5.1: an empty close payload means "no status", which is not an error.
		return StatusNoStatusReceived, "", nil
	case len(payload) == 1:
		return 0, "", fmt.Errorf("%w: a close payload of one byte", ErrProtocol)
	}
	status := CloseStatus(binary.BigEndian.Uint16(payload[:2]))
	reason := string(payload[2:])
	if !utf8.ValidString(reason) {
		return 0, "", fmt.Errorf("%w: a close reason that is not valid UTF-8", ErrProtocol)
	}
	// A peer may not send the codes reserved for local reporting, nor a code below 1000.
	if !status.sendable() && status != StatusNoStatusReceived {
		return 0, "", fmt.Errorf("%w: close code %d may not be sent", ErrProtocol, status)
	}
	return status, reason, nil
}

// Write sends one message as a single unfragmented frame. Server frames are never masked
// (§5.1).
func (c *Conn) Write(op Opcode, payload []byte) error {
	if op != OpText && op != OpBinary {
		return fmt.Errorf("ws: Write takes a text or binary opcode, not %#x", byte(op))
	}
	return c.write(op, payload)
}

// Ping sends a ping. The heartbeat lives above this: what a missing pong means is the
// gateway's decision, not the protocol's.
func (c *Conn) Ping(payload []byte) error {
	if len(payload) > 125 {
		return fmt.Errorf("ws: a ping payload is at most 125 bytes")
	}
	return c.write(opPing, payload)
}

func (c *Conn) write(op Opcode, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.isClosed() {
		return ErrClosed
	}
	if err := c.conn.SetWriteDeadline(time.Now().Add(c.opts.WriteTimeout)); err != nil {
		return err
	}
	if err := writeFrame(c.writer, op, payload, c.client); err != nil {
		return err
	}
	return c.writer.Flush()
}

// Close sends a close frame and shuts the socket down.
//
// It does not wait for the peer's echo. A gateway closing a connection is usually doing so
// because the peer is unresponsive, and waiting on an unresponsive peer to agree is how a
// shutdown takes a minute per connection.
func (c *Conn) Close(status CloseStatus, reason string) error {
	if err := c.closeWith(status, reason); err != nil && !errors.Is(err, ErrClosed) {
		_ = c.conn.Close()
		return err
	}
	return c.conn.Close()
}

func (c *Conn) closeWith(status CloseStatus, reason string) error {
	c.closeMu.Lock()
	if c.closed || c.sentClose {
		c.closeMu.Unlock()
		return ErrClosed
	}
	c.sentClose = true
	c.closeMu.Unlock()

	if !status.sendable() {
		status = StatusNormalClosure
	}
	if len(reason) > 123 { // 125 minus the two-byte code
		reason = reason[:123]
	}
	payload := make([]byte, 2+len(reason))
	binary.BigEndian.PutUint16(payload, uint16(status))
	copy(payload[2:], reason)

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(c.opts.WriteTimeout))
	if err := writeFrame(c.writer, opClose, payload, c.client); err != nil {
		return err
	}
	return c.writer.Flush()
}

func (c *Conn) isClosed() bool {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	return c.closed
}

// frame is one WebSocket frame as read off the wire.
type frame struct {
	fin     bool
	opcode  Opcode
	payload []byte
}

// readFrame reads and unmasks one frame (§5.2). requireMask is true on the server side,
// where §5.1 requires every client frame to be masked, and false on the client side, where
// it requires every server frame not to be.
func readFrame(r *bufio.Reader, max int64, requireMask bool) (frame, error) {
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return frame{}, err
	}

	fin := header[0]&0x80 != 0
	if header[0]&0x70 != 0 {
		// RSV1-3 are for extensions, and none were negotiated. §5.2 says a frame with a
		// reserved bit set must fail the connection rather than be ignored.
		return frame{}, fmt.Errorf("%w: a reserved bit is set and no extension was negotiated", ErrProtocol)
	}
	opcode := Opcode(header[0] & 0x0F)
	switch opcode {
	case opContinuation, OpText, OpBinary, opClose, opPing, opPong:
	default:
		return frame{}, fmt.Errorf("%w: unknown opcode %#x", ErrProtocol, byte(opcode))
	}

	masked := header[1]&0x80 != 0
	if requireMask && !masked {
		// §5.1: every frame from a client is masked, and a server that accepts an unmasked
		// one is a server that can be driven by a crafted HTTP request through a proxy.
		return frame{}, fmt.Errorf("%w: an unmasked frame from a client", ErrProtocol)
	}
	if !requireMask && masked {
		return frame{}, fmt.Errorf("%w: a masked frame from a server", ErrProtocol)
	}

	length := int64(header[1] & 0x7F)
	switch length {
	case 126:
		var extended [2]byte
		if _, err := io.ReadFull(r, extended[:]); err != nil {
			return frame{}, err
		}
		length = int64(binary.BigEndian.Uint16(extended[:]))
	case 127:
		var extended [8]byte
		if _, err := io.ReadFull(r, extended[:]); err != nil {
			return frame{}, err
		}
		value := binary.BigEndian.Uint64(extended[:])
		if value > 1<<62 {
			return frame{}, fmt.Errorf("%w: an absurd payload length", ErrProtocol)
		}
		length = int64(value) //nolint:gosec // bounded above
	}

	if opcode.isControl() {
		// §5.5: a control frame carries at most 125 bytes and is never fragmented.
		if length > 125 {
			return frame{}, fmt.Errorf("%w: a control frame of %d bytes", ErrProtocol, length)
		}
		if !fin {
			return frame{}, fmt.Errorf("%w: a fragmented control frame", ErrProtocol)
		}
	}
	if length > max {
		return frame{}, ErrTooLarge
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return frame{}, err
		}
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return frame{}, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return frame{fin: fin, opcode: opcode, payload: payload}, nil
}

// writeFrame writes one frame, masking it when this endpoint is the client (§5.1).
func writeFrame(w *bufio.Writer, op Opcode, payload []byte, mask bool) error {
	header := []byte{0x80 | byte(op)} // FIN set: this implementation never fragments
	maskBit := byte(0)
	if mask {
		maskBit = 0x80
	}
	switch n := len(payload); {
	case n <= 125:
		header = append(header, maskBit|byte(n))
	case n <= 0xFFFF:
		header = append(header, maskBit|126, 0, 0)
		binary.BigEndian.PutUint16(header[2:], uint16(n)) //nolint:gosec // bounded by the case
	default:
		header = append(header, maskBit|127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[2:], uint64(n)) //nolint:gosec // a length
	}
	if !mask {
		if _, err := w.Write(header); err != nil {
			return err
		}
		_, err := w.Write(payload)
		return err
	}

	key := maskKey()
	header = append(header, key[:]...)
	if _, err := w.Write(header); err != nil {
		return err
	}
	masked := make([]byte, len(payload))
	for i := range payload {
		masked[i] = payload[i] ^ key[i%4]
	}
	_, err := w.Write(masked)
	return err
}

// maskKey is §5.3: a client masks its frames so that a payload it controls cannot be made
// to look like a valid HTTP request to a proxy in the middle. It is obfuscation against
// intermediaries, not secrecy, which is why math/rand is the right tool for it.
func maskKey() [4]byte {
	var key [4]byte
	binary.BigEndian.PutUint32(key[:], rand.Uint32())
	return key
}
