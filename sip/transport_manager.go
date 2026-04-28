package sip

import (
	"cmp"
	"context"
	"iter"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/log"
)

// TransportManager errors.
const (
	// ErrTransportManagerClosed is returned when the transport manager is closed.
	ErrTransportManagerClosed errors.Error = "transport manager closed"
	ErrNoTransport            errors.Error = "no transport"
)

type TransportManager struct {
	transps   syncutil.RWMap[TransportProto, Transport]
	closed    atomic.Bool
	closeOnce sync.Once
	closeErr  error
	log       *slog.Logger

	inReqInts  types.CallbackManager[*transpMngInterceptBinding[InboundRequestInterceptor]]
	inResInts  types.CallbackManager[*transpMngInterceptBinding[InboundResponseInterceptor]]
	outReqInts types.CallbackManager[*transpMngInterceptBinding[OutboundRequestInterceptor]]
	outResInts types.CallbackManager[*transpMngInterceptBinding[OutboundResponseInterceptor]]
}

type transpMngInterceptBinding[T any] struct {
	sync.Mutex
	interceptor T
	unbinds     map[Transport]func()
}

type TransportManagerOptions struct {
	// Logger is the logger.
	// If nil, the [log.Default] is used.
	Logger *slog.Logger
}

func (o *TransportManagerOptions) log() *slog.Logger {
	if o == nil || o.Logger == nil {
		return log.Default()
	}
	return o.Logger
}

// NewTransportManager creates a new transport manager.
// The provided transport will be used as the default transport.
// Options are optional and can be nil, see [TransportManagerOptions] for details.
func NewTransportManager(opts *TransportManagerOptions) *TransportManager {
	return &TransportManager{
		log: opts.log(),
	}
}

func (tpm *TransportManager) getLog() *slog.Logger {
	if tpm == nil || tpm.log == nil {
		return log.Default()
	}
	return tpm.log
}

func (tpm *TransportManager) Close() error {
	if tpm == nil {
		return nil
	}

	tpm.closeOnce.Do(func() {
		tpm.closeErr = tpm.close()
		tpm.closed.Store(true)

		tpm.getLog().Debug("transport manager closed")
	})

	return errors.Wrap(tpm.closeErr)
}

func (tpm *TransportManager) close() error {
	errs := make([]error, 0, tpm.transps.Len())
	for _, tp := range tpm.transps.All() {
		if err := tp.Close(); err != nil {
			errs = append(errs, errors.Errorf("close transport %q: %w", tp.Metadata().Proto, err))
		}

		tpm.untrackTransport(tp)
	}

	return errors.JoinPrefixWrap("transport manager close errors:", errs...)
}

func (tpm *TransportManager) TrackTransport(tp Transport) error {
	if tpm.closed.Load() {
		return errors.Wrap(ErrTransportManagerClosed)
	}

	if tp == nil {
		return errors.NewInvalidArgumentErrorWrap("nil transport")
	}

	key := tp.Metadata().Proto.Canonic()

	if _, ok := tpm.transps.LoadOrStore(key, tp); ok {
		return nil
	}

	tpm.bindTransportInterceptors(tp)

	tpm.getLog().Debug("transport tracked", slog.Any("transport", tp))

	return nil
}

func (tpm *TransportManager) UntrackTransport(tp Transport) error {
	if tpm.closed.Load() {
		return errors.Wrap(ErrTransportManagerClosed)
	}

	if tp == nil {
		return errors.NewInvalidArgumentErrorWrap("nil transport")
	}

	tpm.untrackTransport(tp)

	return nil
}

func (tpm *TransportManager) untrackTransport(tp Transport) {
	key := tp.Metadata().Proto.Canonic()

	if _, ok := tpm.transps.LoadAndDelete(key); !ok {
		return
	}

	tpm.unbindTransportInterceptors(tp)

	tpm.getLog().Debug("transport untracked", slog.Any("transport", tp))
}

func (tpm *TransportManager) GetTransport(proto TransportProto) Transport {
	if tp, ok := tpm.transps.Load(proto.Canonic()); ok {
		return tp
	}
	return nil
}

func (tpm *TransportManager) AllTransports() iter.Seq[Transport] {
	return func(yield func(tp Transport) bool) {
		sorted := slices.SortedFunc(func(yield func(Transport) bool) {
			for _, tp := range tpm.transps.All() {
				if !yield(tp) {
					return
				}
			}
		}, func(a, b Transport) int {
			return cmp.Compare(a.Metadata().Priority, b.Metadata().Priority)
		})
		for _, tp := range sorted {
			if !yield(tp) {
				return
			}
		}
	}
}

func (tpm *TransportManager) MetadataByProto(proto TransportProto) TransportMetadata {
	if tp, ok := tpm.transps.Load(proto.Canonic()); ok {
		return tp.Metadata()
	}
	return TransportMetadata{}
}

