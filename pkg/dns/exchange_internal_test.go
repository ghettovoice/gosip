package dns

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"codeberg.org/miekg/dns"

	"github.com/ghettovoice/gosip/internal/errors"
)

// stubExchConn is a net.Conn test double that records SetDeadline calls,
// captures written bytes and replays prepared read data.
type stubExchConn struct {
	resp []byte // bytes returned by Read calls

	readErr  error
	writeErr error
	setDLErr error
	writeN   int // caps the Write result to simulate a short write

	deadlines []time.Time
	writes    [][]byte
	closed    atomic.Int32
}

func (c *stubExchConn) Read(b []byte) (int, error) {
	if c.readErr != nil {
		return 0, c.readErr
	}
	if len(c.resp) == 0 {
		return 0, io.EOF
	}
	n := copy(b, c.resp)
	c.resp = c.resp[n:]
	return n, nil
}

func (c *stubExchConn) Write(b []byte) (int, error) {
	c.writes = append(c.writes, slices.Clone(b))
	if c.writeErr != nil {
		return 0, c.writeErr
	}
	if c.writeN > 0 && c.writeN < len(b) {
		return c.writeN, nil
	}
	return len(b), nil
}

func (c *stubExchConn) Close() error {
	c.closed.Add(1)
	return nil
}

func (*stubExchConn) LocalAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 55000}
}

func (*stubExchConn) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 53}
}

func (c *stubExchConn) SetDeadline(t time.Time) error {
	if c.setDLErr != nil {
		return c.setDLErr
	}
	c.deadlines = append(c.deadlines, t)
	return nil
}

func (*stubExchConn) SetReadDeadline(time.Time) error  { return nil }
func (*stubExchConn) SetWriteDeadline(time.Time) error { return nil }

func newPackedNAPTRReq(tb testing.TB) *dns.Msg {
	tb.Helper()

	req := dns.NewMsg("example.test.", dns.TypeNAPTR)
	if err := req.Pack(); err != nil {
		tb.Fatalf("req.Pack() error = %v, want nil", err)
	}
	return req
}

// packTestNAPTRResp builds a valid NAPTR response for req, optionally mutated
// before packing.
func packTestNAPTRResp(tb testing.TB, req *dns.Msg, mutate func(*dns.Msg)) []byte {
	tb.Helper()

	resp := &dns.Msg{
		ID:       req.ID,
		Response: true,
		Question: req.Question,
		Answer: []dns.RR{
			&dns.NAPTR{
				Hdr:         dns.Header{Name: "example.test.", Class: dns.ClassINET, TTL: 300},
				Order:       10,
				Preference:  30,
				Flags:       "S",
				Service:     "SIP+D2T",
				Replacement: "_sip._tcp.example.test.",
			},
		},
	}
	if mutate != nil {
		mutate(resp)
	}
	if err := resp.Pack(); err != nil {
		tb.Fatalf("resp.Pack() error = %v, want nil", err)
	}
	return resp.Data
}

func TestExchangeDNS_Deadline(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		ctxDeadline time.Duration // when > 0, the context gets this deadline
		wantCalls   int           // expected SetDeadline calls on the conn
	}{
		{"no context deadline sets no conn deadline", 0, 0},
		{"context deadline applied exactly", time.Minute, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := newPackedNAPTRReq(t)
			conn := &stubExchConn{resp: packTestNAPTRResp(t, req, nil)}

			ctx := t.Context()
			var dl time.Time
			if tt.ctxDeadline > 0 {
				var cancel context.CancelFunc
				dl = time.Now().Add(tt.ctxDeadline)
				ctx, cancel = context.WithDeadline(ctx, dl)
				defer cancel()
			}

			resp, err := exchangeDNS(ctx, conn, req)
			if err != nil {
				t.Fatalf("exchangeDNS() error = %v, want nil", err)
			}

			if len(conn.deadlines) != tt.wantCalls {
				t.Fatalf("conn.SetDeadline calls = %d, want %d", len(conn.deadlines), tt.wantCalls)
			}
			if tt.wantCalls > 0 && !conn.deadlines[0].Equal(dl) {
				t.Errorf("conn.SetDeadline() deadline = %v, want exact context deadline %v", conn.deadlines[0], dl)
			}

			if len(conn.writes) != 1 {
				t.Fatalf("conn.Write calls = %d, want 1", len(conn.writes))
			}
			if !bytes.Equal(conn.writes[0], req.Data) {
				t.Errorf("conn.Write() wrote %d bytes, want the raw request wire data (%d bytes)",
					len(conn.writes[0]), len(req.Data))
			}

			if !resp.Response || resp.ID != req.ID || len(resp.Answer) != 1 {
				t.Errorf("exchangeDNS() resp = %+v, want valid NAPTR response for ID %d", resp, req.ID)
			}
		})
	}
}

