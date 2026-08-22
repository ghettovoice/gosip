package sip

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"net/netip"
	"slices"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/netutil"
	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip/header"
)

// Proxy sentinel errors.
const (
	ErrForwardContextDuplicate Error = "duplicate forward context"
	ErrForwardContextNotFound  Error = "forward context not found"

	errUnsupURIScheme Error = "unsupported URI scheme"
	errToManyHops     Error = "too many hops"
	errUnsupOption    Error = "unsupported option"
	errAuthRequired   Error = "authentication required"
	errNoFwdTargets   Error = "no forward targets"
)

type Proxy struct {
	elm         *Element
	routes      []ProxyRoute
	locAddrs    []Addr
	fwdCtxStore ForwardContextStore
}

var (
	_ InboundRequestInterceptor  = (*Proxy)(nil)
	_ InboundResponseInterceptor = (*Proxy)(nil)
)

type ProxyOptions struct {
	LocalAddrs          []Addr
	ForwardContextStore ForwardContextStore
}

func (o ProxyOptions) locAddrs() []Addr {
	if len(o.LocalAddrs) > 0 {
		return o.LocalAddrs
	}

	addrs := make([]Addr, 0, 2)

	for ip := range netutil.AllHostIPs() {
		addrs = append(addrs, AddrFromIP(ip))
	}

	addrs = append(addrs,
		AddrFromHost("localhost"),
		AddrFromHost("127.0.0.1"),
	)

	return addrs
}

func (o ProxyOptions) fwdCtxStore() ForwardContextStore {
	if o.ForwardContextStore == nil {
		return &MemoryForwardContextStore{}
	}
	return o.ForwardContextStore
}

func NewProxy(elm *Element, routes []ProxyRoute, opts ...ProxyOptions) (*Proxy, error) {
	if elm == nil {
		return nil, errors.ErrorWrap("nil element")
	}

	o := util.LastSliceElemOr(opts, ProxyOptions{})
	prx := &Proxy{
		elm:         elm,
		routes:      routes,
		locAddrs:    o.locAddrs(),
		fwdCtxStore: o.fwdCtxStore(),
	}

	// TODO: restore/reload all forward contexts and related transactions to rebuild proxy state

	elm.UseMiddleware(prx)

	return prx, nil
}

func (prx *Proxy) LogValue() slog.Value {
	if prx == nil {
		return slog.Value{}
	}

	return slog.GroupValue(
		slog.String("ptr", fmt.Sprintf("%p", prx)),
	)
}

const proxyRouteMetaKey = "sip.proxy_route"

type proxyProcessStep = func(context.Context, *ProxyRoute, *RequestEnvelope) error

func (prx *Proxy) InterceptInboundRequest(
	ctx context.Context,
	next RequestReceiver,
	req *RequestEnvelope,
) error {
	route := prx.shouldForwardReq(ctx, req)
	if route == nil {
		return errors.Wrap(next.RecvRequest(ctx, req))
	}

	req.Metadata().Set(proxyRouteMetaKey, route.Name)

	if route.Logger == nil {
		route.Logger = log.LoggerFromValues(ctx, prx.elm)
	}

	route.Logger = route.Logger.With(
		slog.Any("inbound_request", req),
		slog.Any("proxy_route", route),
	)

	route.Logger.LogAttrs(ctx, slog.LevelDebug, "proxy route resolved")

	// For all new inbound requests, including any with unknown methods,
	// an element intending to proxy the request MUST:
	// 	1. Validate the request (Section 16.3);
	// 	2. Initialize server transaction if stateful forward required;
	// 	3. Authorize the request (Section 16.3, step 6);
	// 	4. Preprocess routing information (Section 16.4);
	// 	5. Determine target(s) for the request (Section 16.5);
	// 	6. Forward the request to each target (Section 16.6);
	// 	7. Process all responses (Section 16.7).

	for _, fn := range []proxyProcessStep{
		prx.stepValidateReq,
		prx.stepInitReqSrvTx,
		prx.stepAuthorizeReq,
		prx.stepPreprocessReqRouting,
		prx.stepResolveReqTargets,
		prx.stepForwardReq,
	} {
		if err := fn(ctx, route, req); err != nil {
			return errors.Wrap(err)
		}
	}

	return nil
}

