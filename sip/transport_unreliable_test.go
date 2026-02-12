package sip_test

import (
	"context"
	"errors"
	"math/rand/v2"
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

func TestNewUnreliableTransport(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)

	conn := netmock.NewMockPacketConn(ctrl)
	conn.EXPECT().
		LocalAddr().
		Return(&net.UDPAddr{IP: net.IPv4zero, Port: 5060}).
		MinTimes(1)
	conn.EXPECT().
		Close().
		Return(nil).
		Times(1)

	t.Run("empty protocol", func(t *testing.T) {
		t.Parallel()

		_, got := sip.NewUnreliableTransport("", conn, nil)
		want := sip.ErrInvalidArgument
		if diff := cmp.Diff(got, want, cmpopts.EquateErrors()); diff != "" {
			t.Fatalf("sip.NewUnreliableTransport(\"\", conn, nil) error = %v, want %v\ndiff (-got +want):\n%v", got, want, diff)
		}
	})

	t.Run("nil connection", func(t *testing.T) {
		t.Parallel()

		_, got := sip.NewUnreliableTransport("UDP", nil, nil)
		want := sip.ErrInvalidArgument
		if diff := cmp.Diff(got, want, cmpopts.EquateErrors()); diff != "" {
			t.Fatalf("sip.NewUnreliableTransport(\"UDP\", nil, nil) error = %v, want %v\ndiff (-got +want):\n%v", got, want, diff)
		}
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		tp, err := sip.NewUnreliableTransport("UDP", conn, nil)
		if err != nil {
			t.Fatalf("sip.NewUnreliableTransport(\"UDP\", conn, nil) error = %v, want nil", err)
		}

		if got := tp.Proto(); got != sip.TransportProto("UDP") {
			t.Errorf("tp.Proto() = %q, want \"UDP\"", got)
		}
		if got := tp.Network(); got != "udp" {
			t.Errorf("tp.Network() = %q, want \"udp\"", got)
		}
		if got := tp.Reliable(); got {
			t.Errorf("tp.Reliable() = %v, want false", got)
		}
		if got := tp.Secured(); got {
			t.Errorf("tp.Secured() = %v, want false", got)
		}
		if got := tp.Streamed(); got {
			t.Errorf("tp.Streamed() = %v, want false", got)
		}
		if got := tp.LocalAddr(); got != netip.MustParseAddrPort("0.0.0.0:5060") {
			t.Errorf("tp.LocalAddr() = %v, want 0.0.0.0:5060", got)
		}
		if got := tp.DefaultPort(); got != 5060 {
			t.Errorf("tp.DefaultPort() = %v, want 5060", got)
		}

		if err := tp.Close(t.Context()); err != nil {
			t.Fatalf("tp.Close() error = %v, want nil", err)
		}
	})
}

func setupUnrelTransp(tb testing.TB, onConnClose func()) (*sip.UnreliableTransport, *netmock.MockPacketConn) {
	tb.Helper()

	ctrl := gomock.NewController(tb)
	conn := netmock.NewMockPacketConn(ctrl)

	conn.EXPECT().
		LocalAddr().
		Return(&net.UDPAddr{IP: net.IPv4zero, Port: 5060}).
		MinTimes(1)
	conn.EXPECT().
		SetReadDeadline(gomock.AssignableToTypeOf(time.Time{})).
		Return(nil).
		AnyTimes()
	conn.EXPECT().
		Close().
		Do(func() error {
			if onConnClose != nil {
				onConnClose()
			}
			return nil
		}).
		Return(nil)

	tp, err := sip.NewUnreliableTransport("UDP", conn, &sip.UnreliableTransportOptions{
		DefaultPort: 5060,
		SentBy:      sip.HostPort("127.0.0.1", 0),
		// Log:         log.Console(),
	})
	if err != nil {
		tb.Fatalf("sip.NewUnreliableTransport(\"UDP\", conn, opts) error = %v, want nil", err)
	}

	tb.Cleanup(func() {
		tp.Close(tb.Context())
	})

	return tp, conn
}

