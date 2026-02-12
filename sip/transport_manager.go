package sip

import (
	"context"
	"errors"
	"iter"
	"net/netip"
	"sync"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/types"
)

// TransportManager manages multiple transports.
// It is responsible for tracking transports and serving them and can be used as
// a single point of sending/receiving messages.
type TransportManager struct {
	mu      sync.RWMutex
	transps transpsByProto
	defTp   Transport
	tpsWg   sync.WaitGroup
	closed  bool
	srvCtx  context.Context
	srvOnce sync.Once
	srvErr  error
	serving bool
	srvErrs []error

	inReqInts  types.CallbackManager[*tpmInterceptorBinding[InboundRequestInterceptor]]
	inResInts  types.CallbackManager[*tpmInterceptorBinding[InboundResponseInterceptor]]
	outReqInts types.CallbackManager[*tpmInterceptorBinding[OutboundRequestInterceptor]]
	outResInts types.CallbackManager[*tpmInterceptorBinding[OutboundResponseInterceptor]]

	closeOnce sync.Once
	closeErr  error
}

type tpmInterceptorBinding[T any] struct {
	mu          sync.Mutex
	interceptor T
	unbinds     map[Transport]func()
}

type (
	transpsByProto = map[TransportProto]transpsByAddr
	transpsByAddr  = map[netip.AddrPort]Transport
)

// TrackTransport adds a transport to the manager.
// If isDef is true, the transport will be used as default transport when sending messages.
func (tpm *TransportManager) TrackTransport(tp Transport, isDef bool) error {
	proto, ok := GetTransportProto(tp)
	if !ok {
		return errtrace.Wrap(NewInvalidArgumentError("invalid transport"))
	}
	laddr, ok := GetTransportLocalAddr(tp)
	if !ok {
		return errtrace.Wrap(NewInvalidArgumentError("invalid transport"))
	}

	tpm.mu.Lock()
	if tpm.closed {
		tpm.mu.Unlock()
		return errtrace.Wrap(ErrTransportClosed)
	}

	if isDef {
		tpm.defTp = tp
	}

	if tpm.transps == nil {
		tpm.transps = make(transpsByProto)
	}
	byAddr := tpm.transps[proto]
	if byAddr == nil {
		byAddr = make(transpsByAddr)
		tpm.transps[proto] = byAddr
	}
	if _, ok := byAddr[laddr]; ok {
		tpm.mu.Unlock()
		return nil
	}
	byAddr[laddr] = tp
	shouldServe := tpm.serving
	srvCtx := tpm.srvCtx
	tpm.mu.Unlock()

	tpm.bindTransportInterceptors(tp)

	if shouldServe {
		tpm.serveTransps(srvCtx, []Transport{tp})
	}

	return nil
}

func (tpm *TransportManager) UntrackTransport(tp Transport) error {
	proto, ok := GetTransportProto(tp)
	if !ok {
		return errtrace.Wrap(NewInvalidArgumentError("invalid transport"))
	}
	laddr, ok := GetTransportLocalAddr(tp)
	if !ok {
		return errtrace.Wrap(NewInvalidArgumentError("invalid transport"))
	}

	tpm.mu.Lock()
	byAddr := tpm.transps[proto]
	if byAddr == nil {
		tpm.mu.Unlock()
		return nil
	}
	if _, ok = byAddr[laddr]; !ok {
		tpm.mu.Unlock()
		return nil
	}

	delete(byAddr, laddr)
	if len(byAddr) == 0 {
		delete(tpm.transps, proto)
	}

	if tpm.defTp == tp {
		for _, addrs := range tpm.transps {
			for _, t := range addrs {
				tpm.defTp = t
				break
			}
			break
		}
	}
	tpm.mu.Unlock()

	tpm.unbindTransportInterceptors(tp)
	return nil
}

func (tpm *TransportManager) GetTransport(proto TransportProto, laddr netip.AddrPort) (Transport, bool) {
	tpm.mu.RLock()
	defer tpm.mu.RUnlock()

	tp, ok := tpm.getTransp(proto, laddr)
	if !ok {
		return nil, false
	}
	return tp, true
}

func (tpm *TransportManager) getTransp(proto TransportProto, laddr netip.AddrPort) (Transport, bool) {
	byAddr := tpm.transps[proto]
	if byAddr == nil {
		return nil, false
	}
	tp, ok := byAddr[laddr]
	if !ok {
		return nil, false
	}
	return tp, true
}

