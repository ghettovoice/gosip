package sip

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/util"
)

type closeOnceListener struct {
	net.Listener
	closeOnce sync.Once
	closeErr  error
}

func newCloseOnceListener(ls net.Listener) *closeOnceListener {
	if ls, ok := ls.(*closeOnceListener); ok {
		return ls
	}
	return &closeOnceListener{Listener: ls}
}

func (l *closeOnceListener) Close() error {
	l.closeOnce.Do(func() {
		if err := l.Listener.Close(); err != nil {
			l.closeErr = err
		}
	})
	return errtrace.Wrap(l.closeErr)
}

type closeOncePacketConn struct {
	net.PacketConn
	closeOnce sync.Once
	closeErr  error
}

func newCloseOncePacketConn(c net.PacketConn) *closeOncePacketConn {
	if c, ok := c.(*closeOncePacketConn); ok {
		return c
	}
	return &closeOncePacketConn{PacketConn: c}
}

func (c *closeOncePacketConn) Close() error {
	c.closeOnce.Do(func() {
		if err := c.PacketConn.Close(); err != nil {
			c.closeErr = err
		}
	})
	return errtrace.Wrap(c.closeErr)
}

type closeOnceConn struct {
	net.Conn
	closeOnce sync.Once
	closeErr  error
}

func newCloseOnceConn(c net.Conn) *closeOnceConn {
	if c, ok := c.(*closeOnceConn); ok {
		return c
	}
	return &closeOnceConn{Conn: c}
}

func (c *closeOnceConn) Close() error {
	c.closeOnce.Do(func() {
		if err := c.Conn.Close(); err != nil {
			c.closeErr = err
		}
	})
	return errtrace.Wrap(c.closeErr)
}

type autoCloseConn struct {
	net.Conn
	ttl time.Duration
	tmr atomic.Pointer[time.Timer]
}

func newAutoCloseConn(conn net.Conn, ttl time.Duration) *autoCloseConn {
	if c, ok := conn.(*autoCloseConn); ok {
		return c
	}
	c := &autoCloseConn{
		Conn: conn,
		ttl:  ttl,
	}
	c.resetTmr()
	return c
}

func (c *autoCloseConn) resetTmr() {
	if c.ttl <= 0 {
		return
	}
	if tmr := c.tmr.Load(); tmr == nil {
		c.tmr.Store(time.AfterFunc(c.ttl, func() { c.Close() }))
	} else if !tmr.Reset(c.ttl) {
		// timer was already expired
		tmr.Stop()
	}
}

func (c *autoCloseConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if err != nil {
		return n, errtrace.Wrap(err)
	}
	c.resetTmr()
	return n, nil
}

func (c *autoCloseConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if err != nil {
		return n, errtrace.Wrap(err)
	}
	c.resetTmr()
	return n, nil
}

func (c *autoCloseConn) Close() error {
	if tmr := c.tmr.Swap(nil); tmr != nil {
		tmr.Stop()
	}
	return errtrace.Wrap(c.Conn.Close())
}

type logListener struct {
	net.Listener
	log *slog.Logger
}

func newLogListener(ls net.Listener, log *slog.Logger) *logListener {
	if ls, ok := ls.(*logListener); ok {
		return ls
	}
	return &logListener{Listener: ls, log: log.With("listener", ls)}
}

func (ls *logListener) Accept() (net.Conn, error) {
	conn, err := ls.Listener.Accept()
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	ls.log.LogAttrs(context.Background(), slog.LevelDebug, "connection accepted", slog.Any("connection", conn))
	return conn, nil
}

func (ls *logListener) Close() error {
	if err := ls.Listener.Close(); err != nil {
		ls.log.LogAttrs(context.Background(), slog.LevelDebug, "listener closed with error", slog.Any("error", err))
		return errtrace.Wrap(err)
	}
	ls.log.LogAttrs(context.Background(), slog.LevelDebug, "listener closed")
	return nil
}

