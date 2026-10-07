package log

import (
	"fmt"
	"log/slog"
)

type stringValue[T any] struct {
	val T
	fmt string
}

func (v stringValue[T]) LogValue() slog.Value {
	switch vv := any(v.val).(type) {
	case string:
		return slog.StringValue(vv)
	case []byte:
		return slog.StringValue(string(vv))
	default:
		f := "%+v"
		if v.fmt != "" {
			f = v.fmt
		}
		return slog.StringValue(fmt.Sprintf(f, vv))
	}
}

// StringValue returns a value logger that formats v as string.
func StringValue[T any](v T) slog.LogValuer {
	return stringValue[T]{v, ""}
}

func StringValueWithFormat[T any](v T, f string) slog.LogValuer {
	return stringValue[T]{v, f}
}
