package sip_test

import (
	"context"
	"net"
	"net/netip"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"braces.dev/errtrace"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"go.uber.org/mock/gomock"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/internal/testutil/netmock"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/uri"
)

func TestNewReliableTransport(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)

	ls := netmock.NewMockListener(ctrl)
	ls.EXPECT().
		Addr().
		Return(&net.TCPAddr{IP: net.IPv4zero, Port: 5060}).
		MinTimes(1)
	ls.EXPECT().
		Close().
		Return(nil).
		Times(1)

	t.Run("empty protocol", func(t *testing.T) {
		_, got := sip.NewReliableTransport("", ls, nil)
		want := sip.ErrInvalidArgument
		if diff := cmp.Diff(got, want, cmpopts.EquateErrors()); diff != "" {
			t.Fatalf("sip.NewReliableTransport(\"\", ls, nil) error = %v, want %v\ndiff (-got +want):\n%v",
				got, want, diff,
			)
		}
	})

	t.Run("nil listener", func(t *testing.T) {
		_, got := sip.NewReliableTransport("TCP", nil, nil)
		want := sip.ErrInvalidArgument
		if diff := cmp.Diff(got, want, cmpopts.EquateErrors()); diff != "" {
			t.Fatalf("sip.NewReliableTransport(\"TCP\", nil, nil) error = %v, want %v\ndiff (-got +want):\n%v",
				got, want, diff,
			)
		}
	})

	t.Run("success", func(t *testing.T) {
		tp, err := sip.NewReliableTransport("TCP", ls, &sip.ReliableTransportOptions{
			DefaultPort: 4554,
			Streamed:    true,
		})
		if err != nil {
			t.Fatalf("sip.NewReliableTransport(\"TCP\", ls, opts) error = %v, want nil", err)
		}

		if got := tp.Proto(); got != sip.TransportProto("TCP") {
			t.Errorf("tp.Proto() = %q, want \"TCP\"", got)
		}
		if got := tp.Network(); got != "tcp" {
			t.Errorf("tp.Network() = %q, want \"tcp\"", got)
		}
		if got := tp.Reliable(); !got {
			t.Errorf("tp.Reliable() = %v, want true", got)
		}
		if got := tp.Secured(); got {
			t.Errorf("tp.Secured() = %v, want false", got)
		}
		if got := tp.Streamed(); !got {
			t.Errorf("tp.Streamed() = %v, want true", got)
		}
		if got := tp.LocalAddr(); got != netip.MustParseAddrPort("0.0.0.0:5060") {
			t.Errorf("tp.LocalAddr() = %v, want 0.0.0.0:5060", got)
		}
		if got, want := tp.DefaultPort(), uint16(4554); got != want {
			t.Errorf("tp.DefaultPort() = %v, want %v", got, want)
		}

		if err := tp.Close(); err != nil {
			t.Fatalf("tp.Close() error = %v, want nil", err)
		}
	})
}

func setupRelTransp(
	tb testing.TB,
	ctrl *gomock.Controller,
	onLsClose func(),
	getConn func() net.Conn,
) (*sip.ReliableTransport, *netmock.MockListener) {
	tb.Helper()

	ls := netmock.NewMockListener(ctrl)
	ls.EXPECT().
		Addr().
		Return(&net.TCPAddr{IP: net.IPv4zero, Port: 5060}).
		MinTimes(1)
	ls.EXPECT().
		Close().
		Do(func() error {
			if onLsClose != nil {
				onLsClose()
			}
			return nil
		}).
		Return(nil).
		Times(1)

	tp, err := sip.NewReliableTransport("TCP", ls, &sip.ReliableTransportOptions{
		DefaultPort: 5060,
		Streamed:    true,
		ConnDialer: sip.ConnDialerFunc(func(context.Context, string, netip.AddrPort) (net.Conn, error) {
			return getConn(), nil
		}),
		// Log: log.Console(),
	})
	if err != nil {
		tb.Fatalf("sip.NewReliableTransport(\"TCP\", ls, opts) error = %v, want nil", err)
	}

	tb.Cleanup(func() {
		tp.Close()
	})

	return tp, ls
}

