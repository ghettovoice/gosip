package sip

import (
	"bufio"
	"bytes"
	"io"
	"net/textproto"
	"sync"
)

var txtProtoRdrPool = sync.Pool{
	New: func() any { return new(textproto.Reader) },
}

func getTxtProtoRdr(r *bufio.Reader) *textproto.Reader {
	tr := txtProtoRdrPool.Get().(*textproto.Reader)
	tr.R = r
	return tr
}

func freeTxtProtoRdr(r *textproto.Reader) {
	r.R = nil
	txtProtoRdrPool.Put(r)
}

var bytesBufPool = &sync.Pool{
	New: func() any { return bytes.NewBuffer(make([]byte, 0, 1024)) },
}

func getBytesBuf() *bytes.Buffer { return bytesBufPool.Get().(*bytes.Buffer) }

func freeBytesBuf(b *bytes.Buffer) {
	b.Reset()
	if b.Cap() > maxMsgSize {
		return
	}
	bytesBufPool.Put(b)
}

var bytesRdrPool = sync.Pool{
	New: func() any { return bytes.NewReader(nil) },
}

func getBytesRdr(b []byte) *bytes.Reader {
	r := bytesRdrPool.Get().(*bytes.Reader)
	r.Reset(b)
	return r
}

func freeBytesRdr(r *bytes.Reader) {
	r.Reset(nil)
	bytesRdrPool.Put(r)
}

var bufRdrPool = sync.Pool{
	New: func() any {
		return bufio.NewReaderSize(nil, maxMsgSize)
	},
}

func getBufRdr(r io.Reader) *bufio.Reader {
	br := bufRdrPool.Get().(*bufio.Reader)
	br.Reset(r)
	return br
}

func freeBufRdr(r *bufio.Reader) {
	r.Reset(nil)
	bufRdrPool.Put(r)
}
