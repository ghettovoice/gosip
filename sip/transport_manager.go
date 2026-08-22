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
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/log"
)

const (
	ErrNoTransport            Error = "no transport resolved"
	ErrTransportManagerClosed Error = "transport manager closed"
)

const (
	tpmStateRunning uint32 = iota
	tpmStateClosing
	tpmStateClosed
)

type TransportManager struct {
	Logger *slog.Logger

	lcMu  sync.Mutex
	state atomic.Uint32

	transps   syncutil.RWMap[TransportProto, Transport]
	closeOnce sync.Once
	closeErr  error

	inReqInts  types.CallbackManager[*transpInterceptBinding[InboundRequestInterceptor]]
	inResInts  types.CallbackManager[*transpInterceptBinding[InboundResponseInterceptor]]
	outReqInts types.CallbackManager[*transpInterceptBinding[OutboundRequestInterceptor]]
	outResInts types.CallbackManager[*transpInterceptBinding[OutboundResponseInterceptor]]
}

var (
	_ TransportMetadataProvider = (*TransportManager)(nil)
	_ MessageInterceptorChain   = (*TransportManager)(nil)
)

type transpInterceptBinding[T any] struct {
	sync.Mutex
	interceptor T
	unbinds     map[Transport]func()
}

func (tpm *TransportManager) log() *slog.Logger {
	if tpm.Logger == nil {
		return log.Default()
	}
	return tpm.Logger
}

func (tpm *TransportManager) isClosing() bool {
	return tpm.state.Load() >= tpmStateClosing
}

func (tpm *TransportManager) Close(ctx context.Context) error {
	tpm.closeOnce.Do(func() {
		tpm.lcMu.Lock()
		tpm.state.Store(tpmStateClosing)
		tpm.lcMu.Unlock()

		tpm.closeErr = tpm.close(ctx)

		tpm.lcMu.Lock()
		tpm.state.Store(tpmStateClosed)
		tpm.lcMu.Unlock()

		tpm.log().LogAttrs(ctx, slog.LevelDebug, "transport manager closed")
	})
	return errors.Wrap(tpm.closeErr)
}

func (tpm *TransportManager) close(ctx context.Context) error {
	errs := make([]error, 0, tpm.transps.Len())
	for _, tp := range tpm.transps.All() {
		if err := tp.Close(ctx); err != nil {
			errs = append(errs, errors.Errorf("close transport %q: %w", tp.Metadata().Proto, err))
		}
		tpm.untrackTransp(tp)
	}
	return errors.JoinPrefixWrap("transport manager close errors:", errs...)
}

func (tpm *TransportManager) TrackTransport(tp Transport) error {
	if tp == nil {
		return errors.ErrorWrap("nil transport")
	}

	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	if tpm.isClosing() {
		return errors.Wrap(ErrTransportManagerClosed)
	}

	tpm.trackTransp(tp)
	return nil
}

func (tpm *TransportManager) trackTransp(tp Transport) {
	key := tp.Metadata().Proto.Canonic()
	if _, ok := tpm.transps.LoadOrStore(key, tp); ok {
		return
	}
	tpm.bindTranspInterceptors(tp)

	tpm.log().Debug("transport tracked", slog.Any("transport", tp))
}

func (tpm *TransportManager) UntrackTransport(tp Transport) error {
	if tp == nil {
		return errors.ErrorWrap("nil transport")
	}

	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	if tpm.isClosing() {
		return errors.Wrap(ErrTransportManagerClosed)
	}

	tpm.untrackTransp(tp)
	return nil
}

func (tpm *TransportManager) untrackTransp(tp Transport) {
	key := tp.Metadata().Proto.Canonic()
	if _, ok := tpm.transps.LoadAndDelete(key); !ok {
		return
	}
	tpm.unbindTranspInterceptors(tp)

	tpm.log().Debug("transport untracked", slog.Any("transport", tp))
}

func (tpm *TransportManager) TransportByProto(proto TransportProto) (Transport, bool) {
	tp, ok := tpm.transps.Load(proto.Canonic())
	if !ok {
		return nil, false
	}
	return tp, true
}

func (tpm *TransportManager) AllTransports() iter.Seq[Transport] {
	return func(yield func(tp Transport) bool) {
		sorted := slices.SortedFunc(
			func(yield func(Transport) bool) {
				for _, tp := range tpm.transps.All() {
					if !yield(tp) {
						return
					}
				}
			},
			func(a, b Transport) int {
				return cmp.Compare(a.Metadata().Priority, b.Metadata().Priority)
			},
		)

		for _, tp := range sorted {
			if !yield(tp) {
				return
			}
		}
	}
}

