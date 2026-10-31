package netutil

import (
	"log/slog"
	"net"
	"time"
)

// ListenerDecorator decorates [net.Listener].
type ListenerDecorator = func(net.Listener) net.Listener

// WrapListener composes decorators into a single decorator.
//
// Decorators are applied in direct order, so the first decorator in the list
// will be the innermost wrapper.
func WrapListener(ds ...ListenerDecorator) ListenerDecorator {
	return func(ls net.Listener) net.Listener {
		for _, d := range ds {
			if d != nil {
				ls = d(ls)
			}
		}
		return ls
	}
}

func NewLogListenerDecorator(logger *slog.Logger, level slog.Level) ListenerDecorator {
	return func(ls net.Listener) net.Listener {
		return NewLogListener(ls, logger, level)
	}
}

func NewCloseOnceListenerDecorator() ListenerDecorator {
	return NewCloseOnceListener
}

// ConnDecorator decorates [net.Conn].
type ConnDecorator = func(net.Conn) net.Conn

// WrapConn composes decorators into a single decorator.
//
// Decorators are applied in direct order, so the first decorator in the list
// will be the innermost wrapper.
// For example: log -> close-once -> auto-close.
func WrapConn(ds ...ConnDecorator) ConnDecorator {
	return func(conn net.Conn) net.Conn {
		for _, d := range ds {
			if d != nil {
				conn = d(conn)
			}
		}
		return conn
	}
}

func NewLogConnDecorator(logger *slog.Logger, level slog.Level) ConnDecorator {
	return func(conn net.Conn) net.Conn {
		return NewLogConn(conn, logger, level)
	}
}

func NewCloseOnceConnDecorator() ConnDecorator {
	return NewCloseOnceConn
}

func NewAutoCloseConnDecorator(ttl time.Duration) ConnDecorator {
	return func(conn net.Conn) net.Conn {
		return NewAutoCloseConn(conn, ttl)
	}
}

func NewContextConnDecorator() ConnDecorator {
	return NewContextConn
}

// PacketConnDecorator decorates [net.PacketConn].
type PacketConnDecorator = func(net.PacketConn) net.PacketConn

// WrapPacketConn composes decorators into a single decorator.
//
// Decorators are applied in direct order, so the first decorator in the list
// will be the innermost wrapper.
// For example: log -> close-once.
func WrapPacketConn(ds ...PacketConnDecorator) PacketConnDecorator {
	return func(conn net.PacketConn) net.PacketConn {
		for _, d := range ds {
			if d != nil {
				conn = d(conn)
			}
		}
		return conn
	}
}

func NewLogPacketConnDecorator(logger *slog.Logger, level slog.Level) PacketConnDecorator {
	return func(conn net.PacketConn) net.PacketConn {
		return NewLogPacketConn(conn, logger, level)
	}
}

func NewCloseOncePacketConnDecorator() PacketConnDecorator {
	return NewCloseOncePacketConn
}

func NewAutoClosePacketConnDecorator(ttl time.Duration) PacketConnDecorator {
	return func(conn net.PacketConn) net.PacketConn {
		return NewAutoClosePacketConn(conn, ttl)
	}
}

func NewContextPacketConnDecorator() PacketConnDecorator {
	return NewContextPacketConn
}
