package netutil

import (
	"context"
	"log/slog"
	"net"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
)

type logListener struct {
	net.Listener
	ctx context.Context
	log *slog.Logger
	lvl slog.Level
}

func NewLogListener(ctx context.Context, ls net.Listener, logger *slog.Logger, lvl slog.Level) net.Listener {
	if _, ok := AsListener[*logListener](ls); ok {
		return ls
	}

	return &logListener{
		Listener: ls,
		ctx:      ctx,
		log:      logger.With(slog.Any("listener", ls)),
		lvl:      lvl,
	}
}

func (ls *logListener) Accept() (net.Conn, error) {
	conn, err := ls.Listener.Accept()
	if err != nil {
		return nil, errors.Wrap(err)
	}

	ls.log.LogAttrs(ls.ctx, ls.lvl, "connection accepted", slog.Any("connection", conn))

	return conn, nil
}

func (ls *logListener) Close() error {
	if ls == nil {
		return nil
	}

	if err := ls.Listener.Close(); err != nil {
		ls.log.LogAttrs(ls.ctx, ls.lvl, "listener closed with error", slog.Any("error", err))
		return errors.Wrap(err)
	}

	ls.log.LogAttrs(ls.ctx, ls.lvl, "listener closed")

	return nil
}

func (l *logListener) Unwrap() net.Listener {
	return l.Listener
}

type logConn struct {
	net.Conn
	ctx context.Context
	log *slog.Logger
	lvl slog.Level
}

func NewLogConn(ctx context.Context, conn net.Conn, logger *slog.Logger, lvl slog.Level) net.Conn {
	if c, ok := AsConn[*logConn](conn); ok {
		return c
	}

	return &logConn{
		Conn: conn,
		ctx:  ctx,
		log:  logger.With(slog.Any("connection", conn)),
		lvl:  lvl,
	}
}

func (c *logConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if err != nil {
		return n, errors.Wrap(err)
	}

	c.log.LogAttrs(c.ctx, c.lvl, "connection read buffer",
		slog.String("local_addr", c.LocalAddr().String()),
		slog.String("remote_addr", c.RemoteAddr().String()),
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
		return n, errors.Wrap(err)
	}

	c.log.LogAttrs(c.ctx, c.lvl, "connection wrote buffer",
		slog.String("local_addr", c.LocalAddr().String()),
		slog.String("remote_addr", c.RemoteAddr().String()),
		slog.Group("buffer",
			slog.Int("size", n),
			slog.String("data", util.Ellipsis(string(b[:n]), 1000)),
		),
	)

	return n, nil
}

func (c *logConn) Close() error {
	if c == nil {
		return nil
	}

	if err := c.Conn.Close(); err != nil {
		c.log.LogAttrs(c.ctx, c.lvl, "connection closed with error", slog.Any("error", err))
		return errors.Wrap(err)
	}

	c.log.LogAttrs(c.ctx, c.lvl, "connection closed")

	return nil
}

func (c *logConn) SetDeadline(t time.Time) error {
	if err := c.Conn.SetDeadline(t); err != nil {
		return errors.Wrap(err)
	}

	c.log.LogAttrs(c.ctx, c.lvl, "connection set deadline", slog.Time("deadline", t))

	return nil
}

func (c *logConn) SetReadDeadline(t time.Time) error {
	if err := c.Conn.SetReadDeadline(t); err != nil {
		return errors.Wrap(err)
	}

	c.log.LogAttrs(c.ctx, c.lvl, "connection set read deadline", slog.Time("deadline", t))

	return nil
}

func (c *logConn) SetWriteDeadline(t time.Time) error {
	if err := c.Conn.SetWriteDeadline(t); err != nil {
		return errors.Wrap(err)
	}

	c.log.LogAttrs(c.ctx, c.lvl, "connection set write deadline", slog.Time("deadline", t))

	return nil
}

func (c *logConn) Unwrap() net.Conn {
	return c.Conn
}

type logPacketConn struct {
	net.PacketConn
	ctx context.Context
	log *slog.Logger
	lvl slog.Level
}

func NewLogPacketConn(ctx context.Context, conn net.PacketConn, logger *slog.Logger, lvl slog.Level) net.PacketConn {
	if _, ok := AsPacketConn[*logPacketConn](conn); ok {
		return conn
	}

	return &logPacketConn{
		PacketConn: conn,
		ctx:        ctx,
		log:        logger.With(slog.Any("connection", conn)),
		lvl:        lvl,
	}
}

func (c *logPacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
	n, addr, err := c.PacketConn.ReadFrom(b)
	if err != nil {
		return n, addr, errors.Wrap(err)
	}

	c.log.LogAttrs(c.ctx, c.lvl, "connection read buffer",
		slog.String("local_addr", c.LocalAddr().String()),
		slog.String("remote_addr", addr.String()),
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
		return n, errors.Wrap(err)
	}

	c.log.LogAttrs(c.ctx, c.lvl, "connection wrote buffer",
		slog.String("local_addr", c.LocalAddr().String()),
		slog.String("remote_addr", addr.String()),
		slog.Group("buffer",
			slog.Int("size", n),
			slog.String("data", util.Ellipsis(string(b[:n]), 1000)),
		),
	)

	return n, nil
}

func (c *logPacketConn) Close() error {
	if c == nil {
		return nil
	}

	if err := c.PacketConn.Close(); err != nil {
		c.log.LogAttrs(c.ctx, c.lvl, "connection closed with error", slog.Any("error", err))
		return errors.Wrap(err)
	}

	c.log.LogAttrs(c.ctx, c.lvl, "connection closed")

	return nil
}

func (c *logPacketConn) SetDeadline(t time.Time) error {
	if err := c.PacketConn.SetDeadline(t); err != nil {
		return errors.Wrap(err)
	}

	c.log.LogAttrs(c.ctx, c.lvl, "connection set deadline", slog.Time("deadline", t))

	return nil
}

func (c *logPacketConn) SetReadDeadline(t time.Time) error {
	if err := c.PacketConn.SetReadDeadline(t); err != nil {
		return errors.Wrap(err)
	}

	c.log.LogAttrs(c.ctx, c.lvl, "connection set read deadline", slog.Time("deadline", t))

	return nil
}

func (c *logPacketConn) SetWriteDeadline(t time.Time) error {
	if err := c.PacketConn.SetWriteDeadline(t); err != nil {
		return errors.Wrap(err)
	}

	c.log.LogAttrs(c.ctx, c.lvl, "connection set write deadline", slog.Time("deadline", t))

	return nil
}

func (c *logPacketConn) Unwrap() net.PacketConn {
	return c.PacketConn
}