//nolint:gocognit
func TestReliableTransport_SendRequest(t *testing.T) {
	t.Parallel()

	baseReq := &sip.Request{
		Proto:  sip.ProtoVer20(),
		Method: sip.RequestMethodInfo,
		URI: &uri.SIP{
			User: uri.User("bob"),
			Addr: uri.HostPort("example.com", 5060),
		},
		Headers: make(sip.Headers).
			Set(header.Via{
				{
					Proto:  sip.ProtoVer20(),
					Params: make(header.Values).Set("branch", sip.MagicCookie+".qwerty"),
				},
			}).
			Set(&header.From{
				URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("127.0.0.1")},
				Params: make(header.Values).Set("tag", "abc"),
			}).
			Set(&header.To{
				URI: &uri.SIP{User: uri.User("bob"), Addr: uri.Host("example.com")},
			}).
			Set(header.CallID("123-abc@127.0.0.1")).
			Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInfo}).
			Set(header.MaxForwards(70)),
	}

	t.Run("invalid request", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		tp, _ := setupRelTransp(t, ctrl, nil, nil)

		req := baseReq.Clone().(*sip.Request) //nolint:forcetypeassert
		req.Headers.Del("Via")
		outReq := sip.NewOutboundRequest(req)
		outReq.SetRemoteAddr(netip.MustParseAddrPort("123.123.123.123:5060"))

		got := tp.SendRequest(t.Context(), outReq, nil)
		want := sip.ErrInvalidMessage
		if diff := cmp.Diff(got, want, cmpopts.EquateErrors()); diff != "" {
			t.Errorf("tp.SendRequest(ctx, req, nil) = %v, want %v\ndiff (-got +want):\n%v", got, want, diff)
		}
	})

	t.Run("request with deadline", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		conn := netmock.NewMockConn(ctrl)
		conn.EXPECT().
			LocalAddr().
			Return(&net.TCPAddr{IP: net.IPv4zero, Port: 33555}).
			AnyTimes()
		conn.EXPECT().
			RemoteAddr().
			Return(&net.TCPAddr{IP: net.ParseIP("123.123.123.123").To4(), Port: 5060}).
			AnyTimes()
		conn.EXPECT().
			SetReadDeadline(gomock.AssignableToTypeOf(time.Time{})).
			Return(nil).
			AnyTimes()

		connClosed := make(chan struct{})
		conn.EXPECT().
			Close().
			Do(func() error {
				close(connClosed)
				return nil
			}).
			Return(nil).
			Times(1)
		conn.EXPECT().
			Read(gomock.AssignableToTypeOf([]byte(nil))).
			DoAndReturn(func([]byte) (int, error) {
				<-connClosed
				return 0, errtrace.Wrap(net.ErrClosed)
			}).
			AnyTimes()
		conn.EXPECT().
			Write(gomock.AssignableToTypeOf([]byte(nil))).
			Return(0, os.ErrDeadlineExceeded).
			Times(1)

		deadline := time.Now().Add(1 * time.Second)
		ctx, cancel := context.WithDeadline(t.Context(), deadline)
		defer cancel()
		conn.EXPECT().
			SetWriteDeadline(deadline).
			Return(nil).
			Times(1)
		conn.EXPECT().
			SetWriteDeadline(time.Time{}).
			Return(nil).
			Times(1)

		tp, _ := setupRelTransp(t, ctrl, nil, func() net.Conn { return conn })

		req := baseReq.Clone().(*sip.Request) //nolint:forcetypeassert
		outReq := sip.NewOutboundRequest(req)
		outReq.SetRemoteAddr(netip.MustParseAddrPort("123.123.123.123:5060"))

		got := tp.SendRequest(ctx, outReq, nil)
		want := os.ErrDeadlineExceeded
		if diff := cmp.Diff(got, want, cmpopts.EquateErrors()); diff != "" {
			t.Errorf("tp.SendRequest(ctx, req, nil) = %v, want %v\ndiff (-got +want):\n%v", got, want, diff)
		}
	})

	t.Run("request to invalid address", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		tp, _ := setupRelTransp(t, ctrl, nil, nil)

		req := baseReq.Clone().(*sip.Request) //nolint:forcetypeassert
		outReq := sip.NewOutboundRequest(req)

		got := tp.SendRequest(t.Context(), outReq, nil)
		want := sip.NewInvalidArgumentError("invalid remote address")
		if got.Error() != want.Error() {
			t.Errorf("tp.SendRequest(ctx, req, nil) = %v, want %v\ndiff (-got +want):\n%v",
				got, want, cmp.Diff(got, want, cmpopts.EquateErrors()),
			)
		}
	})

	t.Run("valid request", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		locAddr := netip.MustParseAddrPort("127.0.0.1:33555")
		rmtAddr := netip.MustParseAddrPort("123.123.123.123:5060")

		conn := netmock.NewMockConn(ctrl)
		conn.EXPECT().
			LocalAddr().
			Return(&net.TCPAddr{IP: locAddr.Addr().AsSlice(), Port: int(locAddr.Port())}).
			AnyTimes()
		conn.EXPECT().
			RemoteAddr().
			Return(&net.TCPAddr{IP: rmtAddr.Addr().AsSlice(), Port: int(rmtAddr.Port())}).
			AnyTimes()
		conn.EXPECT().
			SetReadDeadline(gomock.AssignableToTypeOf(time.Time{})).
			Return(nil).
			AnyTimes()
		conn.EXPECT().
			SetWriteDeadline(gomock.AssignableToTypeOf(time.Time{})).
			Return(nil).
			AnyTimes()

		connClosed := make(chan struct{})
		conn.EXPECT().
			Close().
			Do(func() error {
				close(connClosed)
				return nil
			}).
			Return(nil).
			Times(1)

		conn.EXPECT().
			Read(gomock.AssignableToTypeOf([]byte(nil))).
			DoAndReturn(func([]byte) (int, error) {
				<-connClosed
				return 0, errtrace.Wrap(net.ErrClosed)
			}).
			AnyTimes()
		conn.EXPECT().
			Write(gomock.AssignableToTypeOf([]byte(nil))).
			DoAndReturn(func(b []byte) (int, error) {
				return len(b), nil
			}).
			Times(2)

		tp, _ := setupRelTransp(t, ctrl, nil, func() net.Conn { return conn })

		// first
		req := baseReq.Clone().(*sip.Request) //nolint:forcetypeassert
		req.Body = []byte("hello world" + strings.Repeat("x", int(sip.MTU)))
		outReq := sip.NewOutboundRequest(req)
		outReq.SetRemoteAddr(netip.MustParseAddrPort("123.123.123.123:0"))

		got := tp.SendRequest(t.Context(), outReq, nil)
		if got != nil {
			t.Fatalf("tp.SendRequest(ctx, req, nil) = %v, want nil", got)
		}
		if got, want := outReq.Transport(), tp.Proto(); got != want {
			t.Errorf("req.Transport() = %v, want %v", got, want)
		}
		if got, want := outReq.LocalAddr(), locAddr; got != want {
			t.Errorf("req.LocalAddr() = %v, want %v", got, want)
		}
		if got, want := outReq.RemoteAddr(), rmtAddr; got != want {
			t.Errorf("req.RemoteAddr() = %v, want %v", got, want)
		}
		if ct, ok := req.Headers.ContentLength(); ok {
			if got, want := int(ct), len(req.Body); got != want {
				t.Errorf("Content-Length header value = %d, want %d", got, want)
			}
		} else {
			t.Error("Content-Length header is missing")
		}

		// second
		req = baseReq.Clone().(*sip.Request) //nolint:forcetypeassert
		req.Method = sip.RequestMethodOptions
		outReq = sip.NewOutboundRequest(req)
		outReq.SetRemoteAddr(netip.MustParseAddrPort("123.123.123.123:0"))

		got = tp.SendRequest(t.Context(), outReq, nil)
		if got != nil {
			t.Fatalf("tp.SendRequest(ctx, req, nil) = %v, want nil", got)
		}
		if got, want := outReq.Transport(), tp.Proto(); got != want {
			t.Errorf("req.Transport() = %v, want %v", got, want)
		}
		if got, want := outReq.LocalAddr(), locAddr; got != want {
			t.Errorf("req.LocalAddr() = %v, want %v", got, want)
		}
		if got, want := outReq.RemoteAddr(), rmtAddr; got != want {
			t.Errorf("req.RemoteAddr() = %v, want %v", got, want)
		}
		if ct, ok := req.Headers.ContentLength(); ok {
			if got, want := int(ct), len(req.Body); got != want {
				t.Errorf("Content-Length header value = %d, want %d", got, want)
			}
		} else {
			t.Error("Content-Length header is missing")
		}
	})
}

