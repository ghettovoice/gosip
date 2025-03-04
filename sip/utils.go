package sip

import (
	"bufio"
	"io"
	"net/textproto"
	"sync"
)

var jsonNull = []byte("null")

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
	New: func() any { return bufio.NewReaderSize(nil, int(MaxMsgSize)) },
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