func (prx *Proxy) shouldForwardReq(ctx context.Context, req *RequestEnvelope) *ProxyRoute {
	for _, r := range prx.routes {
		if r.MatchRequest(ctx, &r, req) {
			return &r
		}
	}

	return nil
}

// stepValidateReq validates the inbound request.
// RFC 3261 Section 16.3.
func (prx *Proxy) stepValidateReq(ctx context.Context, route *ProxyRoute, req *RequestEnvelope) error {
	// 1. Reasonable syntax check
	if err := req.Validate(); err != nil {
		return errors.Wrap(NewRequestRejectedError(
			err,
			slog.LevelDebug,
			ResponseStatusBadRequest,
		))
	}

	// 2. URI scheme check
	switch ruri := req.URI(); ruri.Scheme() {
	case "sip", "sips":
	default:
		// TODO: maybe this callback should return *URI for further processing
		if route.CheckRequestURI != nil {
			if err := route.CheckRequestURI(ctx, route, req); err != nil {
				return errors.Wrap(err)
			}
		}

		return errors.Wrap(NewRequestRejectedError(
			errUnsupURIScheme,
			slog.LevelDebug,
			ResponseStatusUnsupportedURIScheme,
		))
	}

	if err := prx.validateReqHdrs(ctx, route, req); err != nil {
		return errors.Wrap(err)
	}

	if route.ValidateRequest != nil {
		if err := route.ValidateRequest(ctx, route, req); err != nil {
			return errors.Wrap(err)
		}
	}

	return nil
}

// validateReqHdrs executes validation checks on the request headers.
// RFC 3261 Section 16.3.
func (prx *Proxy) validateReqHdrs(ctx context.Context, route *ProxyRoute, req *RequestEnvelope) error {
	var err error
	req.WithMessage(func(r *Request) {
		// 3. Max-Forwards check
		if maxFwd, ok := r.Headers.MaxForwards(); ok && maxFwd == 0 {
			err = errors.Wrap(NewRequestRejectedError(
				errToManyHops,
				slog.LevelDebug,
				ResponseStatusTooManyHops,
			))

			return
		}

		// 4. Optional Loop Detection check
		// if route.LoopDetection {
		// 	for hop := range r.Headers.Vias() {
		// 		if prx.elm.MatchViaHop(*hop) {
		// 			// TODO: check branch Section 16.6 Step 8.
		// 			_, _ = hop.Branch()
		// 		}
		// 	}
		// }

		// 5. Proxy-Require check
		cpb := prx.elm.Capabilities()

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

			return
		}
	})

	if err == nil {
		return nil
	}

	if errors.Is(err, errToManyHops) && req.Method().Equal(RequestMethodOptions) {
		// TODO: make auto-response on OPTIONS optional?
		cpb := prx.elm.Capabilities()

		sendErr := prx.elm.Respond(ctx, req, ResponseStatusOK, RespondOptions{
			ResponseOptions: ResponseOptions{
				Headers: make(Headers).Set(
					cpb.NewSupportedHeader(),
					cpb.NewAcceptHeader(),
					// cpb.NewAcceptEncodingHeader(),
					// cpb.NewAcceptLanguageHeader(),
				),
			},
		})
		if sendErr == nil {
			return nil
		}

		route.Logger.LogAttrs(ctx, slog.LevelWarn, "failed to auto respond '200 OK' on OPTIONS request",
			slog.Any("error", sendErr),
		)
	}

	return errors.Wrap(err)
}

// stepInitReqSrvTx setups new [ServerTransaction] for inbound request.
// This step should be called right after validation step to prevent
// occasional duplicate requests from being processed multiple times.
func (prx *Proxy) stepInitReqSrvTx(ctx context.Context, route *ProxyRoute, req *RequestEnvelope) error {
	if route.ForwardMode != ForwardModeStateful {
		return nil
	}

	tp, ok := prx.elm.TransportByProto(req.Transport().Proto)
	if !ok {
		for tp = range prx.elm.AllTransports() {
			break
		}
	}

	if tp == nil {
		return errors.Wrap(NewRequestRejectedError(
			ErrNoTransport,
			slog.LevelError,
			ResponseStatusServerInternalError,
		))
	}

	_, err := prx.elm.NewServerTransaction(ctx, req, tp, route.ServerTransactionOptions(ctx, route, req))
	if err != nil {
		return errors.Wrap(NewRequestRejectedError(
			err,
			slog.LevelError,
			ResponseStatusServerInternalError,
		))
	}

	// TODO: hold srv TX ref?

	return nil
}

