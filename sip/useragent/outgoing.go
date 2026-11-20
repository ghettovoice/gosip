package useragent

import (
	"context"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// buildReq builds the endpoint request an outgoing operation sends.
// The request carries the user agent's address of record as the From
// identity and the operation target as both Request-URI and To identity;
// the sequence number, Via branch, Call-ID, and tags follow
// [sip.NewRequest] defaults. The configured contact is attached only to
// OPTIONS requests: a MESSAGE request never carries Contact by package
// contract. A
// non-empty body always gets a Content-Type — the parsed contentType when
// one was supplied so media-type parameters survive, otherwise the
// text/plain default.
func (ua *UserAgent) buildReq(
	method sip.RequestMethod,
	target sip.AnyURI,
	body []byte,
	contentType string,
) (*sip.RequestEnvelope, error) {
	hdrs := make(sip.Headers)

	if ua.opts.Contact != nil && method.Equal(sip.RequestMethodOptions) {
		hdrs.Set(header.Contact{{URI: ua.opts.Contact.Clone()}})
	}

	if len(body) > 0 {
		mt := contentType
		if mt == "" {
			mt = "text/plain"
		}
		ct, err := sip.ParseHeader("Content-Type: " + mt)
		if err != nil {
			return nil, errors.Wrap(err)
		}
		hdrs.Set(ct)
	}

	req, err := sip.NewRequest(
		method,
		target,
		ua.opts.AOR,
		target,
		sip.RequestOptions{RPort: true, Headers: hdrs, Body: body},
	)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return sip.NewRequestEnvelope(req), nil
}

// runOp performs the operation's send through elm and resolves with
// the transaction's final response. The element owns RFC 3263 candidate
// failover inside a single logical send, so a send error — a transport or
// timeout failure, or the 503 that ends confirmation — propagates with its
// [sip.RequestAttemptError] chain intact instead of becoming a fake
// response. A 401/407 final response with a configured authenticator
// enters the authentication retry chain: each leg re-sends a clone of the
// transaction's request after the provider applied credentials, bounded
// by [Options.MaxAuthAttempts]. With a configured redirect policy, a
// 300/301/302 response enqueues its surviving contacts and the operation
// retargets the first approved one: the shared send budget also gates
// authentication retries, a new target's first send never carries
// credentials, and an ordinary final failure from a selected contact falls
// back to the redirect's next pending sibling while a 6xx global failure
// ends the chain.
func (ua *UserAgent) runOp(
	ctx context.Context,
	elm *sip.Element,
	req *sip.RequestEnvelope,
	opts OperationOptions,
) (*sip.ResponseEnvelope, error) {
	// The progress adapter reports only received 101-199 responses — the
	// element cannot surface a bare 100 — and hands the application a deep
	// clone. It is suppressed once the operation context is done: a
	// provisional arriving after cancellation must never reach the
	// application. Progress events can only arrive while the send below is
	// in flight, so the context check also covers terminal suppression.
	var onProgress func(context.Context, *sip.RequestAttempt, *sip.ResponseEnvelope)
	if opts.OnProgress != nil {
		onProgress = func(cbCtx context.Context, _ *sip.RequestAttempt, res *sip.ResponseEnvelope) {
			if ctx.Err() != nil {
				return
			}
			if sts := res.Status(); sts <= sip.ResponseStatusTrying || sts >= sip.ResponseStatusOK {
				return
			}
			opts.OnProgress(cbCtx, cloneOpRes(res))
		}
	}

	sendOpts := sip.SendRequestStatefulOptions{
		Timing:            opts.Timing,
		Logger:            ua.opts.Logger,
		OnAttemptProgress: onProgress,
	}

	// The redirect tracker wraps the operation context once, at entry —
	// the policy deadline bounds the whole chain and is never refreshed
	// between hops — and seeds the visited set with the initial target at
	// depth 0. A nil policy never allocates one.
	var rs *redirectState
	if ua.opts.RedirectPolicy != nil {
		var cancel context.CancelFunc
		rs, ctx, cancel = newRedirectState(ctx, ua.opts.RedirectPolicy)
		defer cancel()
		rs.visited = append(rs.visited, req.URI())
	}

	// authSends counts the authentication retries sent on the wire. It is
	// operation-wide and never resets, so a redirect hop shares the same
	// bound when redirect retargeting lands.
	var authSends int
	for {
		if rs != nil {
			// Every logical send — the initial request, an auth retry,
			// or a redirect hop — counts against the shared budget;
			// element DNS failover inside a send never does.
			rs.sends++
		}
		tx, err := elm.SendRequestStateful(ctx, req, sendOpts)
		if err != nil {
			return nil, errors.Wrap(err)
		}
		res := tx.LastResponse()
		if res == nil {
			return nil, errors.Wrap(NewNoFinalResponseError())
		}

		next, out, err := ua.nextSend(ctx, tx.Request(), res, rs, &authSends)
		if err != nil {
			return out, err
		}
		if next == nil {
			return out, nil
		}
		req = next
	}
}

// nextSend decides the operation's next move after res arrived: the retry
// request an authentication or redirect leg must send, or nil when res
// resolves the operation. The outcome is res itself, or the recorded last
// redirect when the policy's shared budget or its candidates ran out.
func (ua *UserAgent) nextSend(
	ctx context.Context,
	sent *sip.RequestEnvelope,
	res *sip.ResponseEnvelope,
	rs *redirectState,
	authSends *int,
) (*sip.RequestEnvelope, *sip.ResponseEnvelope, error) {
	sts := res.Status()
	switch {
	case isAuthChallenge(sts):
		return ua.authRetry(ctx, sent, res, rs, authSends)
	case rs != nil && (sts.IsRedirection() || (!sts.IsSuccessful() && !sts.IsGlobalFailure())):
		return rs.redirectRetry(ctx, sent, res)
	default:
		return nil, res, nil
	}
}

// authRetry produces the credential retry for a 401/407 challenge. A
// challenge without a configured authenticator resolves as an ordinary
// final response. The shared send budget gates retries on a redirect
// chain exactly like hops: exhausting it resolves the most recent
// redirect — or the challenge itself — without a synthetic error. The
// authentication counter keeps its own configured bound and still fails
// classified with the last challenge attached to the outcome.
func (ua *UserAgent) authRetry(
	ctx context.Context,
	sent *sip.RequestEnvelope,
	res *sip.ResponseEnvelope,
	rs *redirectState,
	authSends *int,
) (*sip.RequestEnvelope, *sip.ResponseEnvelope, error) {
	if ua.opts.Authenticator == nil {
		return nil, res, nil
	}
	if rs != nil && rs.sends >= rs.policy.MaxAttempts {
		return nil, rs.outcome(res), nil
	}
	if *authSends >= ua.opts.MaxAuthAttempts {
		return nil, res, errors.Wrap(NewAuthenticationLimitedError())
	}

	// The retry clones the transaction's sent request; the provider
	// mutates only that clone while the challenge reaches it as a
	// detached copy.
	retry, err := makeRetryReq(sent, nil)
	if err != nil {
		return nil, nil, errors.Wrap(err)
	}
	if err := ua.opts.Authenticator.AuthenticateRequest(ctx, retry, cloneOpRes(res)); err != nil {
		return nil, nil, errors.Wrap(err)
	}
	// A provider that returns after the operation was canceled must not
	// produce a packet: the context is re-checked before the retry is
	// counted and queued.
	if err := ctx.Err(); err != nil {
		return nil, nil, errors.Wrap(err)
	}
	(*authSends)++
	return retry, nil, nil
}
