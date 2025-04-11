package transport_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/transport"
	"github.com/ghettovoice/gosip/sip/uri"
)

func specBaseSendReq(tpPtr *sip.Transport, rmtPortPtr *uint16, rmtRead func() ([]byte, error)) {
	It("should reject invalid request", func(ctx SpecContext) {
		tp := *tpPtr
		rmtPort := *rmtPortPtr
		rmtAddr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), rmtPort)

		Expect(tp.SendRequest(ctx, &sip.Request{
			Proto:  sip.ProtoVer20(),
			Method: sip.RequestMethodInfo,
			URI: &uri.SIP{
				User: uri.User("bob"),
				Addr: uri.HostPort("example.com", rmtPort),
			},
			Headers: make(sip.Headers).
				Set(header.Via{
					{
						Proto:     sip.ProtoVer20(),
						Transport: tp.Proto(),
						Params:    make(header.Values).Set("branch", sip.MagicCookie+".qwerty"),
					},
				}).
				Set(header.MaxForwards(70)),
		}, rmtAddr)).To(MatchError(sip.ErrInvalidMessage))
		Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
			"Listeners":   BeEquivalentTo(0),
			"Connections": BeEquivalentTo(1),
		}), "connection added")
		Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
			"OutboundRequests":         BeEquivalentTo(1),
			"OutboundRequestsRejected": BeEquivalentTo(1),
		}), "request rejected")
	})

	It("should pass request to the handler and reject with it's error", func(ctx SpecContext) {
		tp := *tpPtr
		rmtPort := *rmtPortPtr
		rmtAddr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), rmtPort)

		req := &sip.Request{
			Proto:  sip.ProtoVer20(),
			Method: sip.RequestMethodInfo,
			URI: &uri.SIP{
				User: uri.User("bob"),
				Addr: uri.HostPort("example.com", rmtPort),
			},
			Headers: make(sip.Headers).
				Set(header.Via{
					{
						Proto:     sip.ProtoVer20(),
						Transport: tp.Proto(),
						Params:    make(header.Values).Set("branch", sip.MagicCookie+".qwerty"),
					},
				}).
				Set(&header.From{
					URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("localhost")},
					Params: make(header.Values).Set("tag", "abc"),
				}).
				Set(&header.To{
					URI: &uri.SIP{User: uri.User("bob"), Addr: uri.Host("example.com")},
				}).
				Set(header.CallID("123-abc-xyz@localhost")).
				Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInfo}).
				Set(header.MaxForwards(70)),
		}

		rejectErr := errors.New("test error")
		tp.OnOutboundRequest(sip.RequestHandlerFunc(func(ctx context.Context, r *sip.Request) error {
			Expect(r).To(Equal(req))
			return rejectErr
		}))

		Expect(tp.SendRequest(ctx, req, rmtAddr)).To(MatchError(rejectErr))
		Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
			"Listeners":   BeEquivalentTo(0),
			"Connections": BeEquivalentTo(1),
		}), "connection added")
		Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
			"OutboundRequests":         BeEquivalentTo(1),
			"OutboundRequestsRejected": BeEquivalentTo(1),
		}), "request rejected")
	})

	It("should reject too big request", func(ctx SpecContext) {
		tp := *tpPtr
		rmtPort := *rmtPortPtr
		rmtAddr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), rmtPort)

		if transport.IsStreamed(tp.Proto()) {
			Skip("skip for stream-oriented transport")
		}

		Expect(tp.SendRequest(ctx, &sip.Request{
			Proto:  sip.ProtoVer20(),
			Method: sip.RequestMethodInfo,
			URI: &uri.SIP{
				User: uri.User("bob"),
				Addr: uri.HostPort("example.com", rmtPort),
			},
			Headers: make(sip.Headers).
				Set(header.Via{
					{
						Proto:     sip.ProtoVer20(),
						Transport: tp.Proto(),
						Params:    make(header.Values).Set("branch", sip.MagicCookie+".qwerty"),
					},
				}).
				Set(&header.From{
					URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("localhost")},
					Params: make(header.Values).Set("tag", "abc"),
				}).
				Set(&header.To{
					URI: &uri.SIP{User: uri.User("bob"), Addr: uri.Host("example.com")},
				}).
				Set(header.CallID("123-abc-xyz@localhost")).
				Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInfo}).
				Set(header.MaxForwards(70)),
			Body: make([]byte, sip.MTU),
		}, rmtAddr)).To(MatchError(sip.ErrMessageTooLarge))
		Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
			"Listeners":   BeEquivalentTo(0),
			"Connections": BeEquivalentTo(1),
		}), "connection added")
		Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
			"OutboundRequests":         BeEquivalentTo(1),
			"OutboundRequestsRejected": BeEquivalentTo(1),
		}), "request rejected")
	})

	It("should send valid request", func(ctx SpecContext) {
		tp := *tpPtr
		rmtPort := *rmtPortPtr
		rmtAddr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), rmtPort)

		req := &sip.Request{
			Proto:  sip.ProtoVer20(),
			Method: sip.RequestMethodInfo,
			URI: &uri.SIP{
				User: uri.User("bob"),
				Addr: uri.HostPort("example.com", rmtPort),
			},
			Headers: make(sip.Headers).
				Set(header.Via{
					{
						Proto:     sip.ProtoVer20(),
						Transport: tp.Proto(),
						Params:    make(header.Values).Set("branch", sip.MagicCookie+".qwerty"),
					},
				}).
				Set(&header.From{
					URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("localhost")},
					Params: make(header.Values).Set("tag", "abc"),
				}).
				Set(&header.To{
					URI: &uri.SIP{User: uri.User("bob"), Addr: uri.Host("example.com")},
				}).
				Set(header.CallID("123-abc-xyz@localhost")).
				Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInfo}).
				Set(header.MaxForwards(70)),
			Body: []byte("hello"),
		}

		started := make(chan struct{})
		received := make(chan string, 1)
		go func() {
			defer GinkgoRecover()

			close(started)

			buf, err := rmtRead()
			Expect(err).ToNot(HaveOccurred(), "no errors on remote side")
			received <- string(buf)
			close(received)
		}()
		Eventually(ctx, started).Within(time.Second).Should(BeClosed(), "remote read started")

		tp.OnOutboundRequest(sip.RequestHandlerFunc(func(ctx context.Context, r *sip.Request) error {
			Expect(r).To(Equal(req))
			return nil
		}))
		Expect(tp.SendRequest(ctx, req, rmtAddr)).To(Succeed())
		Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
			"Listeners":   BeEquivalentTo(0),
			"Connections": BeEquivalentTo(1),
		}), "connection added")
		Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
			"OutboundRequests":         BeEquivalentTo(1),
			"OutboundRequestsRejected": BeEquivalentTo(0),
		}), "request sent")

		Eventually(ctx, received).Within(time.Second).Should(Receive(Equal(req.Render())), "remote got request")

		if transport.IsStreamed(tp.Proto()) {
			Expect(req.Headers.Get("Content-Length")[0]).To(Equal(header.ContentLength(5)))
		}

		hdrs := req.Headers.Get("Timestamp")
		Expect(hdrs).To(HaveLen(1), "Timestamp header added")

		ts, ok := hdrs[0].(*header.Timestamp)
		Expect(ok).To(BeTrue(), "Timestamp header has type *header.Timestamp")
		Expect(ts.ReqTime).ToNot(BeZero(), "Timestamp header has non-zero ReqTime")
		Expect(ts.ResDelay).To(BeZero(), "Timestamp header has zero ResDelay")

		Expect(req.Metadata).To(MatchKeys(IgnoreExtras, Keys{
			sip.TransportField:  BeEquivalentTo(tp.Proto()),
			sip.LocalAddrField:  Not(BeZero()),
			sip.RemoteAddrField: Not(BeZero()),
		}))
	})

	It("should re-use opened connection", func(ctx SpecContext) {
		tp := *tpPtr
		rmtPort := *rmtPortPtr
		rmtAddr := netip.AddrPortFrom(netip.AddrFrom4([4]byte{127, 0, 0, 1}), rmtPort)

		req1 := &sip.Request{
			Proto:  sip.ProtoVer20(),
			Method: sip.RequestMethodInfo,
			URI: &uri.SIP{
				User: uri.User("bob"),
				Addr: uri.HostPort("example.com", rmtPort),
			},
			Headers: make(sip.Headers).
				Set(header.Via{
					{
						Proto:     sip.ProtoVer20(),
						Transport: tp.Proto(),
						Params:    make(header.Values).Set("branch", sip.MagicCookie+".qwerty"),
					},
				}).
				Set(&header.From{
					URI:    &uri.SIP{User: uri.User("alice"), Addr: uri.Host("localhost")},
					Params: make(header.Values).Set("tag", "abc"),
				}).
				Set(&header.To{
					URI: &uri.SIP{User: uri.User("bob"), Addr: uri.Host("example.com")},
				}).
				Set(header.CallID("123-abc-xyz@localhost")).
				Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodInfo}).
				Set(header.MaxForwards(70)),
		}

		// TODO разобраться почему блокируется при отправке
		go func() {
			defer GinkgoRecover()

			for {
				_, err := rmtRead()
				if err != nil {
					return
				}
			}
		}()

		Expect(tp.SendRequest(ctx, req1, rmtAddr)).To(Succeed())
		Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
			"Listeners":   BeEquivalentTo(0),
			"Connections": BeEquivalentTo(1),
		}), "connection added")
		Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
			"OutboundRequests":         BeEquivalentTo(1),
			"OutboundRequestsRejected": BeEquivalentTo(0),
		}), "request sent")

		time.Sleep(100 * time.Millisecond)

		req2 := req1.Clone().(*sip.Request) //nolint:forcetypeassert
		req2.Method = sip.RequestMethodOptions
		sip.FirstHeader[*header.CSeq](req2.Headers, "CSeq").Method = sip.RequestMethodOptions
		hop := sip.FirstHeaderElem[header.Via](req2.Headers, "Via")
		hop.Addr = header.Addr{}

		Expect(tp.SendRequest(ctx, req2, rmtAddr)).To(Succeed())
		Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
			"Listeners":   BeEquivalentTo(0),
			"Connections": BeEquivalentTo(1),
		}), "connection re-used")
		Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
			"OutboundRequests":         BeEquivalentTo(2),
			"OutboundRequestsRejected": BeEquivalentTo(0),
		}), "request sent")

		Expect(req2.Metadata).To(MatchKeys(IgnoreExtras, Keys{
			sip.LocalAddrField:  Equal(req1.Metadata[sip.LocalAddrField]),
			sip.RemoteAddrField: Equal(req1.Metadata[sip.RemoteAddrField]),
		}))
	})
}

