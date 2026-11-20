package useragent

import (
	"math"
	"net/netip"
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// retryTestURI parses a URI for retry tests.
func retryTestURI(tb testing.TB, s string) *sip.URI {
	tb.Helper()

	u, err := sip.ParseURI(s)
	if err != nil {
		tb.Fatalf("sip.ParseURI(%q) error = %v, want nil", s, err)
	}
	return u
}

// newSentReq builds the request envelope makeRetryReq consumes: a
// previously sent request carrying a generated Via branch, From tag, and
// Call-ID, optionally with one credential header of each family.
func newSentReq(tb testing.TB, seqNum uint, withCreds bool) *sip.RequestEnvelope {
	tb.Helper()

	aor := retryTestURI(tb, "sip:alice@ua.test")
	target := retryTestURI(tb, "sip:bob@example.test")
	hdrs := make(sip.Headers)
	if withCreds {
		hdrs.
			Set(&header.Authorization{AuthCredentials: &header.BearerCredentials{Token: "t1"}}).
			Set(&header.ProxyAuthorization{AuthCredentials: &header.BearerCredentials{Token: "t2"}})
	}
	req, err := sip.NewRequest(
		sip.RequestMethodMessage,
		target,
		aor,
		target,
		sip.RequestOptions{SeqNum: seqNum, Headers: hdrs},
	)
	if err != nil {
		tb.Fatalf("sip.NewRequest() error = %v, want nil", err)
	}
	return sip.NewRequestEnvelope(req)
}

func TestMakeRetryReq(t *testing.T) {
	t.Parallel()

	t.Run("fresh clone with stable identity", func(t *testing.T) {
		t.Parallel()

		sent := newSentReq(t, 1, false)
		// Per-attempt metadata of the previous send must not leak into
		// the retry envelope.
		sent.SetTransport(sip.UDPMetadata()).
			SetLocalAddr(netip.MustParseAddrPort("192.0.2.10:5060")).
			SetRemoteAddr(netip.MustParseAddrPort("198.51.100.20:5060"))
		sent.Metadata().Set("custom", true)
		sent.MarkForwarded()

		sentHdrs := sent.Headers()
		sentCSeq, _ := sentHdrs.CSeq()
		sentCallID, _ := sentHdrs.CallID()
		sentFrom, _ := sentHdrs.From()
		sentTo, _ := sentHdrs.To()
		sentVia, _ := sentHdrs.FirstVia()
		sentBranch, _ := sentVia.Branch()

		retry, err := makeRetryReq(sent, nil)
		if err != nil {
			t.Fatalf("makeRetryReq() error = %v, want nil", err)
		}
		if retry == nil {
			t.Fatal("makeRetryReq() = nil, want non-nil")
		}
		retryHdrs := retry.Headers()

		cseq, _ := retryHdrs.CSeq()
		if cseq.SeqNum != sentCSeq.SeqNum+1 {
			t.Errorf("retry CSeq SeqNum = %d, want %d", cseq.SeqNum, sentCSeq.SeqNum+1)
		}
		if !cseq.Method.Equal(sentCSeq.Method) {
			t.Errorf("retry CSeq method = %v, want %v", cseq.Method, sentCSeq.Method)
		}

		via, _ := retryHdrs.FirstVia()
		branch, _ := via.Branch()
		if branch == sentBranch || !sip.IsRFC3261Branch(branch) {
			t.Errorf("retry Via branch = %q, want a fresh RFC 3261 branch (sent %q)", branch, sentBranch)
		}

		if callID, _ := retryHdrs.CallID(); callID != sentCallID {
			t.Errorf("retry Call-ID = %v, want %v", callID, sentCallID)
		}
		from, _ := retryHdrs.From()
		if !from.Equal(sentFrom) {
			t.Errorf("retry From = %v, want %v", from, sentFrom)
		}
		to, _ := retryHdrs.To()
		if !to.Equal(sentTo) {
			t.Errorf("retry To = %v, want %v", to, sentTo)
		}
		if !retry.URI().Equal(sent.URI()) {
			t.Errorf("retry URI = %v, want %v", retry.URI(), sent.URI())
		}

		// The fresh envelope drops transport, addresses, and metadata.
		if retry.Transport().IsValid() {
			t.Errorf("retry transport = %v, want unset", retry.Transport())
		}
		if retry.LocalAddr().IsValid() {
			t.Errorf("retry local addr = %v, want unset", retry.LocalAddr())
		}
		if retry.RemoteAddr().IsValid() {
			t.Errorf("retry remote addr = %v, want unset", retry.RemoteAddr())
		}
		if _, ok := retry.Metadata().Get("custom"); ok {
			t.Error("retry metadata carried the custom key, want cleared")
		}
		if retry.Forwarded() {
			t.Error("retry metadata kept the forwarded marker, want cleared")
		}

		// The sent request is never mutated.
		if c, _ := sent.Headers().CSeq(); c.SeqNum != sentCSeq.SeqNum {
			t.Errorf("sent CSeq SeqNum = %d, want unchanged %d", c.SeqNum, sentCSeq.SeqNum)
		}
		if v, _ := sent.Headers().FirstVia(); v != nil {
			if b, _ := v.Branch(); b != sentBranch {
				t.Errorf("sent Via branch = %q, want unchanged %q", b, sentBranch)
			}
		}
	})

	t.Run("cseq boundary", func(t *testing.T) {
		t.Parallel()

		// MaxInt32-1 -> MaxInt32 is the last legal increment.
		sent := newSentReq(t, math.MaxInt32-1, false)
		retry, err := makeRetryReq(sent, nil)
		if err != nil {
			t.Fatalf("makeRetryReq() error = %v, want nil", err)
		}
		cseq, _ := retry.Headers().CSeq()
		if cseq.SeqNum != math.MaxInt32 {
			t.Errorf("retry CSeq SeqNum = %d, want %d", cseq.SeqNum, uint(math.MaxInt32))
		}
		if c, _ := sent.Headers().CSeq(); c.SeqNum != math.MaxInt32-1 {
			t.Errorf("sent CSeq SeqNum = %d, want unchanged %d", c.SeqNum, uint(math.MaxInt32-1))
		}

		// An exhausted sequence fails classified.
		sent = newSentReq(t, math.MaxInt32, false)
		retry, err = makeRetryReq(sent, nil)
		if retry != nil {
			t.Errorf("makeRetryReq() = %v, want nil request", retry)
		}
		if !errors.Is(err, ErrCSeqExhausted) {
			t.Fatalf("makeRetryReq() error = %v, want %v", err, ErrCSeqExhausted)
		}
	})

	t.Run("retarget clears credentials", func(t *testing.T) {
		t.Parallel()

		sent := newSentReq(t, 5, true)
		newTarget := retryTestURI(t, "sip:carol@example.test")

		retry, err := makeRetryReq(sent, newTarget)
		if err != nil {
			t.Fatalf("makeRetryReq() error = %v, want nil", err)
		}
		if !retry.URI().Equal(newTarget) {
			t.Errorf("retry URI = %v, want %v", retry.URI(), newTarget)
		}
		retryHdrs := retry.Headers()
		if got := len(retryHdrs.Get("Authorization")); got != 0 {
			t.Errorf("retargeted retry carried %d Authorization headers, want 0", got)
		}
		if got := len(retryHdrs.Get("Proxy-Authorization")); got != 0 {
			t.Errorf("retargeted retry carried %d Proxy-Authorization headers, want 0", got)
		}

		// Credentials never cross targets, but the dialog identity and
		// the incremented sequence stay.
		sentHdrs := sent.Headers()
		sentCallID, _ := sentHdrs.CallID()
		if callID, _ := retryHdrs.CallID(); callID != sentCallID {
			t.Errorf("retry Call-ID = %v, want %v", callID, sentCallID)
		}
		sentFrom, _ := sentHdrs.From()
		if from, _ := retryHdrs.From(); !from.Equal(sentFrom) {
			t.Errorf("retry From = %v, want %v", from, sentFrom)
		}
		sentCSeq, _ := sentHdrs.CSeq()
		if cseq, _ := retryHdrs.CSeq(); cseq.SeqNum != sentCSeq.SeqNum+1 {
			t.Errorf("retry CSeq SeqNum = %d, want %d", cseq.SeqNum, sentCSeq.SeqNum+1)
		}

		// The caller's URI and the sent request stay untouched.
		if got := len(sent.Headers().Get("Authorization")); got != 1 {
			t.Errorf("sent request has %d Authorization headers, want unchanged 1", got)
		}
		if got := len(sent.Headers().Get("Proxy-Authorization")); got != 1 {
			t.Errorf("sent request has %d Proxy-Authorization headers, want unchanged 1", got)
		}
	})

	t.Run("same target preserves credentials", func(t *testing.T) {
		t.Parallel()

		sent := newSentReq(t, 5, true)

		retry, err := makeRetryReq(sent, nil)
		if err != nil {
			t.Fatalf("makeRetryReq() error = %v, want nil", err)
		}
		retryHdrs := retry.Headers()
		if got := len(retryHdrs.Get("Authorization")); got != 1 {
			t.Errorf("same-target retry carried %d Authorization headers, want 1", got)
		}
		if got := len(retryHdrs.Get("Proxy-Authorization")); got != 1 {
			t.Errorf("same-target retry carried %d Proxy-Authorization headers, want 1", got)
		}
		if !retry.URI().Equal(sent.URI()) {
			t.Errorf("retry URI = %v, want %v", retry.URI(), sent.URI())
		}
	})

	t.Run("nil request rejected", func(t *testing.T) {
		t.Parallel()

		retry, err := makeRetryReq(nil, nil)
		if retry != nil {
			t.Errorf("makeRetryReq() = %v, want nil request", retry)
		}
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("makeRetryReq() error = %v, want %v", err, ErrInvalidRequest)
		}
	})
}