type logConn struct {
	net.Conn
	log *slog.Logger
}

func newLogConn(c net.Conn, log *slog.Logger) *logConn {
	if c, ok := c.(*logConn); ok {
		return c
	}
	return &logConn{Conn: c, log: log.With("connection", c)}
}

func (c *logConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if err != nil {
		return n, errtrace.Wrap(err)
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug,
		fmt.Sprintf("connection read buffer %s -> %s", c.RemoteAddr(), c.LocalAddr()),
		slog.Group("buffer",
			slog.Int("size", n),
			slog.String("data", util.Ellipsis(string(b[:n]), 1000)),
		),
	)
	return n, nil
}

func (c *logConn) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	if err != nil {
		return n, errtrace.Wrap(err)
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug,
		fmt.Sprintf("connection wrote buffer %s -> %s", c.LocalAddr(), c.RemoteAddr()),
		slog.Group("buffer",
			slog.Int("size", n),
			slog.String("data", util.Ellipsis(string(b[:n]), 1000)),
		),
	)
	return n, nil
}

func (c *logConn) Close() error {
	if err := c.Conn.Close(); err != nil {
		c.log.LogAttrs(context.Background(), slog.LevelDebug, "connection closed with error", slog.Any("error", err))
		return errtrace.Wrap(err)
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "connection closed")
	return nil
}

func (c *logConn) SetDeadline(t time.Time) error {
	if err := c.Conn.SetDeadline(t); err != nil {
		return errtrace.Wrap(err)
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "connection set deadline", slog.Time("deadline", t))
	return nil
}

func (c *logConn) SetReadDeadline(t time.Time) error {
	if err := c.Conn.SetReadDeadline(t); err != nil {
		return errtrace.Wrap(err)
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "connection set read deadline", slog.Time("deadline", t))
	return nil
}

func (c *logConn) SetWriteDeadline(t time.Time) error {
	if err := c.Conn.SetWriteDeadline(t); err != nil {
		return errtrace.Wrap(err)
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "connection set write deadline", slog.Time("deadline", t))
	return nil
}

type logPacketConn struct {
	net.PacketConn
	log *slog.Logger
}

func newLogPacketConn(c net.PacketConn, log *slog.Logger) *logPacketConn {
	if c, ok := c.(*logPacketConn); ok {
		return c
	}
	return &logPacketConn{PacketConn: c, log: log.With("connection", c)}
}

func (c *logPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := c.PacketConn.ReadFrom(b)
	if err != nil {
		return n, addr, errtrace.Wrap(err)
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug,
		fmt.Sprintf("connection read buffer %s -> %s", addr, c.LocalAddr()),
		slog.Group("buffer",
			slog.Int("size", n),
			slog.String("data", util.Ellipsis(string(b[:n]), 1000)),
		),
	)
	return n, addr, nil
}

func (c *logPacketConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	n, err := c.PacketConn.WriteTo(b, addr)
	if err != nil {
		return n, errtrace.Wrap(err)
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug,
		fmt.Sprintf("connection wrote buffer %s -> %s", c.LocalAddr(), addr),
		slog.Group("buffer",
			slog.Int("size", n),
			slog.String("data", util.Ellipsis(string(b[:n]), 1000)),
		),
	)
	return n, nil
}

func (c *logPacketConn) Close() error {
	if err := c.PacketConn.Close(); err != nil {
		c.log.LogAttrs(context.Background(), slog.LevelDebug, "connection closed with error", slog.Any("error", err))
		return errtrace.Wrap(err)
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "connection closed")
	return nil
}

func (c *logPacketConn) SetDeadline(t time.Time) error {
	if err := c.PacketConn.SetDeadline(t); err != nil {
		return errtrace.Wrap(err)
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "connection set deadline", slog.Time("deadline", t))
	return nil
}

func (c *logPacketConn) SetReadDeadline(t time.Time) error {
	if err := c.PacketConn.SetReadDeadline(t); err != nil {
		return errtrace.Wrap(err)
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "connection set read deadline", slog.Time("deadline", t))
	return nil
}