func TestExchangeDNS_Errors(t *testing.T) {
	t.Parallel()

	ioErr := errors.New("test io error")

	tests := []struct {
		name    string
		conn    func(testing.TB, *dns.Msg) *stubExchConn
		expired bool  // use an already expired context
		dl      bool  // use a context with a deadline
		wantErr error // expected sentinel via errors.Is; nil means any error
	}{
		{
			name: "wrong response ID",
			conn: func(tb testing.TB, req *dns.Msg) *stubExchConn {
				tb.Helper()
				return &stubExchConn{resp: packTestNAPTRResp(tb, req, func(m *dns.Msg) { m.ID++ })}
			},
			wantErr: dns.ErrID,
		},
		{
			name: "missing response bit",
			conn: func(tb testing.TB, req *dns.Msg) *stubExchConn {
				tb.Helper()
				return &stubExchConn{resp: packTestNAPTRResp(tb, req, func(m *dns.Msg) { m.Response = false })}
			},
		},
		{
			name: "malformed response",
			conn: func(testing.TB, *dns.Msg) *stubExchConn {
				return &stubExchConn{resp: []byte{0xde, 0xad}}
			},
		},
		{
			name:    "set deadline error",
			conn:    func(testing.TB, *dns.Msg) *stubExchConn { return &stubExchConn{setDLErr: ioErr} },
			dl:      true,
			wantErr: ioErr,
		},
		{
			name:    "write error",
			conn:    func(testing.TB, *dns.Msg) *stubExchConn { return &stubExchConn{writeErr: ioErr} },
			wantErr: ioErr,
		},
		{
			name:    "read error",
			conn:    func(testing.TB, *dns.Msg) *stubExchConn { return &stubExchConn{readErr: ioErr} },
			wantErr: ioErr,
		},
		{
			name:    "short write",
			conn:    func(testing.TB, *dns.Msg) *stubExchConn { return &stubExchConn{writeN: 1} },
			wantErr: io.ErrShortWrite,
		},
		{
			name:    "expired context fails before IO",
			expired: true,
			wantErr: context.DeadlineExceeded,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := newPackedNAPTRReq(t)
			conn := &stubExchConn{}
			if tt.conn != nil {
				conn = tt.conn(t, req)
			}

			ctx := t.Context()
			switch {
			case tt.expired:
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer cancel()
			case tt.dl:
				var cancel context.CancelFunc
				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(time.Minute))
				defer cancel()
			}

			resp, err := exchangeDNS(ctx, conn, req)
			if err == nil {
				t.Fatalf("exchangeDNS() = %+v, error = nil, want error", resp)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("exchangeDNS() error = %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
		})
	}
}

func TestExchangeErr(t *testing.T) {
	t.Parallel()

	ioErr := errors.New("test io error")
	timeoutErr := &net.OpError{Op: "read", Net: "udp", Err: os.ErrDeadlineExceeded}
	past := time.Now().Add(-time.Second)
	future := time.Now().Add(time.Minute)

	tests := []struct {
		name     string
		canceled bool
		connDL   time.Time
		err      error
		wantErr  error
	}{
		{"socket timeout with elapsed conn deadline", false, past, timeoutErr, context.DeadlineExceeded},
		{"ordinary error with elapsed conn deadline", false, past, ioErr, ioErr},
		{"socket timeout with future conn deadline", false, future, timeoutErr, timeoutErr},
		{"socket timeout with no conn deadline", false, time.Time{}, timeoutErr, timeoutErr},
		{"ordinary error after cancel", true, time.Time{}, ioErr, context.Canceled},
		{"socket timeout after cancel", true, past, timeoutErr, context.Canceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			if tt.canceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			got := exchangeErr(ctx, tt.connDL, tt.err)
			if !errors.Is(got, tt.wantErr) {
				t.Errorf("exchangeErr() = %v, want errors.Is(_, %v)", got, tt.wantErr)
			}
		})
	}
}

