package sip

import (
	"bufio"
	"io"
	"math"
	"net/textproto"
	"sync"

	"github.com/ghettovoice/timeutil"
)

var (
	sNilTag  = "<nil>"
	bNilTag  = []byte(sNilTag)
	jsonNull = []byte("null")

	noop = func() {}
)

var txtProtoRdrPool = sync.Pool{
	New: func() any { return new(textproto.Reader) },
}

func getTxtProtoRdr(r *bufio.Reader) *textproto.Reader {
	tr := txtProtoRdrPool.Get().(*textproto.Reader) //nolint:forcetypeassert // the concrete type is fixed by construction
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
	br := bufferedRdrPool.Get().(*bufio.Reader) //nolint:forcetypeassert // the concrete type is fixed by construction
	br.Reset(r)
	return br
}

func freeBufferedRdr(r *bufio.Reader) {
	r.Reset(nil)
	bufferedRdrPool.Put(r)
}

func cloneTmrSnapshot(snap *timeutil.TimerSnapshot) *timeutil.TimerSnapshot {
	if snap == nil {
		return nil
	}
	cloned := *snap
	return &cloned
}
