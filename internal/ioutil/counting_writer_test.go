package ioutil_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"testing"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/internal/ioutil"
)

type errorWriter struct {
	failAfter int
	written   int
}

func (ew *errorWriter) Write(p []byte) (n int, err error) {
	if ew.written >= ew.failAfter {
		return 0, errtrace.Wrap(errors.New("write failed"))
	}
	n = len(p)
	if ew.written+n > ew.failAfter {
		n = ew.failAfter - ew.written
	}
	ew.written += n
	if n < len(p) {
		return n, errtrace.Wrap(errors.New("write failed"))
	}
	return n, nil
}

func TestCountingWriter_Write(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	cw := ioutil.NewCountingWriter(buf)

	n, err := cw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}
	if cw.Count() != 5 {
		t.Errorf("expected count 5, got %d", cw.Count())
	}

	n, err = cw.Write([]byte(" world"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 6 {
		t.Errorf("expected 6 bytes written, got %d", n)
	}
	if cw.Count() != 11 {
		t.Errorf("expected count 11, got %d", cw.Count())
	}

	if buf.String() != "hello world" {
		t.Errorf("expected 'hello world', got %q", buf.String())
	}
}

func TestCountingWriter_WriteString(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	cw := ioutil.NewCountingWriter(buf)

	n, err := cw.WriteString("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 4 {
		t.Errorf("expected 4 bytes written, got %d", n)
	}
	if cw.Count() != 4 {
		t.Errorf("expected count 4, got %d", cw.Count())
	}
}

func TestCountingWriter_Fprint(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	cw := ioutil.NewCountingWriter(buf)

	n, err := cw.Fprint("hello", " ", "world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 11 {
		t.Errorf("expected 11 bytes written, got %d", n)
	}
	if cw.Count() != 11 {
		t.Errorf("expected count 11, got %d", cw.Count())
	}
	if buf.String() != "hello world" {
		t.Errorf("expected 'hello world', got %q", buf.String())
	}
}

func TestCountingWriter_Fprintf(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	cw := ioutil.NewCountingWriter(buf)

	n, err := cw.Fprintf("number: %d", 42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 10 {
		t.Errorf("expected 10 bytes written, got %d", n)
	}
	if cw.Count() != 10 {
		t.Errorf("expected count 10, got %d", cw.Count())
	}
}

func TestCountingWriter_Call(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	cw := ioutil.NewCountingWriter(buf)

	renderFunc := func(w io.Writer) (int, error) {
		return errtrace.Wrap2(fmt.Fprint(w, "test"))
	}

	cw.Call(renderFunc)
	num, err := cw.Result()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if num != 4 {
		t.Errorf("expected 4 bytes written, got %d", num)
	}
	if buf.String() != "test" {
		t.Errorf("expected 'test', got %q", buf.String())
	}
}

func TestCountingWriter_ErrorPropagation(t *testing.T) {
	t.Parallel()

	ew := &errorWriter{failAfter: 5}
	cw := ioutil.NewCountingWriter(ew)

	// First write should succeed
	n, err := cw.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error on first write: %v", err)
	}
	if n != 5 {
		t.Errorf("expected 5 bytes written, got %d", n)
	}

	// Second write should fail
	n, err = cw.Write([]byte(" world"))
	if err == nil {
		t.Fatal("expected error on second write")
	}
	if n != 0 {
		t.Errorf("expected 0 bytes written on error, got %d", n)
	}

	// Subsequent writes should immediately return the cached error
	n, err = cw.Write([]byte("test"))
	if err == nil {
		t.Fatal("expected cached error")
	}
	if n != 0 {
		t.Errorf("expected 0 bytes written on cached error, got %d", n)
	}

	if cw.Count() != 5 {
		t.Errorf("expected count 5, got %d", cw.Count())
	}
}

func TestCountingWriter_Chaining(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	cw := ioutil.NewCountingWriter(buf)

	// Chain multiple operations
	cw.Fprint("a")
	cw.Fprint("b")
	cw.WriteString("c")

	num, err := cw.Result()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if num != 3 {
		t.Errorf("expected 3 bytes written, got %d", num)
	}
	if buf.String() != "abc" {
		t.Errorf("expected 'abc', got %q", buf.String())
	}
}

func TestCountingWriter_CallChaining(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	cw := ioutil.NewCountingWriter(buf)

	render1 := func(w io.Writer) (int, error) {
		return errtrace.Wrap2(fmt.Fprint(w, "a"))
	}
	render2 := func(w io.Writer) (int, error) {
		return errtrace.Wrap2(fmt.Fprint(w, "b"))
	}

	cw.Call(render1).Call(render2)
	num, err := cw.Result()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if num != 2 {
		t.Errorf("expected 2 bytes written, got %d", num)
	}
	if buf.String() != "ab" {
		t.Errorf("expected 'ab', got %q", buf.String())
	}
}

func TestCountingWriter_CallErrorStopsChain(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	cw := ioutil.NewCountingWriter(buf)

	render1 := func(w io.Writer) (int, error) {
		return errtrace.Wrap2(fmt.Fprint(w, "a"))
	}
	renderErr := func(w io.Writer) (int, error) {
		return 0, errtrace.Wrap(errors.New("render error"))
	}
	render2 := func(w io.Writer) (int, error) {
		return errtrace.Wrap2(fmt.Fprint(w, "b"))
	}

	cw.Call(render1).Call(renderErr).Call(render2)
	num, err := cw.Result()
	if err == nil {
		t.Fatal("expected error from chain")
	}
	if num != 1 {
		t.Errorf("expected 1 byte written before error, got %d", num)
	}
	if buf.String() != "a" {
		t.Errorf("expected 'a', got %q", buf.String())
	}
}
