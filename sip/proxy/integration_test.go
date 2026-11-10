package proxy_test

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/internal/testutil/dnstest"
	"github.com/ghettovoice/gosip/internal/testutil/siptest"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/proxy"
	"github.com/ghettovoice/gosip/sip/stats"
	"github.com/ghettovoice/gosip/sip/transport"
)

const (
	// wireReadTimeout bounds a single wire read: every expected message on
	// loopback must arrive well inside it.
	wireReadTimeout = 4 * time.Second
	// wireSilenceWindow is the observation window for "nothing was sent"
	// assertions on loopback. It stays below every default transaction
	// retransmission interval (sip.T1 = 500ms) so a client transaction
	// retransmit can never leak into a silence check.
	wireSilenceWindow = 250 * time.Millisecond
)

// loopbackDNS resolves every fixture host to loopback without touching an
// external resolver.
func loopbackDNS() dnstest.StubResolver {
	return dnstest.StubResolver{
		LookupIPFunc: func(context.Context, string, string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		},
	}
}

// wireProxy is one loopback proxy side: an element with a single real
// transport listening on a kernel-assigned loopback port.
type wireProxy struct {
	elm  *sip.Element
	tp   sip.Transport
	lis  sip.TransportListener
	addr netip.AddrPort
	prx  *proxy.Proxy
	rcdr *stats.Recorder
}

// newWireProxy creates the loopback proxy side of the wire harness: a real
// UDP or TCP transport bound to a kernel-assigned loopback port, served by
// a detached goroutine, with the statistics recorder and the proxy bound to
// the element. It is the real-transport variant of the newTestProxy family.
func newWireProxy(
	tb testing.TB,
	proto sip.TransportProto,
	routes []proxy.Route,
	opts proxy.Options,
) *wireProxy {
	tb.Helper()

	elm := &sip.Element{DNSResolver: loopbackDNS()}
	tb.Cleanup(func() {
		if err := elm.Shutdown(context.WithoutCancel(tb.Context())); err != nil {
			tb.Errorf("elm.Shutdown() error = %v, want nil", err)
		}
	})

	var (
		tp  sip.Transport
		err error
	)
	switch proto.Canonic() {
	case "UDP":
		tp, err = transport.NewConnLessTransport(
			sip.UDPMetadata(),
			transport.TransportOptions{PublicAddr: sip.MakeHostAddr("127.0.0.1")},
		)
	case "TCP":
		tp, err = transport.NewConnOrientedTransport(
			sip.TCPMetadata(),
			transport.TransportOptions{PublicAddr: sip.MakeHostAddr("127.0.0.1")},
		)
	default:
		tb.Fatalf("unsupported wire transport %q", proto)
	}
	if err != nil {
		tb.Fatalf("transport.New*Transport() error = %v, want nil", err)
	}

	if err := elm.TrackTransport(tp); err != nil {
		tb.Fatalf("elm.TrackTransport() error = %v, want nil", err)
	}

	rcdr := &stats.Recorder{}
	elm.UseMiddleware(rcdr)

	lis, err := elm.Listen(tb.Context(), proto, "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("elm.Listen() error = %v, want nil", err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- lis.Serve(context.WithoutCancel(tb.Context())) }()
	tb.Cleanup(func() {
		lis.Close()
		if err := <-serveDone; err != nil &&
			!errors.Is(err, net.ErrClosed) &&
			!errors.Is(err, context.Canceled) &&
			!errors.Is(err, transport.ErrTransportClosed) &&
			!errors.Is(err, transport.ErrNetworkClosed) {
			tb.Errorf("lis.Serve() error = %v, want transport closed", err)
		}
	})

	prx, err := proxy.New(routes, opts)
	if err != nil {
		tb.Fatalf("proxy.New() error = %v, want nil", err)
	}
	elm.UseMiddleware(prx)
	prx.BindEventHandler(rcdr)

	return &wireProxy{elm: elm, tp: tp, lis: lis, addr: lis.LocalAddr(), prx: prx, rcdr: rcdr}
}

// wirePeer is one serialized-wire endpoint of the harness. On UDP it is a
// packet connection; on TCP a client endpoint holds a dialed connection
// (plus an optional accept backlog for responses a restarted side dials),
// while a server endpoint holds a listener and its first accepted
// connection.
type wirePeer struct {
	proto sip.TransportProto
	addr  netip.AddrPort

	// UDP endpoint.
	pc net.PacketConn

	// TCP endpoints.
	lis    net.Listener
	conn   net.Conn
	rdr    *siptest.StreamReader
	accept chan net.Conn
}

// newWireUAC opens the upstream endpoint. For TCP it dials the proxy and
// additionally keeps a loopback listener whose address is written into the
// Via sent-by so a restarted proxy side can dial a fresh response
// connection.
func newWireUAC(tb testing.TB, proto sip.TransportProto, proxyAddr netip.AddrPort) *wirePeer {
	tb.Helper()

	p := &wirePeer{proto: proto.Canonic()}
	switch p.proto {
	case "UDP":
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			tb.Fatalf("net.ListenPacket(uac) error = %v, want nil", err)
		}
		tb.Cleanup(func() { pc.Close() })
		p.pc = pc
		p.addr = netip.MustParseAddrPort(pc.LocalAddr().String())
	case "TCP":
		conn, err := net.Dial("tcp", proxyAddr.String())
		if err != nil {
			tb.Fatalf("net.Dial(uac) error = %v, want nil", err)
		}
		tb.Cleanup(func() { conn.Close() })
		p.conn = conn
		p.rdr = siptest.NewStreamReader(tb, conn)

		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			tb.Fatalf("net.Listen(uac) error = %v, want nil", err)
		}
		tb.Cleanup(func() { lis.Close() })
		p.lis = lis
		p.accept = make(chan net.Conn, 4)
		go func() {
			for {
				conn, err := lis.Accept()
				if err != nil {
					return
				}
				p.accept <- conn
			}
		}()
		// The Via sent-by is the listener address: a restarted proxy side
		// without the inbound connection resolves it through Via routing.
		p.addr = netip.MustParseAddrPort(lis.Addr().String())
	default:
		tb.Fatalf("unsupported wire transport %q", proto)
	}
	return p
}

// newWireUAS opens a downstream endpoint bound to a kernel-assigned
// loopback port.
func newWireUAS(tb testing.TB, proto sip.TransportProto) *wirePeer {
	tb.Helper()

	p := &wirePeer{proto: proto.Canonic()}
	switch p.proto {
	case "UDP":
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			tb.Fatalf("net.ListenPacket(uas) error = %v, want nil", err)
		}
		tb.Cleanup(func() { pc.Close() })
		p.pc = pc
		p.addr = netip.MustParseAddrPort(pc.LocalAddr().String())
	case "TCP":
		lis, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			tb.Fatalf("net.Listen(uas) error = %v, want nil", err)
		}
		tb.Cleanup(func() { lis.Close() })
		p.lis = lis
		p.addr = netip.MustParseAddrPort(lis.Addr().String())
		p.accept = make(chan net.Conn, 4)
		go func() {
			for {
				conn, err := lis.Accept()
				if err != nil {
					return
				}
				p.accept <- conn
			}
		}()
	default:
		tb.Fatalf("unsupported wire transport %q", proto)
	}
	return p
}

// send serializes msg onto the wire toward dst.
func (p *wirePeer) send(tb testing.TB, msg sip.Message, dst netip.AddrPort) {
	tb.Helper()

	wire := []byte(shared.Render(msg))
	switch p.proto {
	case "UDP":
		if _, err := p.pc.WriteTo(wire, net.UDPAddrFromAddrPort(dst)); err != nil {
			tb.Fatalf("pc.WriteTo() error = %v, want nil", err)
		}
	case "TCP":
		if p.conn == nil {
			tb.Fatal("wire peer has no accepted connection to write on")
		}
		if _, err := p.conn.Write(wire); err != nil {
			tb.Fatalf("conn.Write() error = %v, want nil", err)
		}
	default:
		tb.Fatalf("unsupported wire transport %q", p.proto)
	}
}