// stepAuthorizeReq authenticates and authorizes the inbound request.
// RFC 3261 Section 16.3, step 6.
func (*Proxy) stepAuthorizeReq(ctx context.Context, route *ProxyRoute, req *RequestEnvelope) error {
	if route.AuthenticateRequest != nil {
		if passed, challenge := route.AuthenticateRequest(ctx, route, req); !passed {
			return errors.Wrap(NewRequestRejectedError(
				errAuthRequired,
				slog.LevelDebug,
				ResponseStatusProxyAuthenticationRequired,
				RespondOptions{
					ResponseOptions: ResponseOptions{
						Headers: make(Headers).Set(&header.ProxyAuthenticate{AuthChallenge: challenge}),
					},
				},
			))
		}
	}

	if route.AuthorizeRequest != nil {
		if err := route.AuthorizeRequest(ctx, route, req); err != nil {
			return errors.Wrap(err)
		}
	}

	return nil
}

// stepPreprocessReqRouting executes inbound request routing pre-process.
// RFC 3261 Section 16.4.
func (prx *Proxy) stepPreprocessReqRouting(_ context.Context, _ *ProxyRoute, req *RequestEnvelope) error {
	req.WithMessage(func(r *Request) {
		prx.processReqStrictRouting(req, r)
		prx.processReqURIMAddr(req, r)
		prx.processReqFirstRoute(req, r)
	})

	return nil
}

func (prx *Proxy) processReqStrictRouting(env *RequestEnvelope, req *Request) {
	ru, ok := req.URI.(*URI)
	if !ok {
		return
	}

	// If the Request-URI of the request contains a value this proxy previously
	// placed into a Record-Route header field (see Section 16.6 item 4),
	// the proxy MUST replace the Request-URI in the request with the last
	// value from the Route header field, and remove that value from the
	// Route header field.
	if !prx.isLocAddr(ru.Addr, env.Transport().DefaultPort) {
		return
	}

	hop, ok := req.Headers.LastRoute()
	if !ok {
		return
	}

	hu, ok := hop.URI.(*URI)
	if !ok {
		return
	}

	req.URI = hu.Clone().(*URI) //nolint:forcetypeassert
	req.Headers.PopLastRoute()
}

func (prx *Proxy) processReqURIMAddr(env *RequestEnvelope, req *Request) {
	ru, ok := req.URI.(*URI)
	if !ok {
		return
	}

	// If the Request-URI contains a maddr parameter, the proxy MUST check
	// to see if its value is in the set of addresses or domains the proxy
	// is configured to be responsible for.  If the Request-URI has a maddr
	// parameter with a value the proxy is responsible for, and the request
	// was received using the port and transport indicated (explicitly or by
	// default) in the Request-URI, the proxy MUST strip the maddr and any
	// non-default port or transport parameter and continue processing as if
	// those values had not been present in the request.
	maddr, ok := ru.MAddr()
	if !ok || !maddr.IsValid() || !prx.isLocAddr(maddr, 0) {
		return
	}

	ruTp := UDPMetadata()
	if ru.Secured {
		ruTp = TLSMetadata()
	}

	if v, ok := ru.Transport(); ok && v.IsValid() {
		// TODO: expose metadata methods on Element? Probably review methods naming
		if m, ok := prx.elm.TransportManager().TransportMetadataByProto(v); ok && m.IsValid() {
			ruTp = m
		}
	}

	ruPort := ruTp.DefaultPort
	if v, ok := ru.Addr.Port(); ok && v > 0 {
		ruPort = v
	}

	if !env.Transport().Proto.Equal(ruTp.Proto) || env.LocalAddr().Port() != ruPort {
		return
	}

	ru.Addr = AddrFromHost(ru.Addr.Host())
	ru.Params.Delete("maddr").Delete("transport")
}

