package sip

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip/header"
)

// prxRequestInput keeps incoming security provenance separate from accepted
// routing state. Both envelopes are owned at the execution boundary; hooks,
// fingerprints, and Record-Route selection retain their working-request view.
type prxRequestInput struct {
	original, working *RequestEnvelope
}

// prxRequiresSIPS reports the original Request-URI's security requirement.
// Receiving a SIP URI over TLS does not itself require a SIPS Request-URI.
func prxRequiresSIPS(original *RequestEnvelope) bool {
	if original == nil {
		return false
	}
	u, ok := original.URI().(*URI)
	return ok && u != nil && u.Secured
}

// validatePrxSecureRequest rejects lost incoming SIPS constraints independently
// of transport selection, including contact, hook, and strict-route rewrites.
func validatePrxSecureRequest(original, out *RequestEnvelope) error {
	if prxRequiresSIPS(original) && !prxRequiresSIPS(out) {
		return errors.PrefixWrap(errSecureDowngrade, "incoming sips request requires a sips request URI")
	}
	return nil
}

// prepareTargetReq builds the owned outbound branch copy of the working
// request for one forwarding target.
// RFC 3261 Section 16.6 items 1-3 and the Route postprocessing rule.
//
// Neither input envelope is mutated or returned; every call clones the working
// request, so fork copies for sibling targets and address retries over the
// same input are fully independent. The copy drops the inbound
// transaction binding and transport/address provenance and is marked as a
// forwarded message, so endpoint conveniences (such as an invented From tag)
// stay off in every downstream send helper.
//
// Processing on the copy follows the protocol order: the target URI is
// cleaned and installed as the Request-URI, a present Max-Forwards is
// decremented exactly once per preparation while an absent one is filled
// with the default without a decrement, and the per-target route set is
// applied. The route's [ProxyRoute.AdjustOutboundRequest] hook then runs and
// its output is revalidated. Finally a non-"lr" first Route value (strict
// next hop) moves into the Request-URI while the target URI becomes the last
// Route value. Via and Record-Route are transport-dependent and are added
// later once the transport is known.
func (*Proxy) prepareTargetReq(
	ctx context.Context,
	route *ProxyRoute,
	input prxRequestInput,
	target ProxyTarget,
) (*RequestEnvelope, error) {
	if target.URI == nil {
		return nil, errors.ErrorfWrap("proxy target %q has no URI", target.ID)
	}

	// 1. Make a copy of the received request.
	out := cloneReqEnvelope(input.working)
	out.Metadata().
		Delete(txKeyMetaKey, txTypeMetaKey, prxFinalizedMetaKey)

	out.SetTransport(TransportMetadata{}).
		SetLocalAddr(netip.AddrPort{}).
		SetRemoteAddr(netip.AddrPort{}).
		WithMessage(func(r *Request) {
			r.forwarded = true

			// 2. Update the Request-URI.
			r.URI = cleanReqURI(target.URI)

			// 3. Update the Max-Forwards header field: an existing value is
			// decremented exactly once per preparation; an absent header is
			// filled with the default and is not decremented again.
			if mf, ok := r.Headers.MaxForwards(); ok {
				if mf > 0 {
					r.Headers.Set(mf - 1)
				}
			} else {
				EnsureRequestMaxForwards(r)
			}

			// Apply the per-target route set.
			for _, hop := range slices.Backward(target.Routes) {
				r.Headers.PrependRoute(hop.Clone())
			}
		})

	// 5. Optionally add additional header fields.
	if route != nil && route.AdjustOutboundRequest != nil {
		if err := route.AdjustOutboundRequest(ctx, input.working, out, target); err != nil {
			return nil, errors.Wrap(err)
		}
		if err := out.Validate(); err != nil {
			return nil, errors.Wrap(err)
		}
	}

	// 6. Postprocess routing information: if the copy contains a Route
	// header field whose first value's URI does not contain an "lr"
	// parameter (a strict next hop), the proxy places the Request-URI into
	// the Route header field as the last value, then places that first
	// Route value into the Request-URI and removes it from the Route
	// header field.
	out.WithMessage(func(r *Request) {
		hop, ok := r.Headers.FirstRoute()
		if !ok {
			return
		}
		hu, ok := hop.URI.(*URI)
		if !ok || hu.Params.Has("lr") {
			return
		}

		popped, ok := r.Headers.PopFirstRoute()
		if !ok {
			return
		}
		r.Headers.AppendRoute(header.RouteHop{URI: r.URI})
		r.URI = popped.URI
	})

	if err := validatePrxSecureRequest(input.original, out); err != nil {
		return nil, errors.Wrap(err)
	}
	return out, nil
}

