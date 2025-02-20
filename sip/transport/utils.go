package transport

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"syscall"

	"github.com/ghettovoice/gosip/internal/log"
	"github.com/ghettovoice/gosip/sip"
)

var bytesBufPool = &sync.Pool{
	New: func() any { return bytes.NewBuffer(make([]byte, 0, 1024)) },
}

func newBytesBuf() *bytes.Buffer {
	return bytesBufPool.Get().(*bytes.Buffer) //nolint:forcetypeassert
}

func freeBytesBuf(b *bytes.Buffer) {
	b.Reset()
	if b.Cap() > sip.MaxMsgSize {
		return
	}
	bytesBufPool.Put(b)
}

func addMsgMdFields(msg sip.Message, keyvals ...any) {
	md := sip.GetMessageMetadata(msg)
	if md == nil {
		md = make(sip.MessageMetadata)
	}
	for i := 0; i < len(keyvals); i += 2 {
		md[fmt.Sprint(keyvals[i])] = keyvals[i+1]
	}
	sip.SetMessageMetadata(msg, md)
}

type closeOnceListener struct {
	net.Listener
	closeOnce sync.Once
	closeErr  error
}

func newCloseOnceListener(ls net.Listener) net.Listener {
	if _, ok := ls.(*closeOnceListener); ok {
		return ls
	}
	return &closeOnceListener{Listener: ls}
}

func (l *closeOnceListener) Close() error {
	l.closeOnce.Do(func() {
		l.closeErr = l.Listener.Close()
	})
	return l.closeErr
}

type closeOncePacketConn struct {
	net.PacketConn
	closeOnce sync.Once
	closeErr  error
}

func newCloseOncePacketConn(c net.PacketConn) net.PacketConn {
	if _, ok := c.(*closeOncePacketConn); ok {
		return c
	}
	return &closeOncePacketConn{PacketConn: c}
}

func (c *closeOncePacketConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.PacketConn.Close()
	})
	return c.closeErr
}

type closeOnceConn struct {
	net.Conn
	closeOnce sync.Once
	closeErr  error
}

func newCloseOnceConn(c net.Conn) net.Conn {
	if _, ok := c.(*closeOnceConn); ok {
		return c
	}
	return &closeOnceConn{Conn: c}
}

func (c *closeOnceConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.Conn.Close()
	})
	return c.closeErr
}

type logPacketConn struct {
	net.PacketConn
	log *slog.Logger
}

func newLogPacketConn(c net.PacketConn, l *slog.Logger) net.PacketConn {
	if _, ok := c.(*logPacketConn); ok {
		return c
	}
	return &logPacketConn{c, l.With("connection", c)}
}

func (c *logPacketConn) WriteTo(b []byte, addr net.Addr) (n int, err error) {
	n, err = c.PacketConn.WriteTo(b, addr)
	if err != nil {
		return n, err
	}
	c.log.Debug("wrote buffer",
		slog.Any("remote_addr", addr),
		slog.Group("buffer",
			slog.Int("size", n),
			slog.Any("data", log.StringValue(b[:n])),
		),
	)
	return n, nil
}

func (c *logPacketConn) ReadFrom(b []byte) (n int, addr net.Addr, err error) {
	n, addr, err = c.PacketConn.ReadFrom(b)
	if err != nil {
		return n, addr, err
	}
	c.log.Debug("read buffer",
		slog.Any("remote_addr", addr),
		slog.Group("buffer",
			slog.Int("size", n),
			slog.Any("data", log.StringValue(b[:n])),
		),
	)
	return n, addr, nil
}

type logConn struct {
	net.Conn
	log *slog.Logger
}

func newLogConn(c net.Conn, l *slog.Logger) net.Conn {
	if _, ok := c.(*logConn); ok {
		return c
	}
	return &logConn{c, l.With("connection", c)}
}

func (c *logConn) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	if err != nil {
		return n, err
	}
	c.log.Debug("wrote buffer",
		slog.Group("buffer",
			slog.Int("size", n),
			slog.Any("data", log.StringValue(b[:n])),
		),
	)
	return n, nil
}

func (c *logConn) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	if err != nil {
		return n, err
	}
	c.log.Debug("read buffer",
		slog.Group("buffer",
			slog.Int("size", n),
			slog.Any("data", log.StringValue(b[:n])),
		),
	)
	return n, nil
}

func isNetError(err error) bool {
	var opErr *net.OpError
	return errors.Is(err, syscall.EINVAL) || errors.As(err, &opErr)
}
