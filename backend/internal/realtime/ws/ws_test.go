package ws

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// RFC 6455 at the frame layer, against bytes written by hand.
//
// A WebSocket implementation is usually proved by Autobahn, which cannot run here. What can
// is this: each rule the RFC states, with the bytes that violate it, and the behaviour the
// RFC requires in response. Every case below cites the section it comes from, so that a
// future change can be checked against the document rather than against this file.

// --- §1.3: the handshake accept value ---

func TestTheAcceptValueIsTheOneInTheRFC(t *testing.T) {
	// RFC 6455 §1.3's own worked example. If this passes, a browser will accept the
	// handshake; if it does not, no amount of the rest working matters.
	if got := acceptKey("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Errorf("accept = %q, want the RFC's own example", got)
	}
}

// --- §4.2.1: what makes a request a handshake ---

func TestTheHandshakeIsRefusedWhenAnythingIsMissing(t *testing.T) {
	good := func() *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/realtime", nil)
		r.Header.Set("Connection", "Upgrade")
		r.Header.Set("Upgrade", "websocket")
		r.Header.Set("Sec-WebSocket-Version", "13")
		r.Header.Set("Sec-WebSocket-Key", base64.StdEncoding.EncodeToString(make([]byte, 16)))
		return r
	}
	if err := checkHandshake(good()); err != nil {
		t.Fatalf("a correct handshake was refused: %v", err)
	}

	for name, break_ := range map[string]func(*http.Request){
		"no Connection header": func(r *http.Request) { r.Header.Del("Connection") },
		"no Upgrade header":    func(r *http.Request) { r.Header.Del("Upgrade") },
		"the wrong version":    func(r *http.Request) { r.Header.Set("Sec-WebSocket-Version", "8") },
		"no key":               func(r *http.Request) { r.Header.Del("Sec-WebSocket-Key") },
		"a key of the wrong size": func(r *http.Request) {
			r.Header.Set("Sec-WebSocket-Key", base64.StdEncoding.EncodeToString(make([]byte, 8)))
		},
		"a key that is not base64": func(r *http.Request) { r.Header.Set("Sec-WebSocket-Key", "not base64!") },
		"a POST":                   func(r *http.Request) { r.Method = http.MethodPost },
	} {
		t.Run(name, func(t *testing.T) {
			r := good()
			break_(r)
			if err := checkHandshake(r); err == nil {
				t.Error("accepted")
			}
		})
	}

	// A comma-separated Connection header is what browsers behind a proxy send, and it is
	// legal: §4.2.1 says the header must *contain* the token.
	r := good()
	r.Header.Set("Connection", "keep-alive, Upgrade")
	if err := checkHandshake(r); err != nil {
		t.Errorf("a comma-separated Connection header was refused: %v", err)
	}
}