// Proxy-owned Via branch namespace.
//
// Every Via value the proxy inserts carries one of these marks inside the
// RFC 3261 magic-cookie space. A branch is composed as
// "<MagicCookie>.<mark>.<fingerprint>.<token>": the mark identifies the
// stateful or stateless namespace, the fingerprint supports optional loop
// detection, and the caller supplies a per-attempt stateful token or a
// keyed deterministic stateless token. Namespace membership alone does not
// prove local ownership; the Via's transport and advertised sent-by must
// also match this instance.
const (
	prxBranchStatefulMark  = "proxy-stateful"
	prxBranchStatelessMark = "proxy-stateless"

	// prxBranchFingerprintLen is the hex length of the loop-detection
	// fingerprint embedded in a proxy-namespace branch.
	prxBranchFingerprintLen = 32
)

// prxFinalizedMetaKey marks an outbound copy that already passed
// [Proxy.finalizeTargetReq]. [Proxy.prepareTargetReq] strips it
// from every fresh clone, so a copy prepared from a looped-back request is
// finalized again while the same envelope can never be finalized twice.
const prxFinalizedMetaKey = "sip.proxy_finalized"

// makePrxBranch composes a proxy-namespace Via branch from the ownership
// mark, the loop-detection fingerprint, and the caller-owned token.
func makePrxBranch(mark, fingerprint, token string) string {
	return MagicCookie + "." + mark + "." + fingerprint + "." + token
}

// parsePrxBranch splits a proxy-namespace Via branch into its ownership
// mark and embedded loop-detection fingerprint. It reports false for any
// branch outside the proxy namespace.
func parsePrxBranch(branch string) (mark, fingerprint string, ok bool) {
	rest, ok := strings.CutPrefix(branch, MagicCookie+".")
	if !ok {
		return "", "", false
	}
	for _, m := range []string{prxBranchStatefulMark, prxBranchStatelessMark} {
		rest2, ok := strings.CutPrefix(rest, m+".")
		if !ok {
			continue
		}
		fp, _, ok := strings.Cut(rest2, ".")
		if !ok || len(fp) != prxBranchFingerprintLen {
			return "", "", false
		}
		return m, fp, true
	}
	return "", "", false
}

// isPrxBranch reports whether the branch belongs to this proxy's Via
// namespace.
func isPrxBranch(branch string) bool {
	_, _, ok := parsePrxBranch(branch)
	return ok
}

const (
	errLoopDetected       errors.Error = "request loop detected"
	errNoRecRouteIdentity errors.Error = "no usable record-route identity"
	errAmbiguousIdentity  errors.Error = "ambiguous record-route identity"
	errSecureDowngrade    errors.Error = "secure route cannot downgrade"
)

// routeFingerprint computes the loop-detection fingerprint for one outbound
// copy. The fingerprint reflects the routing decision (the outbound
// Request-URI, Route set, Proxy-Require, and Proxy-Authorization fields)
// plus the transaction identity of the received request (Call-ID, From tag,
// To tag, and CSeq sequence number), so a request returning with unchanged
// routing-relevant data reproduces the previous fingerprint while a
// changed-route spiral does not.
//
// Proxy-Authorization material feeds the digest only; authentication data
// is never logged or rendered into error values.
func routeFingerprint(in, out *RequestEnvelope) string {
	return prxRouteFingerprint(in, out, false)
}