func (tpm *TransportManager) AllTransports() iter.Seq[Transport] {
	return func(yield func(tp Transport) bool) {
		tpm.mu.RLock()
		defer tpm.mu.RUnlock()

		for _, byAddr := range tpm.transps {
			for _, tp := range byAddr {
				if !yield(tp) {
					return
				}
			}
		}
	}
}

// SendRequest sends the request to the remote address specified in the envelope.
//
// The transport is selected based on the following rules:
//   - if the envelope has a transport and local address, the transport is selected based on them;
//   - if the envelope has no transport or local address, the default transport is selected;
//   - if there is no default transport, the first transport is selected;
//   - if no transport is selected, [ErrNoTransport] is returned.
//
// When the transport is selected, the request is sent using the [Transport.SendRequest] method.
func (tpm *TransportManager) SendRequest(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	opts *SendRequestOptions,
) error {
	tpm.mu.RLock()
	if tpm.closed {
		tpm.mu.RUnlock()
		return errtrace.Wrap(ErrTransportClosed)
	}
	var tp Transport
	if proto, laddr := req.Transport(), req.LocalAddr(); proto != "" && laddr != zeroAddrPort {
		tp, _ = tpm.getTransp(proto, laddr)
	}
	if tp == nil {
		if tpm.defTp == nil {
			for _, byAddr := range tpm.transps {
				for _, tp = range byAddr {
					break
				}
				break
			}
		} else {
			tp = tpm.defTp
		}
	}
	tpm.mu.RUnlock()
	if tp == nil {
		return errtrace.Wrap(ErrNoTransport)
	}
	return errtrace.Wrap(tp.SendRequest(ctx, req, opts))
}

// SendResponse sends the response to a remote address resolved with steps
// defined in RFC 3261 Section 18.2.2. and RFC 3263 Section 5.
//
// The transport is selected based on the following rules:
//   - if the envelope has a transport and local address, the transport is selected based on them;
//   - if the envelope has no transport or local address, the default transport is selected;
//   - if there is no default transport, the first transport is selected;
//   - if no transport is selected, [ErrNoTransport] is returned.
//
// When the transport is selected, the response is sent using the [Transport.SendResponse] method.
func (tpm *TransportManager) SendResponse(
	ctx context.Context,
	res *OutboundResponseEnvelope,
	opts *SendResponseOptions,
) error {
	tpm.mu.RLock()
	if tpm.closed {
		tpm.mu.RUnlock()
		return errtrace.Wrap(ErrTransportClosed)
	}
	var tp Transport
	if proto, laddr := res.Transport(), res.LocalAddr(); proto != "" && laddr != zeroAddrPort {
		tp, _ = tpm.getTransp(proto, laddr)
	}
	if tp == nil {
		if tpm.defTp == nil {
			for _, byAddr := range tpm.transps {
				for _, tp = range byAddr {
					break
				}
				break
			}
		} else {
			tp = tpm.defTp
		}
	}
	tpm.mu.RUnlock()
	if tp == nil {
		return errtrace.Wrap(ErrNoTransport)
	}
	return errtrace.Wrap(tp.SendResponse(ctx, res, opts))
}

func (tpm *TransportManager) Respond(
	ctx context.Context,
	req *InboundRequestEnvelope,
	sts ResponseStatus,
	opts *RespondOptions,
) error {
	return errtrace.Wrap(RespondStateless(ctx, tpm, req, sts, opts))
}

func (tpm *TransportManager) UseInboundRequestInterceptor(interceptor InboundRequestInterceptor) (unbind func()) {
	if interceptor == nil {
		return func() {}
	}

	entry := &tpmInterceptorBinding[InboundRequestInterceptor]{
		interceptor: interceptor,
		unbinds:     make(map[Transport]func()),
	}
	remove := tpm.inReqInts.Add(entry)
	for tp := range tpm.AllTransports() {
		tpm.bindInboundRequestInterceptor(tp, entry)
	}
	return func() {
		remove()
		tpm.unbindInboundRequestInterceptor(entry)
	}
}

func (tpm *TransportManager) UseInboundResponseInterceptor(interceptor InboundResponseInterceptor) (unbind func()) {
	if interceptor == nil {
		return func() {}
	}

	entry := &tpmInterceptorBinding[InboundResponseInterceptor]{
		interceptor: interceptor,
		unbinds:     make(map[Transport]func()),
	}
	remove := tpm.inResInts.Add(entry)
	for tp := range tpm.AllTransports() {
		tpm.bindInboundResponseInterceptor(tp, entry)
	}
	return func() {
		remove()
		tpm.unbindInboundResponseInterceptor(entry)
	}
}