// The Origin check is not in the RFC's handshake rules and matters more than most of them:
// a WebSocket handshake is exempt from the same-origin policy and carries cookies.
func TestAnotherSitesOriginCannotOpenAConnection(t *testing.T) {
	request := func(origin string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://clinic.example/realtime", nil)
		r.Host = "clinic.example"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	if err := checkOrigin(request("https://clinic.example"), nil); err != nil {
		t.Errorf("same origin was refused: %v", err)
	}
	if err := checkOrigin(request(""), nil); err != nil {
		t.Errorf("a native client, which sends no Origin, was refused: %v", err)
	}
	if err := checkOrigin(request("https://evil.example"), nil); err == nil {
		t.Error("another site opened a connection")
	}
	if err := checkOrigin(request("http://localhost:3000"), []string{"http://localhost:3000"}); err != nil {
		t.Errorf("an allowlisted origin was refused: %v", err)
	}
}

// --- §5.2 and §5.5: frames ---

// clientFrame builds a masked frame the way a client would.
func clientFrame(fin bool, op Opcode, payload []byte) []byte {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	if err := writeFrame(w, op, payload, true); err != nil {
		panic(err)
	}
	_ = w.Flush()
	out := buf.Bytes()
	if !fin {
		out[0] &^= 0x80
	}
	return out
}

func readOne(t *testing.T, raw []byte, max int64) (frame, error) {
	t.Helper()
	return readFrame(bufio.NewReader(bytes.NewReader(raw)), max, true)
}

func TestAFrameRoundTrips(t *testing.T) {
	for _, payload := range [][]byte{
		nil,
		[]byte("hello"),
		bytes.Repeat([]byte("x"), 125),   // the largest 7-bit length
		bytes.Repeat([]byte("x"), 126),   // the first 16-bit length
		bytes.Repeat([]byte("x"), 65535), // the largest 16-bit length
		bytes.Repeat([]byte("x"), 65536), // the first 64-bit length
	} {
		got, err := readOne(t, clientFrame(true, OpText, payload), 1<<20)
		if err != nil {
			t.Fatalf("%d bytes: %v", len(payload), err)
		}
		if !bytes.Equal(got.payload, payload) || !got.fin || got.opcode != OpText {
			t.Errorf("%d bytes came back as %d", len(payload), len(got.payload))
		}
	}
}

func TestTheFrameLayerRefusesWhatTheRFCForbids(t *testing.T) {
	// §5.1: a client frame is masked. An unmasked one is how a crafted HTTP request
	// through a proxy would look, and accepting it is the vulnerability the mask exists
	// to prevent.
	unmasked := []byte{0x81, 0x05, 'h', 'e', 'l', 'l', 'o'}
	if _, err := readOne(t, unmasked, 1<<20); !errors.Is(err, ErrProtocol) {
		t.Errorf("an unmasked client frame: %v", err)
	}

	// §5.2: RSV1-3 are for extensions, and none were negotiated.
	reserved := clientFrame(true, OpText, []byte("x"))
	reserved[0] |= 0x40
	if _, err := readOne(t, reserved, 1<<20); !errors.Is(err, ErrProtocol) {
		t.Errorf("a reserved bit: %v", err)
	}

	// §5.2: an opcode nobody defined.
	unknown := clientFrame(true, Opcode(0x3), []byte("x"))
	if _, err := readOne(t, unknown, 1<<20); !errors.Is(err, ErrProtocol) {
		t.Errorf("an unknown opcode: %v", err)
	}

	// §5.5: a control frame is at most 125 bytes and never fragmented.
	big := clientFrame(true, opPing, bytes.Repeat([]byte("x"), 126))
	if _, err := readOne(t, big, 1<<20); !errors.Is(err, ErrProtocol) {
		t.Errorf("an oversized control frame: %v", err)
	}
	fragmented := clientFrame(false, opPing, []byte("x"))
	if _, err := readOne(t, fragmented, 1<<20); !errors.Is(err, ErrProtocol) {
		t.Errorf("a fragmented control frame: %v", err)
	}

	// A frame past the configured limit stops at the header rather than allocating it.
	if _, err := readOne(t, clientFrame(true, OpText, bytes.Repeat([]byte("x"), 300)), 100); !errors.Is(err, ErrTooLarge) {
		t.Errorf("an oversized message: %v", err)
	}
}

// --- §5.4: fragmentation, and §5.6: UTF-8 ---

// pipeConn drives a Conn from a byte slice, which is how a whole conversation can be
// written out by hand and read back.
func serverOn(t *testing.T, client []byte) (*Conn, *bytes.Buffer) {
	t.Helper()
	sent := &bytes.Buffer{}
	conn := &Conn{
		conn:   fakeConn{Reader: bytes.NewReader(client), Writer: sent},
		reader: bufio.NewReader(bytes.NewReader(client)),
		writer: bufio.NewWriter(sent),
		opts:   AcceptOptions{MaxMessage: 1 << 20, ReadTimeout: time.Second, WriteTimeout: time.Second},
	}
	return conn, sent
}

func TestAMessageIsReassembledFromItsFragments(t *testing.T) {
	var conversation []byte
	conversation = append(conversation, clientFrame(false, OpText, []byte("the "))...)
	conversation = append(conversation, clientFrame(false, opContinuation, []byte("junior "))...)
	// §5.5: a control frame may arrive between fragments and must not disturb assembly.
	conversation = append(conversation, clientFrame(true, opPing, []byte("beat"))...)
	conversation = append(conversation, clientFrame(true, opContinuation, []byte("doctor"))...)

	conn, sent := serverOn(t, conversation)
	op, message, err := conn.Read()
	if err != nil {
		t.Fatal(err)
	}
	if op != OpText || string(message) != "the junior doctor" {
		t.Errorf("reassembled %q", message)
	}
	// The ping was answered with a pong carrying the same payload (§5.5.2).
	if !bytes.Contains(sent.Bytes(), []byte("beat")) {
		t.Error("the ping in the middle of a message was not answered")
	}
}

func TestFragmentationRulesAreEnforced(t *testing.T) {
	// A continuation with nothing to continue.
	conn, _ := serverOn(t, clientFrame(true, opContinuation, []byte("x")))
	if _, _, err := conn.Read(); !errors.Is(err, ErrProtocol) {
		t.Errorf("an orphan continuation: %v", err)
	}

	// A new message beginning before the last one finished.
	var interleaved []byte
	interleaved = append(interleaved, clientFrame(false, OpText, []byte("a"))...)
	interleaved = append(interleaved, clientFrame(true, OpText, []byte("b"))...)
	conn, _ = serverOn(t, interleaved)
	if _, _, err := conn.Read(); !errors.Is(err, ErrProtocol) {
		t.Errorf("interleaved messages: %v", err)
	}
}

func TestATextMessageMustBeValidUTF8(t *testing.T) {
	// §5.6. An invalid sequence is 1007, not a byte slice handed to the application.
	conn, _ := serverOn(t, clientFrame(true, OpText, []byte{0xC0, 0x80}))
	if _, _, err := conn.Read(); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("invalid UTF-8 in a text frame: %v", err)
	}

	// The same bytes as binary are fine: binary is bytes.
	conn, _ = serverOn(t, clientFrame(true, OpBinary, []byte{0xC0, 0x80}))
	if _, _, err := conn.Read(); err != nil {
		t.Errorf("invalid UTF-8 in a binary frame was refused: %v", err)
	}
}

