package dialog

import (
	"math"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// TestDialog_NewRequest_CSeqExhausted checks the arithmetic boundary: a
// dialog whose local counter sits at math.MaxUint32-1 yields one more
// request and then refuses without mutating the counter.
func TestDialog_NewRequest_CSeqExhausted(t *testing.T) {
	t.Parallel()

	uri := &sip.URI{Addr: sip.MakeHostAddr("example.test")}
	d := &Dialog{
		id:      ID{CallID: "cseq-overflow@example.test", LocalTag: "a", RemoteTag: "b"},
		loc:     header.NameAddr{URI: uri},
		rmt:     header.NameAddr{URI: uri},
		target:  uri,
		locCSeq: math.MaxUint32 - 1,
	}

	out, err := d.NewRequest(sip.RequestMethodBye)
	if err != nil {
		t.Fatalf("d.NewRequest() error = %v, want nil", err)
	}
	if cseq, ok := out.Headers().CSeq(); !ok || cseq.SeqNum != math.MaxUint32 {
		t.Fatalf("request CSeq = %v, want %d", cseq, uint(math.MaxUint32))
	}
	if _, err := d.NewRequest(sip.RequestMethodBye); !errors.Is(err, ErrCSeqExhausted) {
		t.Fatalf("d.NewRequest() at boundary error = %v, want %v", err, ErrCSeqExhausted)
	}
	if got, want := d.View().LocalCSeq, uint(math.MaxUint32); got != want {
		t.Fatalf("d.View().LocalCSeq = %d, want %d (no mutation on exhaustion)", got, want)
	}
}