func (tpm *TransportManager) UseOutboundRequestInterceptor(interceptor OutboundRequestInterceptor) (unbind func()) {
	if interceptor == nil {
		return func() {}
	}

	entry := &tpmInterceptorBinding[OutboundRequestInterceptor]{
		interceptor: interceptor,
		unbinds:     make(map[Transport]func()),
	}
	remove := tpm.outReqInts.Add(entry)
	for tp := range tpm.AllTransports() {
		tpm.bindOutboundRequestInterceptor(tp, entry)
	}
	return func() {
		remove()
		tpm.unbindOutboundRequestInterceptor(entry)
	}
}

func (tpm *TransportManager) UseOutboundResponseInterceptor(interceptor OutboundResponseInterceptor) (unbind func()) {
	if interceptor == nil {
		return func() {}
	}

	entry := &tpmInterceptorBinding[OutboundResponseInterceptor]{
		interceptor: interceptor,
		unbinds:     make(map[Transport]func()),
	}
	remove := tpm.outResInts.Add(entry)
	for tp := range tpm.AllTransports() {
		tpm.bindOutboundResponseInterceptor(tp, entry)
	}
	return func() {
		remove()
		tpm.unbindOutboundResponseInterceptor(entry)
	}
}

func (tpm *TransportManager) UseInterceptor(interceptor MessageInterceptor) (unbind func()) {
	if interceptor == nil {
		return func() {}
	}

	var unbinds []func()
	if inbound := interceptor.InboundRequestInterceptor(); inbound != nil {
		unbinds = append(unbinds, tpm.UseInboundRequestInterceptor(inbound))
	}
	if inbound := interceptor.InboundResponseInterceptor(); inbound != nil {
		unbinds = append(unbinds, tpm.UseInboundResponseInterceptor(inbound))
	}
	if outbound := interceptor.OutboundRequestInterceptor(); outbound != nil {
		unbinds = append(unbinds, tpm.UseOutboundRequestInterceptor(outbound))
	}
	if outbound := interceptor.OutboundResponseInterceptor(); outbound != nil {
		unbinds = append(unbinds, tpm.UseOutboundResponseInterceptor(outbound))
	}
	return func() {
		for _, fn := range unbinds {
			fn()
		}
	}
}

func (tpm *TransportManager) Serve(ctx context.Context) error {
	tpm.srvOnce.Do(func() {
		tpm.srvErr = tpm.serve(ctx)
	})
	return errtrace.Wrap(tpm.srvErr)
}

func (tpm *TransportManager) serve(ctx context.Context) error {
	tpm.mu.Lock()
	if tpm.closed {
		tpm.mu.Unlock()
		return errtrace.Wrap(ErrTransportClosed)
	}
	if tpm.serving {
		// Serve already running; should not happen due to srvOnce but keep guard.
		tpm.mu.Unlock()
		return nil
	}
	tracked := tpm.snapshotTracked()
	tpm.serving = true
	tpm.srvCtx = ctx
	tpm.mu.Unlock()

	tpm.serveTransps(ctx, tracked)
	tpm.tpsWg.Wait()

	tpm.mu.Lock()
	tpm.serving = false
	tpm.srvCtx = nil
	errs := append([]error(nil), tpm.srvErrs...)
	tpm.srvErrs = nil
	closed := tpm.closed
	tpm.mu.Unlock()

	if len(errs) > 0 {
		return errtrace.Wrap(errorutil.Join(errs...))
	}
	if closed {
		return errtrace.Wrap(ErrTransportClosed)
	}
	return nil
}

func (tpm *TransportManager) Close(ctx context.Context) error {
	tpm.closeOnce.Do(func() {
		tpm.closeErr = tpm.close(ctx)
	})
	return errtrace.Wrap(tpm.closeErr)
}

func (tpm *TransportManager) close(ctx context.Context) error {
	tpm.mu.Lock()
	if tpm.closed {
		tpm.mu.Unlock()
		return nil
	}
	tpm.closed = true
	tracked := tpm.snapshotTracked()
	tpm.transps = nil
	tpm.defTp = nil
	tpm.mu.Unlock()

	var errs []error
	for _, tp := range tracked {
		if err := tp.Close(ctx); err != nil && !errors.Is(err, ErrTransportClosed) {
			errs = append(errs, err)
		}
	}

	tpm.tpsWg.Wait()

	if len(errs) == 0 {
		return nil
	}
	return errtrace.Wrap(errorutil.JoinPrefix("failed to close transports:", errs...))
}