// --- §5.5.1 and §7.4.1: closing ---

func TestACloseIsAnOutcomeAndCarriesItsCode(t *testing.T) {
	payload := append([]byte{0x03, 0xE8}, "goodbye"...) // 1000
	conn, sent := serverOn(t, clientFrame(true, opClose, payload))
	_, _, err := conn.Read()

	var closed CloseError
	if !errors.As(err, &closed) {
		t.Fatalf("a close came back as %v", err)
	}
	if closed.Status != StatusNormalClosure || closed.Reason != "goodbye" {
		t.Errorf("close = %d %q", closed.Status, closed.Reason)
	}
	if CloseStatusOf(err) != StatusNormalClosure {
		t.Error("CloseStatusOf did not read the code")
	}
	// §5.5.1: the code is echoed back.
	if !bytes.Contains(sent.Bytes(), []byte{0x88}) {
		t.Error("no close frame was sent in reply")
	}
}

func TestAMalformedCloseIsAProtocolError(t *testing.T) {
	// A one-byte payload cannot hold a code.
	conn, _ := serverOn(t, clientFrame(true, opClose, []byte{0x03}))
	if _, _, err := conn.Read(); !errors.Is(err, ErrProtocol) {
		t.Errorf("a one-byte close: %v", err)
	}
	// §7.4.1: 1005 and 1006 are for local reporting and may not be sent.
	conn, _ = serverOn(t, clientFrame(true, opClose, []byte{0x03, 0xEE}))
	if _, _, err := conn.Read(); !errors.Is(err, ErrProtocol) {
		t.Errorf("close code 1006 on the wire: %v", err)
	}
	// A reason that is not UTF-8.
	conn, _ = serverOn(t, clientFrame(true, opClose, append([]byte{0x03, 0xE8}, 0xC0, 0x80)))
	if _, _, err := conn.Read(); !errors.Is(err, ErrProtocol) {
		t.Errorf("a close reason that is not UTF-8: %v", err)
	}
	// An empty payload is "no status", which is not an error (§5.5.1).
	conn, _ = serverOn(t, clientFrame(true, opClose, nil))
	if _, _, err := conn.Read(); CloseStatusOf(err) != StatusNoStatusReceived {
		t.Errorf("an empty close: %v", err)
	}
}

func TestSendableCodes(t *testing.T) {
	for code, want := range map[CloseStatus]bool{
		StatusNormalClosure: true, StatusProtocolError: true, 4000: true, 4999: true,
		StatusNoStatusReceived: false, StatusAbnormalClosure: false, 999: false, 5000: false,
	} {
		if got := code.sendable(); got != want {
			t.Errorf("%d sendable = %v, want %v", code, got, want)
		}
	}
}

// --- end to end, over a real socket ---

// The client and the server halves of this file, talking to each other over TCP through the
// real handshake. It is the closest thing to a proof that the two agree; the Chromium test
// in the gateway's own package is the proof that a browser agrees too.
func TestTheClientAndTheServerAgree(t *testing.T) {
	messages := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := Accept(w, r, AcceptOptions{Subprotocols: []string{"dthcms.v1"}})
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close(StatusNormalClosure, "") }()
		for {
			op, payload, err := conn.Read()
			if err != nil {
				return
			}
			messages <- string(payload)
			if err := conn.Write(op, append([]byte("echo:"), payload...)); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, response, err := Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+"/realtime", DialOptions{
		Subprotocols: []string{"dthcms.v1"},
	})
	if err != nil {
		t.Fatalf("dial: %v (%v)", err, response)
	}
	defer func() { _ = client.Close(StatusNormalClosure, "") }()

	if client.Subprotocol() != "dthcms.v1" {
		t.Errorf("subprotocol = %q", client.Subprotocol())
	}
	// A payload that spans every length encoding, and one with multi-byte UTF-8 in it.
	for _, payload := range []string{"hello", "রোগীর নাম", strings.Repeat("x", 200), strings.Repeat("y", 70000)} {
		if err := client.Write(OpText, []byte(payload)); err != nil {
			t.Fatal(err)
		}
		op, echoed, err := client.Read()
		if err != nil {
			t.Fatal(err)
		}
		if op != OpText || string(echoed) != "echo:"+payload {
			t.Fatalf("echo of %d bytes came back as %d", len(payload), len(echoed))
		}
		if got := <-messages; got != payload {
			t.Errorf("the server saw %q", got)
		}
	}
}

func TestTheServerRefusesANonHandshake(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := Accept(w, r, AcceptOptions{}); err == nil {
			t.Error("a plain GET was upgraded")
		}
	}))
	defer server.Close()

	response, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusBadRequest {
		t.Errorf("a plain GET got %d", response.StatusCode)
	}
}
