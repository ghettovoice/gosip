// Package redirector provides a SIP redirect-server component for an
// [sip.Element]. It claims inbound requests matching its routes and
// answers them: a redirection response carrying the resolved contacts on
// success, or a failure response.
package redirector

import (
	"context"
	"io"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/pkg/errclass"
	"github.com/ghettovoice/gosip/pkg/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
)

// Redirector is a SIP redirect-server component for an [sip.Element].
// It claims inbound requests matching its routes and answers them with
// the route's response.
type Redirector struct {
	routes []Route
	// log is the configured fallback logger. The processing element's
	// logger from the request context wins over it.
	log *slog.Logger

	// closed commits the lifecycle end: claiming is sealed so a matched
	// request passes through unchanged afterwards.
	closed atomic.Bool

	// closeOnce runs the one-shot lifecycle seal of [Redirector.Close].
	closeOnce sync.Once
}

var (
	_ io.Closer                     = (*Redirector)(nil)
	_ sip.InboundRequestInterceptor = (*Redirector)(nil)
)

// ErrMissingElement reports a claimed redirector request dispatched without
// a processing element in the interception context.
const ErrMissingElement errors.Error = "missing element"

// NewMissingElementError creates an [ErrMissingElement] error classified as
// an element error.
func NewMissingElementError() error {
	return errclass.Classify(
		errors.Prefix(ErrMissingElement),
		sip.ErrClassElement,
	)
}

// New creates a [Redirector] component.
//
// Routes are validated and copied: later changes to the caller's slice do
// not affect the running redirector. The constructor rejects a route
// without a contact resolver or with a non-zero constant status outside
// {300, 301, 302}. It binds nothing: an explicit
// [sip.Element.UseComponent] registration attaches the redirector to an
// element's processing chain.
//
// Options are optional: no value selects the zero [Options], and when
// several values are supplied the last whole value wins — option fields
// are not merged.
func New(routes []Route, opts ...Options) (*Redirector, error) {
	o := util.LastSliceElem(opts, Options{})

	ownedRoutes := slices.Clone(routes)
	for i := range ownedRoutes {
		if err := ownedRoutes[i].validate(); err != nil {
			return nil, errors.Wrap(err)
		}
	}

	return &Redirector{
		routes: ownedRoutes,
		log:    o.Logger,
	}, nil
}

// Close seals the redirector's lifecycle. It is the component close hook
// of the owning element: one-shot and idempotent, so concurrent and
// repeated calls share the first call's effect. After Close, claiming is
// sealed: a request that would match a route is no longer claimed and
// passes through unchanged.
//
// The redirector owns no goroutines, subscriptions, or request state, so
// Close only stores the closed flag; unbinding from an element happens
// through the [sip.Element.UseComponent] unbind the application holds.
func (rdr *Redirector) Close() error {
	rdr.closeOnce.Do(func() {
		rdr.closed.Store(true)
	})
	return nil
}

// InterceptInboundRequest intercepts an inbound request on the owning
// element's receiver chain.
//
// A request claimed by the first matching route never reaches next. A
// claimed CANCEL is answered 481 Call/Transaction Does Not Exist on its
// own server transaction: the redirector answers synchronously and never
// leaves a request pending, so every matched CANCEL is late by definition.
// Any other claimed request is answered with the route's response — a
// redirection carrying the resolved contacts or a failure status. The
// redirector passes through instead: a request received after
// [Redirector.Close], a request no route matches, and an ACK — which is
// never answered by a redirection response.
func (rdr *Redirector) InterceptInboundRequest(
	ctx context.Context,
	next sip.RequestReceiver,
	req *sip.RequestEnvelope,
) error {
	if rdr.closed.Load() {
		// A sealed redirector claims nothing: the request continues down
		// the receiver chain as if no route matched.
		return errors.Wrap(next.RecvRequest(ctx, req))
	}

	route := rdr.matchRoute(ctx, req)
	if route == nil {
		return errors.Wrap(next.RecvRequest(ctx, req))
	}

	if req.Method().Equal(sip.RequestMethodAck) {
		return errors.Wrap(next.RecvRequest(ctx, req))
	}

	if req.Method().Equal(sip.RequestMethodCancel) {
		return errors.Wrap(rdr.cancelReq(ctx, req))
	}

	return errors.Wrap(rdr.routeReq(ctx, route, req))
}

// matchRoute returns the first route claiming req: a route without a
// request matcher accepts every request at its position.
func (rdr *Redirector) matchRoute(ctx context.Context, req *sip.RequestEnvelope) *Route {
	for i := range rdr.routes {
		r := &rdr.routes[i]
		if r.MatchRequest == nil || r.MatchRequest(ctx, req) {
			return r
		}
	}
	return nil
}

// cancelReq answers a claimed CANCEL with 481 Call/Transaction Does Not
// Exist. The redirector answers synchronously and never leaves a request
// pending, so every matched CANCEL is late by definition: the response
// goes out on the CANCEL's own server transaction — without resolving
// route contacts and without a Contact header — so the transaction layer
// absorbs the CANCEL's retransmissions. A send success consumes the
// request; a send failure is logged and returned.
//
// The processing element is resolved at the point the response needs it:
// a claimed CANCEL without an element in its context fails with
// [ErrMissingElement].
func (rdr *Redirector) cancelReq(ctx context.Context, req *sip.RequestEnvelope) error {
	elm, ok := sip.ElementFromContext(ctx)
	if !ok {
		return errors.Wrap(NewMissingElementError())
	}

	return errors.Wrap(rdr.respond(ctx, elm, req, sip.ResponseStatusCallTransactionDoesNotExist, nil))
}