func (tpm *TransportManager) snapshotTracked() []Transport {
	var count int
	for _, addrs := range tpm.transps {
		count += len(addrs)
	}
	if count == 0 {
		return nil
	}

	tracked := make([]Transport, 0, count)
	for _, addrs := range tpm.transps {
		for _, tp := range addrs {
			tracked = append(tracked, tp)
		}
	}
	return tracked
}

func (tpm *TransportManager) serveTransps(ctx context.Context, tracked []Transport) {
	if len(tracked) == 0 {
		return
	}

	tpm.tpsWg.Add(len(tracked))
	for _, tp := range tracked {
		go tpm.serveTransp(ctx, tp)
	}
}

func (tpm *TransportManager) serveTransp(ctx context.Context, tp Transport) {
	defer tpm.tpsWg.Done()
	defer tpm.UntrackTransport(tp) //nolint:errcheck

	if err := tp.Serve(ctx); err != nil && !errors.Is(err, ErrTransportClosed) {
		tpm.mu.Lock()
		tpm.srvErrs = append(tpm.srvErrs, err)
		tpm.mu.Unlock()
	}
}

func (tpm *TransportManager) bindTransportInterceptors(tp Transport) {
	if tp == nil {
		return
	}

	for entry := range tpm.inReqInts.All() {
		tpm.bindInboundRequestInterceptor(tp, entry)
	}
	for entry := range tpm.inResInts.All() {
		tpm.bindInboundResponseInterceptor(tp, entry)
	}
	for entry := range tpm.outReqInts.All() {
		tpm.bindOutboundRequestInterceptor(tp, entry)
	}
	for entry := range tpm.outResInts.All() {
		tpm.bindOutboundResponseInterceptor(tp, entry)
	}
}

func (tpm *TransportManager) unbindTransportInterceptors(tp Transport) {
	if tp == nil {
		return
	}

	for entry := range tpm.inReqInts.All() {
		tpm.unbindInboundRequestInterceptorFrom(tp, entry)
	}
	for entry := range tpm.inResInts.All() {
		tpm.unbindInboundResponseInterceptorFrom(tp, entry)
	}
	for entry := range tpm.outReqInts.All() {
		tpm.unbindOutboundRequestInterceptorFrom(tp, entry)
	}
	for entry := range tpm.outResInts.All() {
		tpm.unbindOutboundResponseInterceptorFrom(tp, entry)
	}
}

func (*TransportManager) bindInboundRequestInterceptor(
	tp Transport,
	entry *tpmInterceptorBinding[InboundRequestInterceptor],
) {
	if tp == nil || entry == nil || entry.interceptor == nil {
		return
	}

	wrapped := InboundRequestInterceptorFunc(
		func(ctx context.Context, req *InboundRequestEnvelope, next RequestReceiver) error {
			return errtrace.Wrap(entry.interceptor.InterceptInboundRequest(ctx, req, next))
		},
	)

	entry.mu.Lock()
	if _, ok := entry.unbinds[tp]; ok {
		entry.mu.Unlock()
		return
	}
	entry.unbinds[tp] = tp.UseInboundRequestInterceptor(wrapped)
	entry.mu.Unlock()
}

func (*TransportManager) unbindInboundRequestInterceptor(
	entry *tpmInterceptorBinding[InboundRequestInterceptor],
) {
	if entry == nil {
		return
	}

	entry.mu.Lock()
	for tp, unbind := range entry.unbinds {
		if unbind != nil {
			unbind()
		}
		delete(entry.unbinds, tp)
	}
	entry.mu.Unlock()
}

func (*TransportManager) bindInboundResponseInterceptor(
	tp Transport,
	entry *tpmInterceptorBinding[InboundResponseInterceptor],
) {
	if tp == nil || entry == nil || entry.interceptor == nil {
		return
	}

	wrapped := InboundResponseInterceptorFunc(
		func(ctx context.Context, res *InboundResponseEnvelope, next ResponseReceiver) error {
			return errtrace.Wrap(entry.interceptor.InterceptInboundResponse(ctx, res, next))
		},
	)

	entry.mu.Lock()
	if _, ok := entry.unbinds[tp]; ok {
		entry.mu.Unlock()
		return
	}
	entry.unbinds[tp] = tp.UseInboundResponseInterceptor(wrapped)
	entry.mu.Unlock()
}

