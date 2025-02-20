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
	tr, _ := txtProtoRdrPool.Get().(*textproto.Reader)
	tr.R = r
	return tr
}

func freeTxtProtoRdr(r *textproto.Reader) {
	r.R = nil
	txtProtoRdrPool.Put(r)
}

var bytesRdrPool = sync.Pool{
	New: func() any { return bytes.NewReader(nil) },
}

func getBytesRdr(b []byte) *bytes.Reader {
	r, _ := bytesRdrPool.Get().(*bytes.Reader)
	r.Reset(b)
	return r
}

func freeBytesRdr(r *bytes.Reader) {
	r.Reset(nil)
	bytesRdrPool.Put(r)
}

var bufRdrPool = sync.Pool{
	New: func() any {
		return bufio.NewReaderSize(nil, MaxMsgSize)
	},
}

func getBufRdr(r io.Reader) *bufio.Reader {
	br, _ := bufRdrPool.Get().(*bufio.Reader)
	br.Reset(r)
	return br
}

func freeBufRdr(r *bufio.Reader) {
	r.Reset(nil)
	bufRdrPool.Put(r)
}
