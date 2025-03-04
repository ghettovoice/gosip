package log

import "log/slog"

type stringValue[T ~string | ~[]byte] struct {
	v T
}

func (v stringValue[T]) LogValue() slog.Value {
	return slog.StringValue(string(v.v))
}

// StringValue returns a value logger that formats v as string.
func StringValue[T ~string | ~[]byte](v T) slog.LogValuer { return stringValue[T]{v} }
