package ws

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// The client half of RFC 6455.
//
// It exists for two reasons and no others: the gateway's tests need a client that speaks
// the protocol exactly, and an operator needs `realtime probe` to be able to open a
// connection from a terminal. It is not a general-purpose client library and is not
// exported beyond this repository.
//
// The differences from the server half are the two §5.1 requires: this endpoint masks what
// it sends and refuses masked frames from the server.

// DialOptions configures Dial.
type DialOptions struct {
	// Header is sent with the handshake — Authorization, the device headers, Cookie.
	Header http.Header
	// Subprotocols are offered in order of preference.
	Subprotocols []string
	MaxMessage   int64
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	// Dialer opens the TCP connection. Nil means a plain net.Dialer.
	Dialer *net.Dialer
}

// Dial opens a WebSocket connection to a ws:// or wss:// URL.
//
// TLS is not implemented here. Every deployment terminates TLS at the ingress, and a dialer
// that pretended to do TLS without verifying a certificate would be worse than one that
// says plainly it does not do it at all.
func Dial(ctx context.Context, rawURL string, opts DialOptions) (*Conn, *http.Response, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, fmt.Errorf("ws: %w", err)
	}
	if target.Scheme != "ws" {
		return nil, nil, fmt.Errorf("ws: this client speaks ws:// only, not %q — terminate TLS at the ingress", target.Scheme)
	}
	accept := AcceptOptions{MaxMessage: opts.MaxMessage, ReadTimeout: opts.ReadTimeout, WriteTimeout: opts.WriteTimeout}
	accept.defaults()

	dialer := opts.Dialer
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 10 * time.Second}
	}
	host := target.Host
	if target.Port() == "" {
		host += ":80"
	}
	conn, err := dialer.DialContext(ctx, "tcp", host)
	if err != nil {
		return nil, nil, err
	}

	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(key)

	path := target.RequestURI()
	request := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + target.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Key: " + encoded + "\r\n"
	if len(opts.Subprotocols) > 0 {
		request += "Sec-WebSocket-Protocol: " + strings.Join(opts.Subprotocols, ", ") + "\r\n"
	}
	for name, values := range opts.Header {
		for _, value := range values {
			request += name + ": " + value + "\r\n"
		}
	}
	request += "\r\n"

	_ = conn.SetWriteDeadline(time.Now().Add(accept.WriteTimeout))
	if _, err := conn.Write([]byte(request)); err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	_ = conn.SetWriteDeadline(time.Time{})

	_ = conn.SetReadDeadline(time.Now().Add(accept.ReadTimeout))
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	_ = conn.SetReadDeadline(time.Time{})

	if response.StatusCode != http.StatusSwitchingProtocols {
		// The body is the server's explanation and the caller usually wants it.
		return nil, response, fmt.Errorf("ws: the server answered %s", response.Status)
	}
	if got := response.Header.Get("Sec-WebSocket-Accept"); got != acceptKey(encoded) {
		_ = conn.Close()
		return nil, response, fmt.Errorf("ws: the server's accept value is wrong; this is not a WebSocket server")
	}

	return &Conn{
		conn: conn, reader: reader, writer: bufio.NewWriter(conn),
		subprotocol: response.Header.Get("Sec-WebSocket-Protocol"),
		opts:        accept, client: true,
	}, response, nil
}