func (prx *Proxy) processReqFirstRoute(env *RequestEnvelope, req *Request) {
	// If the first value in the Route header field indicates this proxy,
	// the proxy MUST remove that value from the request.
	hop, ok := req.Headers.FirstRoute()
	if !ok {
		return
	}

	hu, ok := hop.URI.(*URI)
	if !ok || !prx.isLocAddr(hu.Addr, env.Transport().DefaultPort) {
		return
	}

	req.Headers.PopFirstRoute()
}

func (prx *Proxy) isLocAddr(addr Addr, defPort uint16) bool {
	if defPort > 0 {
		addr = addr.WithPortIfMissing(defPort)
	} else {
		addr = addr.WithoutPort()
	}

	for _, laddr := range prx.locAddrs {
		if defPort > 0 {
			laddr = laddr.WithPortIfMissing(defPort)
		} else {
			laddr = laddr.WithoutPort()
		}

		if laddr.Equal(addr) {
			return true
		}
	}

	return false
}

// stepResolveReqTargets resolves forward targets for inbound request.
// RFC 3261 Section 16.5.
func (prx *Proxy) stepResolveReqTargets(ctx context.Context, route *ProxyRoute, req *RequestEnvelope) error {
	route.internalTargets = make(chan []*URI, 1)

	var resolved bool
	req.WithMessage(func(r *Request) {
		ru, ok := r.URI.(*URI)
		if !ok {
			return
		}

		// If the Request-URI of the request contains an maddr parameter, the
		// Request-URI MUST be placed into the target set as the only target
		// URI, and the proxy MUST proceed to Section 16.6.
		if addr, ok := ru.MAddr(); ok && addr.IsValid() {
			route.internalTargets <- []*URI{ru}

			close(route.internalTargets)

			resolved = true

			return
		}

		// If the domain of the Request-URI indicates a domain this element is
		// not responsible for, the Request-URI MUST be placed into the target
		// set as the only target, and the element MUST proceed to the task of
		// Request Forwarding (Section 16.6).
		if !prx.isLocAddr(ru.Addr, 0) {
			route.internalTargets <- []*URI{ru}

			close(route.internalTargets)

			resolved = true

			return
		}
	})

	if resolved {
		return nil
	}

	if route.ResolveRequestTargets == nil {
		// If the target set remains empty after applying all of the above, the
		// proxy MUST return an error response, which SHOULD be the 480 (Temporarily Unavailable) response.
		return errors.Wrap(NewRequestRejectedError(
			errNoFwdTargets,
			slog.LevelDebug,
			ResponseStatusTemporarilyUnavailable,
		))
	}

	resolvedTargets, err := route.ResolveRequestTargets(ctx, route, req)
	if err != nil {
		return errors.Wrap(err)
	}

	route.resolvedTargets = resolvedTargets

	return nil
}

