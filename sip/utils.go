package sip

import (
	"bufio"
	"context"
	"io"
	"math"
	"net/textproto"
	"sync"
)

var (
	sNilTag  = "<nil>"
	bNilTag  = []byte(sNilTag)
	jsonNull = []byte("null")
)

var txtProtoRdrPool = sync.Pool{
	New: func() any { return new(textproto.Reader) },
}

func getTxtProtoRdr(r *bufio.Reader) *textproto.Reader {
	tr := txtProtoRdrPool.Get().(*textproto.Reader) //nolint:forcetypeassert
	tr.R = r
	return tr
}

func freeTxtProtoRdr(r *textproto.Reader) {
	r.R = nil
	txtProtoRdrPool.Put(r)
}

var bufferedRdrPool = sync.Pool{
	New: func() any { return bufio.NewReaderSize(nil, math.MaxUint16) },
}

func getBufferedRdr(r io.Reader) *bufio.Reader {
	br := bufferedRdrPool.Get().(*bufio.Reader) //nolint:forcetypeassert
	br.Reset(r)
	return br
}

func freeBufferedRdr(r *bufio.Reader) {
	r.Reset(nil)
	bufferedRdrPool.Put(r)
}

func clampToUint64(value int64) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}

type closer interface {
	Close(ctx context.Context) error
}
