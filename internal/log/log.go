// Package log provides logging utilities.
package log

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/golang-cz/devslog"
	"github.com/phsym/console-slog"
	slogformatter "github.com/samber/slog-formatter"

	"github.com/ghettovoice/gosip/internal/constraints"
)

var newHandler = slogformatter.NewFormatterHandler(
	slogformatter.ErrorFormatter("error"),
	slogformatter.FormatByType(func(ls net.Listener) slog.Value {
		return slog.GroupValue(
			slog.String("type", fmt.Sprintf("%T", ls)),
			slog.String("ptr", fmt.Sprintf("%p", ls)),
			slog.Any("local_addr", ls.Addr()),
		)
	}),
	slogformatter.FormatByType(func(c net.PacketConn) slog.Value {
		return slog.GroupValue(
			slog.String("type", fmt.Sprintf("%T", c)),
			slog.String("ptr", fmt.Sprintf("%p", c)),
			slog.Any("local_addr", c.LocalAddr()),
		)
	}),
	slogformatter.FormatByType(func(c net.Conn) slog.Value {
		return slog.GroupValue(
			slog.String("type", fmt.Sprintf("%T", c)),
			slog.String("ptr", fmt.Sprintf("%p", c)),
			slog.Any("local_addr", c.LocalAddr()),
			slog.Any("remote_addr", c.RemoteAddr()),
		)
	}),
)

// Def is a default logger.
var Def = slog.New(newHandler(
	console.NewHandler(os.Stdout, &console.HandlerOptions{
		AddSource:  true,
		Level:      slog.LevelDebug,
		TimeFormat: time.RFC3339Nano,
	}),
))

// Dev is a developer logger.
var Dev = slog.New(newHandler(
	devslog.NewHandler(os.Stdout, &devslog.Options{
		HandlerOptions: &slog.HandlerOptions{
			AddSource: true,
			Level:     slog.LevelDebug,
		},
		SortKeys:   true,
		TimeFormat: time.RFC3339Nano,
	}),
))

type noopHandler struct{}

func (noopHandler) Enabled(context.Context, slog.Level) bool { return false }

func (noopHandler) Handle(context.Context, slog.Record) error { return nil }

func (h noopHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h noopHandler) WithGroup(string) slog.Handler { return h }

// Noop is a noop logger.
var Noop = slog.New(noopHandler{})

type fmtValue struct {
	v        any
	goSyntax bool
}

func (v fmtValue) LogValue() slog.Value {
	if v.goSyntax {
		return slog.StringValue(fmt.Sprintf("%#v", v.v))
	}
	return slog.StringValue(fmt.Sprintf("%+v", v.v))
}

// FmtValue returns a value logger that formats values using '%+v' or '%#v' syntax.
func FmtValue(v any, goSyntax bool) slog.LogValuer { return fmtValue{v, goSyntax} }

type calcValue struct{ fn func() any }

func (v calcValue) LogValue() slog.Value {
	cv := v.fn()
	switch cv := cv.(type) {
	case slog.Value:
		return cv
	default:
		return slog.AnyValue(cv)
	}
}

// CalcValue returns a value logger that computes a value using a fn.
func CalcValue(fn func() any) slog.LogValuer { return calcValue{fn} }

type stringValue[T constraints.Byteseq] struct {
	v T
}

func (v stringValue[T]) LogValue() slog.Value {
	return slog.StringValue(string(v.v))
}

// StringValue returns a value logger that formats v as string.
func StringValue[T constraints.Byteseq](v T) slog.LogValuer { return stringValue[T]{v} }
