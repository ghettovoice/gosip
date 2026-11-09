package sip

import (
	"context"
	"slices"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip/header"
)

type ElementMiddleware any

// elemMidwareEntry tracks one registered middleware together with the
// transport/transaction bindings that were installed for it, so shutdown can
// detach them before closing the middleware.
type elemMidwareEntry struct {
	mw ElementMiddleware
	// unbinds is assigned under lcMu while the registration lease is held and
	// read under lcMu or after all admitted operations have drained.
	unbinds []func()
	// removed commits the unbind decision under lcMu before any manager unbind
	// or provider call runs, so concurrent or reentrant unbinds are no-ops.
	removed bool
}

// UseMiddleware registers middleware on the element.
//
// Registration is rejected once the element begins closing; the returned unbind
// is then a non-nil no-op and the middleware remains caller-owned. Otherwise
// the returned unbind is idempotent: while the element is running it removes
// the registration, detaches the installed bindings and recomputes
// capabilities; after closing began it is a no-op because shutdown retains
// ownership of removal and detachment.
func (elm *Element) UseMiddleware(mw ElementMiddleware) (unbind func()) {
	elm.ensureInit()

	release, ok := elm.admitOp()
	if !ok {
		return noop
	}
	defer release()

	entry := &elemMidwareEntry{mw: mw}

	elm.lcMu.Lock()
	remove := elm.mws.Add(entry)
	elm.capabilsRev++
	elm.lcMu.Unlock()

	var unbinds []func()
	// bind to transport manager
	if v, ok := mw.(MessageInterceptor); ok {
		unbinds = append(unbinds, elm.tpm.UseMessageInterceptor(elemMsgInceptor{
			elemInReqInceptor:  elemInReqInceptor{elm, v},
			elemInResInceptor:  elemInResInceptor{elm, v},
			elemOutReqInceptor: elemOutReqInceptor{elm, v},
			elemOutResInceptor: elemOutResInceptor{elm, v},
		}))
	} else {
		if v, ok := mw.(InboundRequestInterceptor); ok {
			unbinds = append(unbinds, elm.tpm.UseInboundRequestInterceptor(elemInReqInceptor{elm, v}))
		}
		if v, ok := mw.(InboundResponseInterceptor); ok {
			unbinds = append(unbinds, elm.tpm.UseInboundResponseInterceptor(elemInResInceptor{elm, v}))
		}
		if v, ok := mw.(OutboundRequestInterceptor); ok {
			unbinds = append(unbinds, elm.tpm.UseOutboundRequestInterceptor(elemOutReqInceptor{elm, v}))
		}
		if v, ok := mw.(OutboundResponseInterceptor); ok {
			unbinds = append(unbinds, elm.tpm.UseOutboundResponseInterceptor(elemOutResInceptor{elm, v}))
		}
	}

	// bind to transaction manager
	if v, ok := mw.(TransactionHandler); ok {
		unbinds = append(unbinds, elm.txm.BindTransactionHandler(elemTxHandler{
			elemClnTxHandler: elemClnTxHandler{elm, v},
			elemSrvTxHandler: elemSrvTxHandler{elm, v},
		}))
	} else {
		if v, ok := mw.(ClientTransactionHandler); ok {
			unbinds = append(unbinds, elm.txm.BindClientTransactionHandler(elemClnTxHandler{elm, v}))
		}
		if v, ok := mw.(ServerTransactionHandler); ok {
			unbinds = append(unbinds, elm.txm.BindServerTransactionHandler(elemSrvTxHandler{elm, v}))
		}
	}

	elm.lcMu.Lock()
	entry.unbinds = unbinds
	elm.lcMu.Unlock()

	elm.updateCapabils()

	return func() {
		// The removal itself is an admitted operation: it may recombine
		// capabilities by calling into live providers, so shutdown must wait
		// for it before closing the remaining middleware. An unbind begun
		// after closing is a no-op because shutdown retains ownership.
		release, ok := elm.admitOp()
		if !ok {
			return
		}
		defer release()

		// Commit removal under lcMu before calling any unbind or provider:
		// concurrent and reentrant unbinds observe the committed state and
		// return immediately.
		elm.lcMu.Lock()
		if entry.removed {
			elm.lcMu.Unlock()
			return
		}
		entry.removed = true
		remove()
		elm.capabilsRev++
		unbinds := entry.unbinds
		elm.lcMu.Unlock()

		for _, fn := range unbinds {
			fn()
		}

		elm.updateCapabils()
	}
}

type elemCapabilsProvider interface {
	Capabilities() ElementCapabilities
}

// elemShutdowner is the optional middleware hook invoked by
// [Element.Shutdown] before the element closes.
type elemShutdowner interface {
	Shutdown(ctx context.Context) error
}

// updateCapabils recomputes the element capabilities from a live middleware
// snapshot. A registration or removal committed concurrently advances capabilitiesRevision
// and invalidates the snapshot, so the computation retries with fresh data
// instead of publishing a stale result. User provider calls never run under
// lcMu.
func (elm *Element) updateCapabils() {
	for {
		elm.lcMu.Lock()
		var mws []ElementMiddleware
		for entry := range elm.mws.All() {
			mws = append(mws, entry.mw)
		}
		rev := elm.capabilsRev
		elm.lcMu.Unlock()

		cpb := ElementCapabilities{
			Methods:   slices.Clone(elm.baseCapabils.Methods),
			Options:   slices.Clone(elm.baseCapabils.Options),
			MIMETypes: slices.Clone(elm.baseCapabils.MIMETypes),
		}
		for _, mw := range mws {
			if cpbPrvdr, ok := mw.(elemCapabilsProvider); ok {
				mwCpb := cpbPrvdr.Capabilities()
				cpb.Methods = util.AppendSliceUniqFunc(cpb.Methods, mwCpb.Methods, func(e RequestMethod) string {
					return string(e)
				})
				cpb.Options = util.AppendSliceUniqFunc(cpb.Options, mwCpb.Options, func(e header.OptionTag) string {
					return string(e)
				})
				cpb.MIMETypes = util.AppendSliceUniqFunc(cpb.MIMETypes, mwCpb.MIMETypes, func(e header.MIMERange) string {
					return e.String()
				})
			}
		}

		elm.lcMu.Lock()
		if rev == elm.capabilsRev {
			elm.currCapabils.Store(cpb)
			elm.lcMu.Unlock()
			return
		}
		elm.lcMu.Unlock()
	}
}