func (c *logPacketConn) SetWriteDeadline(t time.Time) error {
	if err := c.PacketConn.SetWriteDeadline(t); err != nil {
		return errtrace.Wrap(err)
	}
	c.log.LogAttrs(context.Background(), slog.LevelDebug, "connection set write deadline", slog.Time("deadline", t))
	return nil
}

type packetConn struct {
	net.Conn
}

func (c *packetConn) ReadFrom(b []byte) (int, net.Addr, error) {
	switch conn := c.Conn.(type) {
	case net.PacketConn:
		return errtrace.Wrap3(conn.ReadFrom(b))
	default:
		n, err := conn.Read(b)
		return n, conn.RemoteAddr(), errtrace.Wrap(err)
	}
}

func (c *packetConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	switch conn := c.Conn.(type) {
	case net.PacketConn:
		return errtrace.Wrap2(conn.WriteTo(b, addr))
	default:
		n, err := conn.Write(b)
		return n, errtrace.Wrap(err)
	}
}

func addrPortToNetAddr(network string, addrPort netip.AddrPort) net.Addr {
	switch strings.ToLower(network) {
	case "udp", "udp4", "udp6":
		return &net.UDPAddr{
			IP:   addrPort.Addr().AsSlice(),
			Port: int(addrPort.Port()),
			Zone: addrPort.Addr().Zone(),
		}
	case "tcp", "tcp4", "tcp6":
		return &net.TCPAddr{
			IP:   addrPort.Addr().AsSlice(),
			Port: int(addrPort.Port()),
			Zone: addrPort.Addr().Zone(),
		}
	case "ip", "ip4", "ip6":
		return &net.IPAddr{
			IP:   addrPort.Addr().AsSlice(),
			Zone: addrPort.Addr().Zone(),
		}
	case "unix", "unixgram", "unixpacket":
		// For Unix domain sockets, we can't meaningfully convert from AddrPort
		panic(errorutil.Errorf("unexpected network %q", network))
	default:
		// For unknown networks, return a basic implementation
		return &netAddr{
			network: network,
			addr:    addrPort.String(),
		}
	}
}

type readDeadlinePacketConn struct {
	net.PacketConn
	readTimeout time.Duration
}

func (c *readDeadlinePacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	if c.readTimeout > 0 {
		if err := c.PacketConn.SetReadDeadline(time.Now().Add(c.readTimeout)); err != nil {
			return 0, nil, errtrace.Wrap(err)
		}
		defer c.PacketConn.SetReadDeadline(zeroTime)
	}
	return errtrace.Wrap3(c.PacketConn.ReadFrom(b))
}

type readDeadlineConn struct {
	net.Conn
	readTimeout time.Duration
}

func (c *readDeadlineConn) Read(b []byte) (int, error) {
	if c.readTimeout > 0 {
		if err := c.Conn.SetReadDeadline(time.Now().Add(c.readTimeout)); err != nil {
			return 0, errtrace.Wrap(err)
		}
		defer c.Conn.SetReadDeadline(zeroTime)
	}
	return errtrace.Wrap2(c.Conn.Read(b))
}

type netAddr struct {
	network string
	addr    string
}

func (a *netAddr) Network() string { return a.network }

func (a *netAddr) String() string { return a.addr }

// NetConnDialer is a connection dialer based on [net.Dialer].
type NetConnDialer struct {
	net.Dialer
}

// DialConn dials a connection to the specified remote address.
func (d *NetConnDialer) DialConn(ctx context.Context, network string, raddr netip.AddrPort) (net.Conn, error) {
	return errtrace.Wrap2(d.DialContext(ctx, network, raddr.String()))
}

var defConnDialer = &NetConnDialer{}

// DefaultConnDialer returns the default connection dialer.
func DefaultConnDialer() *NetConnDialer { return defConnDialer }
