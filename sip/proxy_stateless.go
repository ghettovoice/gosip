package sip

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/netip"
	"strconv"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip/header"
)

// statelessBranch derives the deterministic branch token for a stateless
// outbound copy of the request toward one target.
// RFC 3261 Section 16.11.
//
// A stateless proxy cannot distinguish a retransmission from the original
// request, so the branch component that carries uniqueness MUST be computed
// as a combinatoric function of message parameters that are invariant on
// retransmission; global randomness never participates. The derivation is a
// keyed SHA-256 (HMAC) over canonical length-delimited routing and matching
// fields plus the explicit proxy-stateless ownership marker:
//
//   - the proxy-stateless ownership marker, separating the token from any
//     other keyed derivation over the same input;
//   - the selected target identity (ID, URI, route set, and delivery
//     reference), binding the branch to the deterministic destination;
//   - the inbound Request-URI and Route set;
//   - the transaction-matching identity: Call-ID, From tag, and CSeq
//     sequence number. The method and the To tag are deliberately excluded
//     so a related CANCEL and a non-routed (in-transaction) ACK derive the
//     same branch as the request they relate to;
//   - the top Via identity: the received branch parameter for an RFC 3261
//     input, the whole topmost Via field for a legacy input.
//
// Target and Via values are cloned before they are hashed, so the
// derivation never aliases caller-owned state.
//
// The result is a complete SIP branch that carries the stateless ownership
// mark and is passed to [Proxy.finalizeTargetReq] as the
// caller-supplied uniqueness token; the finalize step wraps it in the
// proxy-namespace mark and the loop-detection fingerprint.
//
// A configured [ProxyOptions.StatelessKey] is required for explicit
// stateless routes: without one the derivation is not authenticated and the
// call reports an empty branch, which the finalize step rejects.
func (prx *Proxy) statelessBranch(req *RequestEnvelope, target ProxyTarget) string {
	return prx.statelessBranchWithKey(prx.opts.StatelessKey, req, target)
}

// statelessBranchWithKey derives the deterministic stateless branch under
// an explicit key, following the same contract as [Proxy.statelessBranch]:
// an empty key or nil request produces no token.
func (*Proxy) statelessBranchWithKey(key []byte, req *RequestEnvelope, target ProxyTarget) string {
	if req == nil || len(key) == 0 {
		return ""
	}

	mac := hmac.New(sha256.New, key)
	write := func(vals ...string) {
		for _, v := range vals {
			_, _ = mac.Write(util.AppendPrefixedString(nil, v))
		}
	}

	write("mark", prxBranchStatelessMark)

	if target.URI != nil {
		u := target.URI.Clone().(*URI) //nolint:forcetypeassert // the concrete type is fixed by construction
		write("trgt", util.ToLower(shared.Render(u)))
	}
	write("tid", target.ID)
	for _, hop := range target.Routes {
		write("tro", hop.Clone().String())
	}
	if target.Delivery != (ProxyDeliveryRef{}) {
		write("tdlv", target.Delivery.Kind, target.Delivery.Key)
	}

	req.WithMessage(func(r *Request) {
		write("ruri", util.ToLower(shared.Render(r.URI)))
		for hop := range r.Headers.Routes() {
			write("route", hop.Clone().String())
		}
		if callID, ok := r.Headers.CallID(); ok {
			write("callid", string(callID))
		}
		if from, ok := r.Headers.From(); ok {
			if tag, ok := from.Tag(); ok {
				write("ftag", tag)
			}
		}
		// The To tag is excluded: an in-transaction ACK carries the
		// acknowledged response's tag while the original request had none.
		if cseq, ok := r.Headers.CSeq(); ok {
			write("cseq", strconv.FormatUint(uint64(cseq.SeqNum), 10))
			// The method is excluded: a related CANCEL or non-routed ACK
			// shares the originating transaction's identity.
		}
		if via, ok := r.Headers.FirstVia(); ok {
			hop := via.Clone()
			if branch, ok := hop.Branch(); ok && IsRFC3261Branch(branch) {
				write("via-br", branch)
			} else {
				write("via", util.ToLower(hop.String()))
			}
		}
	})

	return MagicCookie + "." + prxBranchStatelessMark + "." + hex.EncodeToString(mac.Sum(nil))
}

const (
	// errNoStatelessDest reports that no deterministic destination —
	// a final remote address together with a bound client transport — can
	// be established for a stateless send.
	errNoStatelessDest errors.Error = "no deterministic stateless destination"
	// errPrxStatelessPromote marks a request that must be promoted to
	// stateful admission before the first send because its deterministic
	// stateless invariants cannot be established.
	errPrxStatelessPromote errors.Error = "stateless forward requires stateful promotion"
)

