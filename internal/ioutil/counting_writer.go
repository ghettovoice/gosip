package ioutil

import (
	"fmt"
	"io"
	"sync"

	"braces.dev/errtrace"
)

// CountingWriter wraps an io.Writer and tracks the total number of bytes written.
// It simplifies RenderTo implementations by eliminating manual byte accumulation.
type CountingWriter struct {
	w   io.Writer
	num int
	err error
}

// NewCountingWriter creates a new CountingWriter wrapping the given writer.
func NewCountingWriter(w io.Writer) *CountingWriter {
	return &CountingWriter{w: w}
}

// Write implements io.Writer and tracks bytes written.
func (cw *CountingWriter) Write(p []byte) (n int, err error) {
	if cw.err != nil {
		return 0, errtrace.Wrap(cw.err)
	}
	n, err = cw.w.Write(p)
	cw.num += n
	if err != nil {
		cw.err = errtrace.Wrap(err)
		return n, errtrace.Wrap(cw.err)
	}
	return n, nil
}

// WriteString writes a string and tracks bytes written.
func (cw *CountingWriter) WriteString(s string) (n int, err error) {
	if cw.err != nil {
		return 0, errtrace.Wrap(cw.err)
	}
	n, err = io.WriteString(cw.w, s)
	cw.num += n
	if err != nil {
		cw.err = errtrace.Wrap(err)
		return n, errtrace.Wrap(cw.err)
	}
	return n, nil
}

// Fprint writes formatted output and tracks bytes written.
func (cw *CountingWriter) Fprint(args ...any) (n int, err error) {
	if cw.err != nil {
		return 0, errtrace.Wrap(cw.err)
	}
	n, err = fmt.Fprint(cw.w, args...)
	cw.num += n
	if err != nil {
		cw.err = errtrace.Wrap(err)
		return n, errtrace.Wrap(cw.err)
	}
	return n, nil
}

// Fprintf writes formatted output with a format string and tracks bytes written.
func (cw *CountingWriter) Fprintf(format string, args ...any) (n int, err error) {
	if cw.err != nil {
		return 0, errtrace.Wrap(cw.err)
	}
	n, err = fmt.Fprintf(cw.w, format, args...)
	cw.num += n
	if err != nil {
		cw.err = errtrace.Wrap(err)
		return n, errtrace.Wrap(cw.err)
	}
	return n, nil
}

// Call executes a RenderTo-style function and tracks bytes written.
// This is useful for chaining RenderTo calls.
func (cw *CountingWriter) Call(fn func(io.Writer) (int, error)) *CountingWriter {
	if cw.err != nil {
		return cw
	}
	n, err := fn(cw.w)
	cw.num += n
	if err != nil {
		cw.err = errtrace.Wrap(err)
	}
	return cw
}

// Result returns the total number of bytes written and any error encountered.
func (cw *CountingWriter) Result() (num int, err error) {
	return cw.num, errtrace.Wrap(cw.err)
}

// Err returns any error that occurred during writing.
func (cw *CountingWriter) Err() error {
	return errtrace.Wrap(cw.err)
}

// Count returns the total number of bytes written.
func (cw *CountingWriter) Count() int {
	return cw.num
}

var cntWrtPool = &sync.Pool{
	New: func() any { return &CountingWriter{} },
}

func GetCountingWriter(w io.Writer) *CountingWriter {
	cw := cntWrtPool.Get().(*CountingWriter) //nolint:forcetypeassert
	cw.w = w
	return cw
}

func FreeCountingWriter(cw *CountingWriter) {
	cw.w = nil
	cw.num = 0
	cw.err = nil
	cntWrtPool.Put(cw)
}
