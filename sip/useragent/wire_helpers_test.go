package useragent_test

import (
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/internal/testutil/siptest"
	"github.com/ghettovoice/gosip/sip"
)

// wireReadTimeout bounds every wire read so a missing response fails fast
// instead of hanging the test until the package timeout.
const wireReadTimeout = 4 * time.Second

// wireElement is an element bound to a real loopback UDP socket.
// addr is the listener address inbound peers must target.
type wireElement struct {
	elm  *sip.Element
	addr netip.AddrPort
}

// newWireElem creates an element serving a tracked connectionless UDP
// transport on a loopback socket via the shared siptest loopback helper.
func newWireElem(tb testing.TB) *wireElement {
	tb.Helper()

	elm, addr := siptest.NewLoopbackUDPElement(tb, sip.UDPMetadata())
	return &wireElement{elm: elm, addr: addr}
}

// newRawPeer binds an unconnected loopback UDP socket that acts as the
// remote SIP peer in wire tests.
func newRawPeer(tb testing.TB) *net.UDPConn {
	tb.Helper()

	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		tb.Fatalf("useragent: listen UDP peer: %v", err)
	}
	tb.Cleanup(func() { peer.Close() })
	return peer
}

// sendReq renders req and writes it as a single datagram to addr.
func sendReq(tb testing.TB, pc net.PacketConn, addr netip.AddrPort, req *sip.RequestEnvelope) {
	tb.Helper()

	buf := []byte(shared.Render(req.Message()))
	if _, err := pc.WriteTo(buf, net.UDPAddrFromAddrPort(addr)); err != nil {
		tb.Fatalf("useragent: send request: %v", err)
	}
}

// readReq reads and parses one request datagram, returning the request
// envelope and the datagram source address. Callers must use the returned
// source for replies because the sender's Via may not carry its address.
func readReq(tb testing.TB, pc net.PacketConn) (*sip.RequestEnvelope, netip.AddrPort) {
	tb.Helper()

	if err := pc.SetReadDeadline(time.Now().Add(wireReadTimeout)); err != nil {
		tb.Fatalf("useragent: set read deadline: %v", err)
	}
	buf := make([]byte, 64*1024)
	num, addr, err := pc.ReadFrom(buf)
	if err != nil {
		tb.Fatalf("useragent: read request: %v", err)
	}
	msg, err := sip.ParseMessage(buf[:num])
	if err != nil {
		tb.Fatalf("useragent: parse request: %v", err)
	}
	req, ok := msg.(*sip.Request)
	if !ok {
		tb.Fatalf("useragent: read %T, want *sip.Request", msg)
	}

	udpAddr, ok := addr.(*net.UDPAddr)
	if !ok {
		tb.Fatalf("useragent: read from %T, want *net.UDPAddr", addr)
	}
	return sip.NewRequestEnvelope(req), udpAddr.AddrPort()
}

// readRes reads and parses one response datagram.
func readRes(tb testing.TB, pc net.PacketConn) *sip.ResponseEnvelope {
	tb.Helper()

	if err := pc.SetReadDeadline(time.Now().Add(wireReadTimeout)); err != nil {
		tb.Fatalf("useragent: set read deadline: %v", err)
	}
	buf := make([]byte, 64*1024)
	num, _, err := pc.ReadFrom(buf)
	if err != nil {
		tb.Fatalf("useragent: read response: %v", err)
	}
	msg, err := sip.ParseMessage(buf[:num])
	if err != nil {
		tb.Fatalf("useragent: parse response: %v", err)
	}
	res, ok := msg.(*sip.Response)
	if !ok {
		tb.Fatalf("useragent: read %T, want *sip.Response", msg)
	}
	return sip.NewResponseEnvelope(res)
}

// wireNoResponseWindow bounds the negative read that proves no further
// response was sent; local datagrams arrive in milliseconds.
const wireNoResponseWindow = 500 * time.Millisecond

// expectNoDatagram fails the test when any datagram arrives within the
// negative-read window.
func expectNoDatagram(tb testing.TB, pc net.PacketConn) {
	tb.Helper()

	if err := pc.SetReadDeadline(time.Now().Add(wireNoResponseWindow)); err != nil {
		tb.Fatalf("useragent: set read deadline: %v", err)
	}
	buf := make([]byte, 64*1024)
	num, _, err := pc.ReadFrom(buf)
	if err == nil {
		tb.Fatalf("useragent: unexpected datagram of %d bytes:\n%s", num, buf[:num])
	}
}

// waitChan fails the test when ch is not closed within a bounded wait.
func waitChan(tb testing.TB, ch <-chan struct{}, name string) {
	tb.Helper()

	select {
	case <-ch:
	case <-time.After(wireReadTimeout):
		tb.Fatalf("useragent: %s not closed within %s", name, wireReadTimeout)
	}
}

// uriFor builds a SIP URI pointing at a loopback address.
func uriFor(addr netip.AddrPort) *sip.URI {
	return &sip.URI{
		User: sip.MakeUserInfo("bob"),
		Addr: sip.MakeHostPortAddr(addr.Addr().String(), addr.Port()),
	}
}