// sendDialedTo serializes msg over a fresh TCP connection to dst. It is used
// by downstream endpoints to deliver a message to a restarted proxy side
// that holds no connection to the peer. The connection is retained for the
// message exchange and closed at test cleanup.
func (p *wirePeer) sendDialedTo(tb testing.TB, msg sip.Message, dst netip.AddrPort) {
	tb.Helper()

	if p.proto != "TCP" {
		tb.Fatalf("p.sendDialedTo() requires TCP, got %q", p.proto)
	}
	conn, err := net.Dial("tcp", dst.String())
	if err != nil {
		tb.Fatalf("net.Dial(%v) error = %v, want nil", dst, err)
	}
	tb.Cleanup(func() { conn.Close() })
	if _, err := conn.Write([]byte(shared.Render(msg))); err != nil {
		tb.Fatalf("conn.Write() error = %v, want nil", err)
	}
}

// recv reads and parses the next wire message. On TCP the first call blocks
// for the proxy's outbound connection accept, and accepted-side reads use
// the shared [siptest.StreamReader].
func (p *wirePeer) recv(tb testing.TB) sip.Message {
	tb.Helper()

	switch p.proto {
	case "UDP":
		buf := make([]byte, 64*1024)
		if err := p.pc.SetReadDeadline(time.Now().Add(wireReadTimeout)); err != nil {
			tb.Fatalf("pc.SetReadDeadline() error = %v, want nil", err)
		}
		n, _, err := p.pc.ReadFrom(buf)
		if resetErr := p.pc.SetReadDeadline(time.Time{}); resetErr != nil {
			tb.Fatalf("pc.SetReadDeadline(zero) error = %v, want nil", resetErr)
		}
		if err != nil {
			tb.Fatalf("pc.ReadFrom() error = %v, want one wire message", err)
		}
		msg, err := sip.ParsePacket(buf[:n])
		if err != nil {
			tb.Fatalf("sip.ParsePacket() error = %v, want nil", err)
		}
		return msg
	case "TCP":
		msg, ok := p.tryRecv(tb, wireReadTimeout)
		if !ok {
			tb.Fatalf("timed out reading one wire message over %v", p.proto)
		}
		return msg
	default:
		tb.Fatalf("unsupported wire transport %q", p.proto)
		return nil
	}
}

// recvAccepted reads the next message on a freshly accepted connection of a
// client endpoint — the channel a restarted proxy side dials for Via-routed
// responses.
func (p *wirePeer) recvAccepted(tb testing.TB) sip.Message {
	tb.Helper()

	if p.proto != "TCP" {
		tb.Fatalf("p.recvAccepted() requires TCP, got %q", p.proto)
	}
	var conn net.Conn
	select {
	case conn = <-p.accept:
	case <-time.After(wireReadTimeout):
		tb.Fatal("timed out waiting for the restarted side connection")
	}
	tb.Cleanup(func() { conn.Close() })
	return siptest.NewStreamReader(tb, conn).Read(tb)
}

// expectSilentExcept reads the peer for the whole window and reports
// whether only allowed-method traffic (or nothing) arrived: an inbound
// transaction-generated ACK after a non-2xx final is legitimate wire
// traffic, while a re-forked request is not.
func (p *wirePeer) expectSilentExcept(
	tb testing.TB,
	window time.Duration,
	allowed ...sip.RequestMethod,
) bool {
	tb.Helper()

	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		msg, ok := p.tryRecv(tb, time.Until(deadline))
		if !ok {
			return true
		}
		req, isReq := msg.(*sip.Request)
		if !isReq {
			tb.Logf("unexpected wire message during silence window: %v", msg)
			return false
		}
		okMtd := false
		for _, mtd := range allowed {
			if req.Method.Equal(mtd) {
				okMtd = true
				break
			}
		}
		if !okMtd {
			tb.Logf("unexpected wire request during silence window: %v %v", req.Method, req.URI)
			return false
		}
	}
	return true
}

// tryRecv reads one message within d; it returns false on a clean timeout.
func (p *wirePeer) tryRecv(tb testing.TB, d time.Duration) (sip.Message, bool) {
	tb.Helper()

	if d <= 0 {
		return nil, false
	}
	switch p.proto {
	case "UDP":
		if err := p.pc.SetReadDeadline(time.Now().Add(d)); err != nil {
			tb.Fatalf("pc.SetReadDeadline() error = %v, want nil", err)
		}
		buf := make([]byte, 64*1024)
		n, _, err := p.pc.ReadFrom(buf)
		if resetErr := p.pc.SetReadDeadline(time.Time{}); resetErr != nil {
			tb.Fatalf("pc.SetReadDeadline(zero) error = %v, want nil", resetErr)
		}
		if err != nil {
			nerr, ok := errors.AsType[net.Error](err)
			if ok && nerr.Timeout() {
				return nil, false
			}
			tb.Fatalf("pc.ReadFrom() error = %v, want one wire message", err)
		}
		msg, perr := sip.ParsePacket(buf[:n])
		if perr != nil {
			tb.Fatalf("sip.ParsePacket() error = %v, want nil", perr)
		}
		return msg, true
	case "TCP":
		// Follow the newest accepted connection: a Via-routed response
		// may be dialed to the endpoint on a fresh connection, and the
		// dial can race an in-flight read on the previous one. Reads go
		// through the framed [siptest.StreamReader] so messages coalesced
		// into one segment are never dropped.
		deadline := time.Now().Add(d)
		adopt := func(conn net.Conn) {
			tb.Cleanup(func() { conn.Close() })
			p.conn = conn
			p.rdr = siptest.NewStreamReader(tb, conn)
			// A newly dialed connection may already carry the awaited
			// message; give its reader the full window.
			deadline = time.Now().Add(d)
		}
		for {
			select {
			case conn := <-p.accept:
				adopt(conn)
				continue
			default:
			}
			if p.rdr == nil {
				select {
				case conn := <-p.accept:
					adopt(conn)
					continue
				case <-time.After(time.Until(deadline)):
					return nil, false
				}
			}
			msg, ok := p.rdr.TryRead(tb, time.Until(deadline))
			if ok {
				return msg, true
			}
			// Retry once on a newly dialed connection.
			select {
			case conn := <-p.accept:
				adopt(conn)
				continue
			default:
				return nil, false
			}
		}
	default:
		tb.Fatalf("unsupported wire transport %q", p.proto)
		return nil, false
	}
}

// expectSilent reports whether the peer stays quiet for the whole window:
// a message arriving inside it means something was emitted that should not
// have been.
func (p *wirePeer) expectSilent(tb testing.TB, window time.Duration) bool {
	tb.Helper()

	switch p.proto {
	case "UDP":
		if err := p.pc.SetReadDeadline(time.Now().Add(window)); err != nil {
			tb.Fatalf("pc.SetReadDeadline() error = %v, want nil", err)
		}
		buf := make([]byte, 64*1024)
		n, _, err := p.pc.ReadFrom(buf)
		if resetErr := p.pc.SetReadDeadline(time.Time{}); resetErr != nil {
			tb.Fatalf("pc.SetReadDeadline(zero) error = %v, want nil", resetErr)
		}
		if err == nil {
			if n > 0 {
				if msg, perr := sip.ParsePacket(buf[:n]); perr == nil {
					tb.Logf("unexpected wire message during silence window: %v", msg)
				}
			}
			return n == 0
		}
		nerr, ok := errors.AsType[net.Error](err)
		return ok && nerr.Timeout()
	case "TCP":
		if p.rdr == nil {
			// No connection exists yet: nothing can arrive without an
			// accept, so the silence check is the accept channel itself.
			select {
			case conn := <-p.accept:
				tb.Cleanup(func() { conn.Close() })
				p.conn = conn
				p.rdr = siptest.NewStreamReader(tb, conn)
				return false
			case <-time.After(window):
				return true
			}
		}
		// The framed reader retains any buffered message for later reads,
		// so consuming one here only proves traffic arrived.
		msg, ok := p.rdr.TryRead(tb, window)
		if ok {
			tb.Logf("unexpected wire message during silence window: %v", msg)
		}
		return !ok
	default:
		tb.Fatalf("unsupported wire transport %q", p.proto)
		return false
	}
}