func (tpm *TransportManager) MetadataByNAPTRService(service string) TransportMetadata {
	for _, tp := range tpm.transps.All() {
		if tp.Metadata().NAPTRService == service {
			return tp.Metadata()
		}
	}

	return TransportMetadata{}
}

func (tpm *TransportManager) AllMetadata() iter.Seq[TransportMetadata] {
	return func(yield func(TransportMetadata) bool) {
		for tp := range tpm.AllTransports() {
			if !yield(tp.Metadata()) {
				return
			}
		}
	}
}

func (tpm *TransportManager) UseInboundRequestInterceptor(interceptor InboundRequestInterceptor) (unbind func()) {
	if interceptor == nil || tpm.closed.Load() {
		return func() {}
	}

	entry := &transpMngInterceptBinding[InboundRequestInterceptor]{
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
	if interceptor == nil || tpm.closed.Load() {
		return func() {}
	}

	entry := &transpMngInterceptBinding[InboundResponseInterceptor]{
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
	if interceptor == nil || tpm.closed.Load() {
		return func() {}
	}

	entry := &transpMngInterceptBinding[OutboundRequestInterceptor]{
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
	if interceptor == nil || tpm.closed.Load() {
		return func() {}
	}

	entry := &transpMngInterceptBinding[OutboundResponseInterceptor]{
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
	if interceptor == nil || tpm.closed.Load() {
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

func (*TransportManager) bindInboundRequestInterceptor(tp Transport, entry *transpMngInterceptBinding[InboundRequestInterceptor]) {
	if tp == nil || entry == nil || entry.interceptor == nil {
		return
	}

	wrapped := InboundRequestInterceptorFunc(func(ctx context.Context, req *InboundRequestEnvelope, next RequestReceiver) error {
		return errors.Wrap(entry.interceptor.InterceptInboundRequest(ctx, req, next))
	})

	entry.Lock()
	if _, ok := entry.unbinds[tp]; ok {
		entry.Unlock()
		return
	}

	entry.unbinds[tp] = tp.UseInboundRequestInterceptor(wrapped)
	entry.Unlock()
}

func (*TransportManager) unbindInboundRequestInterceptor(entry *transpMngInterceptBinding[InboundRequestInterceptor]) {
	if entry == nil {
		return
	}

	entry.Lock()
	for tp, unbind := range entry.unbinds {
		if unbind != nil {
			unbind()
		}

		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) bindInboundResponseInterceptor(tp Transport, entry *transpMngInterceptBinding[InboundResponseInterceptor]) {
	if tp == nil || entry == nil || entry.interceptor == nil {
		return
	}

	wrapped := InboundResponseInterceptorFunc(func(ctx context.Context, res *InboundResponseEnvelope, next ResponseReceiver) error {
		return errors.Wrap(entry.interceptor.InterceptInboundResponse(ctx, res, next))
	})

	entry.Lock()
	if _, ok := entry.unbinds[tp]; ok {
		entry.Unlock()
		return
	}

	entry.unbinds[tp] = tp.UseInboundResponseInterceptor(wrapped)
	entry.Unlock()
}

func (*TransportManager) unbindInboundResponseInterceptor(entry *transpMngInterceptBinding[InboundResponseInterceptor]) {
	if entry == nil {
		return
	}

	entry.Lock()
	for tp, unbind := range entry.unbinds {
		if unbind != nil {
			unbind()
		}

		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) bindOutboundRequestInterceptor(tp Transport, entry *transpMngInterceptBinding[OutboundRequestInterceptor]) {
	if tp == nil || entry == nil || entry.interceptor == nil {
		return
	}

	wrapped := OutboundRequestInterceptorFunc(func(ctx context.Context, req *OutboundRequestEnvelope, opts *SendRequestOptions, next RequestSender) error {
		return errors.Wrap(entry.interceptor.InterceptOutboundRequest(ctx, req, opts, next))
	})

	entry.Lock()
	if _, ok := entry.unbinds[tp]; ok {
		entry.Unlock()
		return
	}

	entry.unbinds[tp] = tp.UseOutboundRequestInterceptor(wrapped)
	entry.Unlock()
}

func (*TransportManager) unbindOutboundRequestInterceptor(entry *transpMngInterceptBinding[OutboundRequestInterceptor]) {
	if entry == nil {
		return
	}

	entry.Lock()
	for tp, unbind := range entry.unbinds {
		if unbind != nil {
			unbind()
		}

		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) bindOutboundResponseInterceptor(tp Transport, entry *transpMngInterceptBinding[OutboundResponseInterceptor]) {
	if tp == nil || entry == nil || entry.interceptor == nil {
		return
	}

	wrapped := OutboundResponseInterceptorFunc(func(ctx context.Context, res *OutboundResponseEnvelope, opts *SendResponseOptions, next ResponseSender) error {
		return errors.Wrap(entry.interceptor.InterceptOutboundResponse(ctx, res, opts, next))
	})

	entry.Lock()
	if _, ok := entry.unbinds[tp]; ok {
		entry.Unlock()
		return
	}

	entry.unbinds[tp] = tp.UseOutboundResponseInterceptor(wrapped)
	entry.Unlock()
}

func (*TransportManager) unbindOutboundResponseInterceptor(entry *transpMngInterceptBinding[OutboundResponseInterceptor]) {
	if entry == nil {
		return
	}

	entry.Lock()
	for tp, unbind := range entry.unbinds {
		if unbind != nil {
			unbind()
		}

		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) unbindInboundRequestInterceptorFrom(tp Transport, entry *transpMngInterceptBinding[InboundRequestInterceptor]) {
	if entry == nil || tp == nil {
		return
	}

	entry.Lock()
	if unbind, ok := entry.unbinds[tp]; ok {
		if unbind != nil {
			unbind()
		}

		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) unbindInboundResponseInterceptorFrom(tp Transport, entry *transpMngInterceptBinding[InboundResponseInterceptor]) {
	if entry == nil || tp == nil {
		return
	}

	entry.Lock()
	if unbind, ok := entry.unbinds[tp]; ok {
		if unbind != nil {
			unbind()
		}

		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) unbindOutboundRequestInterceptorFrom(tp Transport, entry *transpMngInterceptBinding[OutboundRequestInterceptor]) {
	if entry == nil || tp == nil {
		return
	}

	entry.Lock()
	if unbind, ok := entry.unbinds[tp]; ok {
		if unbind != nil {
			unbind()
		}

		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) unbindOutboundResponseInterceptorFrom(tp Transport, entry *transpMngInterceptBinding[OutboundResponseInterceptor]) {
	if entry == nil || tp == nil {
		return
	}

	entry.Lock()
	if unbind, ok := entry.unbinds[tp]; ok {
		if unbind != nil {
			unbind()
		}

		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (tpm *TransportManager) SendRequest(ctx context.Context, req *OutboundRequestEnvelope, opts *SendRequestOptions) error {
	if tpm.closed.Load() {
		return errors.Wrap(ErrTransportManagerClosed)
	}

	tp := tpm.resolveReqTransp(req)
	if tp == nil {
		return errors.Wrap(ErrNoTransport)
	}

	return errors.Wrap(tp.SendRequest(ctx, req, opts))
}

func (tpm *TransportManager) resolveReqTransp(req *OutboundRequestEnvelope) Transport {
	tp := tpm.resolveTranspByProto(req.Transport())

	// if tp != nil && !tp.Metadata().Reliable() && len(req.Render(nil)) > int(MTU)-200 {
	// 	for t := range tpm.AllTransports() {
	// 		if t.Metadata().Reliable() {
	// 			tp = t
	// 			break
	// 		}
	// 	}
	// }

	if tp != nil {
		req.SetTransport(tp.Metadata().Proto)
	}

	return tp
}

func (tpm *TransportManager) SendResponse(ctx context.Context, res *OutboundResponseEnvelope, opts *SendResponseOptions) error {
	if tpm.closed.Load() {
		return errors.Wrap(ErrTransportManagerClosed)
	}

	tp := tpm.resolveResTransp(res)
	if tp == nil {
		return errors.Wrap(ErrNoTransport)
	}

	return errors.Wrap(tp.SendResponse(ctx, res, opts))
}

func (tpm *TransportManager) resolveResTransp(res *OutboundResponseEnvelope) Transport {
	proto := res.Transport()
	res.AccessMessage(func(r *Response) {
		if v, ok := r.Headers.FirstViaHop(); ok {
			proto = v.Transport
		}
	})

	tp := tpm.resolveTranspByProto(proto)

	if tp != nil {
		res.SetTransport(tp.Metadata().Proto)
	}

	return tp
}

func (tpm *TransportManager) Respond(
	ctx context.Context,
	req *InboundRequestEnvelope,
	sts ResponseStatus,
	opts *RespondOptions,
) error {
	if tpm.closed.Load() {
		return errors.Wrap(ErrTransportManagerClosed)
	}
	return errors.Wrap(respondStateless(ctx, tpm, req, sts, opts))
}

func (tpm *TransportManager) ListenAndServe(ctx context.Context, proto TransportProto, addr string) error {
	if tpm.closed.Load() {
		return errors.Wrap(ErrTransportManagerClosed)
	}

	tp := tpm.resolveTranspByProto(proto)
	if tp == nil {
		return errors.Wrap(ErrNoTransport)
	}

	return errors.Wrap(tp.ListenAndServe(ctx, addr))
}

func (tpm *TransportManager) resolveTranspByProto(proto TransportProto) Transport {
	if proto.IsValid() {
		if tp, ok := tpm.transps.Load(proto.Canonic()); ok {
			return tp
		}
	}

	return nil
}