func (tpm *TransportManager) TransportMetadataByProto(proto TransportProto) (TransportMetadata, bool) {
	if tp, ok := tpm.transps.Load(proto.Canonic()); ok {
		return tp.Metadata(), true
	}
	return TransportMetadata{}, false
}

func (tpm *TransportManager) TransportMetadataByNAPTRService(service string) (TransportMetadata, bool) {
	for _, tp := range tpm.transps.All() {
		if util.EqFold(tp.Metadata().NAPTRService, service) {
			return tp.Metadata(), true
		}
	}
	return TransportMetadata{}, false
}

func (tpm *TransportManager) AllTransportMetadata() iter.Seq[TransportMetadata] {
	return func(yield func(TransportMetadata) bool) {
		for tp := range tpm.AllTransports() {
			if !yield(tp.Metadata()) {
				return
			}
		}
	}
}

func (tpm *TransportManager) UseInboundRequestInterceptor(interceptor InboundRequestInterceptor) (unbind func()) {
	if interceptor == nil {
		return func() {}
	}

	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	return tpm.useInReqInterceptor(interceptor)
}

func (tpm *TransportManager) useInReqInterceptor(
	interceptor InboundRequestInterceptor,
) (unbind func()) {
	if interceptor == nil || tpm.isClosing() {
		return func() {}
	}

	entry := &transpInterceptBinding[InboundRequestInterceptor]{
		interceptor: interceptor,
		unbinds:     make(map[Transport]func()),
	}
	remove := tpm.inReqInts.Add(entry)
	for tp := range tpm.AllTransports() {
		tpm.bindInReqInterceptor(tp, entry)
	}

	return func() {
		remove()
		tpm.unbindInReqInterceptor(entry)
	}
}

func (tpm *TransportManager) UseInboundResponseInterceptor(interceptor InboundResponseInterceptor) (unbind func()) {
	if interceptor == nil {
		return func() {}
	}

	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	return tpm.useInResInterceptor(interceptor)
}

func (tpm *TransportManager) useInResInterceptor(
	interceptor InboundResponseInterceptor,
) (unbind func()) {
	if interceptor == nil || tpm.isClosing() {
		return func() {}
	}

	entry := &transpInterceptBinding[InboundResponseInterceptor]{
		interceptor: interceptor,
		unbinds:     make(map[Transport]func()),
	}
	remove := tpm.inResInts.Add(entry)
	for tp := range tpm.AllTransports() {
		tpm.bindInResInterceptor(tp, entry)
	}

	return func() {
		remove()
		tpm.unbindInResInterceptor(entry)
	}
}

func (tpm *TransportManager) UseOutboundRequestInterceptor(interceptor OutboundRequestInterceptor) (unbind func()) {
	if interceptor == nil {
		return func() {}
	}

	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	return tpm.useOutReqInterceptor(interceptor)
}

func (tpm *TransportManager) useOutReqInterceptor(
	interceptor OutboundRequestInterceptor,
) (unbind func()) {
	if interceptor == nil || tpm.isClosing() {
		return func() {}
	}

	entry := &transpInterceptBinding[OutboundRequestInterceptor]{
		interceptor: interceptor,
		unbinds:     make(map[Transport]func()),
	}
	remove := tpm.outReqInts.Add(entry)
	for tp := range tpm.AllTransports() {
		tpm.bindOutReqInterceptor(tp, entry)
	}

	return func() {
		remove()
		tpm.unbindOutReqInterceptor(entry)
	}
}

func (tpm *TransportManager) UseOutboundResponseInterceptor(interceptor OutboundResponseInterceptor) (unbind func()) {
	if interceptor == nil {
		return func() {}
	}

	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	return tpm.useOutResInterceptor(interceptor)
}

func (tpm *TransportManager) useOutResInterceptor(
	interceptor OutboundResponseInterceptor,
) (unbind func()) {
	if interceptor == nil || tpm.isClosing() {
		return func() {}
	}

	entry := &transpInterceptBinding[OutboundResponseInterceptor]{
		interceptor: interceptor,
		unbinds:     make(map[Transport]func()),
	}
	remove := tpm.outResInts.Add(entry)
	for tp := range tpm.AllTransports() {
		tpm.bindOutResInterceptor(tp, entry)
	}

	return func() {
		remove()
		tpm.unbindOutResInterceptor(entry)
	}
}