func (*TransportManager) unbindInboundResponseInterceptor(
	entry *tpmInterceptorBinding[InboundResponseInterceptor],
) {
	if entry == nil {
		return
	}

	entry.mu.Lock()
	for tp, unbind := range entry.unbinds {
		if unbind != nil {
			unbind()
		}
		delete(entry.unbinds, tp)
	}
	entry.mu.Unlock()
}

func (*TransportManager) bindOutboundRequestInterceptor(
	tp Transport,
	entry *tpmInterceptorBinding[OutboundRequestInterceptor],
) {
	if tp == nil || entry == nil || entry.interceptor == nil {
		return
	}

	wrapped := OutboundRequestInterceptorFunc(
		func(ctx context.Context, req *OutboundRequestEnvelope, opts *SendRequestOptions, next RequestSender) error {
			return errtrace.Wrap(entry.interceptor.InterceptOutboundRequest(ctx, req, opts, next))
		},
	)

	entry.mu.Lock()
	if _, ok := entry.unbinds[tp]; ok {
		entry.mu.Unlock()
		return
	}
	entry.unbinds[tp] = tp.UseOutboundRequestInterceptor(wrapped)
	entry.mu.Unlock()
}

func (*TransportManager) unbindOutboundRequestInterceptor(
	entry *tpmInterceptorBinding[OutboundRequestInterceptor],
) {
	if entry == nil {
		return
	}

	entry.mu.Lock()
	for tp, unbind := range entry.unbinds {
		if unbind != nil {
			unbind()
		}
		delete(entry.unbinds, tp)
	}
	entry.mu.Unlock()
}

func (*TransportManager) bindOutboundResponseInterceptor(
	tp Transport,
	entry *tpmInterceptorBinding[OutboundResponseInterceptor],
) {
	if tp == nil || entry == nil || entry.interceptor == nil {
		return
	}

	wrapped := OutboundResponseInterceptorFunc(
		func(ctx context.Context, res *OutboundResponseEnvelope, opts *SendResponseOptions, next ResponseSender) error {
			return errtrace.Wrap(entry.interceptor.InterceptOutboundResponse(ctx, res, opts, next))
		},
	)

	entry.mu.Lock()
	if _, ok := entry.unbinds[tp]; ok {
		entry.mu.Unlock()
		return
	}
	entry.unbinds[tp] = tp.UseOutboundResponseInterceptor(wrapped)
	entry.mu.Unlock()
}

func (*TransportManager) unbindOutboundResponseInterceptor(
	entry *tpmInterceptorBinding[OutboundResponseInterceptor],
) {
	if entry == nil {
		return
	}

	entry.mu.Lock()
	for tp, unbind := range entry.unbinds {
		if unbind != nil {
			unbind()
		}
		delete(entry.unbinds, tp)
	}
	entry.mu.Unlock()
}

func (*TransportManager) unbindInboundRequestInterceptorFrom(
	tp Transport,
	entry *tpmInterceptorBinding[InboundRequestInterceptor],
) {
	if entry == nil || tp == nil {
		return
	}

	entry.mu.Lock()
	if unbind, ok := entry.unbinds[tp]; ok {
		if unbind != nil {
			unbind()
		}
		delete(entry.unbinds, tp)
	}
	entry.mu.Unlock()
}

func (*TransportManager) unbindInboundResponseInterceptorFrom(
	tp Transport,
	entry *tpmInterceptorBinding[InboundResponseInterceptor],
) {
	if entry == nil || tp == nil {
		return
	}

	entry.mu.Lock()
	if unbind, ok := entry.unbinds[tp]; ok {
		if unbind != nil {
			unbind()
		}
		delete(entry.unbinds, tp)
	}
	entry.mu.Unlock()
}

func (*TransportManager) unbindOutboundRequestInterceptorFrom(
	tp Transport,
	entry *tpmInterceptorBinding[OutboundRequestInterceptor],
) {
	if entry == nil || tp == nil {
		return
	}

	entry.mu.Lock()
	if unbind, ok := entry.unbinds[tp]; ok {
		if unbind != nil {
			unbind()
		}
		delete(entry.unbinds, tp)
	}
	entry.mu.Unlock()
}

func (*TransportManager) unbindOutboundResponseInterceptorFrom(
	tp Transport,
	entry *tpmInterceptorBinding[OutboundResponseInterceptor],
) {
	if entry == nil || tp == nil {
		return
	}

	entry.mu.Lock()
	if unbind, ok := entry.unbinds[tp]; ok {
		if unbind != nil {
			unbind()
		}
		delete(entry.unbinds, tp)
	}
	entry.mu.Unlock()
}