func TestUnreliableTransport_SendRequest(t *testing.T) {
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
					Proto:     sip.ProtoVer20(),
					Transport: "UDP",
					Params:    make(header.Values).Set("branch", sip.MagicCookie+".qwerty"),
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

	cases := []struct {
		name    string
		wantErr error
		setup   func(
			*testing.T,
			*sip.UnreliableTransport,
			*netmock.MockPacketConn,
		) (context.Context, *sip.OutboundRequestEnvelope)
	}{
		{
			"invalid request",
			sip.ErrInvalidMessage,
			func(
				t *testing.T,
				_ *sip.UnreliableTransport,
				_ *netmock.MockPacketConn,
			) (context.Context, *sip.OutboundRequestEnvelope) {
				t.Helper()

				req := baseReq.Clone().(*sip.Request) //nolint:forcetypeassert
				req.Headers.Del("Via")
				outReq, err := sip.NewOutboundRequestEnvelope(req)
				if err != nil {
					t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
				}
				outReq.SetRemoteAddr(netip.MustParseAddrPort("123.123.123.123:5060"))

				return t.Context(), outReq
			},
		},
		{
			"too big request",
			sip.ErrMessageTooLarge,
			func(
				t *testing.T,
				_ *sip.UnreliableTransport,
				_ *netmock.MockPacketConn,
			) (context.Context, *sip.OutboundRequestEnvelope) {
				t.Helper()

				req := baseReq.Clone().(*sip.Request) //nolint:forcetypeassert
				req.Body = make([]byte, sip.MTU)
				outReq, err := sip.NewOutboundRequestEnvelope(req)
				if err != nil {
					t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
				}
				outReq.SetRemoteAddr(netip.MustParseAddrPort("123.123.123.123:5060"))

				return t.Context(), outReq
			},
		},
		{
			"request with deadline",
			os.ErrDeadlineExceeded,
			func(
				t *testing.T,
				_ *sip.UnreliableTransport,
				conn *netmock.MockPacketConn,
			) (context.Context, *sip.OutboundRequestEnvelope) {
				t.Helper()

				deadline := time.Now().Add(1 * time.Second)
				ctx, cancel := context.WithDeadline(t.Context(), deadline)
				conn.EXPECT().
					SetWriteDeadline(deadline).
					Return(nil).
					Times(1)
				conn.EXPECT().
					SetWriteDeadline(time.Time{}).
					Return(nil).
					Times(1)
				conn.EXPECT().
					WriteTo(
						gomock.AssignableToTypeOf([]byte(nil)),
						gomock.AssignableToTypeOf((*net.UDPAddr)(nil)),
					).
					Return(0, os.ErrDeadlineExceeded).
					Times(1)

				req := baseReq.Clone().(*sip.Request) //nolint:forcetypeassert
				outReq, err := sip.NewOutboundRequestEnvelope(req)
				if err != nil {
					t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
				}
				outReq.SetRemoteAddr(netip.MustParseAddrPort("123.123.123.123:5060"))

				t.Cleanup(cancel)

				return ctx, outReq
			},
		},
		{
			"request to invalid address",
			sip.NewInvalidArgumentError("invalid remote address"),
			func(
				t *testing.T,
				_ *sip.UnreliableTransport,
				_ *netmock.MockPacketConn,
			) (context.Context, *sip.OutboundRequestEnvelope) {
				t.Helper()

				req := baseReq.Clone().(*sip.Request) //nolint:forcetypeassert
				outReq, err := sip.NewOutboundRequestEnvelope(req)
				if err != nil {
					t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
				}

				return t.Context(), outReq
			},
		},
		{
			"valid request",
			nil,
			func(
				t *testing.T,
				tp *sip.UnreliableTransport,
				conn *netmock.MockPacketConn,
			) (context.Context, *sip.OutboundRequestEnvelope) {
				t.Helper()

				conn.EXPECT().
					SetWriteDeadline(gomock.AssignableToTypeOf(time.Time{})).
					Return(nil).
					AnyTimes()
				conn.EXPECT().
					WriteTo(
						gomock.AssignableToTypeOf([]byte(nil)),
						gomock.Cond(func(x *net.UDPAddr) bool {
							return x.IP.Equal(net.ParseIP("123.123.123.123")) && x.Port == 5060
						}),
					).
					DoAndReturn(func(b []byte, addr net.Addr) (int, error) {
						return len(b), nil
					}).
					Times(1)

				req := baseReq.Clone().(*sip.Request) //nolint:forcetypeassert
				outReq, err := sip.NewOutboundRequestEnvelope(req)
				if err != nil {
					t.Fatalf("sip.NewOutboundRequestEnvelope() error = %v, want nil", err)
				}
				outReq.SetRemoteAddr(netip.MustParseAddrPort("123.123.123.123:0"))

				return t.Context(), outReq
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tp, conn := setupUnrelTransp(t, nil)
			ctx, req := c.setup(t, tp, conn)

			got := tp.SendRequest(ctx, req, nil)
			if got != c.wantErr && !errors.Is(got, c.wantErr) && got.Error() != c.wantErr.Error() { //nolint:errorlint
				t.Fatalf("tp.SendRequest(ctx, req, nil) error = %v, want %v\ndiff (-got +want):\n%v",
					got, c.wantErr, cmp.Diff(got, c.wantErr, cmpopts.EquateErrors()),
				)
			}

			if c.wantErr == nil {
				if got, want := req.Transport(), tp.Proto(); got != want {
					t.Errorf("req.Transport() = %v, want %v", got, want)
				}

				if got, want := req.LocalAddr(), tp.LocalAddr(); got != want {
					t.Errorf("req.LocalAddr() = %v, want %v", got, want)
				}
			}
		})
	}
}