//nolint:gocognit
func TestReliableTransport_SendResponse(t *testing.T) {
	t.Parallel()

	baseRes := &sip.Response{
		Proto:  sip.ProtoVer20(),
		Status: sip.ResponseStatusOK,
		Headers: make(sip.Headers).
			Set(header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "TCP",
					Addr:      header.HostPort("123.123.123.123", 5060),
					Params:    make(header.Values).Set("branch", sip.MagicCookie+".qwerty"),
				},
			}).
			Set(&header.From{
				URI:    &uri.SIP{User: uri.User("bob"), Addr: uri.Host("example.com")},
				Params: make(header.Values).Set("tag", "abc"),
			}).
			Set(&header.To{
				URI: &uri.SIP{User: uri.User("alice"), Addr: uri.Host("127.0.0.1")},
			}).
			Set(header.CallID("123-abc@example.com")).
			Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInfo}),
	}

	t.Run("invalid response", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		tp, _ := setupRelTransp(t, ctrl, nil, nil)

		res := baseRes.Clone().(*sip.Response) //nolint:forcetypeassert
		res.Status = 55
		outRes := sip.NewOutboundResponse(res)

		got := tp.SendResponse(t.Context(), outRes, nil)
		want := sip.ErrInvalidMessage
		if diff := cmp.Diff(got, want, cmpopts.EquateErrors()); diff != "" {
			t.Errorf("tp.SendResponse(ctx, res, nil) = %v, want %v\ndiff (-got +want):\n%v", got, want, diff)
		}
	})

	t.Run("mismatched transport", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		tp, _ := setupRelTransp(t, ctrl, nil, nil)

		res := baseRes.Clone().(*sip.Response) //nolint:forcetypeassert
		via, _ := res.Headers.FirstVia()
		via.Transport = "UDP"
		outRes := sip.NewOutboundResponse(res)

		got := tp.SendResponse(t.Context(), outRes, nil)
		want := sip.NewInvalidArgumentError(`transport mismatch: got "UDP", want "TCP"`)
		if got.Error() != want.Error() {
			t.Errorf("tp.SendResponse(ctx, res, nil) = %v, want %v\ndiff (-got +want):\n%v",
				got, want, cmp.Diff(got, want, cmpopts.EquateErrors()),
			)
		}
	})

	t.Run("response to opened connection", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		conn := netmock.NewMockConn(ctrl)
		conn.EXPECT().
			LocalAddr().
			Return(&net.TCPAddr{IP: net.IPv4zero, Port: 5060}).
			AnyTimes()
		conn.EXPECT().
			RemoteAddr().
			Return(&net.TCPAddr{IP: net.ParseIP("123.123.123.123").To4(), Port: 12345}).
			AnyTimes()
		conn.EXPECT().
			SetReadDeadline(gomock.AssignableToTypeOf(time.Time{})).
			Return(nil).
			AnyTimes()
		conn.EXPECT().
			SetWriteDeadline(gomock.AssignableToTypeOf(time.Time{})).
			Return(nil).
			AnyTimes()

		connClosed := make(chan struct{})
		conn.EXPECT().
			Close().
			Do(func() error {
				close(connClosed)
				return nil
			}).
			Return(nil).
			Times(1)

		var reqSent atomic.Bool
		conn.EXPECT().
			Read(gomock.AssignableToTypeOf([]byte(nil))).
			DoAndReturn(func(b []byte) (int, error) {
				if reqSent.CompareAndSwap(false, true) {
					req := "INFO sip:alice@127.0.0.1:5060 SIP/2.0\r\n" +
						"Via: SIP/2.0/TCP 123.123.123.123:5060;branch=" + sip.MagicCookie + ".qwerty\r\n" +
						"From: sip:bob@example.com;tag=abc\r\n" +
						"To: sip:alice@127.0.0.1\r\n" +
						"Call-ID: 123-abc@example.com\r\n" +
						"CSeq: 1 INFO\r\n" +
						"Max-Forwards: 70\r\n" +
						"Content-Length: 0\r\n\r\n"
					return copy(b, []byte(req)), nil
				}
				<-connClosed
				return 0, errtrace.Wrap(net.ErrClosed)
			}).
			AnyTimes()
		conn.EXPECT().
			Write(gomock.AssignableToTypeOf([]byte(nil))).
			DoAndReturn(func(b []byte) (int, error) {
				return len(b), nil
			}).
			Times(1)

		lsClosed := make(chan struct{})
		tp, ls := setupRelTransp(t, ctrl, func() { close(lsClosed) }, nil)

		var connAccepted atomic.Bool
		ls.EXPECT().
			Accept().
			DoAndReturn(func() (net.Conn, error) {
				if connAccepted.CompareAndSwap(false, true) {
					return conn, nil
				}
				<-lsClosed
				return nil, errtrace.Wrap(net.ErrClosed)
			}).
			Times(2)

		reqRecv := make(chan struct{})
		unbind := tp.OnRequest(func(context.Context, sip.ServerTransport, *sip.InboundRequest) {
			close(reqRecv)
		})
		defer unbind()

		go tp.Serve() //nolint:errcheck

		select {
		case <-reqRecv:
		case <-time.After(1 * time.Second):
			t.Fatal("timeout waiting for request")
		}

		res := baseRes.Clone().(*sip.Response) //nolint:forcetypeassert
		outRes := sip.NewOutboundResponse(res)
		outRes.SetRemoteAddr(netip.AddrPortFrom(netip.AddrFrom4([4]byte{123, 123, 123, 123}), 12345))

		if got := tp.SendResponse(t.Context(), outRes, nil); got != nil {
			t.Fatalf("tp.SendResponse(ctx, res, nil) = %v, want nil", got)
		}
		if got, want := outRes.Transport(), tp.Proto(); got != want {
			t.Errorf("outRes.Transport() = %v, want %v", got, want)
		}
		if got, want := outRes.LocalAddr(), netip.AddrPortFrom(netip.IPv4Unspecified(), 5060); got != want {
			t.Errorf("outRes.LocalAddr() = %v, want %v", got, want)
		}
		if got, want := outRes.RemoteAddr(), netip.AddrPortFrom(netip.AddrFrom4([4]byte{123, 123, 123, 123}), 12345); got != want {
			t.Errorf("outRes.RemoteAddr() = %v, want %v", got, want)
		}
		if ct, ok := res.Headers.ContentLength(); ok {
			if got, want := int(ct), len(res.Body); got != want {
				t.Errorf("Content-Length header value = %d, want %d", got, want)
			}
		} else {
			t.Error("Content-Length header is missing")
		}
	})

	t.Run("response to Via IP and port", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		conn := netmock.NewMockConn(ctrl)
		conn.EXPECT().
			LocalAddr().
			Return(&net.TCPAddr{IP: net.IPv4zero, Port: 33000}).
			AnyTimes()
		conn.EXPECT().
			RemoteAddr().
			Return(&net.TCPAddr{IP: net.ParseIP("123.123.123.123").To4(), Port: 5060}).
			AnyTimes()
		conn.EXPECT().
			SetReadDeadline(gomock.AssignableToTypeOf(time.Time{})).
			Return(nil).
			AnyTimes()
		conn.EXPECT().
			SetWriteDeadline(gomock.AssignableToTypeOf(time.Time{})).
			Return(nil).
			AnyTimes()

		connClosed := make(chan struct{})
		conn.EXPECT().
			Close().
			Do(func() error {
				close(connClosed)
				return nil
			}).
			Return(nil).
			Times(1)
		conn.EXPECT().
			Read(gomock.AssignableToTypeOf([]byte(nil))).
			DoAndReturn(func([]byte) (int, error) {
				<-connClosed
				return 0, errtrace.Wrap(net.ErrClosed)
			}).
			AnyTimes()
		conn.EXPECT().
			Write(gomock.AssignableToTypeOf([]byte(nil))).
			DoAndReturn(func(b []byte) (int, error) {
				return len(b), nil
			}).
			Times(1)

		tp, _ := setupRelTransp(t, ctrl, nil, func() net.Conn { return conn })

		res := baseRes.Clone().(*sip.Response) //nolint:forcetypeassert
		outRes := sip.NewOutboundResponse(res)

		if got := tp.SendResponse(t.Context(), outRes, nil); got != nil {
			t.Fatalf("tp.SendResponse(ctx, res, nil) = %v, want nil", got)
		}
		if got, want := outRes.Transport(), tp.Proto(); got != want {
			t.Errorf("outRes.Transport() = %v, want %v", got, want)
		}
		if got, want := outRes.LocalAddr(), netip.AddrPortFrom(netip.IPv4Unspecified(), 33000); got != want {
			t.Errorf("outRes.LocalAddr() = %v, want %v", got, want)
		}
		if got, want := outRes.RemoteAddr(), netip.AddrPortFrom(netip.AddrFrom4([4]byte{123, 123, 123, 123}), 5060); got != want {
			t.Errorf("outRes.RemoteAddr() = %v, want %v", got, want)
		}
		if ct, ok := res.Headers.ContentLength(); ok {
			if got, want := int(ct), len(res.Body); got != want {
				t.Errorf("Content-Length header value = %d, want %d", got, want)
			}
		} else {
			t.Error("Content-Length header is missing")
		}
	})
}

