package useragent

import (
	"math"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

// isAuthChallenge reports whether sts is a 401/407 authentication
// challenge response status.
func isAuthChallenge(sts sip.ResponseStatus) bool {
	return sts.Equal(sip.ResponseStatusUnauthorized) ||
		sts.Equal(sip.ResponseStatusProxyAuthenticationRequired)
}

// makeRetryReq builds the request an outgoing operation sends on a
// retry leg: a clone of the actually sent request — or of an owned
// pristine template — so the transaction's cached request is never
// mutated. Every logical retry is a new send: the CSeq sequence number
// increments, the top Via receives a fresh RFC 3261 branch (the element's
// attempt runner only regenerates a branch between candidates of one
// send, so the retry must set it explicitly), while Call-ID, the From
// tag, and the To identity stay untouched. The clone is returned in a
// fresh envelope so per-attempt transport, address, and metadata of the
// previous leg are cleared.
//
// A nil target keeps the request on the same target and preserves the
// credential headers a previous leg applied. A non-nil target retargets
// the request URI and deletes both the Authorization and
// Proxy-Authorization families: credentials never cross targets.
func makeRetryReq(sent *sip.RequestEnvelope, target sip.AnyURI) (*sip.RequestEnvelope, error) {
	if sent == nil || sent.MessageEnvelope == nil {
		return nil, errors.Wrap(NewInvalidRequestError("nil request envelope"))
	}

	// The contained message is cloned under the envelope lock: sent stays
	// untouched whatever it is — a transaction's actual request or an
	// owned template.
	var msg *sip.Request
	sent.WithMessage(func(r *sip.Request) {
		if r != nil {
			msg, _ = r.Clone().(*sip.Request)
		}
	})
	if msg == nil {
		return nil, errors.Wrap(NewInvalidRequestError("nil request message"))
	}

	cseq, ok := msg.Headers.CSeq()
	if !ok || cseq == nil {
		return nil, errors.Wrap(NewInvalidRequestError("missing CSeq header"))
	}
	if cseq.SeqNum >= math.MaxInt32 {
		return nil, errors.Wrap(NewCSeqExhaustedError())
	}
	cseq.SeqNum++

	via, ok := msg.Headers.FirstVia()
	if !ok || via == nil {
		return nil, errors.Wrap(NewInvalidRequestError("missing Via header"))
	}
	if via.Params == nil {
		via.Params = make(sip.Values)
	}
	via.Params.Set("branch", sip.GenerateBranch(0))

	if !util.IsNil(target) {
		msg.URI = target.Clone()
		msg.Headers.Delete("Authorization", "Proxy-Authorization")
	}

	return sip.NewRequestEnvelope(msg), nil
}
