package siptest

import (
	"iter"
	"net"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/sip"
)

const streamReadTimeout = 5 * time.Second

// StreamReader parses SIP messages sequentially while retaining parser
// read-ahead. It is not safe for concurrent use; reuse one reader per
// connection.
type StreamReader struct {
	conn net.Conn
	next func() (sip.Message, error, bool)
}

// NewStreamReader creates a parser for a connection and stops it during test
// cleanup. The caller retains ownership of the connection.
func NewStreamReader(tb testing.TB, conn net.Conn) *StreamReader {
	tb.Helper()

	next, stop := iter.Pull2(sip.ParseStream(conn))
	tb.Cleanup(stop)

	return &StreamReader{conn: conn, next: next}
}

// Read reads and parses one SIP message from the stream.
func (r *StreamReader) Read(tb testing.TB) sip.Message {
	tb.Helper()

	if err := r.conn.SetReadDeadline(time.Now().Add(streamReadTimeout)); err != nil {
		tb.Fatalf("conn.SetReadDeadline() error = %v, want nil", err)
	}

	msg, err, ok := r.next()
	if resetErr := r.conn.SetReadDeadline(time.Time{}); resetErr != nil {
		tb.Fatalf("conn.SetReadDeadline(zero) error = %v, want nil", resetErr)
	}
	if !ok {
		tb.Fatalf("sip.ParseStream() produced no result, want one message")
	}
	if err != nil {
		tb.Fatalf("sip.ParseStream() error = %v, want nil", err)
	}
	if msg == nil {
		tb.Fatalf("sip.ParseStream() message = nil, want non-nil")
	}

	return msg
}
