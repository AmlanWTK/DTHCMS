package ws

import (
	"io"
	"net"
	"time"
)

// fakeConn is a net.Conn over a reader and a writer, so a whole conversation can be written
// out as bytes and read back without a socket.
type fakeConn struct {
	io.Reader
	io.Writer
}

func (fakeConn) Close() error                     { return nil }
func (fakeConn) LocalAddr() net.Addr              { return fakeAddr{} }
func (fakeConn) RemoteAddr() net.Addr             { return fakeAddr{} }
func (fakeConn) SetDeadline(time.Time) error      { return nil }
func (fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (fakeConn) SetWriteDeadline(time.Time) error { return nil }

type fakeAddr struct{}

func (fakeAddr) Network() string { return "fake" }
func (fakeAddr) String() string  { return "fake" }