// routeReq answers a claimed inbound request with the route's response.
// A rejected authorization is answered 403 Forbidden without invoking the
// contact resolver, a resolver failure is answered 500 Server Internal
// Error, and an empty resolution is answered 404 Not Found; failure
// responses carry no Contact header. Otherwise the resolved contacts are
// carried into the response Contact header and the response status is
// selected by the route's [Route.SelectStatus] callback, falling back to
// the constant [Route.Status] with the zero status meaning
// 302 Moved Temporarily; an unsupported selected status is answered
// 500 Server Internal Error without a Contact header.
//
// The processing element is resolved at the point the response needs it:
// a claimed request without an element in its context fails with
// [ErrMissingElement].
func (rdr *Redirector) routeReq(ctx context.Context, route *Route, req *sip.RequestEnvelope) error {
	var (
		sts      sip.ResponseStatus
		contacts header.Contact
	)

	if route.AuthorizeRequest != nil {
		if err := route.AuthorizeRequest(ctx, req); err != nil {
			rdr.logger(ctx).DebugContext(ctx, "redirector request rejected",
				slog.String("route", route.Name),
				slog.Any("inbound_request", req),
				slog.Any("error", err),
			)
			sts = sip.ResponseStatusForbidden
		}
	}

	if sts == 0 {
		var err error
		contacts, err = route.ResolveContacts(ctx, req)
		switch {
		case err != nil:
			rdr.logger(ctx).WarnContext(ctx, "redirector contact resolution failed",
				slog.String("route", route.Name),
				slog.Any("inbound_request", req),
				slog.Any("error", err),
			)
			contacts = nil
			sts = sip.ResponseStatusServerInternalError
		case len(contacts) == 0:
			contacts = nil
			sts = sip.ResponseStatusNotFound
		default:
			rdr.logger(ctx).DebugContext(ctx, "redirector route resolved",
				slog.String("route", route.Name),
				slog.Any("inbound_request", req),
			)
			sts, err = selectStatus(ctx, route, req, contacts)
			if err != nil {
				rdr.logger(ctx).WarnContext(ctx, "redirector status selection failed",
					slog.String("route", route.Name),
					slog.Any("inbound_request", req),
					slog.Any("error", err),
				)
				contacts = nil
				sts = sip.ResponseStatusServerInternalError
			}
		}
	}

	elm, ok := sip.ElementFromContext(ctx)
	if !ok {
		return errors.Wrap(NewMissingElementError())
	}

	return errors.Wrap(rdr.respond(ctx, elm, req, sts, contacts))
}

// selectStatus resolves the response status for a matched route whose
// contacts resolved successfully. A configured [Route.SelectStatus]
// callback decides from the request and the resolved contacts; its result
// is restricted to the supported redirect set, and an unsupported result
// is an error. Without a callback the route's constant [Route.Status] is
// used, with the zero status meaning 302 Moved Temporarily.
func selectStatus(
	ctx context.Context,
	route *Route,
	req *sip.RequestEnvelope,
	contacts header.Contact,
) (sip.ResponseStatus, error) {
	if route.SelectStatus == nil {
		if route.Status == 0 {
			return sip.ResponseStatusMovedTemporarily, nil
		}
		return route.Status, nil
	}

	sts := route.SelectStatus(ctx, req, contacts)
	if !redirectStatusSupported(sts) {
		return 0, errclass.Classify(
			errors.ErrorfWrap(
				"invalid redirector route %q: unsupported selected status %d",
				route.Name,
				sts,
			),
			sip.ErrClassElement,
		)
	}
	return sts, nil
}

// respond sends the route's response through the processing element.
// contacts is carried into the response Contact header; nil contacts —
// used for failure statuses — produce a response without a Contact
// header. A send failure is logged and returned.
func (rdr *Redirector) respond(
	ctx context.Context,
	elm *sip.Element,
	req *sip.RequestEnvelope,
	sts sip.ResponseStatus,
	contacts header.Contact,
) error {
	opts := sip.RespondStatefulOptions{}
	if len(contacts) > 0 {
		opts.ResponseOptions.Headers = sip.Headers{}.Set(contacts)
	}

	if _, err := elm.RespondStateful(ctx, req, sts, opts); err != nil {
		rdr.logger(ctx).WarnContext(ctx, "redirector response send failed",
			slog.Any("inbound_request", req),
			slog.Any("error", err),
		)
		return errors.Wrap(err)
	}
	return nil
}

// logger resolves the processing logger from the interception context: a
// context-carried logger wins, then the owning element's logger, then the
// configured [Options.Logger] fallback, then the package default.
func (rdr *Redirector) logger(ctx context.Context) *slog.Logger {
	if elm, ok := sip.ElementFromContext(ctx); ok {
		return log.LoggerFromValues(ctx, elm)
	}
	return log.LoggerFromValues(ctx, rdr.log)
}