//nolint:gocognit
func TestUnreliableTransport_SendResponse(t *testing.T) {
	t.Parallel()

	baseRes := &sip.Response{
		Proto:  sip.ProtoVer20(),
		Status: sip.ResponseStatusOK,
		Headers: make(sip.Headers).
			Set(header.Via{
				{
					Proto:     sip.ProtoVer20(),
					Transport: "UDP",
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

	cases := []struct {
		name    string
		wantErr error
		setup   func(
			*testing.T,
			*sip.UnreliableTransport,
			*netmock.MockPacketConn,
		) (context.Context, *sip.OutboundResponseEnvelope, netip.AddrPort)
	}{
		{
			"invalid response",
			sip.ErrInvalidMessage,
			func(
				t *testing.T,
				_ *sip.UnreliableTransport,
				_ *netmock.MockPacketConn,
			) (context.Context, *sip.OutboundResponseEnvelope, netip.AddrPort) {
				t.Helper()

				res := baseRes.Clone().(*sip.Response) //nolint:forcetypeassert
				res.Status = 55
				outRes, err := sip.NewOutboundResponseEnvelope(res)
				if err != nil {
					t.Fatalf("sip.NewOutboundResponseEnvelope() error = %v, want nil", err)
				}

				return t.Context(), outRes, netip.AddrPort{}
			},
		},
		{
			"response to Via IP and port",
			nil,
			func(
				t *testing.T,
				tp *sip.UnreliableTransport,
				conn *netmock.MockPacketConn,
			) (context.Context, *sip.OutboundResponseEnvelope, netip.AddrPort) {
				t.Helper()

				conn.EXPECT().
					SetWriteDeadline(gomock.AssignableToTypeOf(time.Time{})).
					Return(nil).
					AnyTimes()
				conn.EXPECT().
					WriteTo(
						gomock.AssignableToTypeOf([]byte(nil)),
						gomock.Cond(func(x *net.UDPAddr) bool { return x.IP.Equal(net.ParseIP("123.123.123.123")) && x.Port == 5060 }),
					).
					DoAndReturn(func(b []byte, addr net.Addr) (int, error) {
						return len(b), nil
					}).
					Times(1)

				res := baseRes.Clone().(*sip.Response) //nolint:forcetypeassert
				outRes, err := sip.NewOutboundResponseEnvelope(res)
				if err != nil {
					t.Fatalf("sip.NewOutboundResponseEnvelope() error = %v, want nil", err)
				}

				return t.Context(), outRes, netip.MustParseAddrPort("123.123.123.123:5060")
			},
		},
		{
			"response to Via received and port",
			nil,
			func(
				t *testing.T,
				tp *sip.UnreliableTransport,
				conn *netmock.MockPacketConn,
			) (context.Context, *sip.OutboundResponseEnvelope, netip.AddrPort) {
				t.Helper()

				conn.EXPECT().
					SetWriteDeadline(gomock.AssignableToTypeOf(time.Time{})).
					Return(nil).
					AnyTimes()
				conn.EXPECT().
					WriteTo(
						gomock.AssignableToTypeOf([]byte(nil)),
						gomock.Cond(func(x *net.UDPAddr) bool {
							return x.IP.Equal(net.ParseIP("123.123.123.123")) && x.Port == 5060
						}),
					).
					DoAndReturn(func(b []byte, addr net.Addr) (int, error) {
						return len(b), nil
					}).
					Times(1)

				res := baseRes.Clone().(*sip.Response) //nolint:forcetypeassert
				via, _ := res.Headers.FirstVia()
				via.Addr = header.HostPort("example.com", 5060)
				via.Params.Set("received", "123.123.123.123")
				outRes, err := sip.NewOutboundResponseEnvelope(res)
				if err != nil {
					t.Fatalf("sip.NewOutboundResponseEnvelope() error = %v, want nil", err)
				}

				return t.Context(), outRes, netip.MustParseAddrPort("123.123.123.123:5060")
			},
		},
		{
			"response to Via received and rport",
			nil,
			func(
				t *testing.T,
				tp *sip.UnreliableTransport,
				conn *netmock.MockPacketConn,
			) (context.Context, *sip.OutboundResponseEnvelope, netip.AddrPort) {
				t.Helper()

				conn.EXPECT().
					SetWriteDeadline(gomock.AssignableToTypeOf(time.Time{})).
					Return(nil).
					AnyTimes()
				conn.EXPECT().
					WriteTo(
						gomock.AssignableToTypeOf([]byte(nil)),
						gomock.Cond(func(x *net.UDPAddr) bool {
							return x.IP.Equal(net.ParseIP("123.123.123.123")) && x.Port == 555
						}),
					).
					DoAndReturn(func(b []byte, addr net.Addr) (int, error) {
						return len(b), nil
					}).
					Times(1)

				res := baseRes.Clone().(*sip.Response) //nolint:forcetypeassert
				via, _ := res.Headers.FirstVia()
				via.Addr = header.HostPort("example.com", 5060)
				via.Params.Set("received", "123.123.123.123").Set("rport", "555")
				outRes, err := sip.NewOutboundResponseEnvelope(res)
				if err != nil {
					t.Fatalf("sip.NewOutboundResponseEnvelope() error = %v, want nil", err)
				}

				return t.Context(), outRes, netip.MustParseAddrPort("123.123.123.123:555")
			},
		},
		{
			"response to Via maddr and default port",
			nil,
			func(
				t *testing.T,
				tp *sip.UnreliableTransport,
				conn *netmock.MockPacketConn,
			) (context.Context, *sip.OutboundResponseEnvelope, netip.AddrPort) {
				t.Helper()

				conn.EXPECT().
					SetWriteDeadline(gomock.AssignableToTypeOf(time.Time{})).
					Return(nil).
					AnyTimes()
				conn.EXPECT().
					WriteTo(
						gomock.AssignableToTypeOf([]byte(nil)),
						gomock.Cond(func(x *net.UDPAddr) bool {
							return x.IP.Equal(net.ParseIP("123.123.123.123")) && x.Port == 5060
						}),
					).
					DoAndReturn(func(b []byte, addr net.Addr) (int, error) {
						return len(b), nil
					}).
					Times(1)

				res := baseRes.Clone().(*sip.Response) //nolint:forcetypeassert
				via, _ := res.Headers.FirstVia()
				via.Addr = header.HostPort("example.com", 5060)
				via.Params.Set("maddr", "123.123.123.123")
				outRes, err := sip.NewOutboundResponseEnvelope(res)
				if err != nil {
					t.Fatalf("sip.NewOutboundResponseEnvelope() error = %v, want nil", err)
				}

				return t.Context(), outRes, netip.MustParseAddrPort("123.123.123.123:5060")
			},
		},
		// TODO: fails on GitHub Test action, probably due to different DNS resolution
		// {
		// 	"response to Via resolved IP and port",
		// 	nil,
		// 	func(t *testing.T, _ *sip.UnreliableTransport, conn *MockPacketConn) (context.Context, *sip.Response, netip.AddrPort) {
		// 		t.Helper()

		// 		res := baseRes.Clone().(*sip.Response) //nolint:forcetypeassert
		// 		hop, _ := sip.FirstHeaderElem[header.Via](res.Headers, "Via")
		// 		hop.Addr = header.HostPort("example.com", 5060)

		// 		ips, err := net.LookupIP("example.com")
		// 		if err != nil {
		// 			t.Fatalf("setup failed: net.LookupIP(\"example.com\") failed: %v", err)
		// 		}
		// 		if len(ips) == 0 {
		// 			t.Fatalf("setup failed: net.LookupIP(\"example.com\") returned no IP addresses")
		// 		}

		// 		conn.EXPECT().
		// 			WriteTo(
		// 				gomock.AssignableToTypeOf([]byte(nil)),
		// 				gomock.Cond(func(x *net.UDPAddr) bool { return x.IP.Equal(ips[0]) && x.Port == 5060 }),
		// 			).
		// 			Return(len(res.Render(nil)), nil).
		// 			Times(1)

		// 		addr, _ := netip.AddrFromSlice(ips[0])
		// 		return t.Context(), res, netip.AddrPortFrom(addr, 5060)
		// 	},
		// },
		// TODO test response to host and port resolved via DNS SRV requests
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tp, conn := setupUnrelTransp(t, nil)
			ctx, res, addr := c.setup(t, tp, conn)

			//nolint:errorlint
			if got, want := tp.SendResponse(ctx, res, nil), c.wantErr; got != want && !errors.Is(got, want) && got.Error() != want.Error() {
				t.Fatalf("tp.SendResponse(ctx, res, nil) = %+v, want %+v\ndiff (-got +want):\n%v",
					got, want, cmp.Diff(got, want, cmpopts.EquateErrors()),
				)
			}

			if c.wantErr == nil {
				if got, want := res.Transport(), tp.Proto(); got != want {
					t.Errorf("res.Transport() = %v, want %v", got, want)
				}

				if got, want := res.LocalAddr(), tp.LocalAddr(); got != want {
					t.Errorf("res.LocalAddr() = %v, want %v", got, want)
				}

				if got, want := res.RemoteAddr(), addr; got != want {
					t.Errorf("res.RemoteAddr() = %v, want %v", got, want)
				}
			}
		})
	}
}

func TestUnreliableTransport_ReceiveRequests(t *testing.T) {
	t.Parallel()

	type readPacket struct {
		buf  []byte
		addr netip.AddrPort
		err  error
	}
	readPackets := make(chan readPacket)

	type writePacket struct {
		buf  []byte
		addr netip.AddrPort
		// err  error
	}
	writePackets := make(chan writePacket)

	connClosed := make(chan struct{})

	tp, conn := setupUnrelTransp(t, func() {
		close(connClosed)
		close(readPackets)
		close(writePackets)
	})

	conn.EXPECT().
		ReadFrom(gomock.AssignableToTypeOf([]byte(nil))).
		DoAndReturn(func(b []byte) (int, net.Addr, error) {
			p, ok := <-readPackets
			if !ok {
				return 0, nil, errtrace.Wrap(net.ErrClosed)
			}
			if p.err != nil {
				return 0, nil, errtrace.Wrap(p.err)
			}
			n := copy(b, p.buf)
			addr := &net.UDPAddr{IP: p.addr.Addr().AsSlice(), Port: int(p.addr.Port())}
			return n, addr, nil
		}).
		AnyTimes()
	conn.EXPECT().
		SetWriteDeadline(gomock.AssignableToTypeOf(time.Time{})).
		Return(nil).
		AnyTimes()
	conn.EXPECT().
		WriteTo(gomock.AssignableToTypeOf([]byte(nil)), gomock.AssignableToTypeOf((*net.UDPAddr)(nil))).
		DoAndReturn(func(b []byte, addr net.Addr) (int, error) {
			select {
			case <-connClosed:
				return 0, errtrace.Wrap(net.ErrClosed)
			default:
				writePackets <- writePacket{
					buf:  slices.Clone(b),
					addr: netip.MustParseAddrPort(addr.String()),
				}
				return len(b), nil
			}
		}).
		AnyTimes()

	rmtDone := make(chan struct{})
	go func() {
		// some trash, must be skipped
		readPackets <- readPacket{
			buf:  []byte("hello!"),
			addr: netip.MustParseAddrPort("123.123.123.123:5060"),
		}
		readPackets <- readPacket{
			buf:  []byte("qwerty"),
			addr: netip.MustParseAddrPort("123.123.123.123:5060"),
		}

		// invalid request must be skipped
		readPackets <- readPacket{
			buf: []byte(
				"ACK sip:127.0.0.1:5060 SIP/2.0\r\n" +
					"Via: SIP/2.0/UDP example.com:5060;branch=" + sip.MagicCookie + ".qwerty\r\n" +
					"From: Bob <sip:bob@example.com>;tag=abc\r\n" +
					"To: Alice <sip:alice@127.0.0.1>\r\n" +
					"\r\n",
			),
			addr: netip.MustParseAddrPort("123.123.123.123:5060"),
		}

		// ping must be skipped
		readPackets <- readPacket{
			buf:  []byte("\r\n\r\n"),
			addr: netip.MustParseAddrPort("123.123.123.123:5060"),
		}

		// valid request must be received
		readPackets <- readPacket{
			buf: []byte(
				"INVITE sip:127.0.0.1:5060 SIP/2.0\r\n" +
					"Via: SIP/2.0/UDP example.com:5060;branch=" + sip.MagicCookie + ".qwerty\r\n" +
					"From: Bob <sip:bob@example.com>;tag=abc\r\n" +
					"To: Alice <sip:alice@127.0.0.1>\r\n" +
					"Call-ID: 123-abc-xyz@example.com\r\n" +
					"CSeq: 1 INVITE\r\n" +
					"Max-Forwards: 70\r\n" +
					"Content-Length: 12\r\n" +
					"\r\n" +
					"hello world!this is will be skipped",
			),
			addr: netip.MustParseAddrPort("123.123.123.123:5060"),
		}

		// temp conn error must be skipped
		readPackets <- readPacket{
			err: os.ErrDeadlineExceeded,
		}

		// bad request must be discarded with 400 response
		readPackets <- readPacket{
			buf: []byte(
				"OPTIONS sip:127.0.0.1:5060 SIP/2.0\r\n" +
					"Via: SIP/2.0/UDP example.com:5060;branch=" + sip.MagicCookie + ".qwerty;rport\r\n" +
					"From: Bob <sip:bob@example.com>;tag=abc\r\n" +
					"To: Alice <sip:alice@127.0.0.1>\r\n" +
					"Call-ID: 123-abc-xyz@example.com\r\n" +
					"CSeq: 1 INVITE\r\n" +
					"Max-Forwards: 70\r\n" +
					"qwerty\r\n" +
					"\r\n",
			),
			addr: netip.MustParseAddrPort("123.123.123.123:555"),
		}

		close(rmtDone)
	}()

	reqs := make(chan *sip.InboundRequestEnvelope)
	unbind := tp.UseInboundRequestInterceptor(
		sip.InboundRequestInterceptorFunc(
			func(_ context.Context, req *sip.InboundRequestEnvelope, _ sip.RequestReceiver) error {
				reqs <- req
				return nil
			},
		),
	)
	defer unbind()

	go tp.Serve(t.Context()) //nolint:errcheck

	// got valid request
	req := <-reqs
	wantMsg := "INVITE sip:127.0.0.1:5060 SIP/2.0\r\n" +
		"Via: SIP/2.0/UDP example.com:5060;branch=" + sip.MagicCookie + ".qwerty;received=123.123.123.123\r\n" +
		"From: \"Bob\" <sip:bob@example.com>;tag=abc\r\n" +
		"To: \"Alice\" <sip:alice@127.0.0.1>\r\n" +
		"Call-ID: 123-abc-xyz@example.com\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"Max-Forwards: 70\r\n" +
		"Content-Length: 12\r\n" +
		"\r\n" +
		"hello world!"
	if diff := cmp.Diff(req.Render(nil), wantMsg); diff != "" {
		t.Errorf("unexpected request received\ndiff (-got +want)\n%s", diff)
	}
	if got, want := req.Transport(), tp.Proto(); got != want {
		t.Errorf("req.Transport() = %v, want %v", got, want)
	}
	if got, want := req.LocalAddr(), tp.LocalAddr(); got != want {
		t.Errorf("req.LocalAddr() = %v, want %v", got, want)
	}
	if !req.RemoteAddr().IsValid() {
		t.Errorf("req.RemoteAddr() = %v is invalid", req.RemoteAddr())
	}

	// got bad request and must send 400 response
	pkt, ok := <-writePackets
	if !ok {
		t.Fatal("unexpected end of outbound packets")
	}
	wantMsg = "SIP/2.0 400 Bad Request\r\n" +
		"Via: SIP/2.0/UDP example.com:5060;branch=" + sip.MagicCookie + ".qwerty;received=123.123.123.123;rport=555\r\n" +
		"From: \"Bob\" <sip:bob@example.com>;tag=abc\r\n" +
		"To: \"Alice\" <sip:alice@127.0.0.1>;tag=[a-zA-Z0-9]+\r\n" +
		"Call-ID: 123-abc-xyz@example.com\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"\r\n"
	gotMsg := string(pkt.buf)
	if match, err := regexp.MatchString(wantMsg, gotMsg); err != nil {
		t.Errorf("compile regexp failed: %v", err)
	} else if !match {
		t.Errorf("unexpected response sent\ndiff (-got +want)\n%v", cmp.Diff(gotMsg, wantMsg))
	}
	if got, want := pkt.addr, netip.MustParseAddrPort("123.123.123.123:555"); got != want {
		t.Errorf("unexpected response remote address %v, want %v", got, want)
	}

	<-rmtDone
}

func TestUnreliableTransport_ReceiveRequests_PanicInHandler(t *testing.T) {
	t.Parallel()

	type readPacket struct {
		buf  []byte
		addr netip.AddrPort
		err  error
	}
	readPackets := make(chan readPacket)

	type writePacket struct {
		buf  []byte
		addr netip.AddrPort
	}
	writePackets := make(chan writePacket)

	connClosed := make(chan struct{})

	tp, conn := setupUnrelTransp(t, func() {
		close(connClosed)
		close(readPackets)
		close(writePackets)
	})

	conn.EXPECT().
		ReadFrom(gomock.AssignableToTypeOf([]byte(nil))).
		DoAndReturn(func(b []byte) (int, net.Addr, error) {
			p, ok := <-readPackets
			if !ok {
				return 0, nil, errtrace.Wrap(net.ErrClosed)
			}
			if p.err != nil {
				return 0, nil, errtrace.Wrap(p.err)
			}
			n := copy(b, p.buf)
			addr := &net.UDPAddr{IP: p.addr.Addr().AsSlice(), Port: int(p.addr.Port())}
			return n, addr, nil
		}).
		AnyTimes()
	conn.EXPECT().
		SetWriteDeadline(gomock.AssignableToTypeOf(time.Time{})).
		Return(nil).
		AnyTimes()
	conn.EXPECT().
		WriteTo(gomock.AssignableToTypeOf([]byte(nil)), gomock.AssignableToTypeOf((*net.UDPAddr)(nil))).
		DoAndReturn(func(b []byte, addr net.Addr) (int, error) {
			select {
			case <-connClosed:
				return 0, errtrace.Wrap(net.ErrClosed)
			default:
				writePackets <- writePacket{
					buf:  slices.Clone(b),
					addr: netip.MustParseAddrPort(addr.String()),
				}
				return len(b), nil
			}
		}).
		AnyTimes()

	reqs := make(chan *sip.InboundRequestEnvelope, 1)
	var calls atomic.Uint32
	unbind := tp.UseInboundRequestInterceptor(
		sip.InboundRequestInterceptorFunc(
			func(_ context.Context, req *sip.InboundRequestEnvelope, _ sip.RequestReceiver) error {
				if calls.Add(1) == 1 {
					panic("boom")
				}
				reqs <- req
				return nil
			},
		),
	)
	defer unbind()

	go tp.Serve(t.Context()) //nolint:errcheck

	rmtDone := make(chan struct{})
	go func() {
		defer close(rmtDone)

		readPackets <- readPacket{
			buf: []byte(
				"INVITE sip:127.0.0.1:5060 SIP/2.0\r\n" +
					"Via: SIP/2.0/UDP example.com:5060;branch=" + sip.MagicCookie + ".panic;rport\r\n" +
					"From: Bob <sip:bob@example.com>;tag=abc\r\n" +
					"To: Alice <sip:alice@127.0.0.1>\r\n" +
					"Call-ID: 123-abc-xyz@example.com\r\n" +
					"CSeq: 1 INVITE\r\n" +
					"Max-Forwards: 70\r\n" +
					"Content-Length: 0\r\n" +
					"\r\n",
			),
			addr: netip.MustParseAddrPort("123.123.123.123:5060"),
		}

		readPackets <- readPacket{
			buf: []byte(
				"INVITE sip:127.0.0.1:5060 SIP/2.0\r\n" +
					"Via: SIP/2.0/UDP example.com:5060;branch=" + sip.MagicCookie + ".ok;rport\r\n" +
					"From: Bob <sip:bob@example.com>;tag=abc\r\n" +
					"To: Alice <sip:alice@127.0.0.1>\r\n" +
					"Call-ID: 124-abc-xyz@example.com\r\n" +
					"CSeq: 1 INVITE\r\n" +
					"Max-Forwards: 70\r\n" +
					"Content-Length: 0\r\n" +
					"\r\n",
			),
			addr: netip.MustParseAddrPort("123.123.123.123:5060"),
		}
	}()

	select {
	case pkt := <-writePackets:
		gotMsg := string(pkt.buf)
		if !strings.HasPrefix(gotMsg, "SIP/2.0 500 Server Internal Error\r\n") {
			t.Fatalf("unexpected response sent: %q", gotMsg)
		}
		if got, want := pkt.addr, netip.MustParseAddrPort("123.123.123.123:5060"); got != want {
			t.Fatalf("unexpected response remote address %v, want %v", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for 500 response")
	}

	select {
	case <-reqs:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for next request after panic")
	}

	<-rmtDone
}

func TestUnreliableTransport_ReceiveRequests_ContentLengthTooLarge(t *testing.T) {
	t.Parallel()

	type readPacket struct {
		buf  []byte
		addr netip.AddrPort
		err  error
	}
	readPackets := make(chan readPacket)

	type writePacket struct {
		buf  []byte
		addr netip.AddrPort
		// err  error
	}
	writePackets := make(chan writePacket)

	connClosed := make(chan struct{})

	tp, conn := setupUnrelTransp(t, func() {
		close(connClosed)
		close(readPackets)
		close(writePackets)
	})

	conn.EXPECT().
		ReadFrom(gomock.AssignableToTypeOf([]byte(nil))).
		DoAndReturn(func(b []byte) (int, net.Addr, error) {
			p, ok := <-readPackets
			if !ok {
				return 0, nil, errtrace.Wrap(net.ErrClosed)
			}
			if p.err != nil {
				return 0, nil, errtrace.Wrap(p.err)
			}
			n := copy(b, p.buf)
			addr := &net.UDPAddr{IP: p.addr.Addr().AsSlice(), Port: int(p.addr.Port())}
			return n, addr, nil
		}).
		AnyTimes()
	conn.EXPECT().
		SetWriteDeadline(gomock.AssignableToTypeOf(time.Time{})).
		Return(nil).
		AnyTimes()
	conn.EXPECT().
		WriteTo(gomock.AssignableToTypeOf([]byte(nil)), gomock.AssignableToTypeOf((*net.UDPAddr)(nil))).
		DoAndReturn(func(b []byte, addr net.Addr) (int, error) {
			select {
			case <-connClosed:
				return 0, errtrace.Wrap(net.ErrClosed)
			default:
				writePackets <- writePacket{
					buf:  slices.Clone(b),
					addr: netip.MustParseAddrPort(addr.String()),
				}
				return len(b), nil
			}
		}).
		AnyTimes()

	reqRecv := make(chan struct{})
	unbind := tp.UseInboundRequestInterceptor(
		sip.InboundRequestInterceptorFunc(
			func(context.Context, *sip.InboundRequestEnvelope, sip.RequestReceiver) error {
				close(reqRecv)
				return nil
			},
		),
	)
	defer unbind()

	rmtDone := make(chan struct{})
	go func() {
		readPackets <- readPacket{
			buf: []byte(
				"OPTIONS sip:127.0.0.1:5060 SIP/2.0\r\n" +
					"Via: SIP/2.0/UDP example.com:5060;branch=" + sip.MagicCookie + ".qwerty;rport\r\n" +
					"From: Bob <sip:bob@example.com>;tag=abc\r\n" +
					"To: Alice <sip:alice@127.0.0.1>\r\n" +
					"Call-ID: 123-abc-xyz@example.com\r\n" +
					"CSeq: 1 INVITE\r\n" +
					"Max-Forwards: 70\r\n" +
					"Content-Length: " + strconv.Itoa(int(sip.MaxMsgSize+1)) + "\r\n" +
					"\r\n",
			),
			addr: netip.MustParseAddrPort("123.123.123.123:555"),
		}

		close(rmtDone)
	}()

	go tp.Serve(t.Context()) //nolint:errcheck

	// got 413 response and must not call request handler
	var pkt writePacket
	var ok bool
	select {
	case pkt, ok = <-writePackets:
		if !ok {
			t.Fatal("unexpected end of outbound packets")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for response")
	}
	wantMsg := "SIP/2.0 413 Request Entity Too Large\r\n" +
		"Via: SIP/2.0/UDP example.com:5060;branch=" + sip.MagicCookie + ".qwerty;received=123.123.123.123;rport=555\r\n" +
		"From: \"Bob\" <sip:bob@example.com>;tag=abc\r\n" +
		"To: \"Alice\" <sip:alice@127.0.0.1>;tag=[a-zA-Z0-9]+\r\n" +
		"Call-ID: 123-abc-xyz@example.com\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"\r\n"
	gotMsg := string(pkt.buf)
	if match, err := regexp.MatchString(wantMsg, gotMsg); err != nil {
		t.Errorf("compile regexp failed: %v", err)
	} else if !match {
		t.Errorf("unexpected response sent\ndiff (-got +want)\n%v", cmp.Diff(gotMsg, wantMsg))
	}
	if got, want := pkt.addr, netip.MustParseAddrPort("123.123.123.123:555"); got != want {
		t.Errorf("unexpected response remote address %v, want %v", got, want)
	}

	select {
	case <-reqRecv:
		t.Fatal("request handler called, want it not to be called")
	case <-time.After(50 * time.Millisecond):
	}

	<-rmtDone
}

func TestUnreliableTransport_ReceiveResponses(t *testing.T) {
	t.Parallel()

	type readPacket struct {
		buf  []byte
		addr netip.AddrPort
		err  error
	}
	readPackets := make(chan readPacket)

	type writePacket struct {
		buf  []byte
		addr netip.AddrPort
		// err  error
	}
	writePackets := make(chan writePacket)

	connClosed := make(chan struct{})

	tp, conn := setupUnrelTransp(t, func() {
		close(connClosed)
		close(readPackets)
		close(writePackets)
	})

	conn.EXPECT().
		ReadFrom(gomock.AssignableToTypeOf([]byte(nil))).
		DoAndReturn(func(b []byte) (int, net.Addr, error) {
			p, ok := <-readPackets
			if !ok {
				return 0, nil, errtrace.Wrap(net.ErrClosed)
			}
			if p.err != nil {
				return 0, nil, errtrace.Wrap(p.err)
			}
			n := copy(b, p.buf)
			addr := &net.UDPAddr{IP: p.addr.Addr().AsSlice(), Port: int(p.addr.Port())}
			return n, addr, nil
		}).
		AnyTimes()
	conn.EXPECT().
		SetWriteDeadline(gomock.AssignableToTypeOf(time.Time{})).
		Return(nil).
		AnyTimes()
	conn.EXPECT().
		WriteTo(gomock.AssignableToTypeOf([]byte(nil)), gomock.AssignableToTypeOf((*net.UDPAddr)(nil))).
		DoAndReturn(func(b []byte, addr net.Addr) (int, error) {
			select {
			case <-connClosed:
				return 0, errtrace.Wrap(net.ErrClosed)
			default:
				writePackets <- writePacket{
					buf:  slices.Clone(b),
					addr: netip.MustParseAddrPort(addr.String()),
				}
				return len(b), nil
			}
		}).
		AnyTimes()

	rmtDone := make(chan struct{})
	go func() {
		// some trash, must be skipped
		readPackets <- readPacket{
			buf:  []byte("hello!"),
			addr: netip.MustParseAddrPort("123.123.123.123:5060"),
		}
		readPackets <- readPacket{
			buf:  []byte("qwerty"),
			addr: netip.MustParseAddrPort("123.123.123.123:5060"),
		}

		// ping must be skipped
		readPackets <- readPacket{
			buf:  []byte("\r\n\r\n"),
			addr: netip.MustParseAddrPort("123.123.123.123:5060"),
		}

		// valid response must be received
		readPackets <- readPacket{
			buf: []byte(
				"SIP/2.0 200 OK\r\n" +
					"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=" + sip.MagicCookie + ".qwerty\r\n" +
					"From: \"Alice\" <sip:alice@127.0.0.1>;tag=abc\r\n" +
					"To: \"Bob\" <sip:bob@example.com>;tag=zxc\r\n" +
					"Call-ID: 123-abc-xyz@127.0.0.1\r\n" +
					"CSeq: 1 INVITE\r\n" +
					"\r\n",
			),
			addr: netip.MustParseAddrPort("5.5.5.5:5060"),
		}

		// temp conn error must be skipped
		readPackets <- readPacket{
			err: os.ErrDeadlineExceeded,
		}

		// invalid response must be skipped
		readPackets <- readPacket{
			buf: []byte(
				"SIP/2.0 200 OK\r\n" +
					"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=" + sip.MagicCookie + ".qwerty\r\n" +
					"From: \"Alice\" <sip:alice@127.0.0.1>;tag=abc\r\n" +
					"To: \"Bob\" <sip:bob@example.com>;tag=zxc\r\n" +
					"\r\n",
			),
			addr: netip.MustParseAddrPort("5.5.5.5:5060"),
		}

		// bad response must be skipped
		readPackets <- readPacket{
			buf: []byte(
				"SIP/2.0 200 OK\r\n" +
					"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=" + sip.MagicCookie + ".qwerty\r\n" +
					"From: \"Alice\" <sip:alice@127.0.0.1>;tag=abc\r\n" +
					"To: \"Bob\" <sip:bob@example.com>;tag=zxc\r\n" +
					"Call-ID: 123-abc-xyz@127.0.0.1\r\n" +
					"CSeq: 1 INVITE\r\n" +
					"Content-Length: 12\r\n" +
					"\r\n",
			),
			addr: netip.MustParseAddrPort("5.5.5.5:5060"),
		}

		// not our response must be skipped
		readPackets <- readPacket{
			buf: []byte(
				"SIP/2.0 200 OK\r\n" +
					"Via: SIP/2.0/UDP example.com:5060;branch=" + sip.MagicCookie + ".qwerty\r\n" +
					"From: \"Alice\" <sip:alice@127.0.0.1>;tag=abc\r\n" +
					"To: \"Bob\" <sip:bob@example.com>;tag=zxc\r\n" +
					"Call-ID: 123-abc-xyz@127.0.0.1\r\n" +
					"CSeq: 1 INVITE\r\n" +
					"\r\n",
			),
			addr: netip.MustParseAddrPort("5.5.5.5:5060"),
		}

		close(rmtDone)
	}()

	ress := make(chan *sip.InboundResponseEnvelope)
	unbind := tp.UseInboundResponseInterceptor(
		sip.InboundResponseInterceptorFunc(
			func(_ context.Context, res *sip.InboundResponseEnvelope, _ sip.ResponseReceiver) error {
				ress <- res
				return nil
			},
		),
	)
	defer unbind()

	go tp.Serve(t.Context()) //nolint:errcheck

	// got valid response
	res := <-ress
	wantMsg := "SIP/2.0 200 OK\r\n" +
		"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=" + sip.MagicCookie + ".qwerty\r\n" +
		"From: \"Alice\" <sip:alice@127.0.0.1>;tag=abc\r\n" +
		"To: \"Bob\" <sip:bob@example.com>;tag=zxc\r\n" +
		"Call-ID: 123-abc-xyz@127.0.0.1\r\n" +
		"CSeq: 1 INVITE\r\n" +
		"\r\n"
	if diff := cmp.Diff(res.Render(nil), wantMsg); diff != "" {
		t.Errorf("unexpected response received\ndiff (-got +want)\n%s", diff)
	}
	if got, want := res.Transport(), tp.Proto(); got != want {
		t.Errorf("res.Transport() = %v, want %v", got, want)
	}
	if got, want := res.LocalAddr(), tp.LocalAddr(); got != want {
		t.Errorf("res.LocalAddr() = %v, want %v", got, want)
	}
	if !res.RemoteAddr().IsValid() {
		t.Errorf("res.RemoteAddr() = %v is invalid", res.RemoteAddr())
	}

	<-rmtDone
}

func BenchmarkUnreliableTransport_ReceiveMessages(b *testing.B) {
	conn, err := net.ListenPacket("udp4", "127.0.0.1:5060")
	if err != nil {
		b.Fatalf("net.ListenPacket(\"udp4\", \"127.0.0.1:5060\") error = %v, want nil", err)
	}
	defer conn.Close()

	tp, err := sip.NewUnreliableTransport("UDP", conn, &sip.UnreliableTransportOptions{
		// Log: logutil.Default,
	})
	if err != nil {
		b.Fatalf("sip.NewUnreliableTransport(\"UDP\", conn, opts) error = %v, want nil", err)
	}
	defer tp.Close(b.Context())

	reqs := make(chan *sip.InboundRequestEnvelope, 1)
	cncOnReq := tp.UseInboundRequestInterceptor(
		sip.InboundRequestInterceptorFunc(
			func(_ context.Context, req *sip.InboundRequestEnvelope, _ sip.RequestReceiver) error {
				reqs <- req
				return nil
			},
		),
	)
	defer cncOnReq()

	ress := make(chan *sip.InboundResponseEnvelope, 1)
	cncOnRes := tp.UseInboundResponseInterceptor(
		sip.InboundResponseInterceptorFunc(
			func(_ context.Context, res *sip.InboundResponseEnvelope, _ sip.ResponseReceiver) error {
				ress <- res
				return nil
			},
		),
	)
	defer cncOnRes()

	rmtConn, err := net.Dial("udp4", "127.0.0.1:5060")
	if err != nil {
		b.Fatalf("net.Dial(\"udp4\", \"127.0.0.1:5060\") error = %v, want nil", err)
	}
	defer rmtConn.Close()

	msgs := []string{
		"INVITE sip:alice@127.0.0.1:5060 SIP/2.0\r\n" +
			"Via: SIP/2.0/UDP example.com:5060;branch=" + sip.MagicCookie + ".qwerty\r\n" +
			"From: Bob <sip:bob@example.com>;tag=abc\r\n" +
			"To: Alice <sip:alice@127.0.0.1>\r\n" +
			"Call-ID: 123-abc-xyz@example.com\r\n" +
			"CSeq: 1 INVITE\r\n" +
			"Max-Forwards: 70\r\n" +
			"Content-Length: 12\r\n" +
			"\r\n" +
			"hello world!",
		"SIP/2.0 200 OK\r\n" +
			"Via: SIP/2.0/UDP 127.0.0.1:5060;branch=" + sip.MagicCookie + ".qwerty\r\n" +
			"From: \"Alice\" <sip:alice@127.0.0.1>;tag=abc\r\n" +
			"To: \"Bob\" <sip:bob@example.com>;tag=zxc\r\n" +
			"Call-ID: 123-abc-xyz@127.0.0.1\r\n" +
			"CSeq: 1 INVITE\r\n" +
			"\r\n",
		"OPTIONS sip:alice@127.0.0.1:5060 SIP/2.0\r\n" +
			"Via: SIP/2.0/UDP example.com:5060;branch=" + sip.MagicCookie + ".qwerty;rport\r\n" +
			"From: Bob <sip:bob@example.com>;tag=abc\r\n" +
			"To: Alice <sip:alice@127.0.0.1>\r\n" +
			"Call-ID: 123-abc-xyz@example.com\r\n" +
			"CSeq: 1 INVITE\r\n" +
			"Max-Forwards: 70\r\n" +
			"\r\n",
	}

	b.ResetTimer()
	for b.Loop() {
		msg := msgs[rand.IntN(len(msgs))] //nolint:gosec

		_, err := rmtConn.Write([]byte(msg))
		if err != nil {
			b.Fatalf("rmtConn.Write(\"%s\") error = %v, want nil", msg, err)
		}

		select {
		case <-reqs:
			// b.Log("request received:", req)
		case <-ress:
			// b.Log("response received:", res)
		}
	}
}
