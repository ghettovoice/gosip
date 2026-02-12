// Package log provides preconfigured loggers and utilities.
package log

//go:generate errtrace -w .

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sync/atomic"
	"time"

	"github.com/golang-cz/devslog"
	conslog "github.com/phsym/console-slog"
	slogfmt "github.com/samber/slog-formatter"

	"github.com/ghettovoice/gosip/internal/types"
)

var newHandler = slogfmt.NewFormatterHandler(
	slogfmt.ErrorFormatter("error"),
	slogfmt.FormatByType(func(ls net.Listener) slog.Value {
		return slog.GroupValue(
			slog.String("type", fmt.Sprintf("%T", ls)),
			slog.Any("local_addr", ls.Addr()),
		)
	}),
	slogfmt.FormatByType(func(c net.PacketConn) slog.Value {
		return slog.GroupValue(
			slog.String("type", fmt.Sprintf("%T", c)),
			slog.Any("local_addr", c.LocalAddr()),
		)
	}),
	slogfmt.FormatByType(func(c net.Conn) slog.Value {
		return slog.GroupValue(
			slog.String("type", fmt.Sprintf("%T", c)),
			slog.Any("local_addr", c.LocalAddr()),
			slog.Any("remote_addr", c.RemoteAddr()),
		)
	}),
)

var console = slog.New(newHandler(
	conslog.NewHandler(os.Stdout, &conslog.HandlerOptions{
		AddSource:  true,
		Level:      slog.LevelDebug,
		TimeFormat: time.RFC3339Nano,
	}),
))

// Console returns the logger configured for console output.
func Console() *slog.Logger { return console }

var develop = slog.New(newHandler(
	devslog.NewHandler(os.Stdout, &devslog.Options{
		HandlerOptions: &slog.HandlerOptions{
			AddSource: true,
			Level:     slog.LevelDebug,
		},
		SortKeys:   true,
		TimeFormat: time.RFC3339Nano,
	}),
))

// Develop returns the logger configured for extended output useful during development.
func Develop() *slog.Logger { return develop }

var noop = slog.New(noopHandler{})

// Noop returns no-op logger that write nothing.
func Noop() *slog.Logger { return noop }

var _default atomic.Pointer[slog.Logger]

// Default returns the default logger.
// From the start it is set to [Noop] logger.
func Default() *slog.Logger { return _default.Load() }

// SetDefault overwrites the default logger.
func SetDefault(l *slog.Logger) {
	if l == nil {
		l = noop
	}
	_default.Store(l)
}

func init() {
	_default.Store(noop)
}

var loggerKey types.ContextKey = "logger"

// ContextWithLogger returns a new context with the logger set.
func ContextWithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

// LoggerFromValues returns the logger from the values.
//
// It returns the first logger found in the values.
// To find a logger, it checks each value in the order:
//   - [context.Context]
//   - [slog.Logger]
//   - object implementing interface{ Logger() *slog.Logger }
//
// If no logger is found, it returns the [Default] logger.
func LoggerFromValues(vals ...any) *slog.Logger {
	for _, val := range vals {
		switch v := val.(type) {
		case context.Context:
			if l, ok := v.Value(loggerKey).(*slog.Logger); ok && l != nil {
				return l
			}
		case *slog.Logger:
			if v != nil {
				return v
			}
		case interface{ Logger() *slog.Logger }:
			if l := v.Logger(); l != nil {
				return l
			}
		}
	}
	return Default()
}