// routeFingerprintStateless mirrors the related-method exclusions of the
// stateless branch token: the To tag is excluded so a non-routed ACK
// carrying the acknowledged response's tag derives the same fingerprint,
// and the same complete Via branch, as the request it relates to.
func routeFingerprintStateless(in, out *RequestEnvelope) string {
	return prxRouteFingerprint(in, out, true)
}

func prxRouteFingerprint(in, out *RequestEnvelope, stateless bool) string {
	h := sha256.New()
	write := func(parts ...string) {
		for _, p := range parts {
			_, _ = h.Write([]byte(p))
			_, _ = h.Write([]byte{0})
		}
	}

	out.WithMessage(func(r *Request) { writeRouteAttrs(write, r) })

	if in != nil {
		in.WithMessage(func(r *Request) { writeTxAttrs(write, r, stateless) })
	}

	sum := h.Sum(nil)
	return hex.EncodeToString(sum[:prxBranchFingerprintLen/2])
}

func writeRouteAttrs(write func(parts ...string), r *Request) {
	write("ruri", shared.Render(r.URI))
	for hop := range r.Headers.Routes() {
		write("route", hop.String())
	}
	for opt := range r.Headers.ProxyRequireOptions() {
		write("preq", string(opt))
	}
	for _, hdr := range r.Headers.Get("Proxy-Authorization") {
		write("pauth", shared.Render(hdr))
	}
}

func writeTxAttrs(write func(parts ...string), r *Request, stateless bool) {
	if callID, ok := r.Headers.CallID(); ok {
		write("callid", string(callID))
	}
	if from, ok := r.Headers.From(); ok {
		if tag, ok := from.Tag(); ok {
			write("ftag", tag)
		}
	}
	// The To tag is skipped in stateless mode, matching the token
	// exclusions of the stateless branch derivation: an in-transaction ACK
	// carries the acknowledged response's tag while the original request
	// had none.
	if !stateless {
		if to, ok := r.Headers.To(); ok {
			if tag, ok := to.Tag(); ok {
				write("ttag", tag)
			}
		}
	}
	if cseq, ok := r.Headers.CSeq(); ok {
		write("cseq", strconv.FormatUint(uint64(cseq.SeqNum), 10))
	}
}

// ownsVia checks local advertised sent-by ownership using the old Via's
// transport, not the transport that received the returning request.
func (prx *Proxy) ownsVia(via header.ViaHop) bool {
	if !via.IsValid() {
		return false
	}
	tp, ok := prx.elm.TransportByProto(via.Transport)
	return ok && tp != nil && tp.MatchSentBy(via.Addr)
}

// detectLoop reports whether the outbound copy repeats a routing
// fingerprint this proxy already emitted for the same request.
// RFC 3261 Section 16.3 item 4 and Section 16.6 item 8.
//
// Only locally owned Vias with usable proxy-namespace branches are witnesses;
// a shared namespace or fingerprint alone does not establish ownership.
// Every owned hop is checked, so foreign or changed-route hops cannot hide a
// deeper match. The branch mark selects [routeFingerprintStateless] or
// [routeFingerprint] normalization; only changed fingerprints remain a spiral.
// Callers invoke this only when [ProxyOptions.DetectLoops] is enabled.
func (prx *Proxy) detectLoop(in, out *RequestEnvelope) bool {
	if in == nil || out == nil {
		return false
	}

	statefulFP := routeFingerprint(in, out)
	statelessFP := routeFingerprintStateless(in, out)
	var loop bool
	in.WithMessage(func(r *Request) {
		for hop := range r.Headers.Vias() {
			if hop == nil || !prx.ownsVia(*hop) {
				continue
			}
			branch, ok := hop.Branch()
			if !ok {
				continue
			}
			mark, prev, ok := parsePrxBranch(branch)
			if !ok {
				continue
			}
			fp := statefulFP
			if mark == prxBranchStatelessMark {
				fp = statelessFP
			}
			if prev == fp {
				loop = true
				return
			}
		}
	})
	return loop
}