func TestExchangeDNS_CancelRead(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		srv, cln := net.Pipe()
		defer func() { _ = cln.Close() }()
		defer func() { _ = srv.Close() }()

		req := newPackedNAPTRReq(t)
		reqLen := len(req.Data)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		peerErrs := make(chan error, 1)
		go func() {
			// Consume the request, then wait past the former 2s library timeout
			// before canceling the caller context.
			if _, err := io.ReadFull(srv, make([]byte, reqLen)); err != nil {
				_ = srv.Close()
				peerErrs <- err
				return
			}
			time.Sleep(3 * time.Second)
			peerErrs <- nil
			cancel()
		}()

		start := time.Now()
		_, err := exchangeDNS(ctx, cln, req)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("exchangeDNS() error = %v, want %v", err, context.Canceled)
		}
		if d := time.Since(start); d < 3*time.Second {
			t.Fatalf("exchangeDNS() returned after %v, want blocked read until cancel at 3s", d)
		}
		if perr := <-peerErrs; perr != nil {
			t.Fatalf("peer read error = %v, want nil", perr)
		}
	})
}

func TestExchangeDNS_DeadlineExceeded(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		srv, cln := net.Pipe()
		defer func() { _ = cln.Close() }()
		defer func() { _ = srv.Close() }()

		req := newPackedNAPTRReq(t)
		reqLen := len(req.Data)
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()

		peerErrs := make(chan error, 1)
		go func() {
			// Consume the request; never respond.
			_, err := io.ReadFull(srv, make([]byte, reqLen))
			peerErrs <- err
		}()

		start := time.Now()
		_, err := exchangeDNS(ctx, cln, req)
		_ = cln.Close() // release a pending peer read if the exchange returned early
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("exchangeDNS() error = %v, want %v", err, context.DeadlineExceeded)
		}
		if d := time.Since(start); d < time.Second || d >= 2*time.Second {
			t.Fatalf("exchangeDNS() returned after %v, want context deadline at 1s", d)
		}
		if perr := <-peerErrs; perr != nil {
			t.Fatalf("peer read error = %v, want nil", perr)
		}
	})
}

func TestExchangeDNS_CancelWrite(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		srv, cln := net.Pipe()
		defer func() { _ = cln.Close() }()
		defer func() { _ = srv.Close() }()

		req := newPackedNAPTRReq(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// Nobody reads the request; cancellation unblocks the pending Write.
		go func() {
			time.Sleep(time.Second)
			cancel()
		}()

		start := time.Now()
		_, err := exchangeDNS(ctx, cln, req)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("exchangeDNS() error = %v, want %v", err, context.Canceled)
		}
		if d := time.Since(start); d < time.Second {
			t.Fatalf("exchangeDNS() returned after %v, want blocked write until cancel at 1s", d)
		}
	})
}

func TestExchangeDNS_SlowResponse(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		srv, cln := net.Pipe()
		defer func() { _ = cln.Close() }()
		defer func() { _ = srv.Close() }()

		req := newPackedNAPTRReq(t)
		reqLen := len(req.Data)
		respData := packTestNAPTRResp(t, req, nil)

		peerErrs := make(chan error, 1)
		go func() {
			// Consume the request, then respond after the former 2s library timeout.
			if _, err := io.ReadFull(srv, make([]byte, reqLen)); err != nil {
				_ = srv.Close()
				peerErrs <- err
				return
			}
			time.Sleep(3 * time.Second)
			_, err := srv.Write(respData)
			peerErrs <- err
		}()

		start := time.Now()
		resp, err := exchangeDNS(t.Context(), cln, req)
		_ = cln.Close() // release a pending peer write if the exchange returned early
		if err != nil {
			t.Fatalf("exchangeDNS() error = %v, want nil", err)
		}
		if d := time.Since(start); d < 3*time.Second {
			t.Fatalf("exchangeDNS() returned after %v, want >= 3s wait for the response", d)
		}
		if resp.ID != req.ID || !resp.Response || len(resp.Answer) != 1 {
			t.Errorf("exchangeDNS() resp = %+v, want valid NAPTR response for ID %d", resp, req.ID)
		}
		if perr := <-peerErrs; perr != nil {
			t.Fatalf("peer error = %v, want nil", perr)
		}
	})
}
