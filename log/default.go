package log

import (
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"slices"
	"sync"
)

type DefaultLogger struct {
	lvl  Level
	flds map[string]any
	dbg  *logger
	warn *logger
	err  *logger
}

func NewDefaultLogger(scope string, lvl Level, out io.Writer) *DefaultLogger {
	if scope == "" {
		scope = "gosip"
	}
	if out == nil {
		out = os.Stderr
	}
	stdFlags := log.LstdFlags | log.LUTC | log.Lmicroseconds | log.Lshortfile
	return &DefaultLogger{
		lvl:  lvl,
		dbg:  &logger{log: log.New(out, fmt.Sprintf("%s DEBUG | ", scope), stdFlags)},
		warn: &logger{log: log.New(out, fmt.Sprintf("%s WARNING | ", scope), stdFlags)},
		err:  &logger{log: log.New(out, fmt.Sprintf("%s ERROR | ", scope), stdFlags)},
	}
}

func (l *DefaultLogger) WithFields(flds map[string]any) Logger {
	return &DefaultLogger{
		lvl:  l.lvl,
		flds: flds,
		dbg:  l.dbg,
		warn: l.warn,
		err:  l.err,
	}
}

func (l *DefaultLogger) WithLevel(level Level) *DefaultLogger {
	l.lvl.Set(level)
	return l
}

func (l *DefaultLogger) WithDebugLog(dbg *log.Logger) *DefaultLogger {
	l.dbg.SetLogger(dbg)
	return l
}

func (l *DefaultLogger) WithWarnLog(warn *log.Logger) *DefaultLogger {
	l.warn.SetLogger(warn)
	return l
}

func (l *DefaultLogger) WithErrorLog(err *log.Logger) *DefaultLogger {
	l.err.SetLogger(err)
	return l
}

func (l *DefaultLogger) logf(logger *logger, lvl Level, msg string, flds map[string]any) {
	if logger == nil || l.lvl.Get() < lvl {
		return
	}

	allFlds := getFields(len(l.flds) + len(flds))
	defer freeFields(allFlds)
	resolveFields(allFlds, l.flds)
	resolveFields(allFlds, flds)

	var dump []byte
	if val, ok := allFlds["~dump"]; ok {
		delete(allFlds, "~dump")
		switch v := val.(type) {
		case []byte:
			dump = v
		case string:
			dump = []byte(v)
		}
	}

	fldsBytes := getBytes(0, 4*len(allFlds))
	defer freeBytes(fldsBytes)

	fldsBytes = appendFields(fldsBytes, allFlds)

	msgBytes := getBytes(0, 3+len(msg)+len(fldsBytes)+len(dump))
	defer freeBytes(msgBytes)

	msgBytes = append(msgBytes, msg...)
	msgBytes = append(msgBytes, ' ')
	msgBytes = append(msgBytes, fldsBytes...)
	if len(dump) > 0 {
		msgBytes = append(msgBytes, '\n')
		msgBytes = append(msgBytes, dump...)
	}

	if err := logger.Output(3, string(msgBytes)); err != nil {
		fmt.Fprintf(os.Stderr, "Unable to log: %s", err)
	}
}

func (l *DefaultLogger) Debug(msg string, flds map[string]any) {
	l.logf(l.dbg, LevelDebug, msg, flds)
}

func (l *DefaultLogger) Warn(msg string, flds map[string]any) {
	l.logf(l.warn, LevelWarn, msg, flds)
}

func (l *DefaultLogger) Error(msg string, flds map[string]any) {
	l.logf(l.err, LevelError, msg, flds)
}

type logger struct {
	sync.RWMutex
	log *log.Logger
}

func (l *logger) SetLogger(log *log.Logger) {
	l.Lock()
	l.log = log
	l.Unlock()
}

func (l *logger) Output(calldepth int, s string) error {
	l.RLock()
	err := l.log.Output(calldepth+1, s)
	l.RUnlock()
	return err
}

var fldsPool = sync.Pool{
	New: func() any { return make(map[string]any) },
}

func getFields(size int) map[string]any {
	if size == 0 {
		return fldsPool.Get().(map[string]any)
	}
	return make(map[string]any, size)
}

func freeFields(flds map[string]any) {
	if len(flds) > 100 {
		return
	}
	clear(flds)
	fldsPool.Put(flds)
}

var bytesPool = sync.Pool{
	New: func() any { return make([]byte, 0) },
}

func getBytes(size ...int) []byte {
	var l, c int
	if len(size) > 0 {
		l, c = size[0], size[0]+size[0]/2
		if len(size) > 1 {
			c = size[1]
		}
	}
	if l == 0 && c == 0 {
		return bytesPool.Get().([]byte)
	}
	return make([]byte, l, c)
}

func freeBytes(b []byte) {
	if cap(b) > 1000 {
		return
	}
	clear(b)
	bytesPool.Put(b[:0])
}

func resolveFields(dst, flds map[string]any) {
	for k, v := range flds {
		if len(k) > 0 && k[0] == '~' {
			if f, ok := v.(func() any); ok && f != nil {
				v = f()
				if k != "~dump" {
					k = k[1:]
				}
			}
		}
		dst[k] = v
	}
}

func appendFields(dst []byte, flds map[string]any) []byte {
	if len(flds) == 0 {
		return dst
	}

	dst = append(dst, '{')
	for i, k := range slices.Sorted(maps.Keys(flds)) {
		if i > 0 {
			dst = append(dst, ' ')
		}
		dst = fmt.Appendf(dst, "%s=", k)
		switch v := flds[k].(type) {
		case string:
			dst = fmt.Appendf(dst, "%q", v)
		case fmt.Stringer:
			dst = fmt.Appendf(dst, "%q", v)
		// case []byte:
		// 	b = fmt.Appendf(b, "%q", v)
		default:
			dst = fmt.Appendf(dst, "%v", v)
		}
	}
	dst = append(dst, '}')
	return dst
}