// stepForwardReq forwards inbound request to resolved targets set.
// RFC 3261 Section 16.6.
//
//nolint:gocognit
func (prx *Proxy) stepForwardReq(ctx context.Context, route *ProxyRoute, inReq *RequestEnvelope) error {
	if route.ForwardMode == ForwardModeStateful {
		txKey, _ := inReq.Metadata().Get(txKeyMetaKey)
		//nolint:forcetypeassert
		route.fwdCtx = &ForwardContext{
			ServerTransactionKey: txKey.(ServerTransactionKey),
		}

		if err := prx.fwdCtxStore.Store(ctx, route.fwdCtx); err != nil {
			return errors.Wrap(NewRequestRejectedError(
				err,
				slog.LevelWarn,
				ResponseStatusServerInternalError,
			))
		}
	}

	var (
		trgtsNum int
		trgtErrs []error
	)

	for trgts := range route.targets() {
		for _, trgt := range trgts {
			trgtsNum++

			// For each target, the proxy forwards the request following these steps:
			// 1. Make a copy of the received request
			outReq := inReq.Clone().(*RequestEnvelope) //nolint:forcetypeassert

			outReq.Metadata().
				Delete(txKeyMetaKey, txTypeMetaKey)

			outReq.SetTransport(TransportMetadata{}).
				SetLocalAddr(netip.AddrPort{}).
				SetRemoteAddr(netip.AddrPort{}).
				WithMessage(func(r *Request) {
					// 2. Update the Request-URI
					ru := trgt.Clone().(*URI) //nolint:forcetypeassert
					ru.Params.Delete("method")
					ru.Headers.Clear()
					r.URI = ru

					// 3. Update the Max-Forwards header field
					if maxFwd, ok := r.Headers.MaxForwards(); ok {
						maxFwd--
						r.Headers.Set(maxFwd)
					}

					// 4. Optionally add a Record-route header field value
					if route.RecordRoute {
						// TODO: учесть SIPS URI, TLS, какой адрес из локальных использовать и т.д.
						// пока простейшая реализация
						r.Headers.PrependRecordRoute(header.RouteHop{
							URI: &URI{
								// TODO: уникальный идентификатор для отслеживания ответов
								// User: User("TODO"),
								Addr:   prx.locAddrs[0],
								Params: new(Values).Set("lr", ""),
							},
						})
					}
				})

			// 5. Optionally add additional header fields
			// 6. Postprocess routing information
			if route.ResolveRequestRoutes != nil {
				hdrs, err := route.ResolveRequestRoutes(ctx, route, inReq)
				if err != nil {
					// TODO: probably implement a custom error struct with all context fields: route, target, in/out req
					trgtErrs = append(trgtErrs, errors.ErrorfWrap("resolve routes for outbound request to %q target: %w", trgt, err))
					continue
				}

				outReq.WithMessage(func(r *Request) {
					for _, hdr := range slices.Backward(slices.Collect(hdrs)) {
						r.Headers.Prepend(hdr)
					}
				})
			}

			if route.AdjustOutRequest != nil {
				if err := route.AdjustOutRequest(ctx, route, inReq, outReq); err != nil {
					// TODO: probably implement a custom error struct with all context fields: route, target, in/out req
					trgtErrs = append(trgtErrs, errors.Errorf("adjust outbound request to %q target: %w", trgt, err))
					continue
				}
			}

			// If the copy contains a Route header field, the proxy MUST
			// inspect the URI in its first value.  If that URI does not
			// contain an lr parameter, the proxy MUST modify the copy as
			// follows:
			//  -  The proxy MUST place the Request-URI into the Route header
			//     field as the last value.
			//  -  The proxy MUST then place the first Route header field value
			//     into the Request-URI and remove that value from the Route
			//     header field.
			// outReq.WithMessage(func(r *Request) {
			// 	hop, ok := r.Headers.FirstRoute()
			// 	if !ok {
			// 		return
			// 	}

			// 	if u, ok := hop.URI.(*URI); !ok || !u.LR() {
			// 		return
			// 	}

			// 	r.Headers.AppendRoute(header.RouteHop{URI: r.URI.Clone()})
			// 	r.URI = hop.URI.Clone()
			// 	r.Headers.PopFirstRoute()
			// })

			// 7. Determine the next-hop address, port, and transport
			// 8. Add a Via header field value
			// 9. Add a Content-Length header field if necessary
			// 10. Forward the new request

			// Prepend a dummy valid Via: transport and sent-by values will be overwritten
			// by selected transport during send process.
			outReq.WithMessage(func(r *Request) {
				r.Headers.PrependVia(header.ViaHop{
					Proto:     protoVer20,
					Transport: udpMeta.Proto,
					Addr:      AddrFromHost(util.RandString(8) + ".invalid"),
					Params:    make(types.Values).Set("branch", prx.genForwardBranch(ctx, route, inReq)),
				})
			})

			// TODO: step 7. здесь похоже не подойдёт реализованные elm.SendRequest/elm.SendRequestStateful
			// in stateless mode, proxy should switch to stateful after first failure attempt
			if route.ForwardMode == ForwardModeStateful {
				txOpts := route.ClientTransactionOptions(ctx, route, inReq, outReq)

				tx, err := prx.elm.SendRequestStateful(ctx, outReq, SendRequestStatefulOptions{
					SendOptions: txOpts.SendOptions,
					Timing:      txOpts.Timing,
					Logger:      txOpts.Logger,
				})
				if err != nil {
					trgtErrs = append(trgtErrs, errors.Errorf("send outbound request to %q target: %w", trgt, err))
					continue
				}

				route.fwdCtx.ClientTransactionKeys = append(route.fwdCtx.ClientTransactionKeys, tx.Key())

				if err := prx.fwdCtxStore.Store(ctx, route.fwdCtx); err != nil {
					route.Logger.LogAttrs(ctx, slog.LevelError, "failed to update forward context",
						slog.Any("error", err),
						slog.Any("forward_context", route.fwdCtx),
					)
					// TODO: resolve what to do?
					// If the context wasn't updated in the store, then it will be impossible to find
					// origin server transaction for response forwarding.
				}
			}

			// 11. Set timer C
		}
	}

	if trgtsNum == 0 || trgtsNum == len(trgtErrs) {
		// If the target set remains empty after applying all of the above, the
		// proxy MUST return an error response, which SHOULD be the 480 (Temporarily Unavailable) response.
		return errors.Wrap(NewRequestRejectedError(
			errNoFwdTargets,
			slog.LevelDebug,
			ResponseStatusTemporarilyUnavailable,
		))
	}

	return nil
}