func (tpm *TransportManager) UseMessageInterceptor(interceptor MessageInterceptor) (unbind func()) {
	if interceptor == nil {
		return func() {}
	}

	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	if tpm.isClosing() {
		return func() {}
	}

	unbinds := []func(){
		tpm.useInReqInterceptor(interceptor),
		tpm.useInResInterceptor(interceptor),
		tpm.useOutReqInterceptor(interceptor),
		tpm.useOutResInterceptor(interceptor),
	}

	return func() {
		for _, fn := range unbinds {
			fn()
		}
	}
}

func (tpm *TransportManager) bindTranspInterceptors(tp Transport) {
	for entry := range tpm.inReqInts.All() {
		tpm.bindInReqInterceptor(tp, entry)
	}

	for entry := range tpm.inResInts.All() {
		tpm.bindInResInterceptor(tp, entry)
	}

	for entry := range tpm.outReqInts.All() {
		tpm.bindOutReqInterceptor(tp, entry)
	}

	for entry := range tpm.outResInts.All() {
		tpm.bindOutResInterceptor(tp, entry)
	}
}

func (tpm *TransportManager) unbindTranspInterceptors(tp Transport) {
	for entry := range tpm.inReqInts.All() {
		tpm.unbindInReqInterceptorFrom(tp, entry)
	}

	for entry := range tpm.inResInts.All() {
		tpm.unbindInResInterceptorFrom(tp, entry)
	}

	for entry := range tpm.outReqInts.All() {
		tpm.unbindOutReqInterceptorFrom(tp, entry)
	}

	for entry := range tpm.outResInts.All() {
		tpm.unbindOutResInterceptorFrom(tp, entry)
	}
}

func (*TransportManager) bindInReqInterceptor(
	tp Transport,
	entry *transpInterceptBinding[InboundRequestInterceptor],
) {
	wrapped := InboundRequestInterceptorFunc(
		func(ctx context.Context, next RequestReceiver, req *RequestEnvelope) error {
			return errors.Wrap(entry.interceptor.InterceptInboundRequest(ctx, next, req))
		},
	)

	entry.Lock()
	if _, ok := entry.unbinds[tp]; ok {
		entry.Unlock()
		return
	}
	entry.unbinds[tp] = tp.UseInboundRequestInterceptor(wrapped)
	entry.Unlock()
}