// wireReq builds the serialized upstream request of the harness: the Via
// sent-by names the endpoint's public address so responses can come back.
// wireReqOpts carries the per-request fields of a wire UAC request.
type wireReqOpts struct {
	branch, callID, fromTag, user string
	maxForwards                   header.MaxForwards
	body                          []byte
}

func wireReq(
	tb testing.TB,
	proto sip.TransportProto,
	mtd sip.RequestMethod,
	viaAddr netip.AddrPort,
	o wireReqOpts,
) *sip.Request {
	tb.Helper()

	return &sip.Request{
		Proto:  sip.ProtoVer20(),
		Method: mtd,
		URI: &sip.URI{
			User: sip.MakeUserInfo(o.user),
			Addr: sip.MakeHostAddr("voip.test"),
		},
		Headers: make(sip.Headers).
			Set(header.Via{{
				Proto:     sip.ProtoVer20(),
				Transport: proto,
				Addr:      sip.MakeHostPortAddr(viaAddr.Addr().String(), viaAddr.Port()),
				Params:    make(sip.Values).Set("branch", o.branch),
			}}).
			Set(&header.From{
				URI:    &sip.URI{User: sip.MakeUserInfo("uac"), Addr: sip.MakeHostAddr("uac.test")},
				Params: make(sip.Values).Set("tag", o.fromTag),
			}).
			Set(&header.To{
				URI: &sip.URI{User: sip.MakeUserInfo(o.user), Addr: sip.MakeHostAddr("voip.test")},
			}).
			Set(header.CallID(o.callID)).
			Set(&header.CSeq{SeqNum: 1, Method: mtd}).
			Set(o.maxForwards).
			Set(header.ContentLength(len(o.body))),
		Body: o.body,
	}
}

// wireAck derives an ACK from the original request and the upstream
// response it acknowledges, per RFC 3261: an in-transaction (non-2xx) ACK
// keeps the request's branch, an end-to-end (2xx) ACK gets its own.
func wireAck(
	tb testing.TB,
	inv *sip.Request,
	toTag string,
	branch string,
	routes []header.RouteHop,
	ruri *sip.URI,
) *sip.Request {
	tb.Helper()

	ack := inv.Clone().(*sip.Request)
	ack.Method = sip.RequestMethodAck
	ack.Body = nil
	ack.Headers.
		Set(&header.CSeq{SeqNum: 1, Method: sip.RequestMethodAck}).
		Set(header.ContentLength(0))
	if via, ok := ack.Headers.FirstVia(); ok {
		via.Params.Set("branch", branch)
	}
	if to, ok := ack.Headers.To(); ok && toTag != "" {
		if to.Params == nil {
			to.Params = make(sip.Values)
		}
		to.Params.Set("tag", toTag)
	}
	if ruri != nil {
		ack.URI = ruri
	}
	for _, hop := range slices.Backward(routes) {
		ack.Headers.PrependRoute(hop.Clone())
	}
	return ack
}

// wireRes builds a response to a parsed wire request as a downstream
// endpoint would emit it.
func wireRes(tb testing.TB, req *sip.Request, sts sip.ResponseStatus, toTag string) *sip.Response {
	tb.Helper()

	res, err := req.NewResponse(sts, sip.ResponseOptions{LocalTag: toTag})
	if err != nil {
		tb.Fatalf("req.NewResponse(%v) error = %v, want nil", sts, err)
	}
	res.Headers.Set(header.ContentLength(len(res.Body)))
	return res
}

// recvWireReq reads messages until a request carrying callID arrives and
// returns it parsed; stale traffic of other calls is skipped.
func recvWireReq(tb testing.TB, p *wirePeer, callID string) *sip.Request {
	tb.Helper()

	deadline := time.Now().Add(wireReadTimeout)
	for time.Now().Before(deadline) {
		msg := p.recv(tb)
		req, ok := msg.(*sip.Request)
		if !ok {
			continue
		}
		if cid, ok := req.Headers.CallID(); ok && string(cid) == callID {
			return req
		}
	}
	tb.Fatalf("no wire request with Call-ID %q", callID)
	return nil
}

// recvWireRes reads messages until a response carrying callID arrives and
// returns it parsed.
func recvWireRes(tb testing.TB, p *wirePeer, callID string) *sip.Response {
	tb.Helper()

	deadline := time.Now().Add(wireReadTimeout)
	for time.Now().Before(deadline) {
		msg := p.recv(tb)
		res, ok := msg.(*sip.Response)
		if !ok {
			continue
		}
		if cid, ok := res.Headers.CallID(); ok && string(cid) == callID {
			return res
		}
	}
	tb.Fatalf("no wire response with Call-ID %q", callID)
	return nil
}

// wireResCSeqMethod returns the CSeq method of a parsed wire response.
func wireResCSeqMethod(tb testing.TB, res *sip.Response) sip.RequestMethod {
	tb.Helper()

	cseq, ok := res.Headers.CSeq()
	if !ok || cseq == nil {
		tb.Fatal("wire response has no CSeq")
	}
	return cseq.Method
}

// reqToTag returns the To tag of a parsed wire message.
func resToTag(tb testing.TB, res *sip.Response) string {
	tb.Helper()

	to, ok := res.Headers.To()
	if !ok || to == nil {
		tb.Fatal("wire response has no To header")
	}
	tag, _ := to.Tag()
	return tag
}

// wireVias returns the parsed Via hops of a wire message in order.
func wireVias(tb testing.TB, hdrs sip.Headers) []*header.ViaHop {
	tb.Helper()

	return slices.Collect(hdrs.Vias())
}

// loopbackTarget builds a fixture target URI resolved to loopback through
// the fake DNS table; the explicit port keeps resolution on the A/AAAA
// branch and the transport parameter pins the wire protocol.
func loopbackTarget(id, user, host string, port uint16, proto sip.TransportProto) proxy.Target {
	return proxy.Target{
		ID: id,
		URI: &sip.URI{
			User:   sip.MakeUserInfo(user),
			Addr:   sip.MakeHostPortAddr(host, port),
			Params: make(sip.Values).Set("transport", strings.ToLower(string(proto))),
		},
	}
}

// assertFwdCopy pins the exact serialized form of one outbound proxy
// request: Max-Forwards decremented to zero, the proxy-owned top Via, the
// preserved prior hop, the advertised Record-Route, the optional per-target
// Route value, the verbatim body, and the untouched dialog headers.
// fwdCopyExpectation carries the invariants a forwarded wire copy
// must satisfy for one scenario.
type fwdCopyExpectation struct {
	proto                      sip.TransportProto
	proxyAddr, uacAddr         netip.AddrPort
	uacBranch, callID, fromTag string
	targetHost                 string
	targetPort                 uint16
	wantRoute                  string
	origBody                   []byte
}