func (*Proxy) genForwardBranch(context.Context, *ProxyRoute, *RequestEnvelope) string {
	// TODO: implement Section 16.6 Step 8
	// Stateful mode - calculate random branch, based on request or RFC 3261 branch
	// Stateless - always based on message
	// + inbound request local addr
	// надо как то кодировать в branch информацию для правльного forward ответа с нужного транспорта, порта и т.д.
	// + loop detection
	return ""
}

func (prx *Proxy) InterceptInboundResponse(
	ctx context.Context,
	next ResponseReceiver,
	res *ResponseEnvelope,
) error {
	if !prx.shouldForwardRes(ctx, res) {
		return errors.Wrap(next.RecvResponse(ctx, res))
	}

	// TODO: forward response if needed and skip next receiver Section 16.7
	return nil
}

func (*Proxy) shouldForwardRes(ctx context.Context, res *ResponseEnvelope) bool {
	// TODO: implement proxy filter logic
	return false
}

// ProxyRoute represents a routing rule for the proxy.
//
// TODO: implement default implementations for:
//   - MatchRequest: methods list, all, etc.
//   - AuthenticateRequest: digest, jwt
type ProxyRoute struct {
	Name          string
	LoopDetection bool
	ForwardMode   ForwardMode
	RecordRoute   bool
	Logger        *slog.Logger

	MatchRequest func(ctx context.Context, route *ProxyRoute, inReq *RequestEnvelope) bool

	ValidateRequest func(ctx context.Context, route *ProxyRoute, inReq *RequestEnvelope) error
	CheckRequestURI func(ctx context.Context, route *ProxyRoute, inReq *RequestEnvelope) error

	AuthenticateRequest func(ctx context.Context, route *ProxyRoute, inReq *RequestEnvelope) (bool, header.AuthChallenge)
	AuthorizeRequest    func(ctx context.Context, route *ProxyRoute, inReq *RequestEnvelope) error

	ServerTransactionOptions func(ctx context.Context, route *ProxyRoute, inReq *RequestEnvelope) ServerTransactionOptions
	ClientTransactionOptions func(ctx context.Context, route *ProxyRoute, inReq, outReq *RequestEnvelope) ClientTransactionOptions

	ResolveRequestTargets func(ctx context.Context, route *ProxyRoute, inReq *RequestEnvelope) (iter.Seq[[]*URI], error)

	ResolveRequestRoutes func(ctx context.Context, route *ProxyRoute, inReq *RequestEnvelope) (iter.Seq[header.Route], error)
	AdjustOutRequest     func(ctx context.Context, route *ProxyRoute, inReq, outReq *RequestEnvelope) error

	internalTargets chan []*URI
	resolvedTargets iter.Seq[[]*URI]

	fwdCtx *ForwardContext
}

func (r *ProxyRoute) LogValue() slog.Value {
	if r == nil {
		return slog.Value{}
	}
	return slog.StringValue(r.Name)
}

