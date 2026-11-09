package sip

import (
	"context"
	"log/slog"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip/header"
)

// validateReq validates an inbound request claimed by a route.
// RFC 3261 Section 16.3.
//
// The checks run in protocol order and never reject a request merely for an
// unknown method or an unrelated extension header. A failed check produces an
// inspectable [RequestRejectedError] carrying the suggested response and its
// options; non-rejection failures propagate unchanged. Method response rules
// are applied last: an ACK is never answered, so a rejected ACK is silently
// consumed instead, and a CANCEL is never authentication-challenged.
func (prx *Proxy) validateReq(_ context.Context, _ *ProxyRoute, req *RequestEnvelope) error {
	return adjustReqRejection(req, prx.checkReq(req))
}

// checkReq runs the RFC 3261 Section 16.3 checks in order and reports a
// failed check as a [RequestRejectedError].
func (prx *Proxy) checkReq(req *RequestEnvelope) error {
	// 1. Reasonable syntax check.
	if err := req.Validate(); err != nil {
		return errors.Wrap(NewRequestRejectedError(err, slog.LevelDebug, ResponseStatusBadRequest))
	}

	// 2. URI scheme check.
	switch ruri := req.URI(); ruri.Scheme() {
	case "sip", "sips":
	default:
		return errors.Wrap(NewRequestRejectedError(
			errUnsupURIScheme,
			slog.LevelDebug,
			ResponseStatusUnsupportedURIScheme,
		))
	}

	cpb := prx.elm.Capabilities()

	var err error
	req.WithMessage(func(r *Request) {
		// 3. Max-Forwards check.
		if maxFwd, ok := r.Headers.MaxForwards(); ok && maxFwd == 0 {
			err = errors.Wrap(exhaustedHopsRejection(r, cpb))
		}
	})
	if err != nil {
		return err
	}

	// 4. Optional loop detection check.
	// The received request serves as both fingerprint inputs at admission:
	// its own routing data is compared with the fingerprint embedded in a
	// previous own-Via.
	if prx.opts.DetectLoops && prx.detectLoop(req, req) {
		return errors.Wrap(NewRequestRejectedError(
			errLoopDetected,
			slog.LevelDebug,
			ResponseStatusLoopDetected,
		))
	}

	req.WithMessage(func(r *Request) {
		// 5. Proxy-Require check.
		var unsupOpts header.Unsupported
		for opt := range r.Headers.ProxyRequireOptions() {
			if !cpb.SupportsOption(opt) {
				unsupOpts = append(unsupOpts, opt)
			}
		}

		if len(unsupOpts) > 0 {
			err = errors.Wrap(NewRequestRejectedError(
				errUnsupOption,
				slog.LevelDebug,
				ResponseStatusBadExtension,
				RespondOptions{
					ResponseOptions: ResponseOptions{
						Headers: make(Headers).Set(unsupOpts),
					},
				},
			))
		}
	})

	return err
}

// exhaustedHopsRejection builds the rejection for a request that arrived with
// an exhausted Max-Forwards value. Under the documented local-answer policy an
// OPTIONS request is answered with a 200 response carrying the element
// capabilities instead of the usual 483 rejection; any other method is
// rejected with 483.
func exhaustedHopsRejection(req *Request, cpb ElementCapabilities) *RequestRejectedError {
	if req.Method.Equal(RequestMethodOptions) {
		return NewRequestRejectedError(
			errTooManyHops,
			slog.LevelDebug,
			ResponseStatusOK,
			RespondOptions{
				ResponseOptions: ResponseOptions{
					Headers: make(Headers).Set(
						cpb.NewSupportedHeader(),
						cpb.NewAcceptHeader(),
					),
				},
			},
		)
	}

	return NewRequestRejectedError(errTooManyHops, slog.LevelDebug, ResponseStatusTooManyHops)
}

// adjustReqRejection applies the method response rules to a validation
// failure: an ACK is never answered and a CANCEL is never
// authentication-challenged, so a suggested response the method forbids is
// replaced by silent consumption of the request.
// RFC 3261 Sections 9.2 and 16.3.
func adjustReqRejection(req *RequestEnvelope, err error) error {
	if err == nil {
		return nil
	}

	rej, ok := errors.AsType[*RequestRejectedError](err)
	if !ok {
		return err
	}

	switch {
	case req.Method().Equal(RequestMethodAck):
		return errors.JoinWrap(errPrxReqConsumed, rej.cause)
	case req.Method().Equal(RequestMethodCancel) &&
		(rej.resSts.Equal(ResponseStatusUnauthorized) ||
			rej.resSts.Equal(ResponseStatusProxyAuthenticationRequired)):
		return errors.JoinWrap(errPrxReqConsumed, rej.cause)
	default:
		return err
	}
}

// preprocessRouting rewrites the inbound request routing information on an
// owned working copy and returns it.
// RFC 3261 Section 16.4.
//
// The inbound envelope is never mutated; target resolution and forwarding
// proceed from the returned copy. The route's [ProxyRoute.PreprocessRoute]
// hook runs on the copy before the core consumes local Route values, so it
// can inspect a local topmost Route while it is still present; the hook's
// output is then revalidated before consumption. Processing follows the
// Section 16.4 order: strict-router recovery restores the final Route URI
// into the Request-URI when the Request-URI names a configured local
// identity, a local "maddr" the proxy is responsible for is stripped when
// the request was received on the indicated transport and port, and a local
// topmost Route value is removed. Non-local Route values are preserved.
func (prx *Proxy) preprocessRouting(
	ctx context.Context,
	route *ProxyRoute,
	req *RequestEnvelope,
) (*RequestEnvelope, error) {
	out := cloneReqEnvelope(req)

	if route != nil && route.PreprocessRoute != nil {
		if err := route.PreprocessRoute(ctx, out); err != nil {
			return nil, errors.Wrap(err)
		}
		if err := prx.validateReq(ctx, route, out); err != nil {
			return nil, errors.Wrap(err)
		}
	}

	prx.consumeLocRouting(out)

	return out, nil
}

// consumeLocRouting applies the deterministic RFC 3261 Section 16.4
// consumption to a routing working copy in protocol order: strict-router
// recovery restores the final Route URI into the Request-URI when the
// Request-URI names a configured local identity, a local "maddr" the
// proxy is responsible for is stripped when the request was received on
// the indicated transport and port, and a local topmost Route value is
// removed. Non-local Route values are preserved. Unlike
// [Proxy.preprocessRouting] it runs no application hook and no
// revalidation. Consumption runs once for a fresh working request after
// [ProxyRoute.PreprocessRoute]; restoration adopts the saved WorkingRequest
// without replaying the hook or local-route consumption.
func (prx *Proxy) consumeLocRouting(out *RequestEnvelope) {
	out.WithMessage(func(r *Request) {
		prx.processReqStrictRouting(out, r)
		prx.processReqURIMAddr(out, r)
		prx.processReqFirstRoute(out, r)
	})
}

// isServedAddr reports whether the address names a domain the proxy's
// location service is responsible for. A served domain authorizes a location
// lookup; it is not one of the proxy's owned routing identities.
func (prx *Proxy) isServedAddr(addr Addr) bool {
	return matchPrxAddr(prx.domainAddrs, addr, 0)
}