func assertFwdCopy(tb testing.TB, req *sip.Request, want fwdCopyExpectation) {
	tb.Helper()

	proto := want.proto
	proxyAddr := want.proxyAddr
	uacAddr := want.uacAddr
	uacBranch := want.uacBranch
	callID := want.callID
	fromTag := want.fromTag
	targetHost := want.targetHost
	targetPort := want.targetPort
	wantRoute := want.wantRoute
	origBody := want.origBody
	tb.Helper()

	if got := req.Method; !got.Equal(sip.RequestMethodInvite) && !got.Equal(sip.RequestMethodInfo) {
		tb.Fatalf("forwarded method = %v, want INVITE or INFO", got)
	}

	gotMF, okMF := req.Headers.MaxForwards()
	if !okMF || gotMF != 0 {
		tb.Fatalf("wire max-forwards=%d (present=%v), want 0", gotMF, okMF)
	}

	vias := wireVias(tb, req.Headers)
	if got := len(vias); got != 2 {
		tb.Fatalf("wire via count=%d, want 2", got)
	}
	top := vias[0]
	if !top.Transport.Equal(proto) {
		tb.Fatalf("top via transport=%v, want %v", top.Transport, proto)
	}
	if got, want := top.Addr.Host(), proxyAddr.Addr().String(); got != want {
		tb.Fatalf("top via sent-by=%q, want host %q", got, want)
	}
	branch, ok := top.Branch()
	if !ok || !strings.HasPrefix(branch, sip.MagicCookie+".proxy-") {
		tb.Fatalf("top via branch=%q (present=%v), want proxy-owned mark", branch, ok)
	}
	if got, want := vias[1].Addr.String(), uacAddr.String(); got != want {
		tb.Fatalf("prior via sent-by=%q, want %q", got, want)
	}
	if got, _ := vias[1].Branch(); got != uacBranch {
		tb.Fatalf("prior via branch=%q, want %q", got, uacBranch)
	}

	rr, ok := req.Headers.FirstRecordRoute()
	if !ok || rr == nil || rr.URI == nil {
		tb.Fatal("wire record-route missing, want advertised identity")
	}
	rru, ok := rr.URI.(*sip.URI)
	if !ok {
		tb.Fatal("wire record-route URI is not a SIP URI")
	}
	if got := rru.Addr.Host(); got != "proxy.test" {
		tb.Fatalf("record-route host=%q, want %q", got, "proxy.test")
	}
	if !rru.LR() {
		tb.Fatal("record-route misses the lr parameter")
	}

	hop, hasRoute := req.Headers.FirstRoute()
	switch {
	case wantRoute == "" && hasRoute:
		tb.Fatalf("wire route=%v, want none", hop)
	case wantRoute != "":
		if !hasRoute {
			tb.Fatalf("wire route missing, want %q", wantRoute)
		}
		ru, ok := hop.URI.(*sip.URI)
		if !ok {
			tb.Fatal("wire route URI is not a SIP URI")
		}
		if got := ru.Addr.Host(); got != wantRoute {
			tb.Fatalf("wire route host=%q, want %q", got, wantRoute)
		}
		if !ru.LR() {
			tb.Fatal("wire route misses the lr parameter")
		}
	}

	if diff := cmp.Diff(origBody, req.Body); diff != "" {
		tb.Fatalf("wire body (-want +got): %s", diff)
	}

	from, _ := req.Headers.From()
	if tag, _ := from.Tag(); tag != fromTag {
		tb.Fatalf("wire from tag=%q, want %q", tag, fromTag)
	}
	to, _ := req.Headers.To()
	if tag, _ := to.Tag(); tag != "" {
		tb.Fatalf("wire to tag=%q, want tagless", tag)
	}
	if cid, _ := req.Headers.CallID(); string(cid) != callID {
		tb.Fatalf("wire call-id=%q, want %q", cid, callID)
	}
	cseq, _ := req.Headers.CSeq()
	if cseq.SeqNum != 1 {
		tb.Fatalf("wire cseq=%d, want 1", cseq.SeqNum)
	}

	ru, ok := req.URI.(*sip.URI)
	if !ok {
		tb.Fatal("wire request URI is not a SIP URI")
	}
	if got := ru.Addr.Host(); got != targetHost {
		tb.Fatalf("wire request URI host=%q, want %q", got, targetHost)
	}
	if port, _ := ru.Addr.Port(); port != targetPort {
		tb.Fatalf("wire request URI port=%d, want %d", port, targetPort)
	}
}

// collectEvts returns a channel of proxy events for later kind
// assertions.
func collectEvts(prx *proxy.Proxy) chan proxy.Event {
	evts := make(chan proxy.Event, 256)
	prx.BindEventHandler(proxy.EventHandlerFunc(
		func(_ context.Context, ev proxy.Event) { evts <- ev },
	))
	return evts
}

// waitEvtCounts accumulates events by kind until every wanted kind
// reaches its total or the deadline passes.
func waitEvtCounts(tb testing.TB, events chan proxy.Event, want map[proxy.EventKind]int) {
	tb.Helper()

	counts := make(map[proxy.EventKind]int)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		done := true
		for kind, wantN := range want {
			if counts[kind] < wantN {
				done = false
				break
			}
		}
		if done {
			return
		}
		select {
		case ev := <-events:
			counts[ev.Kind]++
		case <-time.After(50 * time.Millisecond):
		}
	}
	tb.Fatalf("proxy event counts=%v, want at least %v", counts, want)
}

