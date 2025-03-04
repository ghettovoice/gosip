package log

import (
	"context"
	"log/slog"
)

type noopHandler struct{}

func (noopHandler) Enabled(context.Context, slog.Level) bool { return false }

func (noopHandler) Handle(context.Context, slog.Record) error { return nil }

func (h noopHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h noopHandler) WithGroup(string) slog.Handler { return h }