// shutdownMidware runs the optional Shutdown hook of every registered
// middleware in registration order. The whole pass is one admitted operation,
// so a concurrent Close cannot close middleware while a hook is still
// running; hooks must cooperate with the passed context. Every hook runs even
// after a hook error or context cancellation, and the lease is released
// before the caller proceeds to Close.
func (elm *Element) shutdownMidware(ctx context.Context) error {
	release, ok := elm.admitOp()
	if !ok {
		return nil
	}
	defer release()

	ctx = ContextWithElement(ctx, elm)
	var errs []error
	for entry := range elm.mws.All() {
		sh, ok := entry.mw.(elemShutdowner)
		if !ok {
			continue
		}
		if err := sh.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// elemInReqInceptor adapts a middleware inbound request interceptor to the
// element callback lease: the middleware is invoked only while callbacks are
// admitted, and a call after shutdown reports the element as closed.
type elemInReqInceptor struct {
	elm     *Element
	intcptr InboundRequestInterceptor
}

func (i elemInReqInceptor) InterceptInboundRequest(
	ctx context.Context,
	next RequestReceiver,
	req *RequestEnvelope,
) error {
	release, ok := i.elm.admitCallback()
	if !ok {
		return errors.Wrap(NewElementClosedError())
	}
	defer release()

	return errors.Wrap(i.intcptr.InterceptInboundRequest(ContextWithElement(ctx, i.elm), next, req))
}

// elemInResInceptor adapts a middleware inbound response interceptor to the
// element callback lease.
type elemInResInceptor struct {
	elm     *Element
	intcptr InboundResponseInterceptor
}

func (i elemInResInceptor) InterceptInboundResponse(
	ctx context.Context,
	next ResponseReceiver,
	res *ResponseEnvelope,
) error {
	release, ok := i.elm.admitCallback()
	if !ok {
		return errors.Wrap(NewElementClosedError())
	}
	defer release()

	return errors.Wrap(i.intcptr.InterceptInboundResponse(ContextWithElement(ctx, i.elm), next, res))
}

// elemOutReqInceptor adapts a middleware outbound request interceptor to the
// element callback lease.
type elemOutReqInceptor struct {
	elm     *Element
	intcptr OutboundRequestInterceptor
}

func (i elemOutReqInceptor) InterceptOutboundRequest(
	ctx context.Context,
	next RequestSender,
	req *RequestEnvelope,
	opts ...SendRequestOptions,
) error {
	release, ok := i.elm.admitCallback()
	if !ok {
		return errors.Wrap(NewElementClosedError())
	}
	defer release()

	return errors.Wrap(i.intcptr.InterceptOutboundRequest(ContextWithElement(ctx, i.elm), next, req, opts...))
}

// elemOutResInceptor adapts a middleware outbound response interceptor to the
// element callback lease.
type elemOutResInceptor struct {
	elm     *Element
	intcptr OutboundResponseInterceptor
}

func (i elemOutResInceptor) InterceptOutboundResponse(
	ctx context.Context,
	next ResponseSender,
	res *ResponseEnvelope,
	opts ...SendResponseOptions,
) error {
	release, ok := i.elm.admitCallback()
	if !ok {
		return errors.Wrap(NewElementClosedError())
	}
	defer release()

	return errors.Wrap(i.intcptr.InterceptOutboundResponse(ContextWithElement(ctx, i.elm), next, res, opts...))
}

// elemMsgInceptor adapts a middleware [MessageInterceptor] to the element
// callback lease on all four interception points by composing the individual
// interceptor adapters.
type elemMsgInceptor struct {
	elemInReqInceptor
	elemInResInceptor
	elemOutReqInceptor
	elemOutResInceptor
}

// elemTxHandler adapts a middleware [TransactionHandler] to the
// element callback lease by composing the individual handler adapters.
type elemTxHandler struct {
	elemClnTxHandler
	elemSrvTxHandler
}

// elemClnTxHandler adapts a middleware [ClientTransactionHandler] to the element
// callback lease: a notification captured after callback admission was sealed
// is skipped instead of reaching closed middleware.
type elemClnTxHandler struct {
	elm  *Element
	hdlr ClientTransactionHandler
}

func (h elemClnTxHandler) HandleClientTransaction(ctx context.Context, tx ClientTransaction) {
	release, ok := h.elm.admitCallback()
	if !ok {
		return
	}
	defer release()

	h.hdlr.HandleClientTransaction(ContextWithElement(ctx, h.elm), tx)
}

// elemSrvTxHandler adapts a middleware [ServerTransactionHandler] to the element
// callback lease.
type elemSrvTxHandler struct {
	elm  *Element
	hdlr ServerTransactionHandler
}

func (h elemSrvTxHandler) HandleServerTransaction(ctx context.Context, tx ServerTransaction) {
	release, ok := h.elm.admitCallback()
	if !ok {
		return
	}
	defer release()

	h.hdlr.HandleServerTransaction(ContextWithElement(ctx, h.elm), tx)
}
