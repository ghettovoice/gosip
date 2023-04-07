package log

import (
	"sync/atomic"
)

type Logger interface {
	WithFields(flds map[string]any) Logger
	Debug(msg string, flds map[string]any)
	Warn(msg string, flds map[string]any)
	Error(msg string, flds map[string]any)
}

type Level uint32

const (
	LevelDisabled Level = iota
	LevelError
	LevelWarn
	LevelDebug
)

func (l *Level) Set(newLevel Level) {
	atomic.StoreUint32((*uint32)(l), uint32(newLevel))
}

func (l *Level) Get() Level {
	return Level(atomic.LoadUint32((*uint32)(l)))
}

func (l Level) String() string {
	if l > LevelDebug {
		return "unknown"
	}
	return [...]string{"disabled", "error", "warning", "debug"}[l]
}