func specBaseSendRes(tpPtr *sip.Transport, reqPtr **sip.Request, rmtRead func() ([]byte, error)) {
	// TODO add cases when connection is broken, send failed and we need to resolve next address to try

	It("should reject invalid response", func(ctx SpecContext) {
		tp := *tpPtr
		req := *reqPtr
		locAddr := req.Metadata[sip.LocalAddrField].(netip.AddrPort) //nolint:forcetypeassert

		tp.OnOutboundResponse(sip.ResponseHandlerFunc(func(_ context.Context, r *sip.Response) error {
			r.Headers.Del("From")
			r.Headers.Del("To")
			return nil
		}))

		Expect(tp.SendResponse(ctx, sip.NewResponse(req, sip.ResponseStatusTrying), locAddr)).
			To(MatchError(sip.ErrInvalidMessage))
		Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
			"OutboundResponses":         BeEquivalentTo(1),
			"OutboundResponsesRejected": BeEquivalentTo(1),
		}), "response rejected")

		if transport.IsReliable(tp.Proto()) {
			Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"Connections": BeEquivalentTo(1),
			}))
		} else {
			Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"Connections": BeEquivalentTo(0),
			}))
		}
	})

	It("should send valid response", func(ctx SpecContext) {
		tp := *tpPtr
		req := *reqPtr
		locAddr := req.Metadata[sip.LocalAddrField].(netip.AddrPort) //nolint:forcetypeassert

		// setup reader on remote side
		started := make(chan struct{})
		received := make(chan string, 1)
		go func() {
			defer GinkgoRecover()

			close(started)

			buf, err := rmtRead()
			Expect(err).ToNot(HaveOccurred(), "remote read no error")
			received <- string(buf)
		}()
		Eventually(ctx, started).Within(time.Second).Should(BeClosed(), "remote start reading")

		// sending response
		var res *sip.Response
		tp.OnOutboundResponse(sip.ResponseHandlerFunc(func(ctx context.Context, r *sip.Response) error {
			res = r
			return nil
		}))
		Expect(tp.SendResponse(ctx, sip.NewResponse(req, sip.ResponseStatusTrying), locAddr)).
			To(Succeed())
		Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
			"OutboundResponses":         BeEquivalentTo(1),
			"OutboundResponsesRejected": BeEquivalentTo(0),
		}), "response sent")

		if transport.IsReliable(tp.Proto()) {
			Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"Connections": BeEquivalentTo(1),
			}))
		} else {
			Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"Connections": BeEquivalentTo(0),
			}))
		}

		Eventually(ctx, received).Within(time.Second).Should(Receive(Equal(res.Render())), "remote got response")

		// check sent response on our side
		if transport.IsStreamed(tp.Proto()) {
			Expect(res.Headers.Get("Content-Length")[0]).To(Equal(header.ContentLength(0)))
		}

		Expect(res).ToNot(BeNil())
		hdrs := res.Headers.Get("Timestamp")
		Expect(hdrs).To(HaveLen(1), "Timestamp header added")
		ts, ok := hdrs[0].(*header.Timestamp)
		Expect(ok).To(BeTrue(), "Timestamp header has type *header.Timestamp")
		Expect(ts.ResDelay).ToNot(BeZero(), "Timestamp header has non-zero ResDelay")

		Expect(res.Metadata).To(MatchKeys(IgnoreExtras, Keys{
			sip.TransportField:  BeEquivalentTo(tp.Proto()),
			sip.LocalAddrField:  Not(BeZero()),
			sip.RemoteAddrField: Not(BeZero()),
		}))
	})
}

