package auth

import (
	"context"
	"strings"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

// renderURI renders u through its error-aware RenderTo, preserving a
// renderer's failure instead of trusting partial text.
func renderURI(u uri.URI) (string, error) {
	var sb strings.Builder
	_, err := u.RenderTo(&sb)
	return sb.String(), errors.Wrap(err)
}

// clientMsgs validates the client authentication inputs and returns
// detached message snapshots: mutating them does not affect the envelopes.
// A non-nil contained message with a valid method and target URI suffices;
// transaction headers are not required. Only 401/407 responses carry
// challenges.
func clientMsgs(ctx context.Context, req *sip.RequestEnvelope, res *sip.ResponseEnvelope) (*sip.Request, *sip.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, errors.Wrap(err)
	}
	if req == nil || req.MessageEnvelope == nil {
		return nil, nil, errors.ErrorWrap("nil request envelope")
	}
	if res == nil || res.MessageEnvelope == nil {
		return nil, nil, errors.ErrorWrap("nil response envelope")
	}
	reqMsg, err := authReqMsg(req)
	if err != nil {
		return nil, nil, errors.Wrap(err)
	}

	resMsg := resSnapshot(res.MessageEnvelope)
	if resMsg == nil {
		return nil, nil, errors.ErrorWrap("nil response message")
	}
	if resMsg.Status != sip.ResponseStatusUnauthorized &&
		resMsg.Status != sip.ResponseStatusProxyAuthenticationRequired {
		return nil, nil, errors.ErrorfWrap("non-challenge response status %d", resMsg.Status)
	}
	return reqMsg, resMsg, nil
}

// serverMsg validates the server authentication input and returns a
// detached request snapshot.
func serverMsg(ctx context.Context, req *sip.RequestEnvelope) (*sip.Request, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.Wrap(err)
	}
	if req == nil || req.MessageEnvelope == nil {
		return nil, errors.ErrorWrap("nil request envelope")
	}
	return errors.Wrap2(authReqMsg(req))
}

// reqSnapshot returns a detached clone of the envelope's contained
// request, or nil when it holds none. WithMessage is used instead of
// Message because a manually constructed envelope holding a nil message
// panics inside the sip clone path.
func reqSnapshot(env *sip.MessageEnvelope[*sip.Request]) *sip.Request {
	var msg *sip.Request
	env.WithMessage(func(m *sip.Request) {
		if m != nil {
			msg, _ = m.Clone().(*sip.Request)
		}
	})
	return msg
}

// resSnapshot is the response counterpart of requestSnapshot.
func resSnapshot(env *sip.MessageEnvelope[*sip.Response]) *sip.Response {
	var msg *sip.Response
	env.WithMessage(func(m *sip.Response) {
		if m != nil {
			msg, _ = m.Clone().(*sip.Response)
		}
	})
	return msg
}

// authReqMsg returns the envelope's contained message when it can
// carry authentication. ACK and CANCEL are not authentication exchanges.
func authReqMsg(req *sip.RequestEnvelope) (*sip.Request, error) {
	msg := reqSnapshot(req.MessageEnvelope)
	if msg == nil {
		return nil, errors.ErrorWrap("nil request message")
	}
	if !msg.Method.IsValid() {
		return nil, errors.ErrorfWrap("invalid request method %q", msg.Method)
	}
	if msg.Method.Equal(sip.RequestMethodAck) || msg.Method.Equal(sip.RequestMethodCancel) {
		return nil, errors.Wrap(sip.NewRequestMethodNotAllowedError("authentication not required"))
	}
	if !shared.IsValid(msg.URI) {
		return nil, errors.ErrorWrap("invalid request URI")
	}
	return msg, nil
}

// digestChals collects the Digest challenges of one family from a
// message's headers.
func digestChals(hdrs sip.Headers, proxy bool) []*header.DigestChallenge {
	seq := hdrs.AuthenticateChallenges
	if proxy {
		seq = hdrs.ProxyAuthenticateChallenges
	}
	var chals []*header.DigestChallenge
	for chal := range seq() {
		if digest, ok := chal.(*header.DigestChallenge); ok {
			chals = append(chals, digest)
		}
	}
	return chals
}

// withReqCommit runs fn under the request envelope lock, rechecking
// cancellation once the lock is held so a late cancellation can never slip
// a commit through. It is the shared commit boundary for all client
// authenticators.
func withReqCommit(ctx context.Context, req *sip.RequestEnvelope, fn func(*sip.Request)) error {
	var commitErr error
	req.WithMessage(func(msg *sip.Request) {
		if commitErr = ctx.Err(); commitErr != nil {
			return
		}
		fn(msg)
	})
	return errors.Wrap(commitErr)
}

// applyDigestCreds replaces existing Digest credentials of the exact
// realm in one header family, or appends new ones. Other realms, schemes,
// and non-auth fields stay untouched; a nil header map is allocated only
// here, at commit. Typed-nil header wrappers and credentials in the
// original request are skipped safely.
func applyDigestCreds(hdrs *sip.Headers, proxy bool, realm string, credentials *header.DigestCredentials) {
	if *hdrs == nil {
		*hdrs = make(sip.Headers)
	}

	name := sip.HeaderName("Authorization")
	if proxy {
		name = "Proxy-Authorization"
	}
	replaced := false
	for _, hdr := range hdrs.Get(name) {
		var authCreds *header.AuthCredentials
		switch h := hdr.(type) {
		case *header.Authorization:
			if !proxy && h != nil {
				authCreds = &h.AuthCredentials
			}
		case *header.ProxyAuthorization:
			if proxy && h != nil {
				authCreds = &h.AuthCredentials
			}
		}
		if authCreds == nil {
			continue
		}
		if dc, ok := (*authCreds).(*header.DigestCredentials); ok && dc != nil && dc.Realm == realm {
			*authCreds = credentials
			replaced = true
		}
	}
	if replaced {
		return
	}
	if proxy {
		*hdrs = hdrs.Append(&header.ProxyAuthorization{AuthCredentials: credentials})
	} else {
		*hdrs = hdrs.Append(&header.Authorization{AuthCredentials: credentials})
	}
}
