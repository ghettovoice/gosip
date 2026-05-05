package netutil

import (
	"context"
	"log/slog"
	"net"
	"slices"
	"time"
)

// ListenerDecorator decorates [net.Listener].
type ListenerDecorator = func(context.Context, net.Listener) net.Listener

// WrapListener composes decorators into a single decorator.
//
// Decorators are applied in reverse order, so the first decorator in the list
// will be the outermost wrapper.
func WrapListener(ds ...ListenerDecorator) ListenerDecorator {
	return func(ctx context.Context, ls net.Listener) net.Listener {
		for _, d := range slices.Backward(ds) {
			if d != nil {
				ls = d(ctx, ls)
			}
		}

		return ls
	}
}

func NewLogListenerDecorator(logger *slog.Logger, level slog.Level) ListenerDecorator {
	return func(ctx context.Context, ls net.Listener) net.Listener {
		return NewLogListener(ctx, ls, logger, level)
	}
}

func NewCloseOnceListenerDecorator() ListenerDecorator {
	return func(ctx context.Context, ls net.Listener) net.Listener {
		return NewCloseOnceListener(ls)
	}
}

// ConnDecorator decorates [net.Conn].
type ConnDecorator = func(context.Context, net.Conn) net.Conn

// WrapConn composes decorators into a single decorator.
//
// Decorators are applied in reverse order, so the first decorator in the list
// will be the outermost wrapper.
// For example: auto-close -> close-once -> log.
func WrapConn(ds ...ConnDecorator) ConnDecorator {
	return func(ctx context.Context, conn net.Conn) net.Conn {
		for _, d := range slices.Backward(ds) {
			if d != nil {
				conn = d(ctx, conn)
			}
		}

		return conn
	}
}

func NewLogConnDecorator(logger *slog.Logger, level slog.Level) ConnDecorator {
	return func(ctx context.Context, conn net.Conn) net.Conn {
		return NewLogConn(ctx, conn, logger, level)
	}
}

func NewCloseOnceConnDecorator() ConnDecorator {
	return func(ctx context.Context, conn net.Conn) net.Conn {
		return NewCloseOnceConn(conn)
	}
}

func NewAutoCloseConnDecorator(ttl time.Duration) ConnDecorator {
	return func(ctx context.Context, conn net.Conn) net.Conn {
		return NewAutoCloseConn(conn, ttl)
	}
}

func NewContextConnDecorator() ConnDecorator {
	return func(ctx context.Context, conn net.Conn) net.Conn {
		return NewContextConn(conn)
	}
}

// PacketConnDecorator decorates [net.PacketConn].
type PacketConnDecorator = func(context.Context, net.PacketConn) net.PacketConn

// WrapPacketConn composes decorators into a single decorator.
//
// Decorators are applied in reverse order, so the first decorator in the list
// will be the outermost wrapper.
// For example: close-once -> log.
func WrapPacketConn(ds ...PacketConnDecorator) PacketConnDecorator {
	return func(ctx context.Context, conn net.PacketConn) net.PacketConn {
		for _, d := range slices.Backward(ds) {
			if d != nil {
				conn = d(ctx, conn)
			}
		}

		return conn
	}
}

func NewLogPacketConnDecorator(logger *slog.Logger, level slog.Level) PacketConnDecorator {
	return func(ctx context.Context, conn net.PacketConn) net.PacketConn {
		return NewLogPacketConn(ctx, conn, logger, level)
	}
}

func NewCloseOncePacketConnDecorator() PacketConnDecorator {
	return func(ctx context.Context, conn net.PacketConn) net.PacketConn {
		return NewCloseOncePacketConn(conn)
	}
}

func NewAutoClosePacketConnDecorator(ttl time.Duration) PacketConnDecorator {
	return func(ctx context.Context, conn net.PacketConn) net.PacketConn {
		return NewAutoClosePacketConn(conn, ttl)
	}
}

func NewContextPacketConnDecorator() PacketConnDecorator {
	return func(ctx context.Context, conn net.PacketConn) net.PacketConn {
		return NewContextPacketConn(conn)
	}
}