func (*TransportManager) unbindInReqInterceptor(entry *transpInterceptBinding[InboundRequestInterceptor]) {
	entry.Lock()
	for tp, unbind := range entry.unbinds {
		unbind()
		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) bindInResInterceptor(
	tp Transport,
	entry *transpInterceptBinding[InboundResponseInterceptor],
) {
	wrapped := InboundResponseInterceptorFunc(
		func(ctx context.Context, next ResponseReceiver, res *ResponseEnvelope) error {
			return errors.Wrap(entry.interceptor.InterceptInboundResponse(ctx, next, res))
		},
	)

	entry.Lock()
	if _, ok := entry.unbinds[tp]; ok {
		entry.Unlock()
		return
	}
	entry.unbinds[tp] = tp.UseInboundResponseInterceptor(wrapped)
	entry.Unlock()
}

func (*TransportManager) unbindInResInterceptor(entry *transpInterceptBinding[InboundResponseInterceptor]) {
	entry.Lock()
	for tp, unbind := range entry.unbinds {
		unbind()
		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) bindOutReqInterceptor(
	tp Transport,
	entry *transpInterceptBinding[OutboundRequestInterceptor],
) {
	wrapped := OutboundRequestInterceptorFunc(
		func(ctx context.Context, next RequestSender, req *RequestEnvelope, opts ...SendRequestOptions) error {
			return errors.Wrap(entry.interceptor.InterceptOutboundRequest(ctx, next, req, opts...))
		},
	)

	entry.Lock()
	if _, ok := entry.unbinds[tp]; ok {
		entry.Unlock()
		return
	}
	entry.unbinds[tp] = tp.UseOutboundRequestInterceptor(wrapped)
	entry.Unlock()
}

func (*TransportManager) unbindOutReqInterceptor(entry *transpInterceptBinding[OutboundRequestInterceptor]) {
	entry.Lock()
	for tp, unbind := range entry.unbinds {
		unbind()
		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) bindOutResInterceptor(
	tp Transport,
	entry *transpInterceptBinding[OutboundResponseInterceptor],
) {
	wrapped := OutboundResponseInterceptorFunc(
		func(ctx context.Context, next ResponseSender, res *ResponseEnvelope, opts ...SendResponseOptions) error {
			return errors.Wrap(entry.interceptor.InterceptOutboundResponse(ctx, next, res, opts...))
		},
	)

	entry.Lock()
	if _, ok := entry.unbinds[tp]; ok {
		entry.Unlock()
		return
	}
	entry.unbinds[tp] = tp.UseOutboundResponseInterceptor(wrapped)
	entry.Unlock()
}

func (*TransportManager) unbindOutResInterceptor(entry *transpInterceptBinding[OutboundResponseInterceptor]) {
	entry.Lock()
	for tp, unbind := range entry.unbinds {
		unbind()
		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) unbindInReqInterceptorFrom(
	tp Transport,
	entry *transpInterceptBinding[InboundRequestInterceptor],
) {
	entry.Lock()
	if unbind, ok := entry.unbinds[tp]; ok {
		unbind()
		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) unbindInResInterceptorFrom(
	tp Transport,
	entry *transpInterceptBinding[InboundResponseInterceptor],
) {
	entry.Lock()
	if unbind, ok := entry.unbinds[tp]; ok {
		unbind()
		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) unbindOutReqInterceptorFrom(
	tp Transport,
	entry *transpInterceptBinding[OutboundRequestInterceptor],
) {
	entry.Lock()
	if unbind, ok := entry.unbinds[tp]; ok {
		unbind()
		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) unbindOutResInterceptorFrom(
	tp Transport,
	entry *transpInterceptBinding[OutboundResponseInterceptor],
) {
	entry.Lock()
	if unbind, ok := entry.unbinds[tp]; ok {
		unbind()
		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (tpm *TransportManager) admitReqTransp(req *RequestEnvelope) (Transport, error) {
	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	if tpm.isClosing() {
		return nil, errors.Wrap(ErrTransportManagerClosed)
	}

	tp, ok := tpm.TransportFromRequest(req)
	if !ok {
		return nil, errors.Wrap(ErrNoTransport)
	}
	return tp, nil
}

func (tpm *TransportManager) admitResTransp(res *ResponseEnvelope) (Transport, error) {
	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	if tpm.isClosing() {
		return nil, errors.Wrap(ErrTransportManagerClosed)
	}

	tp, ok := tpm.TransportFromResponse(res)
	if !ok {
		return nil, errors.Wrap(ErrNoTransport)
	}
	return tp, nil
}

func (tpm *TransportManager) SendRequest(
	ctx context.Context,
	req *RequestEnvelope,
	opts ...SendRequestOptions,
) error {
	tp, err := tpm.admitReqTransp(req)
	if err != nil {
		return errors.Wrap(err)
	}
	return errors.Wrap(tp.SendRequest(ctx, req, opts...))
}

func (tpm *TransportManager) TransportFromRequest(req *RequestEnvelope) (Transport, bool) {
	proto := req.Transport().Proto
	if !proto.IsValid() {
		req.WithMessage(func(r *Request) {
			if v, ok := r.Headers.FirstVia(); ok && v != nil {
				proto = v.Transport
			}
		})
	}

	tp, ok := tpm.TransportByProto(proto)
	if !ok {
		return nil, false
	}
	return tp, true
}

func (tpm *TransportManager) SendResponse(
	ctx context.Context,
	res *ResponseEnvelope,
	opts ...SendResponseOptions,
) error {
	tp, err := tpm.admitResTransp(res)
	if err != nil {
		return errors.Wrap(err)
	}
	return errors.Wrap(tp.SendResponse(ctx, res, opts...))
}

func (tpm *TransportManager) TransportFromResponse(res *ResponseEnvelope) (Transport, bool) {
	proto := res.Transport().Proto
	if !proto.IsValid() {
		res.WithMessage(func(r *Response) {
			if v, ok := r.Headers.FirstVia(); ok && v != nil {
				proto = v.Transport
			}
		})
	}

	tp, ok := tpm.TransportByProto(proto)
	if !ok {
		return nil, false
	}
	return tp, true
}

func (tpm *TransportManager) Respond(
	ctx context.Context,
	req *RequestEnvelope,
	sts ResponseStatus,
	opts ...RespondOptions,
) error {
	tp, err := tpm.admitReqTransp(req)
	if err != nil {
		return errors.Wrap(err)
	}
	return errors.Wrap(tp.Respond(ctx, req, sts, opts...))
}

func (tpm *TransportManager) Listen(
	ctx context.Context,
	proto TransportProto,
	addr string,
) (TransportListener, error) {
	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	if tpm.isClosing() {
		return nil, errors.Wrap(ErrTransportManagerClosed)
	}

	tp, ok := tpm.TransportByProto(proto)
	if !ok {
		return nil, errors.Wrap(ErrNoTransport)
	}

	return errors.Wrap2(tp.Listen(ctx, addr))
}

func (tpm *TransportManager) MatchSentBy(addr Addr) bool {
	for _, tp := range tpm.transps.All() {
		if tp.MatchSentBy(addr) {
			return true
		}
	}
	return false
}