// waitStats polls the recorder until want matches every named counter:
// upstream send results settle asynchronously through the coordinator, so
// the wire observation alone does not prove the counters committed yet.
func waitStats(tb testing.TB, rcdr *stats.Recorder, want stats.Proxy) {
	tb.Helper()

	deadline := time.Now().Add(2 * time.Second)
	var got stats.Proxy
	for time.Now().Before(deadline) {
		got = rcdr.Report().Proxy
		match := true
		if want.RequestsHandled != 0 && got.RequestsHandled != want.RequestsHandled {
			match = false
		}
		if want.ForwardAttemptsSucceeded != 0 && got.ForwardAttemptsSucceeded != want.ForwardAttemptsSucceeded {
			match = false
		}
		if want.RequestsForwarded != 0 && got.RequestsForwarded != want.RequestsForwarded {
			match = false
		}
		if want.ResponsesForwarded != 0 && got.ResponsesForwarded != want.ResponsesForwarded {
			match = false
		}
		if want.RequestsCompleted != 0 && got.RequestsCompleted != want.RequestsCompleted {
			match = false
		}
		if match {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	tb.Fatalf("proxy stats = %+v, want at least %+v", got, want)
}

// TestProxy_Integration exercises the full proxy pipeline on the serialized
// wire over loopback UDP and TCP: stateful three-fork aggregation with the
// exact outbound form, CANCEL and ACK dispatch, both non-INVITE server
// transaction behavior profiles, stateless forwarding, retransmission and
// stray discard handling, a dormant restore with activation on a fresh
// side, and the lifecycle seal — all while the observation stream and the
// statistics recorder keep count.
//
//nolint:tparallel // subtests share the integration fixture and cannot run in parallel
func TestProxy_Integration(t *testing.T) {
	t.Parallel()

	t.Run("UDP", func(t *testing.T) { runIntegration(t, "UDP") }) //nolint:paralleltest // UDP and TCP runs share the test fixture
	t.Run("TCP", func(t *testing.T) { runIntegration(t, "TCP") }) //nolint:paralleltest // UDP and TCP runs share the test fixture
}

func runIntegration(t *testing.T, proto sip.TransportProto) {
	t.Helper()

	ctx := t.Context()
	proto = proto.Canonic()

	uasA := newWireUAS(t, proto)
	uasB := newWireUAS(t, proto)
	uasC := newWireUAS(t, proto)

	var tpRef sip.ClientTransport
	opts := proxy.Options{
		LocalIdentities: []*sip.URI{
			{Addr: sip.MakeHostAddr("proxy.test")},
		},
		ServedDomains: []string{"voip.test"},
		StatelessKey:  []byte("integration-stateless-key"),
		// Fixture DNS keeps stateless sends deterministic for hostname
		// next hops: every fixture resolves to loopback under the
		// recorded port.
		ResolveStatelessDestination: func(
			_ context.Context,
			_ *sip.RequestEnvelope,
			trgt proxy.Target,
		) (proxy.BoundDelivery, error) {
			u := trgt.URI
			if u == nil {
				return proxy.BoundDelivery{}, errors.Errorf("target %q misses a URI", trgt.ID)
			}
			port, _ := u.Addr.Port()
			return proxy.BoundDelivery{
				Transport:  tpRef,
				RemoteAddr: netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), port),
			}, nil
		},
	}

	// The upstream transaction's automatic 100 Trying and the downstream
	// transaction's Timer A retransmissions are pushed past the test
	// window on every stateful route so only proxy-forwarded traffic
	// reaches the wire reads and silence checks.
	statefulOpts := sip.ServerTransactionOptions{
		Timing: sip.TimingConfig{Time100: time.Hour},
	}
	clientOpts := sip.ClientTransactionOptions{
		Timing: sip.TimingConfig{T1: time.Hour},
	}

	// The routes discriminate requests by Call-ID, so one proxy serves
	// every scenario in sequence.
	routes := []proxy.Route{
		{
			Name:                     "fork",
			ForwardMode:              proxy.ForwardModeStateful,
			RecordRoute:              true,
			ServerTransactionOptions: statefulOpts,
			ClientTransactionOptions: clientOpts,
			MatchRequest: func(_ context.Context, req *sip.RequestEnvelope) bool {
				var match bool
				req.WithMessage(func(r *sip.Request) {
					if cid, ok := r.Headers.CallID(); ok {
						match = string(cid) == "call-fork" && r.Method.Equal(sip.RequestMethodInvite)
					}
				})
				return match
			},
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{{
					loopbackTarget("A", "alice", "uas-a.test", uasA.addr.Port(), proto),
					func() proxy.Target {
						trgt := loopbackTarget("B", "alice", "uas-b.test", uasB.addr.Port(), proto)
						// A loose route still picks the next hop on the
						// wire: the fixture host must resolve to the peer,
						// so the explicit port keeps it on loopback.
						trgt.Routes = []header.RouteHop{{URI: &sip.URI{
							Addr:   sip.MakeHostPortAddr("edge-b.test", uasB.addr.Port()),
							Params: make(sip.Values).Set("lr", ""),
						}}}
						return trgt
					}(),
					loopbackTarget("C", "alice", "uas-c.test", uasC.addr.Port(), proto),
				}}}, nil
			},
		},
		{
			Name:                     "ok",
			ForwardMode:              proxy.ForwardModeStateful,
			RecordRoute:              true,
			ServerTransactionOptions: statefulOpts,
			ClientTransactionOptions: clientOpts,
			MatchRequest: func(_ context.Context, req *sip.RequestEnvelope) bool {
				var match bool
				req.WithMessage(func(r *sip.Request) {
					if cid, ok := r.Headers.CallID(); ok {
						match = string(cid) == "call-ok"
					}
				})
				return match
			},
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{{
					loopbackTarget("D", "bob", "uas-a.test", uasA.addr.Port(), proto),
				}}}, nil
			},
		},
		{
			Name:                     "cancel",
			ForwardMode:              proxy.ForwardModeStateful,
			ServerTransactionOptions: statefulOpts,
			ClientTransactionOptions: clientOpts,
			MatchRequest: func(_ context.Context, req *sip.RequestEnvelope) bool {
				var match bool
				req.WithMessage(func(r *sip.Request) {
					if cid, ok := r.Headers.CallID(); ok {
						match = string(cid) == "call-cancel"
					}
				})
				return match
			},
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{{
					loopbackTarget("E", "carol", "uas-b.test", uasB.addr.Port(), proto),
				}}}, nil
			},
		},
		{
			Name:        "sl",
			ForwardMode: proxy.ForwardModeStateless,
			MatchRequest: func(_ context.Context, req *sip.RequestEnvelope) bool {
				var match bool
				req.WithMessage(func(r *sip.Request) {
					if cid, ok := r.Headers.CallID(); ok {
						match = string(cid) == "call-sl"
					}
				})
				return match
			},
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{{
					loopbackTarget("F", "dave", "127.0.0.1", uasC.addr.Port(), proto),
				}}}, nil
			},
		},
		{
			Name:        "ni-rfc",
			ForwardMode: proxy.ForwardModeStateful,
			ServerTransactionOptions: sip.ServerTransactionOptions{
				NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorRFC4320,
				// A short sip.T1/sip.T2 pair keeps the delayed Trying on a
				// loopback timescale without touching defaults.
				Timing: sip.TimingConfig{T1: 50 * time.Millisecond, T2: 200 * time.Millisecond},
			},
			MatchRequest: func(_ context.Context, req *sip.RequestEnvelope) bool {
				var match bool
				req.WithMessage(func(r *sip.Request) {
					if cid, ok := r.Headers.CallID(); ok {
						match = string(cid) == "call-ni-rfc"
					}
				})
				return match
			},
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{{
					loopbackTarget("G", "eve", "uas-a.test", uasA.addr.Port(), proto),
				}}}, nil
			},
		},
		{
			Name:        "ni-legacy",
			ForwardMode: proxy.ForwardModeStateful,
			ServerTransactionOptions: sip.ServerTransactionOptions{
				NonInviteServerTransactionBehavior: sip.NonInviteServerTransactionBehaviorLegacy,
			},
			MatchRequest: func(_ context.Context, req *sip.RequestEnvelope) bool {
				var match bool
				req.WithMessage(func(r *sip.Request) {
					if cid, ok := r.Headers.CallID(); ok {
						match = string(cid) == "call-ni-legacy"
					}
				})
				return match
			},
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{{
					loopbackTarget("H", "frank", "uas-b.test", uasB.addr.Port(), proto),
				}}}, nil
			},
		},
		{
			Name:                     "restore",
			ForwardMode:              proxy.ForwardModeStateful,
			ServerTransactionOptions: statefulOpts,
			ClientTransactionOptions: clientOpts,
			MatchRequest: func(_ context.Context, req *sip.RequestEnvelope) bool {
				var match bool
				req.WithMessage(func(r *sip.Request) {
					if cid, ok := r.Headers.CallID(); ok {
						match = string(cid) == "call-restore"
					}
				})
				return match
			},
			ResolveRequestTargets: func(context.Context, *sip.RequestEnvelope) (proxy.TargetPlan, error) {
				return proxy.TargetPlan{Groups: [][]proxy.Target{{
					loopbackTarget("P", "grace", "uas-a.test", uasA.addr.Port(), proto),
					loopbackTarget("Q", "grace", "uas-b.test", uasB.addr.Port(), proto),
				}}}, nil
			},
		},
	}

	side := newWireProxy(t, proto, routes, opts)
	tpRef = side.tp
	uac := newWireUAC(t, proto, side.addr)
	events := collectEvts(side.prx)
	snapCh := make(chan *proxy.RequestSnapshot, 32)
	side.prx.BindSnapshotHandler(proxy.RequestSnapshotHandlerFunc(
		func(_ context.Context, snap *proxy.RequestSnapshot) {
			select {
			case snapCh <- snap:
			default:
			}
		},
	))

	sendToSide := func(p *wirePeer, msg sip.Message) { p.send(t, msg, side.addr) }

	// --- Scenario: stateful three-fork aggregation on the serialized wire. ---
	origBody := []byte("v=0\r\no=- 1 1 IN IP4 127.0.0.1\r\ns=-\r\n")
	inv := wireReq(t, proto, sip.RequestMethodInvite, uac.addr, wireReqOpts{
		branch:      sip.MagicCookie + ".uac-fork",
		callID:      "call-fork",
		fromTag:     "uac-fork-tag",
		user:        "alice",
		maxForwards: 1,
		body:        origBody,
	})
	sendToSide(uac, inv)

	forkReqs := map[string]*sip.Request{
		"A": recvWireReq(t, uasA, "call-fork"),
		"B": recvWireReq(t, uasB, "call-fork"),
		"C": recvWireReq(t, uasC, "call-fork"),
	}
	uasHosts := map[string]string{"A": "uas-a.test", "B": "uas-b.test", "C": "uas-c.test"}
	uasPorts := map[string]uint16{"A": uasA.addr.Port(), "B": uasB.addr.Port(), "C": uasC.addr.Port()}
	uasPeers := map[string]*wirePeer{"A": uasA, "B": uasB, "C": uasC}
	for id, out := range forkReqs {
		wantRoute := ""
		if id == "B" {
			wantRoute = "edge-b.test"
		}
		assertFwdCopy(t, out, fwdCopyExpectation{
			proto:      proto,
			proxyAddr:  side.addr,
			uacAddr:    uac.addr,
			uacBranch:  sip.MagicCookie + ".uac-fork",
			callID:     "call-fork",
			fromTag:    "uac-fork-tag",
			targetHost: uasHosts[id],
			targetPort: uasPorts[id],
			wantRoute:  wantRoute,
			origBody:   origBody,
		})
	}

	sendToSide(uasA, wireRes(t, forkReqs["A"], sip.ResponseStatusRinging, "a-tag"))
	sendToSide(uasB, wireRes(t, forkReqs["B"], sip.ResponseStatusSessionProgress, "b-tag"))
	seenProv := make(map[int]bool)
	for range 2 {
		res := recvWireRes(t, uac, "call-fork")
		seenProv[int(res.Status)] = true
	}
	if !seenProv[int(sip.ResponseStatusRinging)] || !seenProv[int(sip.ResponseStatusSessionProgress)] {
		t.Fatalf("upstream provisionals=%v, want both 180 and 183", seenProv)
	}

	// The three finals aggregate: the earliest equal-rank arrival wins, so
	// the 404 goes out ahead of its siblings to make arrival order
	// deterministic on both transports.
	sendToSide(uasA, wireRes(t, forkReqs["A"], sip.ResponseStatusNotFound, "a-tag"))
	time.Sleep(50 * time.Millisecond)
	sendToSide(uasB, wireRes(t, forkReqs["B"], sip.ResponseStatusBusyHere, "b-tag"))
	sendToSide(uasC, wireRes(t, forkReqs["C"], sip.ResponseStatusTemporarilyUnavailable, "c-tag"))

	var upstreamFinal *sip.Response
	for range 3 {
		res := recvWireRes(t, uac, "call-fork")
		if res.Status.IsFinal() {
			upstreamFinal = res
			break
		}
	}
	if upstreamFinal == nil || upstreamFinal.Status != sip.ResponseStatusNotFound {
		t.Fatalf("aggregated upstream final=%v, want 404", upstreamFinal)
	}
	if got := resToTag(t, upstreamFinal); got != "a-tag" {
		t.Fatalf("aggregated final To tag=%q, want %q", got, "a-tag")
	}

	// The brief's assertion core: one claimed request, three settled
	// attempts, one forwarded request, three forwarded responses.
	waitStats(t, side.rcdr, stats.Proxy{
		RequestsHandled:          1,
		ForwardAttemptsSucceeded: 3,
		RequestsForwarded:        1,
		ResponsesForwarded:       3,
		RequestsCompleted:        1,
	})
	// Events are emitted from the coordinator drain; collect the committed
	// sequence until every expected kind reaches its total.
	waitEvtCounts(t, events, map[proxy.EventKind]int{
		proxy.EventKindRequestHandled:         1,
		proxy.EventKindForwardAttemptFinished: 3,
		proxy.EventKindRequestForwarded:       1,
		proxy.EventKindResponseForwarded:      3,
		proxy.EventKindRequestCompleted:       1,
	})

	// A retransmitted INVITE is absorbed by its server transaction, which
	// re-sends the stored final without a new downstream fork.
	sendToSide(uac, inv)
	if res := recvWireRes(t, uac, "call-fork"); res.Status != sip.ResponseStatusNotFound {
		t.Fatalf("retransmitted response=%v, want 404", res.Status)
	}
	for id, p := range uasPeers {
		if !p.expectSilentExcept(t, wireSilenceWindow, sip.RequestMethodAck) {
			t.Fatalf("uas %s observed a late fork, want none", id)
		}
	}

	// The in-transaction ACK for the non-2xx final is dispatched to the
	// matching INVITE transaction and absorbed — nothing crosses the wire.
	// A Timer G retransmission of the stored 404 may already be in flight
	// when the ACK lands, so drain a bounded window before requiring
	// silence.
	sendToSide(uac, wireAck(t, inv, "a-tag", sip.MagicCookie+".uac-fork", nil, nil))
	time.Sleep(600 * time.Millisecond)
	for {
		if _, ok := uac.tryRecv(t, 50*time.Millisecond); !ok {
			break
		}
	}
	if !uac.expectSilent(t, wireSilenceWindow) {
		t.Fatal("uac observed traffic after the in-transaction ACK, want none")
	}

	// --- Scenario: 2xx forwarding plus end-to-end ACK. ---
	inv2 := wireReq(t, proto, sip.RequestMethodInvite, uac.addr, wireReqOpts{
		branch:      sip.MagicCookie + ".uac-ok",
		callID:      "call-ok",
		fromTag:     "uac-ok-tag",
		user:        "bob",
		maxForwards: header.MaxForwards(70),
	})
	sendToSide(uac, inv2)
	out2 := recvWireReq(t, uasA, "call-ok")

	sendToSide(uasA, wireRes(t, out2, sip.ResponseStatusSessionProgress, "d-tag"))
	sendToSide(uasA, wireRes(t, out2, sip.ResponseStatusOK, "d-tag"))
	if res := recvWireRes(t, uac, "call-ok"); res.Status != sip.ResponseStatusSessionProgress {
		t.Fatalf("upstream status=%v, want 183", res.Status)
	}
	res200 := recvWireRes(t, uac, "call-ok")
	if res200.Status != sip.ResponseStatusOK {
		t.Fatalf("upstream status=%v, want 200", res200.Status)
	}
	if got := resToTag(t, res200); got != "d-tag" {
		t.Fatalf("forwarded 200 To tag=%q, want %q (verbatim dialog identity)", got, "d-tag")
	}
	if got := len(wireVias(t, res200.Headers)); got != 1 {
		t.Fatalf("upstream 200 via count=%d, want 1 (proxy hop stripped)", got)
	}

	// The end-to-end ACK returns through the proxy's recorded route; the
	// local hop is consumed and the copy lands on the acknowledged target.
	ackRoutes := []header.RouteHop{{URI: &sip.URI{
		Addr:   sip.MakeHostAddr("proxy.test"),
		Params: make(sip.Values).Set("lr", ""),
	}}}
	ackRURI := &sip.URI{
		User:   sip.MakeUserInfo("bob"),
		Addr:   sip.MakeHostPortAddr("127.0.0.1", uasA.addr.Port()),
		Params: make(sip.Values).Set("transport", strings.ToLower(string(proto))),
	}
	sendToSide(uac, wireAck(t, inv2, "d-tag", sip.MagicCookie+".uac-ok.ack", ackRoutes, ackRURI))
	gotAck := recvWireReq(t, uasA, "call-ok")
	if !gotAck.Method.Equal(sip.RequestMethodAck) {
		t.Fatalf("downstream method=%v, want ACK", gotAck.Method)
	}
	if _, ok := gotAck.Headers.FirstRoute(); ok {
		t.Fatal("downstream ACK still carries the proxy's route hop")
	}
	vias := wireVias(t, gotAck.Headers)
	if len(vias) != 2 || !vias[0].Transport.Equal(proto) {
		t.Fatalf("downstream ACK vias=%v, want proxy hop over %v", vias, proto)
	}
	if branch, _ := vias[0].Branch(); !strings.HasPrefix(branch, sip.MagicCookie+".proxy-stateless.") {
		t.Fatalf("end-to-end ACK branch=%q, want proxy-stateless mark", branch)
	}
	if cseq, _ := gotAck.Headers.CSeq(); !cseq.Method.Equal(sip.RequestMethodAck) {
		t.Fatalf("downstream ACK cseq method=%v, want ACK", cseq.Method)
	}

	// --- Scenario: matching CANCEL acknowledged and forwarded downstream. ---
	inv3 := wireReq(t, proto, sip.RequestMethodInvite, uac.addr, wireReqOpts{
		branch:      sip.MagicCookie + ".uac-cancel",
		callID:      "call-cancel",
		fromTag:     "uac-cancel-tag",
		user:        "carol",
		maxForwards: header.MaxForwards(70),
	})
	sendToSide(uac, inv3)
	out3 := recvWireReq(t, uasB, "call-cancel")

	cnc, err := sip.NewCancelRequestEnvelope(sip.NewRequestEnvelope(inv3))
	if err != nil {
		t.Fatalf("sip.NewCancelRequestEnvelope() error = %v, want nil", err)
	}
	// Stream transports need an explicit Content-Length for framing;
	// sip.EnsureMessageContentLength skips empty bodies.
	cnc.WithMessage(func(r *sip.Request) {
		r.Headers.Set(header.ContentLength(0))
	})
	// Move the branch to Proceeding first so the downstream CANCEL is
	// emitted immediately rather than deferred to a racing provisional.
	sendToSide(uasB, wireRes(t, out3, sip.ResponseStatusRinging, "e-tag"))
	time.Sleep(100 * time.Millisecond)
	sendToSide(uac, cnc.Message())

	var gotCncRes *sip.Response
	for range 4 {
		gotCncRes = recvWireRes(t, uac, "call-cancel")
		if mtd := wireResCSeqMethod(t, gotCncRes); mtd.Equal(sip.RequestMethodCancel) {
			break
		}
	}
	if gotCncRes.Status != sip.ResponseStatusOK {
		t.Fatalf("upstream cancel response=%v, want 200", gotCncRes.Status)
	}

	// The downstream CANCEL of a Proceeding branch travels through the
	// branch's own cancel client transaction.
	gotCnc := recvWireReq(t, uasB, "call-cancel")
	if !gotCnc.Method.Equal(sip.RequestMethodCancel) {
		t.Fatalf("downstream method=%v, want CANCEL", gotCnc.Method)
	}
	if branch, _ := wireVias(t, gotCnc.Headers)[0].Branch(); !strings.HasPrefix(branch, sip.MagicCookie+".proxy-") {
		t.Fatalf("downstream CANCEL branch=%q, want proxy-owned", branch)
	}
	sendToSide(uasB, wireRes(t, gotCnc, sip.ResponseStatusOK, "e-tag"))
	sendToSide(uasB, wireRes(t, out3, sip.ResponseStatusRequestTerminated, "e-tag"))

	var sawTerminated bool
	for !sawTerminated {
		res := recvWireRes(t, uac, "call-cancel")
		mtd := wireResCSeqMethod(t, res)
		switch {
		case mtd.Equal(sip.RequestMethodCancel):
			continue
		case res.Status == sip.ResponseStatusRequestTerminated:
			sawTerminated = true
		case res.Status.IsProvisional():
			continue
		default:
			t.Fatalf("unexpected upstream response=%v", res.Status)
		}
	}

	// The cancelled request's server transaction retransmits the 487 until
	// the hop-by-hop ACK confirms it; send it and drain any in-flight
	// retransmission before the next scenario.
	sendToSide(uac, wireAck(t, inv3, "e-tag", sip.MagicCookie+".uac-cancel", nil, nil))
	time.Sleep(600 * time.Millisecond)
	for {
		if _, ok := uac.tryRecv(t, 50*time.Millisecond); !ok {
			break
		}
	}

	// --- Scenario: explicit stateless route emits a marked copy and relays
	// the response statelessly. ---
	info := wireReq(t, proto, sip.RequestMethodInfo, uac.addr, wireReqOpts{
		branch:      sip.MagicCookie + ".uac-sl",
		callID:      "call-sl",
		fromTag:     "uac-sl-tag",
		user:        "dave",
		maxForwards: 1,
	})
	sendToSide(uac, info)
	outSL := recvWireReq(t, uasC, "call-sl")
	if got := outSL.Method; !got.Equal(sip.RequestMethodInfo) {
		t.Fatalf("stateless downstream method=%v, want INFO", got)
	}
	topVia := wireVias(t, outSL.Headers)[0]
	if branch, _ := topVia.Branch(); !strings.HasPrefix(branch, sip.MagicCookie+".proxy-stateless.") {
		t.Fatalf("stateless branch=%q, want proxy-stateless mark", branch)
	}
	if got := len(wireVias(t, outSL.Headers)); got != 2 {
		t.Fatalf("stateless via count=%d, want 2", got)
	}
	if gotMF, ok := outSL.Headers.MaxForwards(); !ok || gotMF != 0 {
		t.Fatalf("stateless max-forwards=%v (present=%v), want 0", gotMF, ok)
	}

	sendToSide(uasC, wireRes(t, outSL, sip.ResponseStatusOK, "f-tag"))
	resSL := recvWireRes(t, uac, "call-sl")
	if resSL.Status != sip.ResponseStatusOK {
		t.Fatalf("stateless upstream response=%v, want 200", resSL.Status)
	}
	if got := len(wireVias(t, resSL.Headers)); got != 1 {
		t.Fatalf("stateless response via count=%d, want 1 (proxy hop stripped)", got)
	}
	if got := resToTag(t, resSL); got != "f-tag" {
		t.Fatalf("stateless response To tag=%q, want %q", got, "f-tag")
	}

	// --- Scenario: both non-INVITE server transaction profiles on the wire. ---
	infoRFC := wireReq(t, proto, sip.RequestMethodInfo, uac.addr, wireReqOpts{
		branch:      sip.MagicCookie + ".uac-ni-rfc",
		callID:      "call-ni-rfc",
		fromTag:     "uac-rfc-tag",
		user:        "eve",
		maxForwards: header.MaxForwards(70),
	})
	sendToSide(uac, infoRFC)
	outRFC := recvWireReq(t, uasA, "call-ni-rfc")

	// RFC 4320: the delayed Trying becomes eligible after sip.T1 backoff
	// reaches sip.T2 — the shortened timing pair keeps it on a loopback scale.
	res100 := recvWireRes(t, uac, "call-ni-rfc")
	if res100.Status != sip.ResponseStatusTrying {
		t.Fatalf("rfc4320 first upstream response=%v, want 100", res100.Status)
	}
	// A downstream non-INVITE provisional never crosses upstream.
	sendToSide(uasA, wireRes(t, outRFC, sip.ResponseStatusSessionProgress, "g-tag"))
	sendToSide(uasA, wireRes(t, outRFC, sip.ResponseStatusOK, "g-tag"))
	resRFC := recvWireRes(t, uac, "call-ni-rfc")
	if resRFC.Status != sip.ResponseStatusOK {
		t.Fatalf("rfc4320 upstream response=%v, want 200 (183 suppressed)", resRFC.Status)
	}

	infoLeg := wireReq(t, proto, sip.RequestMethodInfo, uac.addr, wireReqOpts{
		branch:      sip.MagicCookie + ".uac-ni-legacy",
		callID:      "call-ni-legacy",
		fromTag:     "uac-leg-tag",
		user:        "frank",
		maxForwards: header.MaxForwards(70),
	})
	sendToSide(uac, infoLeg)
	outLeg := recvWireReq(t, uasB, "call-ni-legacy")
	sendToSide(uasB, wireRes(t, outLeg, sip.ResponseStatusOK, "h-tag"))
	resLeg := recvWireRes(t, uac, "call-ni-legacy")
	if resLeg.Status != sip.ResponseStatusOK {
		t.Fatalf("legacy upstream response=%v, want 200 (no synthetic 100)", resLeg.Status)
	}

	// A stray response carrying a stateful-marked branch no transaction
	// owns is consumed silently — the late non-INVITE discard.
	stray := forkReqs["A"].Clone().(*sip.Request)
	stray.Method = sip.RequestMethodInfo
	stray.Headers.Set(&header.CSeq{SeqNum: 9, Method: sip.RequestMethodInfo})
	if via, ok := stray.Headers.FirstVia(); ok {
		via.Params.Set("branch", sip.MagicCookie+".proxy-stateful.0123456789abcdef0123456789abcdef.deadbeef")
	}
	sendToSide(uasA, wireRes(t, stray, sip.ResponseStatusOK, "stray-tag"))
	if !uac.expectSilent(t, wireSilenceWindow) {
		t.Fatal("uac observed traffic for a stray response, want silence")
	}

	// --- Scenario: dormant restore plus activation on a restarted side. ---
	inv4 := wireReq(t, proto, sip.RequestMethodInvite, uac.addr, wireReqOpts{
		branch:      sip.MagicCookie + ".uac-restore",
		callID:      "call-restore",
		fromTag:     "uac-restore-tag",
		user:        "grace",
		maxForwards: header.MaxForwards(70),
	})
	sendToSide(uac, inv4)
	outP := recvWireReq(t, uasA, "call-restore")
	outQ := recvWireReq(t, uasB, "call-restore")

	sendToSide(uasA, wireRes(t, outP, sip.ResponseStatusNotFound, "p-tag"))
	sendToSide(uasB, wireRes(t, outQ, sip.ResponseStatusRinging, "q-tag"))
	if res := recvWireRes(t, uac, "call-restore"); res.Status != sip.ResponseStatusRinging {
		t.Fatalf("upstream provisional=%v, want 180", res.Status)
	}

	srvKey, err := sip.MakeServerTransactionKey(inv4)
	if err != nil {
		t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
	}
	rq, ok := side.prx.LookupRequest(side.elm, srvKey)
	if !ok {
		t.Fatal("side.prx.LookupRequest(side.elm, ) found no owned request")
	}
	snap, err := rq.Snapshot()
	if err != nil {
		t.Fatalf("rq.Snapshot() error = %v, want nil", err)
	}
	srvTx, ok := side.elm.LoadServerTransaction(srvKey)
	if !ok {
		t.Fatal("upstream transaction not registered")
	}
	srvSnapper, ok := srvTx.(sip.ServerTransactionSnapshotter)
	if !ok {
		t.Fatal("upstream transaction does not implement sip.ServerTransactionSnapshotter")
	}
	srvSnap, err := srvSnapper.Snapshot()
	if err != nil {
		t.Fatalf("srvSnapper.Snapshot() error = %v, want nil", err)
	}
	keyQ, err := sip.MakeClientTransactionKey(outQ)
	if err != nil {
		t.Fatalf("sip.MakeClientTransactionKey() error = %v, want nil", err)
	}
	clnTx, ok := side.elm.LoadClientTransaction(keyQ)
	if !ok {
		t.Fatal("forwarding client transaction of target Q not registered")
	}
	clnSnapper, ok := clnTx.(sip.ClientTransactionSnapshotter)
	if !ok {
		t.Fatal("client transaction of target Q does not implement sip.ClientTransactionSnapshotter")
	}
	clnSnap, err := clnSnapper.Snapshot()
	if err != nil {
		t.Fatalf("clnSnapper.Snapshot() error = %v, want nil", err)
	}

	// The restart side is a fresh element with its own loopback transport
	// and a proxy carrying the same route name in the same mode.
	side2 := newWireProxy(t, proto, []proxy.Route{{
		Name:        "restore",
		ForwardMode: proxy.ForwardModeStateful,
		MatchRequest: func(context.Context, *sip.RequestEnvelope) bool {
			return false
		},
	}}, proxy.Options{})

	srvTx2, err := sip.RestoreServerTransaction(srvSnap, side2.tp)
	if err != nil {
		t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
	}
	clnTx2, err := sip.RestoreClientTransaction(clnSnap, side2.tp)
	if err != nil {
		t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
	}
	// The restored branch transaction must sit in the restarted manager for
	// the wire response to reach it.
	if err := side2.elm.RegisterClientTransaction(ctx, clnTx2); err != nil {
		t.Fatalf("elm.RegisterClientTransaction() error = %v, want nil", err)
	}

	// Earlier scenarios may have left published revisions in the channel:
	// drain them so the dormant check observes only new publications.
	for {
		select {
		case <-snapCh:
		default:
			goto drained
		}
	}