// chooseRecRoute selects the advertised Record-Route URI for one outbound
// copy. RFC 3261 Section 16.6 item 4.
//
// A configured [ProxyOptions.RecordRouteURI] hook is consulted first; its
// result is cloned before use, so the application keeps ownership of the
// returned value. Without a hook, exactly one compatible configured
// [ProxyOptions.LocalIdentities] entry must exist: compatible means usable
// as an advertised reachable address and matching the secure scheme the
// direction requires. Zero or multiple compatible identities, an unusable
// configured or hook-selected URI, and a hook error are all explicit
// failures — the proxy never guesses a first host interface.
func (prx *Proxy) chooseRecRoute(ctx context.Context, in, out *RequestEnvelope) (*URI, error) {
	secure := recRouteNeedsSecure(in, out)

	var u *URI
	if prx.opts.RecordRouteURI != nil {
		hu, err := prx.opts.RecordRouteURI(ctx, in, out)
		if err != nil {
			return nil, errors.Wrap(err)
		}
		if hu == nil {
			return nil, errors.PrefixWrap(errNoRecRouteIdentity, "hook returned nil URI")
		}
		u = hu.Clone().(*URI) //nolint:forcetypeassert // the concrete type is fixed by construction
	} else {
		var compat []*URI
		for _, id := range prx.opts.LocalIdentities {
			if recRouteURIUsable(id) && id.Secured == secure {
				compat = append(compat, id)
			}
		}
		switch len(compat) {
		case 0:
			return nil, errors.PrefixWrap(errNoRecRouteIdentity, "no compatible configured identity")
		case 1:
			u = compat[0].Clone().(*URI) //nolint:forcetypeassert // the concrete type is fixed by construction
		default:
			return nil, errors.PrefixWrap(
				errAmbiguousIdentity,
				"%d compatible configured identities", len(compat),
			)
		}
	}

	if !recRouteURIUsable(u) {
		return nil, errors.PrefixWrap(errNoRecRouteIdentity, "unusable URI %q", u)
	}
	if secure && !u.Secured {
		// A secure route is never answered with a weaker advertised URI.
		return nil, errors.PrefixWrap(errSecureDowngrade, "record-route %q", u)
	}

	if u.Params == nil {
		u.Params = make(Values)
	}
	u.Params.Set("lr", "")
	return u, nil
}

// recRouteURIUsable reports whether the URI can serve as a reachable
// advertised Record-Route identity.
func recRouteURIUsable(u *URI) bool {
	if !u.IsValid() || !u.Addr.IsValid() {
		return false
	}
	if ip := u.Addr.IP(); ip != nil && ip.IsUnspecified() {
		// A wildcard listen address is not a reachable advertised identity.
		return false
	}
	return true
}

// nextHopIsSecure reports whether the request's next hop requires a secure
// scheme: the outbound Request-URI or the first Route value names a SIPS
// URI.
func nextHopIsSecure(r *Request) bool {
	if u, ok := r.URI.(*URI); ok && u.Secured {
		return true
	}
	if hop, ok := r.Headers.FirstRoute(); ok {
		if hu, ok := hop.URI.(*URI); ok && hu.Secured {
			return true
		}
	}
	return false
}

// recRouteNeedsSecure reports whether the advertised Record-Route must
// carry the secure scheme: the request arrived over a secured transport or
// its next hop requires SIPS.
func recRouteNeedsSecure(in, out *RequestEnvelope) bool {
	if in != nil && in.Transport().Secured() {
		return true
	}
	var secure bool
	out.WithMessage(func(r *Request) { secure = nextHopIsSecure(r) })
	return secure
}

// recRouteRequired reports whether an advertised Record-Route must be
// inserted: either the route asks for it or a SIPS boundary transition
// mandates it so in-dialog requests can return through this proxy with
// matching security. RFC 3261 Section 16.6 item 4.
func recRouteRequired(route *ProxyRoute, in, out *RequestEnvelope) bool {
	if route != nil && route.RecordRoute {
		return true
	}
	var outSecure bool
	out.WithMessage(func(r *Request) { outSecure = nextHopIsSecure(r) })
	return in != nil && in.Transport().Secured() != outSecure
}