// statelessBranchToken derives the caller-supplied branch token for the
// stateless send of req toward target. The configured
// [ProxyOptions.StatelessKey] serves every explicitly stateless route; a
// proxy without one — a stateful-only proxy — falls back to its
// instance-local key solely for the response-ineligible methods whose
// forwarding is still mandatory without a transaction: the unmatched
// CANCEL of RFC 3261 Section 16.10 and the end-to-end ACK. The fallback
// never authorizes an explicitly configured stateless route: [NewProxy]
// rejects one configured without a key.
func (prx *Proxy) statelessBranchToken(req *RequestEnvelope, target ProxyTarget) string {
	if len(prx.opts.StatelessKey) > 0 {
		return prx.statelessBranch(req, target)
	}
	if req != nil && (req.Method().Equal(RequestMethodCancel) || req.Method().Equal(RequestMethodAck)) {
		return prx.statelessBranchWithKey(prx.slFallbackKey, req, target)
	}
	return ""
}

// statelessDest picks the single deterministic destination of one
// target — the final remote address and the bound client transport — that
// a stateless send commits to.
//
// A nonzero [ProxyDeliveryRef] is bound through
// [ProxyOptions.ResolveDelivery]. Otherwise the next-hop URI decides: a
// literal-IP next hop uses the built-in deterministic selection — the
// locator's numeric-target branch emits exactly one candidate without
// touching DNS — while a hostname or otherwise custom next hop is only
// reachable through the application-guaranteed
// [ProxyOptions.ResolveStatelessDestination] contract. Every failure is
// marked [errNoStatelessDestination] so the caller can decide whether the
// request may be promoted to stateful handling instead.
func (prx *Proxy) statelessDest(
	ctx context.Context,
	req, out *RequestEnvelope,
	target ProxyTarget,
) (ProxyBoundDelivery, error) {
	if target.Delivery != (ProxyDeliveryRef{}) {
		bound, err := prx.bindDelivery(ctx, target.Delivery)
		if err != nil {
			return ProxyBoundDelivery{}, errors.JoinWrap(errNoStatelessDest, err)
		}
		return bound, nil
	}

	trgt, err := prx.elm.ResolveRequestTarget(ctx, out)
	if err != nil {
		return ProxyBoundDelivery{}, errors.JoinWrap(errNoStatelessDest, err)
	}

	var trgtIP net.IP
	if maddr, ok := trgt.MAddr(); ok && maddr.IsValid() {
		trgtIP = maddr.IP()
	} else {
		trgtIP = trgt.Addr.IP()
	}

	if trgtIP == nil {
		if prx.opts.ResolveStatelessDestination == nil {
			return ProxyBoundDelivery{}, errors.PrefixWrap(
				errNoStatelessDest,
				"target %q requires a stable resolver",
				target.ID,
			)
		}
		return errors.Wrap2(prx.opts.ResolveStatelessDestination(ctx, req, target))
	}

	// Literal-IP targets use the built-in deterministic selection: the
	// locator emits exactly one candidate for a numeric next hop and never
	// consults DNS.
	prx.elm.ensureInit()
	addr, ok := util.SeqFirst(prx.elm.srvLctr.LookupRequestAddrs(
		ctx, trgt, LookupMessageAddrsOptions{StableDNSRecordsOrder: true},
	))
	if !ok {
		return ProxyBoundDelivery{}, errors.PrefixWrap(
			errNoStatelessDest,
			"no candidate for target %q",
			target.ID,
		)
	}
	tp, ok := prx.elm.TransportByProto(addr.Transport)
	if !ok {
		return ProxyBoundDelivery{}, errors.PrefixWrap(
			errNoStatelessDest,
			"no %s transport for target %q",
			addr.Transport, target.ID,
		)
	}
	return ProxyBoundDelivery{Transport: tp, RemoteAddr: addr.Addr}, nil
}