func specBaseRecvReq(tpPtr *sip.Transport, locPortPtr, rmtPortPtr *uint16, rmtWrite func([]byte) error, rmtRead func() ([]byte, error)) {
	It("should ignore any inbound trash", func(ctx SpecContext) {
		tp := *tpPtr

		var handlerCalled atomic.Bool
		tp.OnInboundRequest(sip.RequestHandlerFunc(func(_ context.Context, _ *sip.Request) error {
			handlerCalled.Store(true)
			return nil
		}))

		Expect(rmtWrite(bytes.Repeat([]byte("a"), 100))).To(Succeed())

		time.Sleep(time.Millisecond)

		Expect(rmtWrite([]byte("\r\n\t\r\n"))).To(Succeed())

		Eventually(ctx, func(g Gomega) {
			g.Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"InboundRequests":         BeEquivalentTo(0),
				"InboundRequestsRejected": BeEquivalentTo(0),
			}))
		}).Within(time.Second).Should(Succeed(), "trash ignored")
		Expect(handlerCalled.Load()).To(BeFalse(), "handler is not called")
	})

	It("should silently discard invalid request without mandatory headers", func(ctx SpecContext) {
		tp := *tpPtr
		locPort := *locPortPtr

		var handlerCalled atomic.Bool
		tp.OnInboundRequest(sip.RequestHandlerFunc(func(_ context.Context, _ *sip.Request) error {
			handlerCalled.Store(true)
			return nil
		}))

		Expect(rmtWrite([]byte(fmt.Sprintf("INVITE sip:localhost:%d SIP/2.0\r\n\r\n", locPort)))).To(Succeed())

		Eventually(ctx, func(g Gomega) {
			g.Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"InboundRequests":         BeEquivalentTo(1),
				"InboundRequestsRejected": BeEquivalentTo(1),
			}))
		}).Within(time.Second).Should(Succeed(), "request discarded")
		Expect(handlerCalled.Load()).To(BeFalse(), "handler not called")
	})

	It("should discard request that was parsed with errors", func(ctx SpecContext) {
		tp := *tpPtr
		locPort := *locPortPtr
		rmtPort := *rmtPortPtr

		var handlerCalled atomic.Bool
		tp.OnInboundRequest(sip.RequestHandlerFunc(func(_ context.Context, _ *sip.Request) error {
			handlerCalled.Store(true)
			return nil
		}))

		readStarted := make(chan struct{})
		readDone := make(chan struct{})
		go func() {
			defer GinkgoRecover()

			close(readStarted)

			buf, err := rmtRead()
			Expect(err).ToNot(HaveOccurred())
			var ct string
			if transport.IsStreamed(tp.Proto()) {
				ct = "Content-Length: 0\r\n"
			}
			Expect(string(buf)).To(MatchRegexp(
				"SIP/2\\.0 400 Bad Request\r\n"+
					"Via: SIP/2\\.0/%s example\\.com:%d;branch=%s\\.qwerty;received=127\\.0\\.0\\.1\r\n"+
					"From: \"Bob\" <sip:bob@localhost>;tag=abc\r\n"+
					"To: \"Alice\" <sip:alice@localhost>;tag=\\w{16}\r\n"+
					"Call-ID: 123-abc-xyz@localhost\r\n"+
					"CSeq: 1 INVITE\r\n"+
					ct+
					"\r\n",
				tp.Proto(),
				rmtPort,
				sip.MagicCookie,
			))
			close(readDone)
		}()
		<-readStarted

		Expect(rmtWrite([]byte(fmt.Sprintf(
			"INVITE sip:alice@localhost:%d SIP/2.0\r\n"+
				"Via: SIP/2.0/%s example.com:%d;branch=%s.qwerty\r\n"+
				"From: Bob <sip:bob@localhost>;tag=abc\r\n"+
				"To: Alice <sip:alice@localhost>\r\n"+
				"Call-ID: 123-abc-xyz@localhost\r\n"+
				"CSeq: 1 INVITE\r\n"+
				"Max-Forwards: 70\r\n"+
				"$$$---!!!\r\n"+
				"\r\n",
			locPort,
			tp.Proto(),
			rmtPort,
			sip.MagicCookie,
		)))).To(Succeed())

		Eventually(ctx, func(g Gomega) {
			g.Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"InboundRequests":         BeEquivalentTo(1),
				"InboundRequestsRejected": BeEquivalentTo(1),
			}))
		}).Within(time.Second).Should(Succeed(), "request discarded")
		Expect(handlerCalled.Load()).To(BeFalse(), "handler not called")

		Eventually(ctx, readDone).Within(time.Second).Should(BeClosed(), "remote received response")
	})

	It("should discard request without Content-Length header", func(ctx SpecContext) {
		tp := *tpPtr
		locPort := *locPortPtr
		rmtPort := *rmtPortPtr

		if !transport.IsStreamed(tp.Proto()) {
			Skip("skip for packet-oriented transport")
		}

		var handlerCalled atomic.Bool
		tp.OnInboundRequest(sip.RequestHandlerFunc(func(_ context.Context, _ *sip.Request) error {
			handlerCalled.Store(true)
			return nil
		}))

		readStarted := make(chan struct{})
		readDone := make(chan struct{})
		go func() {
			defer GinkgoRecover()

			close(readStarted)

			buf, err := rmtRead()
			Expect(err).ToNot(HaveOccurred())
			Expect(string(buf)).To(MatchRegexp(
				"SIP/2\\.0 400 Bad Request\r\n"+
					"Via: SIP/2\\.0/%s example\\.com:%d;branch=%s\\.qwerty;received=127\\.0\\.0\\.1\r\n"+
					"From: \"Bob\" <sip:bob@example\\.com>;tag=abc\r\n"+
					"To: \"Alice\" <sip:alice@localhost>;tag=\\w{16}\r\n"+
					"Call-ID: 123-abc-xyz@example\\.com\r\n"+
					"CSeq: 1 INVITE\r\n"+
					"Content-Length: 0\r\n"+
					"\r\n",
				tp.Proto(),
				rmtPort,
				sip.MagicCookie,
			))
			close(readDone)
		}()
		<-readStarted

		Expect(rmtWrite([]byte(fmt.Sprintf(
			"INVITE sip:alice@127.0.0.1:%d SIP/2.0\r\n"+
				"Via: SIP/2.0/%s example.com:%d;branch=%s.qwerty\r\n"+
				"From: \"Bob\" <sip:bob@example.com>;tag=abc\r\n"+
				"To: \"Alice\" <sip:alice@localhost>\r\n"+
				"Call-ID: 123-abc-xyz@example.com\r\n"+
				"CSeq: 1 INVITE\r\n"+
				"Max-Forwards: 70\r\n"+
				"Contact: <sip:bob@127.0.0.1:%[3]d>\r\n"+
				"\r\n"+
				"hello world!",
			locPort,
			tp.Proto(),
			rmtPort,
			sip.MagicCookie,
		)))).To(Succeed())

		Eventually(ctx, func(g Gomega) {
			g.Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"InboundRequests":         BeEquivalentTo(1),
				"InboundRequestsRejected": BeEquivalentTo(1),
			}))
		}).Within(time.Second).Should(Succeed(), "request discarded")
		Expect(handlerCalled.Load()).To(BeFalse(), "handler not called")

		Eventually(ctx, readDone).Within(time.Second).Should(BeClosed(), "remote received response")
	})

	It("should reject too large request", func(ctx SpecContext) {
		tp := *tpPtr
		locPort := *locPortPtr
		rmtPort := *rmtPortPtr

		if !transport.IsStreamed(tp.Proto()) {
			Skip("skip for packet-oriented transport")
		}

		var handlerCalled atomic.Bool
		tp.OnInboundRequest(sip.RequestHandlerFunc(func(_ context.Context, _ *sip.Request) error {
			handlerCalled.Store(true)
			return nil
		}))

		readStarted := make(chan struct{})
		readDone := make(chan struct{})
		go func() {
			defer GinkgoRecover()

			close(readStarted)

			buf, err := rmtRead()
			Expect(err).ToNot(HaveOccurred())
			Expect(string(buf)).To(MatchRegexp(
				"SIP/2\\.0 513 Message Too Large\r\n"+
					"Via: SIP/2\\.0/%s example\\.com:%d;branch=%s\\.qwerty;received=127\\.0\\.0\\.1\r\n"+
					"From: \"Bob\" <sip:bob@example\\.com>;tag=abc\r\n"+
					"To: \"Alice\" <sip:alice@localhost>;tag=\\w{16}\r\n"+
					"Call-ID: 123-abc-xyz@example\\.com\r\n"+
					"CSeq: 1 INVITE\r\n"+
					"Content-Length: 0\r\n"+
					"\r\n",
				tp.Proto(),
				rmtPort,
				sip.MagicCookie,
			))
			close(readDone)
		}()
		<-readStarted

		Expect(rmtWrite([]byte(fmt.Sprintf(
			"INVITE sip:alice@127.0.0.1:%d SIP/2.0\r\n"+
				"Via: SIP/2.0/%s example.com:%d;branch=%s.qwerty\r\n"+
				"From: \"Bob\" <sip:bob@example.com>;tag=abc\r\n"+
				"To: \"Alice\" <sip:alice@localhost>\r\n"+
				"Call-ID: 123-abc-xyz@example.com\r\n"+
				"CSeq: 1 INVITE\r\n"+
				"Max-Forwards: 70\r\n"+
				"Contact: <sip:bob@127.0.0.1:%[3]d>\r\n"+
				"Content-Length: 67000\r\n"+
				"\r\n"+
				strings.Repeat("a", 67000),
			locPort,
			tp.Proto(),
			rmtPort,
			sip.MagicCookie,
		)))).To(Succeed())

		Eventually(ctx, func(g Gomega) {
			g.Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"InboundRequests":         BeEquivalentTo(1),
				"InboundRequestsRejected": BeEquivalentTo(1),
			}))
		}).Within(time.Second).Should(Succeed(), "request discarded")
		Expect(handlerCalled.Load()).To(BeFalse(), "handler not called")

		Eventually(ctx, readDone).Within(time.Second).Should(BeClosed(), "remote received response")
	})

	It("should accept valid request", func(ctx SpecContext) {
		tp := *tpPtr
		locPort := *locPortPtr
		rmtPort := *rmtPortPtr

		// setup inbound message handler
		inReqs := make(chan *sip.Request, 1)
		tp.OnInboundRequest(sip.RequestHandlerFunc(func(_ context.Context, r *sip.Request) error {
			inReqs <- r
			return nil
		}))

		// remote side sends the request
		Expect(rmtWrite([]byte(fmt.Sprintf(
			"INVITE sip:alice@127.0.0.1:%d SIP/2.0\r\n"+
				"Via: SIP/2.0/%s example.com:%d;branch=%s.qwerty;rport\r\n"+
				"From: \"Bob\" <sip:bob@example.com>;tag=abc\r\n"+
				"To: \"Alice\" <sip:alice@localhost>\r\n"+
				"Call-ID: 123-abc-xyz@example.com\r\n"+
				"CSeq: 1 INVITE\r\n"+
				"Max-Forwards: 70\r\n"+
				"Contact: <sip:bob@127.0.0.1:%[3]d>\r\n"+
				"Timestamp: %.3[5]f\r\n"+
				"Content-Length: 5\r\n"+
				"\r\n"+
				"hello world!",
			locPort,
			tp.Proto(),
			rmtPort,
			sip.MagicCookie,
			float64(time.Now().UTC().UnixNano())/float64(time.Second),
		)))).To(Succeed())

		// validate received message
		var req *sip.Request
		Eventually(ctx, inReqs).Within(time.Second).Should(Receive(&req), "message accepted")
		Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
			"InboundRequests":         BeEquivalentTo(1),
			"InboundRequestsRejected": BeEquivalentTo(0),
		}), "request accepted")

		Expect(req.Render()).Should(MatchRegexp(
			"INVITE sip:alice@127\\.0\\.0\\.1:%d SIP/2\\.0\r\n"+
				"Via: SIP/2\\.0/%s example\\.com:%d;branch=%s\\.qwerty[^\\s]*\r\n"+
				"From: \"Bob\" <sip:bob@example\\.com>;tag=abc\r\n"+
				"To: \"Alice\" <sip:alice@localhost>\r\n"+
				"Call-ID: 123-abc-xyz@example\\.com\r\n"+
				"CSeq: 1 INVITE\r\n"+
				"Contact: <sip:bob@127\\.0\\.0\\.1:%[3]d>\r\n"+
				"Max-Forwards: 70\r\n"+
				"Timestamp: \\d*\\.\\d{3}\r\n"+
				"Content-Length: 5\r\n"+
				"\r\n"+
				"hello",
			locPort,
			tp.Proto(),
			rmtPort,
			sip.MagicCookie,
		))

		viaHop := sip.FirstHeaderElem[header.Via](req.Headers, "Via")
		Expect(viaHop).ToNot(BeNil(), "Via is not empty")
		Expect(viaHop.Params.Last("received")).To(Equal("127.0.0.1"), "received param is added")

		p, err := strconv.Atoi(viaHop.Params.Last("rport"))
		Expect(err).ToNot(HaveOccurred(), "rport is not empty")
		Expect(p).To(BeEquivalentTo(rmtPort), "rport is correct")

		Expect(req.Metadata).To(MatchKeys(IgnoreExtras, Keys{
			sip.TransportField:  BeEquivalentTo(tp.Proto()),
			sip.LocalAddrField:  Not(BeZero()),
			sip.RemoteAddrField: Not(BeZero()),
		}))
	})
}