drained:

	rq2, err := side2.prx.RestoreRequest(sip.ContextWithElement(ctx, side2.elm), snap, proxy.RestoreBindings{
		Element:            side2.elm,
		ServerTransaction:  srvTx2,
		ClientTransactions: map[sip.ClientTransactionKey]sip.ClientTransaction{keyQ: clnTx2},
	})
	if err != nil {
		t.Fatalf("prx.RestoreRequest() error = %v, want nil", err)
	}

	// Dormant: no sends, no snapshot revisions, no upstream traffic.
	if !uasB.expectSilent(t, wireSilenceWindow) {
		t.Fatal("uas B observed traffic while dormant, want none")
	}
	select {
	case snap := <-snapCh:
		t.Fatalf("snapshot revision published while dormant: %+v", snap)
	default:
	}

	if err := rq2.Start(ctx); err != nil {
		t.Fatalf("rq.Start() error = %v, want nil", err)
	}

	// The still-live branch answers after activation: the restarted side
	// forwards it upstream through the restored server transaction.
	restoredRes := wireRes(t, outQ, sip.ResponseStatusOK, "q-tag")
	if proto == "TCP" {
		uasB.sendDialedTo(t, restoredRes, side2.addr)
		got := uac.recvAccepted(t)
		res2, ok := got.(*sip.Response)
		if !ok || res2.Status != sip.ResponseStatusOK {
			t.Fatalf("restored upstream message=%v, want 200 response", got)
		}
		if tag := resToTag(t, res2); tag != "q-tag" {
			t.Fatalf("restored response To tag=%q, want %q", tag, "q-tag")
		}
	} else {
		uasB.send(t, restoredRes, side2.addr)
		for {
			res := recvWireRes(t, uac, "call-restore")
			if res.Status == sip.ResponseStatusOK {
				if tag := resToTag(t, res); tag != "q-tag" {
					t.Fatalf("restored response To tag=%q, want %q", tag, "q-tag")
				}
				break
			}
		}
	}

	// --- Lifecycle: a sealed proxy claims nothing further on the wire. ---
	if err := side.prx.Shutdown(context.WithoutCancel(ctx)); err != nil {
		t.Fatalf("side.prx.Shutdown() error = %v, want nil", err)
	}
	handledBefore := side.rcdr.Report().Proxy.RequestsHandled

	late := wireReq(t, proto, sip.RequestMethodInvite, uac.addr, wireReqOpts{
		branch:      sip.MagicCookie + ".uac-sealed",
		callID:      "call-sealed",
		fromTag:     "uac-sealed-tag",
		user:        "nobody",
		maxForwards: header.MaxForwards(70),
	})
	sendToSide(uac, late)
	if res := recvWireRes(t, uac, "call-sealed"); res.Status != sip.ResponseStatusServiceUnavailable {
		t.Fatalf("post-seal response=%v, want 503 (unclaimed pass-through)", res.Status)
	}
	if got := side.rcdr.Report().Proxy.RequestsHandled; got != handledBefore {
		t.Fatalf("post-seal RequestsHandled=%d, want %d", got, handledBefore)
	}

	if err := side2.prx.Shutdown(context.WithoutCancel(ctx)); err != nil {
		t.Fatalf("side2.prx.Shutdown() error = %v, want nil", err)
	}

	// The observation totals close the scenario: every claimed request
	// emitted its handled/completion pair without a rejection, timeout or
	// cancellation besides the one explicit CANCEL.
	final := side.rcdr.Report().Proxy
	if final.RequestsHandled < 7 {
		t.Fatalf("total RequestsHandled=%d, want at least 7", final.RequestsHandled)
	}
	// The cancelled INVITE completes with a forwarded 487 response, so its
	// completion outcome is "response", not "cancelled": RequestsCancelled
	// only counts the generated-487 path.
	if final.RequestsCancelled != 0 {
		t.Fatalf("total RequestsCancelled=%d, want 0", final.RequestsCancelled)
	}
	if final.RequestsRejected != 0 || final.RequestsTimedOut != 0 {
		t.Fatalf("unexpected outcome counters: %+v", final)
	}
	if err := side.elm.Shutdown(context.WithoutCancel(ctx)); err != nil {
		t.Fatalf("side.elm.Shutdown() error = %v, want nil", err)
	}
}