func (r *ProxyRoute) targets() iter.Seq[[]*URI] {
	return func(yield func([]*URI) bool) {
		seen := make([]*URI, 0, 10)

		for uris := range r.resolvedTargets {
			uris = slices.DeleteFunc(uris, func(u1 *URI) bool {
				return slices.ContainsFunc(seen, func(u2 *URI) bool {
					return u1.Equal(u2)
				})
			})
			seen = append(seen, uris...)

			if !yield(uris) {
				return
			}
		}

		for uris := range r.internalTargets {
			uris = slices.DeleteFunc(uris, func(u1 *URI) bool {
				return slices.ContainsFunc(seen, func(u2 *URI) bool {
					return u1.Equal(u2)
				})
			})
			seen = append(seen, uris...)

			if !yield(uris) {
				return
			}
		}
	}
}

type ForwardMode uint

const (
	ForwardModeStateless ForwardMode = iota
	ForwardModeStateful
)

func ForwardModeFromString(s string) ForwardMode {
	switch util.LCase(s) {
	case "stateful":
		return ForwardModeStateful
	case "stateless":
		fallthrough
	default:
		return ForwardModeStateless
	}
}

func (m ForwardMode) String() string {
	switch m {
	case ForwardModeStateful:
		return "stateful"
	case ForwardModeStateless:
		fallthrough
	default:
		return "stateless"
	}
}

type ForwardContext struct {
	ServerTransactionKey  ServerTransactionKey   `json:"server_transaction_key"`
	ClientTransactionKeys []ClientTransactionKey `json:"client_transaction_keys"`
	Responses             []*Response            `json:"responses"`
}

func (fc *ForwardContext) LogValue() slog.Value {
	if fc == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.Any("server_transaction_key", fc.ServerTransactionKey),
		slog.Any("client_transaction_keys", fc.ClientTransactionKeys),
	)
}

type ForwardContextStore interface {
	LoadByTransactionKey(ctx context.Context, txKey any) (*ForwardContext, error)
	LoadAll(ctx context.Context) (iter.Seq[*ForwardContext], error)
	Store(ctx context.Context, fwdCtx *ForwardContext) error
	Delete(ctx context.Context, fwdCtx *ForwardContext) error
}

type MemoryForwardContextStore struct {
	bySrvTxKey syncutil.RWMap[ServerTransactionKey, *ForwardContext]
	byClnTxKey syncutil.RWMap[ClientTransactionKey, *ForwardContext]
}

func (s *MemoryForwardContextStore) LoadByTransactionKey(ctx context.Context, txKey any) (*ForwardContext, error) {
	switch k := txKey.(type) {
	case ServerTransactionKey:
		if fc, ok := s.bySrvTxKey.Load(k); ok {
			return fc, nil
		}
		return nil, errors.Wrap(ErrForwardContextNotFound)
	case ClientTransactionKey:
		if fc, ok := s.byClnTxKey.Load(k); ok {
			return fc, nil
		}
		return nil, errors.Wrap(ErrForwardContextNotFound)
	default:
		return nil, errors.ErrorWrap("invalid transaction key type")
	}
}

func (s *MemoryForwardContextStore) LoadAll(ctx context.Context) (iter.Seq[*ForwardContext], error) {
	return func(yield func(*ForwardContext) bool) {
		for _, fc := range s.bySrvTxKey.All() {
			if !yield(fc) {
				return
			}
		}
	}, nil
}

func (s *MemoryForwardContextStore) Store(ctx context.Context, fc *ForwardContext) error {
	if actual, loaded := s.bySrvTxKey.LoadOrStore(fc.ServerTransactionKey, fc); loaded && actual != fc {
		return errors.Wrap(ErrDuplicateTransaction)
	}
	for _, key := range fc.ClientTransactionKeys {
		s.byClnTxKey.Store(key, fc)
	}
	return nil
}

func (s *MemoryForwardContextStore) Delete(ctx context.Context, fc *ForwardContext) error {
	s.bySrvTxKey.Delete(fc.ServerTransactionKey)
	for _, key := range fc.ClientTransactionKeys {
		s.byClnTxKey.Delete(key)
	}
	return nil
}