func TestReliableTransport_ReceiveRequests(t *testing.T) {
	t.Parallel()

	type readPacket struct {
		buf []byte
		err error
	}
	readPackets := make(chan readPacket, 1)

	type writePacket struct {
		buf []byte
	}
	writePackets := make(chan writePacket)

	ctrl := gomock.NewController(t)
	lsClosed := make(chan struct{})
	tp, ls := setupRelTransp(t, ctrl, func() { close(lsClosed) }, nil)

	conn := netmock.NewMockConn(ctrl)
	conn.EXPECT().
		LocalAddr().
		Return(&net.TCPAddr{IP: net.IPv4zero, Port: 5060}).
		MinTimes(1)
	conn.EXPECT().
		RemoteAddr().
		Return(&net.TCPAddr{IP: net.ParseIP("123.123.123.123").To4(), Port: 12345}).
		MinTimes(1)
	conn.EXPECT().
		SetReadDeadline(gomock.AssignableToTypeOf(time.Time{})).
		Return(nil).
		AnyTimes()

	connClosed := make(chan struct{})
	conn.EXPECT().
		Close().
		Do(func() error {
			close(connClosed)
			return nil
		}).
		Return(nil).
		Times(1)

	conn.EXPECT().
		Read(gomock.AssignableToTypeOf([]byte(nil))).
		DoAndReturn(func(b []byte) (int, error) {
			select {
			case <-connClosed:
				return 0, errtrace.Wrap(net.ErrClosed)
			case p := <-readPackets:
				if p.err != nil {
					return 0, errtrace.Wrap(p.err)
				}
				n := copy(b, p.buf)
				return n, nil
			}
		}).
		AnyTimes()
	conn.EXPECT().
		Write(gomock.AssignableToTypeOf([]byte(nil))).
		DoAndReturn(func(b []byte) (int, error) {
			select {
			case <-connClosed:
				return 0, errtrace.Wrap(net.ErrClosed)
			default:
				writePackets <- writePacket{
					buf: slices.Clone(b),
				}
				return len(b), nil
			}
		}).
		AnyTimes()
	conn.EXPECT().
		SetWriteDeadline(gomock.AssignableToTypeOf(time.Time{})).
		Return(nil).
		AnyTimes()

	var connAccepted atomic.Bool
	ls.EXPECT().
		Accept().
		DoAndReturn(func() (net.Conn, error) {
			if connAccepted.CompareAndSwap(false, true) {
				return conn, nil
			}
			<-lsClosed
			return nil, errtrace.Wrap(net.ErrClosed)
		}).
		Times(2)

	rmtDone := make(chan struct{})
	go func() {
		// remote side
		defer close(rmtDone)

		send := func(p readPacket) bool {
			select {
			case <-connClosed:
				return false
			case readPackets <- p:
				return true
			}
		}

		// ping must be skipped
		if !send(readPacket{
			buf: []byte("\r\n\r\n"),
		}) {
			return
		}

		// temp conn error must be skipped
		if !send(readPacket{
			err: os.ErrDeadlineExceeded,
		}) {
			return
		}

		// valid request must be received
		if !send(readPacket{
			buf: []byte(
				"INVITE sip:127.0.0.1:5060 SIP/2.0\r\n" +
					"Via: SIP/2.0/TCP example.com:5060;branch=" + sip.MagicCookie + ".qwerty\r\n" +
					"From: Bob <sip:bob@example.com>;tag=abc\r\n" +
					"To: Alice <sip:alice@127.0.0.1>\r\n" +
					"Call-ID: 123-abc-xyz@example.com\r\n" +
					"CSeq: 1 INVITE\r\n" +
					"Max-Forwards: 70\r\n" +
					"Content-Length: 12\r\n" +
					"\r\n" +
					"hello world!",
			),
		}) {
			return
		}

		// too big request must be discarded with 413 response
		if !send(readPacket{
			buf: []byte(
				"OPTIONS sip:127.0.0.1:5060 SIP/2.0\r\n" +
					"Via: SIP/2.0/TCP example.com:5060;branch=" + sip.MagicCookie + ".qwerty\r\n" +
					"From: Bob <sip:bob@example.com>;tag=abc\r\n" +
					"To: Alice <sip:alice@127.0.0.1>\r\n" +
					"Call-ID: 123-abc-xyz@example.com\r\n" +
					"CSeq: 1 INVITE\r\n" +
					"Max-Forwards: 70\r\n" +
					"Content-Length: " + strconv.Itoa(int(sip.MaxMsgSize+1)) + "\r\n" +
					"\r\n",
			),
		}) {
			return
		}
	}()

	reqs := make(chan *sip.InboundRequest)
	unbind := tp.OnRequest(func(_ context.Context, _ sip.ServerTransport, req *sip.InboundRequest) {
		reqs <- req
	})
	defer unbind()

	go tp.Serve() //nolint:errcheck

	// got valid request
	req := <-reqs
	wantMsg := "INVITE sip:127.0.0.1:5060 SIP/2.0\r\n" +
		"Via: SIP/2.0/TCP example.com:5060;branch=" + sip.MagicCookie + ".qwerty;received=123.123.123.123\r\n" +
		"From: \"Bob\" <sip:bob@example.com>;tag=abc\r\n" +
		"To: \"Alice\" <sip:alice@127.0.0.1>\r\n" +
		"Call-ID: 123-abc-xyz@example.com\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Max-Forwards: 70\r\n" +
		"Content-Length: 12\r\n" +
		"\r\n" +
		"hello world!"
	if diff := cmp.Diff(req.Render(nil), wantMsg); diff != "" {
		t.Errorf("unexpected request received\ndiff (-got +want)\n%v", diff)
	}

	// got too big request and must send 413 response
	pkt, ok := <-writePackets
	if !ok {
		t.Fatal("unexpected end of outbound packets")
	}
	wantMsg = "SIP/2.0 413 Request Entity Too Large\r\n" +
		"Via: SIP/2.0/TCP example.com:5060;branch=" + sip.MagicCookie + ".qwerty;received=123.123.123.123\r\n" +
		"From: \"Bob\" <sip:bob@example.com>;tag=abc\r\n" +
		"To: \"Alice\" <sip:alice@127.0.0.1>;tag=[a-zA-Z0-9]+\r\n" +
		"Call-ID: 123-abc-xyz@example.com\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Content-Length: 0\r\n" +
		"\r\n"
	gotMsg := string(pkt.buf)
	if match, err := regexp.MatchString(wantMsg, gotMsg); err != nil {
		t.Errorf("compile regexp failed: %v", err)
	} else if !match {
		t.Errorf("unexpected response sent\ndiff (-got +want)\n%v", cmp.Diff(gotMsg, wantMsg))
	}

	<-rmtDone
}