// prepareStatelessFwd builds the finalized stateless outbound copy of
// input.working for one target and resolves its single deterministic destination.
//
// Destination and branch invariants are established before the returned
// copy may be sent: a failure there is marked [errNoStatelessDestination]
// and wrapped with the [errPrxStatelessPromote] signal for a
// response-eligible request. Failures no forwarding mode can repair — a
// detected loop, a SIPS downgrade, an unusable Record-Route identity —
// surface unmarked. The bound delivery is validated before any send, so a
// missing bound flow fails without a dial.
func (prx *Proxy) prepareStatelessFwd(
	ctx context.Context,
	route *ProxyRoute,
	input prxRequestInput,
	target ProxyTarget,
) (*RequestEnvelope, ProxyBoundDelivery, error) {
	out, err := prx.prepareTargetReq(ctx, route, input, target)
	if err != nil {
		return nil, ProxyBoundDelivery{}, errors.Wrap(err)
	}

	req := input.working
	bound, err := prx.statelessDest(ctx, req, out, target)
	if err != nil {
		return nil, ProxyBoundDelivery{}, errors.Wrap(prx.statelessPromoteErr(req, err))
	}
	if bound.Transport == nil {
		return nil, ProxyBoundDelivery{}, errors.Wrap(prx.statelessPromoteErr(
			req,
			errors.PrefixWrap(errNoStatelessDest, "no bound transport for target %q", target.ID),
		))
	}
	if !bound.RemoteAddr.IsValid() {
		return nil, ProxyBoundDelivery{}, errors.Wrap(prx.statelessPromoteErr(
			req,
			errors.PrefixWrap(errNoStatelessDest, "no bound remote address for target %q", target.ID),
		))
	}

	out.SetTransport(bound.Transport.Metadata()).
		SetLocalAddr(bound.LocalAddr).
		SetRemoteAddr(bound.RemoteAddr)

	branch := prx.statelessBranchToken(req, target)
	if branch == "" {
		return nil, ProxyBoundDelivery{}, errors.Wrap(prx.statelessPromoteErr(
			req,
			errors.PrefixWrap(errNoStatelessDest, "no stateless branch key"),
		))
	}

	// The emitted copy is always stateless-owned: even a stateful route's
	// unmatched CANCEL takes the stateless send, so its Via carries the
	// stateless ownership mark regardless of the route mode.
	slRoute := route
	if slRoute != nil {
		mode := *slRoute
		mode.ForwardMode = ForwardModeStateless
		slRoute = &mode
	}
	if err := prx.finalizeTargetReq(ctx, slRoute, input, out, branch); err != nil {
		return nil, ProxyBoundDelivery{}, errors.Wrap(err)
	}

	return out, bound, nil
}

// statelessPromotable reports whether a request whose deterministic
// stateless invariants cannot be established may be re-admitted through
// the stateful pipeline before the first send. An ACK is never promoted —
// it has no response context — and an unmatched CANCEL stays on its
// mandatory stateless path of RFC 3261 Section 16.10: its undecidable
// routing is a routing failure, not a new response context.
func (prx *Proxy) statelessPromotable(req *RequestEnvelope) bool {
	if req == nil {
		return false
	}
	switch m := req.Method(); {
	case m.Equal(RequestMethodAck):
		return false
	case m.Equal(RequestMethodCancel):
		return prx.statelessCancelMatched(req)
	default:
		return true
	}
}

// statelessCancelMatched reports whether the CANCEL relates to an upstream
// request still owned by a live proxy coordinator: the related INVITE
// shares the whole server transaction identity with only the CSeq method
// differing.
func (prx *Proxy) statelessCancelMatched(req *RequestEnvelope) bool {
	key, err := cancelMatchKey(req)
	if err != nil {
		return false
	}
	_, ok := prx.loadSrvReq(key)
	return ok
}

// statelessPromoteErr marks a deterministic-invariant failure with the
// promotion signal when the request is response-eligible; otherwise the
// failure is returned unchanged.
func (prx *Proxy) statelessPromoteErr(req *RequestEnvelope, err error) error {
	if prx.statelessPromotable(req) {
		return errors.JoinWrap(errPrxStatelessPromote, err)
	}
	return errors.Wrap(err)
}

// forwardStateless sends one stateless outbound copy of req toward a single
// target.
//
// It commits to exactly one destination — a final remote address and a
// bound client transport — resolved by [Proxy.prepareStatelessFwd] and
// sends through the bound transport's own sender, which preserves the
// element observation and preparation chain: [RequestAttempt.DoStateless]
// is never used because its implicit transport retries and size fallbacks
// would break the one-destination guarantee. A failure before the send
// carries the [errPrxStatelessPromote] signal when the request is
// response-eligible; a send failure reports the error with no
// alternate-destination retry.
func (prx *Proxy) forwardStateless(
	ctx context.Context,
	route *ProxyRoute,
	req *RequestEnvelope,
	target ProxyTarget,
) error {
	out, bound, err := prx.prepareStatelessFwd(ctx, route, prxRequestInput{original: req, working: req}, target)
	if err != nil {
		return errors.Wrap(err)
	}
	return errors.Wrap(bound.Transport.SendRequest(ctx, out))
}

// prxResMark returns the proxy ownership mark embedded in the top Via
// branch of res, reporting false when the response is outside this
// proxy's branch namespace.
func prxResMark(res *ResponseEnvelope) (mark, fingerprint string, ok bool) {
	if res == nil {
		return "", "", false
	}
	res.WithMessage(func(r *Response) {
		via, found := r.Headers.FirstVia()
		if !found || via == nil {
			return
		}
		branch, found := via.Branch()
		if !found {
			return
		}
		mark, fingerprint, ok = parsePrxBranch(branch)
	})
	return mark, fingerprint, ok
}