func specBaseRecvRes(tpPtr *sip.Transport, locPortPtr *uint16, rmtWrite func([]byte) error) {
	It("should ignore any inbound trash", func(ctx SpecContext) {
		tp := *tpPtr

		var handlerCalled atomic.Bool
		tp.OnInboundResponse(sip.ResponseHandlerFunc(func(_ context.Context, _ *sip.Response) error {
			handlerCalled.Store(true)
			return nil
		}))

		Expect(rmtWrite(bytes.Repeat([]byte("a"), 100))).To(Succeed())

		time.Sleep(time.Millisecond)

		Expect(rmtWrite([]byte("\r\n\t\r\n"))).To(Succeed())

		Eventually(ctx, func(g Gomega) {
			g.Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"InboundRequests":         BeEquivalentTo(0),
				"InboundRequestsRejected": BeEquivalentTo(0),
			}))
		}).Within(time.Second).Should(Succeed(), "trash ignored")
		Expect(handlerCalled.Load()).To(BeFalse(), "handler not called")
	})

	It("should silently discard invalid response without mandatory headers", func(ctx SpecContext) {
		tp := *tpPtr

		var handlerCalled atomic.Bool
		tp.OnInboundResponse(sip.ResponseHandlerFunc(func(_ context.Context, _ *sip.Response) error {
			handlerCalled.Store(true)
			return nil
		}))

		Expect(rmtWrite([]byte(
			"SIP/2.0 200 OK\r\n" +
				"Content-Length: 0\r\n" +
				"\r\n",
		))).To(Succeed())

		Eventually(ctx, func(g Gomega) {
			g.Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"InboundResponses":         BeEquivalentTo(1),
				"InboundResponsesRejected": BeEquivalentTo(1),
			}))
		}).Within(time.Second).Should(Succeed(), "response discarded")
		Expect(handlerCalled.Load()).To(BeFalse(), "handler not called")
	})

	It("should silently discard response that was parsed with errors", func(ctx SpecContext) {
		tp := *tpPtr
		locPort := *locPortPtr

		var handlerCalled atomic.Bool
		tp.OnInboundResponse(sip.ResponseHandlerFunc(func(_ context.Context, _ *sip.Response) error {
			handlerCalled.Store(true)
			return nil
		}))

		Expect(rmtWrite([]byte(fmt.Sprintf(
			"SIP/2.0 200 OK\r\n"+
				"Via: SIP/2.0/%s 127.0.0.1:%d;branch=%s.qwerty\r\n"+
				"From: \"Alice\" <sip:alice@localhost>;tag=abc\r\n"+
				"To: \"Bob\" <sip:bob@example.com>;tag=zxc\r\n"+
				"Call-ID: 123-abc-xyz@localhost\r\n"+
				"CSeq: 1 INVITE\r\n"+
				"Max-Forwards: 70\r\n"+
				"$$$---!!!\r\n"+
				"\r\n",
			tp.Proto(),
			locPort,
			sip.MagicCookie,
		)))).To(Succeed())

		Eventually(ctx, func(g Gomega) {
			g.Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"InboundResponses":         BeEquivalentTo(1),
				"InboundResponsesRejected": BeEquivalentTo(1),
			}))
		}).Within(time.Second).Should(Succeed(), "response discarded")
		Expect(handlerCalled.Load()).To(BeFalse(), "handler not called")
	})

	It(`should silently discard response with topmost Via's '"sent-by" field not matching to the SentByHost option`, func(ctx SpecContext) {
		tp := *tpPtr
		locPort := *locPortPtr

		var handlerCalled atomic.Bool
		tp.OnInboundResponse(sip.ResponseHandlerFunc(func(_ context.Context, _ *sip.Response) error {
			handlerCalled.Store(true)
			return nil
		}))

		Expect(rmtWrite([]byte(fmt.Sprintf(
			"SIP/2.0 200 OK\r\n"+
				"Via: SIP/2.0/%s 127.0.0.1:%d;branch=%s.qwerty\r\n"+
				"From: \"Alice\" <sip:alice@localhost>;tag=abc\r\n"+
				"To: \"Bob\" <sip:bob@example.com>;tag=zxc\r\n"+
				"Call-ID: 123-abc-xyz@localhost\r\n"+
				"CSeq: 1 INVITE\r\n"+
				"Max-Forwards: 70\r\n"+
				"\r\n",
			tp.Proto(),
			locPort,
			sip.MagicCookie,
		)))).To(Succeed())

		Eventually(ctx, func(g Gomega) {
			g.Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"InboundResponses":         BeEquivalentTo(1),
				"InboundResponsesRejected": BeEquivalentTo(1),
			}))
		}).Within(time.Second).Should(Succeed(), "response discarded")
		Expect(handlerCalled.Load()).To(BeFalse(), "handler not called")
	})

	It("should silently discard response without Content-Length header", func(ctx SpecContext) {
		tp := *tpPtr
		locPort := *locPortPtr

		if !transport.IsStreamed(tp.Proto()) {
			Skip("skip for packet-oriented transport")
		}

		var handlerCalled atomic.Bool
		tp.OnInboundResponse(sip.ResponseHandlerFunc(func(_ context.Context, _ *sip.Response) error {
			handlerCalled.Store(true)
			return nil
		}))

		Expect(rmtWrite([]byte(fmt.Sprintf(
			"SIP/2.0 200 OK\r\n"+
				"Via: SIP/2.0/%s localhost:%d;branch=%s.qwerty\r\n"+
				"From: \"Alice\" <sip:alice@localhost>;tag=abc\r\n"+
				"To: \"Bob\" <sip:bob@example.com>;tag=zxc\r\n"+
				"Call-ID: 123-abc-xyz@localhost\r\n"+
				"CSeq: 1 INVITE\r\n"+
				"Max-Forwards: 70\r\n"+
				"\r\n",
			tp.Proto(),
			locPort,
			sip.MagicCookie,
		)))).To(Succeed())

		Eventually(ctx, func(g Gomega) {
			g.Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
				"InboundResponses":         BeEquivalentTo(1),
				"InboundResponsesRejected": BeEquivalentTo(1),
			}))
		}).Within(time.Second).Should(Succeed(), "response discarded")
		Expect(handlerCalled.Load()).To(BeFalse(), "handler not called")
	})

	It("should accept valid response", func(ctx SpecContext) {
		tp := *tpPtr
		locPort := *locPortPtr

		inRess := make(chan *sip.Response, 1)
		tp.OnInboundResponse(sip.ResponseHandlerFunc(func(_ context.Context, r *sip.Response) error {
			inRess <- r
			return nil
		}))

		reqTstamp := time.Now().UTC().Add(-2000 * time.Millisecond)
		Expect(rmtWrite([]byte(fmt.Sprintf(
			"SIP/2.0 200 OK\r\n"+
				"Via: SIP/2.0/%s localhost:%d;branch=%s.qwerty\r\n"+
				"From: \"Alice\" <sip:alice@localhost>;tag=abc\r\n"+
				"To: \"Bob\" <sip:bob@example.com>;tag=zxc\r\n"+
				"Call-ID: 123-abc-xyz@localhost\r\n"+
				"CSeq: 1 INVITE\r\n"+
				"Max-Forwards: 70\r\n"+
				"Timestamp: %.3f 0.250\r\n"+
				"Content-Length: 0\r\n"+
				"\r\n",
			tp.Proto(),
			locPort,
			sip.MagicCookie,
			float64(reqTstamp.UnixNano())/float64(time.Second),
		)))).To(Succeed())

		var res *sip.Response
		Eventually(ctx, inRess).Within(time.Second).Should(Receive(&res), "response accepted")
		Expect(tp.Stats()).To(MatchFields(IgnoreExtras, Fields{
			"InboundResponses":         BeEquivalentTo(1),
			"InboundResponsesRejected": BeEquivalentTo(0),
		}), "response accepted")

		Expect(res.Render()).Should(MatchRegexp(
			"SIP/2\\.0 200 OK\r\n"+
				"Via: SIP/2\\.0/%s localhost:%d;branch=%s\\.qwerty\r\n"+
				"From: \"Alice\" <sip:alice@localhost>;tag=abc\r\n"+
				"To: \"Bob\" <sip:bob@example.com>;tag=zxc\r\n"+
				"Call-ID: 123-abc-xyz@localhost\r\n"+
				"CSeq: 1 INVITE\r\n"+
				"Max-Forwards: 70\r\n"+
				"Timestamp: %.3f 0.250\r\n"+
				"Content-Length: 0\r\n"+
				"\r\n",
			tp.Proto(),
			locPort,
			sip.MagicCookie,
			float64(reqTstamp.UnixNano())/float64(time.Second),
		))

		r := tp.Stats()
		Expect(r.MessageRTTMeasurements).To(BeEquivalentTo(1), "message RTT measured")
		Expect(r.MessageRTT).To(And(BeNumerically(">=", 1750*time.Millisecond), BeNumerically("<=", 2*time.Second)))

		Expect(res.Metadata).To(MatchKeys(IgnoreExtras, Keys{
			sip.TransportField:  BeEquivalentTo(tp.Proto()),
			sip.LocalAddrField:  Not(BeZero()),
			sip.RemoteAddrField: Not(BeZero()),
		}))
	})
}