// prxViaSentBy builds the sent-by address for the proxy's own Via hop.
// The bound local address wins; otherwise a syntactically valid placeholder
// is used and the selected transport fills the real sent-by on the wire.
func prxViaSentBy(out *RequestEnvelope) Addr {
	if laddr := out.LocalAddr(); laddr.Addr().IsValid() && laddr.Port() > 0 {
		return MakeHostPortAddr(laddr.Addr().String(), laddr.Port())
	}
	return MakeHostAddr(util.RandString(8) + ".invalid")
}

// finalizeTargetReq applies the transport-dependent transformations to
// a prepared outbound copy once the transport is known.
// RFC 3261 Section 16.6 items 4, 8, and 9.
//
// It consumes the transport and local/remote addresses already selected on
// out, verifies the original SIPS URI constraint and secure-next-hop transport
// requirement, performs optional loop detection, inserts the
// advertised Record-Route when required, and prepends exactly one
// proxy-owned Via carrying a fresh owned branch for this outer address
// attempt — branch is the caller-supplied uniqueness token wrapped in the
// proxy namespace marker and loop-detection fingerprint. Attempt and
// stateless callers invoke it after the transport is chosen and before the
// send; the element's pre-wire UDP-to-reliable size switch remains the only
// branch-reuse exception.
func (prx *Proxy) finalizeTargetReq(
	ctx context.Context,
	route *ProxyRoute,
	input prxRequestInput,
	out *RequestEnvelope,
	branch string,
) error {
	tp := out.Transport()
	if !tp.IsValid() {
		return errors.ErrorWrap("proxy outbound request has no selected transport")
	}
	if branch == "" {
		return errors.ErrorWrap("proxy outbound request has no owned branch token")
	}

	if err := validatePrxSecureRequest(input.original, out); err != nil {
		return errors.Wrap(err)
	}
	var sipsNextHop bool
	out.WithMessage(func(r *Request) { sipsNextHop = nextHopIsSecure(r) })
	if (prxRequiresSIPS(input.original) || sipsNextHop) && !tp.Secured() {
		return errors.PrefixWrap(errSecureDowngrade, "sips next hop over %s", tp.Proto)
	}
	in := input.working

	// Exactly one finalize per prepared copy; a second call would corrupt
	// upstream response routing. All pre-checks stay read-only so a rejected
	// call leaves the copy untouched. A previous-cycle own Via deeper in the
	// stack (a looped-back request) is not a duplicate.
	if out.Metadata().Has(prxFinalizedMetaKey) {
		return errors.ErrorWrap("proxy Via already inserted")
	}

	if prx.opts.DetectLoops && prx.detectLoop(in, out) {
		return errors.Wrap(NewRequestRejectedError(
			errLoopDetected,
			slog.LevelDebug,
			ResponseStatusLoopDetected,
		))
	}

	// Insert the advertised Record-Route before the own Via, following the
	// protocol item order.
	if recRouteRequired(route, in, out) {
		rrURI, err := prx.chooseRecRoute(ctx, in, out)
		if err != nil {
			return errors.Wrap(err)
		}
		out.WithMessage(func(r *Request) {
			r.Headers.PrependRecordRoute(header.RouteHop{URI: rrURI})
		})
	}

	mark := prxBranchStatefulMark
	fp := routeFingerprint(in, out)
	if route != nil && route.ForwardMode == ForwardModeStateless {
		mark = prxBranchStatelessMark
		fp = routeFingerprintStateless(in, out)
	}
	fullBranch := makePrxBranch(mark, fp, branch)

	out.WithMessage(func(r *Request) {
		r.Headers.PrependVia(header.ViaHop{
			Proto:     protoVer20,
			Transport: tp.Proto,
			Addr:      prxViaSentBy(out),
			Params:    make(Values).Set("branch", fullBranch),
		})
	})
	out.Metadata().Set(prxFinalizedMetaKey, true)

	return errors.Wrap(out.Validate())
}