// shouldForwardRes reports whether res is authentically owned by this
// proxy instance's stateless path: the top Via carries the
// proxy-stateless branch mark and its sent-by names one of this
// element's transport addresses. The mark alone never authorizes the
// claim — a foreign element emits the same namespace on its own Via, so
// the Via itself must prove it is the one this instance inserted.
func (prx *Proxy) shouldForwardRes(ctx context.Context, res *ResponseEnvelope) bool {
	if res == nil {
		return false
	}

	var via header.ViaHop
	res.WithMessage(func(r *Response) {
		if v, ok := r.Headers.FirstVia(); ok && v != nil {
			via = v.Clone()
		}
	})
	branch, ok := via.Branch()
	if !ok {
		return false
	}
	mark, _, ok := parsePrxBranch(branch)
	if !ok || mark != prxBranchStatelessMark {
		return false
	}
	return via.IsValid() && prx.matchResSentBy(ctx, via)
}

// matchResSentBy reports whether the Via sent-by names this element: the
// receiving transport carried by the context is authoritative — the Via
// must name its protocol and one of its public addresses — otherwise the
// transport the Via was inserted for decides.
func (prx *Proxy) matchResSentBy(ctx context.Context, via header.ViaHop) bool {
	if tp, ok := TransportFromContext(ctx); ok {
		return tp.Metadata().Proto.Equal(via.Transport) && tp.MatchSentBy(via.Addr)
	}
	tp, ok := prx.elm.TransportByProto(via.Transport)
	return ok && tp != nil && tp.MatchSentBy(via.Addr)
}

// hasLiveClnTx reports whether res matches a live client transaction
// of the stateful path.
func (prx *Proxy) hasLiveClnTx(res *ResponseEnvelope) bool {
	key, err := MakeClientTransactionKey(res)
	if err != nil {
		return false
	}
	tx, ok := prx.elm.LoadClientTransaction(key)
	return ok && tx != nil && tx.State() != TransactionStateTerminated
}

// forwardStatelessRes returns an inbound response owned by this proxy's
// stateless path to the prior hop recorded in the Via stack.
// RFC 3261 Section 16.7.
//
// An owned copy is cloned — the inbound envelope is never mutated — and
// exactly one top Via, the one this instance inserted, is removed. The
// copy carries no transport or address provenance: the recorded next Via
// alone decides the destination through that hop's bound transport and
// its own response sender, which applies the RFC 3261 Section 18.2.2
// received/rport resolution to the recorded prior hop. The forwarded
// To-tag preservation marker keeps the element and transport from
// inventing a dialog tag. No transaction is created and no synthetic
// context is fabricated.
//
// A response with no remaining Via belongs to a locally generated
// operation — for example a locally answered CANCEL — and is consumed
// rather than forwarded; an unroutable next hop is discarded the same
// way with no alternate routing.
func (prx *Proxy) forwardStatelessRes(ctx context.Context, res *ResponseEnvelope) error {
	out := cloneResEnvelope(res)

	var (
		nextVia header.ViaHop
		hasNext bool
	)
	out.WithMessage(func(r *Response) {
		r.Headers.PopFirstVia()
		if v, ok := r.Headers.FirstVia(); ok && v != nil {
			nextVia = v.Clone()
			hasNext = true
		}
		// A forwarded response keeps its dialog identity verbatim.
		r.preserveToTag = true
	})
	if !hasNext || !nextVia.IsValid() {
		return nil
	}

	tp, ok := prx.elm.TransportByProto(nextVia.Transport)
	if !ok || tp == nil {
		return errors.Wrap(NewNoTransportError())
	}

	out.SetTransport(TransportMetadata{}).
		SetLocalAddr(netip.AddrPort{}).
		SetRemoteAddr(netip.AddrPort{})

	err := tp.SendResponse(ctx, out)
	if err == nil {
		// A standalone stateless relay is its own observation: it carries
		// no fabricated request ID, sequence, or route association — only
		// the detached result data the wire result proved.
		prx.emitEvt(ctx, ProxyEvent{
			Kind:       ProxyEventResponseForwarded,
			Time:       time.Now(),
			StatusCode: int(out.Status()),
			Method:     resMethodOf(out),
		})
	}
	return errors.Wrap(err)
}

// resMethodOf returns the CSeq method carried by a response envelope.
func resMethodOf(res *ResponseEnvelope) (m RequestMethod) {
	res.WithMessage(func(r *Response) {
		if cseq, ok := r.Headers.CSeq(); ok && cseq != nil {
			m = cseq.Method
		}
	})
	return m
}