func TestReliableTransport_ReceiveRequests_PanicInHandler(t *testing.T) {
	t.Parallel()

	type readPacket struct {
		buf []byte
		err error
	}
	readPackets := make(chan readPacket)

	type writePacket struct {
		buf []byte
	}
	writePackets := make(chan writePacket)

	ctrl := gomock.NewController(t)
	lsClosed := make(chan struct{})
	tp, ls := setupRelTransp(t, ctrl, func() { close(lsClosed) }, nil)

	conn := netmock.NewMockConn(ctrl)
	conn.EXPECT().
		LocalAddr().
		Return(&net.TCPAddr{IP: net.IPv4zero, Port: 5060}).
		MinTimes(1)
	conn.EXPECT().
		RemoteAddr().
		Return(&net.TCPAddr{IP: net.ParseIP("123.123.123.123").To4(), Port: 12345}).
		MinTimes(1)
	conn.EXPECT().
		SetReadDeadline(gomock.AssignableToTypeOf(time.Time{})).
		Return(nil).
		AnyTimes()
	conn.EXPECT().
		SetWriteDeadline(gomock.AssignableToTypeOf(time.Time{})).
		Return(nil).
		AnyTimes()

	connClosed := make(chan struct{})
	conn.EXPECT().
		Close().
		Do(func() error {
			select {
			case <-connClosed:
			default:
				close(connClosed)
			}
			return nil
		}).
		Return(nil).
		Times(1)

	conn.EXPECT().
		Read(gomock.AssignableToTypeOf([]byte(nil))).
		DoAndReturn(func(b []byte) (int, error) {
			select {
			case <-connClosed:
				return 0, errtrace.Wrap(net.ErrClosed)
			case p := <-readPackets:
				if p.err != nil {
					return 0, errtrace.Wrap(p.err)
				}
				n := copy(b, p.buf)
				return n, nil
			}
		}).
		AnyTimes()

	conn.EXPECT().
		Write(gomock.AssignableToTypeOf([]byte(nil))).
		DoAndReturn(func(b []byte) (int, error) {
			select {
			case <-connClosed:
				return 0, errtrace.Wrap(net.ErrClosed)
			default:
				writePackets <- writePacket{buf: slices.Clone(b)}
				return len(b), nil
			}
		}).
		AnyTimes()

	var connAccepted atomic.Bool
	ls.EXPECT().
		Accept().
		DoAndReturn(func() (net.Conn, error) {
			if connAccepted.CompareAndSwap(false, true) {
				return conn, nil
			}
			<-lsClosed
			return nil, errtrace.Wrap(net.ErrClosed)
		}).
		Times(2)

	unbind := tp.OnRequest(func(context.Context, sip.ServerTransport, *sip.InboundRequest) {
		panic("boom")
	})
	defer unbind()

	go tp.Serve() //nolint:errcheck

	readPackets <- readPacket{buf: []byte(
		"INVITE sip:127.0.0.1:5060 SIP/2.0\r\n" +
			"Via: SIP/2.0/TCP example.com:5060;branch=" + sip.MagicCookie + ".panic\r\n" +
			"From: Bob <sip:bob@example.com>;tag=abc\r\n" +
			"To: Alice <sip:alice@127.0.0.1>\r\n" +
			"Call-ID: 123-abc-xyz@example.com\r\n" +
			"CSeq: 1 INVITE\r\n" +
			"Max-Forwards: 70\r\n" +
			"Content-Length: 0\r\n" +
			"\r\n",
	)}

	select {
	case pkt := <-writePackets:
		gotMsg := string(pkt.buf)
		if !strings.HasPrefix(gotMsg, "SIP/2.0 500 Server Internal Error\r\n") {
			t.Fatalf("unexpected response sent: %q", gotMsg)
		}
		if !strings.Contains(gotMsg, "\r\nRetry-After: 60\r\n") {
			t.Fatalf("missing Retry-After header in response: %q", gotMsg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for 500 response")
	}

	select {
	case <-connClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for connection close")
	}
}
