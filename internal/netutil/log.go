package netutil

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

type logLis struct {
	net.Listener
	log *slog.Logger
	lvl slog.Level
}

func NewLogListener(ls net.Listener, logger *slog.Logger, lvl slog.Level) net.Listener {
	if _, ok := AsListener[*logLis](ls); ok {
		return ls
	}

	return &logLis{
		Listener: ls,
		log:      logger.With(slog.Any("listener", ls)),
		lvl:      lvl,
	}
}

func (l *logLis) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, errors.Wrap(err)
	}

	l.log.LogAttrs(context.Background(), l.lvl, "connection accepted", slog.Any("connection", conn))

	return conn, nil
}

func (l *logLis) Close() error {
	if err := l.Listener.Close(); err != nil {
		return errors.Wrap(err)
	}

	l.log.LogAttrs(context.Background(), l.lvl, "listener closed")

	return nil
}

func (l *logLis) Unwrap() net.Listener {
	if l == nil {
		return nil
	}
	return l.Listener
}

type logConn struct {
	net.Conn
	log *slog.Logger
	lvl slog.Level
}

func NewLogConn(conn net.Conn, logger *slog.Logger, lvl slog.Level) net.Conn {
	if c, ok := AsConn[*logConn](conn); ok {
		return c
	}

	return &logConn{
		Conn: conn,
		log:  logger.With(slog.Any("connection", conn)),
		lvl:  lvl,
	}
}

func (c *logConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if err != nil {
		return n, errors.Wrap(err)
	}

	c.log.LogAttrs(
		context.Background(), c.lvl, "connection read buffer",
		slog.String("local_addr", c.LocalAddr().String()),
		slog.String("remote_addr", c.RemoteAddr().String()),
		slog.Group(
			"buffer",
			slog.Int("size", n),
			slog.String("data", util.Ellipsis(string(b[:n]), 1000)),
		),
	)

	return n, nil
}

func (c *logConn) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	if err != nil {
		return n, errors.Wrap(err)
	}

	c.log.LogAttrs(
		context.Background(), c.lvl, "connection wrote buffer",
		slog.String("local_addr", c.LocalAddr().String()),
		slog.String("remote_addr", c.RemoteAddr().String()),
		slog.Group(
			"buffer",
			slog.Int("size", n),
			slog.String("data", util.Ellipsis(string(b[:n]), 1000)),
		),
	)

	return n, nil
}

func (c *logConn) Close() error {
	if err := c.Conn.Close(); err != nil {
		return errors.Wrap(err)
	}

	c.log.LogAttrs(context.Background(), c.lvl, "connection closed")

	return nil
}

func (c *logConn) SetDeadline(t time.Time) error {
	if err := c.Conn.SetDeadline(t); err != nil {
		return errors.Wrap(err)
	}

	c.log.LogAttrs(context.Background(), c.lvl, "connection set deadline", slog.Time("deadline", t))

	return nil
}

func (c *logConn) SetReadDeadline(t time.Time) error {
	if err := c.Conn.SetReadDeadline(t); err != nil {
		return errors.Wrap(err)
	}

	c.log.LogAttrs(context.Background(), c.lvl, "connection set read deadline", slog.Time("deadline", t))

	return nil
}

func (c *logConn) SetWriteDeadline(t time.Time) error {
	if err := c.Conn.SetWriteDeadline(t); err != nil {
		return errors.Wrap(err)
	}

	c.log.LogAttrs(context.Background(), c.lvl, "connection set write deadline", slog.Time("deadline", t))

	return nil
}

func (c *logConn) Unwrap() net.Conn {
	if c == nil {
		return nil
	}
	return c.Conn
}

type logPktConn struct {
	net.PacketConn
	log *slog.Logger
	lvl slog.Level
}

func NewLogPacketConn(conn net.PacketConn, logger *slog.Logger, lvl slog.Level) net.PacketConn {
	if _, ok := AsPacketConn[*logPktConn](conn); ok {
		return conn
	}

	return &logPktConn{
		PacketConn: conn,
		log:        logger.With(slog.Any("connection", conn)),
		lvl:        lvl,
	}
}

func (c *logPktConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := c.PacketConn.ReadFrom(b)
	if err != nil {
		return n, addr, errors.Wrap(err)
	}

	c.log.LogAttrs(
		context.Background(), c.lvl, "connection read buffer",
		slog.String("local_addr", c.LocalAddr().String()),
		slog.String("remote_addr", addr.String()),
		slog.Group(
			"buffer",
			slog.Int("size", n),
			slog.String("data", util.Ellipsis(string(b[:n]), 1000)),
		),
	)

	return n, addr, nil
}

func (c *logPktConn) WriteTo(b []byte, addr net.Addr) (int, error) {
	n, err := c.PacketConn.WriteTo(b, addr)
	if err != nil {
		return n, errors.Wrap(err)
	}

	c.log.LogAttrs(
		context.Background(), c.lvl, "connection wrote buffer",
		slog.String("local_addr", c.LocalAddr().String()),
		slog.String("remote_addr", addr.String()),
		slog.Group(
			"buffer",
			slog.Int("size", n),
			slog.String("data", util.Ellipsis(string(b[:n]), 1000)),
		),
	)

	return n, nil
}

func (c *logPktConn) Close() error {
	if err := c.PacketConn.Close(); err != nil {
		return errors.Wrap(err)
	}

	c.log.LogAttrs(context.Background(), c.lvl, "connection closed")

	return nil
}

func (c *logPktConn) SetDeadline(t time.Time) error {
	if err := c.PacketConn.SetDeadline(t); err != nil {
		return errors.Wrap(err)
	}

	c.log.LogAttrs(context.Background(), c.lvl, "connection set deadline", slog.Time("deadline", t))

	return nil
}

func (c *logPktConn) SetReadDeadline(t time.Time) error {
	if err := c.PacketConn.SetReadDeadline(t); err != nil {
		return errors.Wrap(err)
	}

	c.log.LogAttrs(context.Background(), c.lvl, "connection set read deadline", slog.Time("deadline", t))

	return nil
}

func (c *logPktConn) SetWriteDeadline(t time.Time) error {
	if err := c.PacketConn.SetWriteDeadline(t); err != nil {
		return errors.Wrap(err)
	}

	c.log.LogAttrs(context.Background(), c.lvl, "connection set write deadline", slog.Time("deadline", t))

	return nil
}

func (c *logPktConn) Unwrap() net.PacketConn {
	if c == nil {
		return nil
	}
	return c.PacketConn
}
