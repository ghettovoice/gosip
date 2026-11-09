package sip

import (
	"cmp"
	"context"
	"iter"
	"log/slog"
	"runtime"
	"slices"
	"sync"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/shared"
	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/pkg/log"
)

const (
	tpmStateRunning uint32 = iota
	tpmStateClosing
	tpmStateClosed
)

type TransportManager struct {
	Logger *slog.Logger

	lcMu  sync.Mutex
	state uint32

	tps       syncutil.RWMap[TransportProto, Transport]
	closeOnce sync.Once
	closeErr  error

	inReqInts  shared.CallbackManager[*tpInceptBinding[InboundRequestInterceptor]]
	inResInts  shared.CallbackManager[*tpInceptBinding[InboundResponseInterceptor]]
	outReqInts shared.CallbackManager[*tpInceptBinding[OutboundRequestInterceptor]]
	outResInts shared.CallbackManager[*tpInceptBinding[OutboundResponseInterceptor]]
}

var (
	_ TransportMetadataProvider = (*TransportManager)(nil)
	_ MessageInterceptorChain   = (*TransportManager)(nil)
)

type tpInceptBinding[T any] struct {
	sync.Mutex
	incept  T
	unbinds map[Transport]func()
}

func (tpm *TransportManager) log() *slog.Logger {
	if tpm.Logger == nil {
		return log.Default()
	}
	return tpm.Logger
}

func (tpm *TransportManager) isClosing() bool {
	return tpm.state >= tpmStateClosing
}

func (tpm *TransportManager) Close() error {
	tpm.closeOnce.Do(func() {
		tpm.lcMu.Lock()
		tpm.state = tpmStateClosing
		tpm.lcMu.Unlock()

		tpm.closeErr = tpm.close()

		tpm.lcMu.Lock()
		tpm.state = tpmStateClosed
		tpm.lcMu.Unlock()

		tpm.log().Debug("transport manager closed")
	})
	return errors.Wrap(tpm.closeErr)
}

func (tpm *TransportManager) close() error {
	var (
		wg     sync.WaitGroup
		errsMu sync.Mutex
		errs   []error
	)

	collectErr := func(err error) {
		if err == nil {
			return
		}

		errsMu.Lock()
		errs = append(errs, err)
		errsMu.Unlock()
	}

	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	run := func(fn func()) {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			fn()
		})
	}

	for _, tp := range tpm.tps.All() {
		run(func() {
			if err := tp.Close(); err != nil {
				collectErr(errors.Errorf("close transport %q: %w", tp.Metadata().Proto, err))
			}
			tpm.untrackTp(tp)
		})
	}

	wg.Wait()

	return errors.JoinPrefixWrap("transport manager close errors:", errs...)
}

func (tpm *TransportManager) TrackTransport(tp Transport) error {
	if tp == nil {
		return errors.ErrorWrap("nil transport")
	}

	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	if tpm.isClosing() {
		return errors.Wrap(NewTransportManagerClosedError())
	}

	tpm.trackTp(tp)
	return nil
}

func (tpm *TransportManager) trackTp(tp Transport) {
	key := tp.Metadata().Proto.Canonic()
	if _, ok := tpm.tps.LoadOrStore(key, tp); ok {
		return
	}
	tpm.bindTpIncepts(tp)

	tpm.log().Debug("transport tracked", slog.Any("transport", tp))
}

func (tpm *TransportManager) UntrackTransport(tp Transport) error {
	if tp == nil {
		return errors.ErrorWrap("nil transport")
	}

	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	if tpm.isClosing() {
		return errors.Wrap(NewTransportManagerClosedError())
	}

	tpm.untrackTp(tp)
	return nil
}

func (tpm *TransportManager) untrackTp(tp Transport) {
	key := tp.Metadata().Proto.Canonic()
	stored, ok := tpm.tps.LoadAndDelete(key)
	if !ok {
		return
	}
	// unbind interceptors from the tracked (wrapped) transport instance,
	// because it was used as the binding key during tracking
	tpm.unbindTpIncepts(stored)

	tpm.log().Debug("transport untracked", slog.Any("transport", tp))
}

func (tpm *TransportManager) TransportByProto(proto TransportProto) (Transport, bool) {
	tp, ok := tpm.tps.Load(proto.Canonic())
	if !ok {
		return nil, false
	}
	return tp, true
}

func (tpm *TransportManager) AllTransports() iter.Seq[Transport] {
	return func(yield func(tp Transport) bool) {
		sorted := slices.SortedFunc(
			func(yield func(Transport) bool) {
				for _, tp := range tpm.tps.All() {
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
	if tp, ok := tpm.tps.Load(proto.Canonic()); ok {
		return tp.Metadata(), true
	}
	return TransportMetadata{}, false
}

func (tpm *TransportManager) TransportMetadataByNAPTRService(service string) (TransportMetadata, bool) {
	for _, tp := range tpm.tps.All() {
		if util.EqualFold(tp.Metadata().NAPTRService, service) {
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

func (tpm *TransportManager) UseInboundRequestInterceptor(i InboundRequestInterceptor) (unbind func()) {
	if i == nil {
		return noop
	}

	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	return tpm.useInReqIncept(i)
}

func (tpm *TransportManager) useInReqIncept(i InboundRequestInterceptor) (unbind func()) {
	if i == nil || tpm.isClosing() {
		return noop
	}

	entry := &tpInceptBinding[InboundRequestInterceptor]{
		incept:  i,
		unbinds: make(map[Transport]func()),
	}
	remove := tpm.inReqInts.Add(entry)
	for _, tp := range tpm.tps.All() {
		tpm.bindInReqIncept(tp, entry)
	}

	return func() {
		remove()
		tpm.unbindInReqIncept(entry)
	}
}

func (tpm *TransportManager) UseInboundResponseInterceptor(i InboundResponseInterceptor) (unbind func()) {
	if i == nil {
		return noop
	}

	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	return tpm.useInResIncept(i)
}

func (tpm *TransportManager) useInResIncept(i InboundResponseInterceptor) (unbind func()) {
	if i == nil || tpm.isClosing() {
		return noop
	}

	entry := &tpInceptBinding[InboundResponseInterceptor]{
		incept:  i,
		unbinds: make(map[Transport]func()),
	}
	remove := tpm.inResInts.Add(entry)
	for _, tp := range tpm.tps.All() {
		tpm.bindInResIncept(tp, entry)
	}

	return func() {
		remove()
		tpm.unbindInResIncept(entry)
	}
}

func (tpm *TransportManager) UseOutboundRequestInterceptor(i OutboundRequestInterceptor) (unbind func()) {
	if i == nil {
		return noop
	}

	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	return tpm.useOutReqIncept(i)
}

func (tpm *TransportManager) useOutReqIncept(i OutboundRequestInterceptor) (unbind func()) {
	if i == nil || tpm.isClosing() {
		return noop
	}

	entry := &tpInceptBinding[OutboundRequestInterceptor]{
		incept:  i,
		unbinds: make(map[Transport]func()),
	}
	remove := tpm.outReqInts.Add(entry)
	for _, tp := range tpm.tps.All() {
		tpm.bindOutReqIncept(tp, entry)
	}

	return func() {
		remove()
		tpm.unbindOutReqIncept(entry)
	}
}

func (tpm *TransportManager) UseOutboundResponseInterceptor(i OutboundResponseInterceptor) (unbind func()) {
	if i == nil {
		return noop
	}

	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	return tpm.useOutResIncept(i)
}

func (tpm *TransportManager) useOutResIncept(i OutboundResponseInterceptor) (unbind func()) {
	if i == nil || tpm.isClosing() {
		return noop
	}

	entry := &tpInceptBinding[OutboundResponseInterceptor]{
		incept:  i,
		unbinds: make(map[Transport]func()),
	}
	remove := tpm.outResInts.Add(entry)
	for _, tp := range tpm.tps.All() {
		tpm.bindOutResIncept(tp, entry)
	}

	return func() {
		remove()
		tpm.unbindOutResIncept(entry)
	}
}

func (tpm *TransportManager) UseMessageInterceptor(i MessageInterceptor) (unbind func()) {
	if i == nil {
		return noop
	}

	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	if tpm.isClosing() {
		return noop
	}

	unbinds := []func(){
		tpm.useInReqIncept(i),
		tpm.useInResIncept(i),
		tpm.useOutReqIncept(i),
		tpm.useOutResIncept(i),
	}

	return func() {
		for _, fn := range unbinds {
			fn()
		}
	}
}

func (tpm *TransportManager) bindTpIncepts(tp Transport) {
	for entry := range tpm.inReqInts.All() {
		tpm.bindInReqIncept(tp, entry)
	}

	for entry := range tpm.inResInts.All() {
		tpm.bindInResIncept(tp, entry)
	}

	for entry := range tpm.outReqInts.All() {
		tpm.bindOutReqIncept(tp, entry)
	}

	for entry := range tpm.outResInts.All() {
		tpm.bindOutResIncept(tp, entry)
	}
}

func (tpm *TransportManager) unbindTpIncepts(tp Transport) {
	for entry := range tpm.inReqInts.All() {
		tpm.unbindInReqInceptFrom(tp, entry)
	}

	for entry := range tpm.inResInts.All() {
		tpm.unbindInResInceptFrom(tp, entry)
	}

	for entry := range tpm.outReqInts.All() {
		tpm.unbindOutReqInceptFrom(tp, entry)
	}

	for entry := range tpm.outResInts.All() {
		tpm.unbindOutResInceptFrom(tp, entry)
	}
}

func (*TransportManager) bindInReqIncept(
	tp Transport,
	entry *tpInceptBinding[InboundRequestInterceptor],
) {
	wrapped := InboundRequestInterceptorFunc(
		func(ctx context.Context, next RequestReceiver, req *RequestEnvelope) error {
			return errors.Wrap(entry.incept.InterceptInboundRequest(ctx, next, req))
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

func (*TransportManager) unbindInReqIncept(entry *tpInceptBinding[InboundRequestInterceptor]) {
	entry.Lock()
	for tp, unbind := range entry.unbinds {
		unbind()
		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) bindInResIncept(
	tp Transport,
	entry *tpInceptBinding[InboundResponseInterceptor],
) {
	wrapped := InboundResponseInterceptorFunc(
		func(ctx context.Context, next ResponseReceiver, res *ResponseEnvelope) error {
			return errors.Wrap(entry.incept.InterceptInboundResponse(ctx, next, res))
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

func (*TransportManager) unbindInResIncept(entry *tpInceptBinding[InboundResponseInterceptor]) {
	entry.Lock()
	for tp, unbind := range entry.unbinds {
		unbind()
		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) bindOutReqIncept(
	tp Transport,
	entry *tpInceptBinding[OutboundRequestInterceptor],
) {
	wrapped := OutboundRequestInterceptorFunc(
		func(ctx context.Context, next RequestSender, req *RequestEnvelope, opts ...SendRequestOptions) error {
			return errors.Wrap(entry.incept.InterceptOutboundRequest(ctx, next, req, opts...))
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

func (*TransportManager) unbindOutReqIncept(entry *tpInceptBinding[OutboundRequestInterceptor]) {
	entry.Lock()
	for tp, unbind := range entry.unbinds {
		unbind()
		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) bindOutResIncept(
	tp Transport,
	entry *tpInceptBinding[OutboundResponseInterceptor],
) {
	wrapped := OutboundResponseInterceptorFunc(
		func(ctx context.Context, next ResponseSender, res *ResponseEnvelope, opts ...SendResponseOptions) error {
			return errors.Wrap(entry.incept.InterceptOutboundResponse(ctx, next, res, opts...))
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

func (*TransportManager) unbindOutResIncept(entry *tpInceptBinding[OutboundResponseInterceptor]) {
	entry.Lock()
	for tp, unbind := range entry.unbinds {
		unbind()
		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) unbindInReqInceptFrom(
	tp Transport,
	entry *tpInceptBinding[InboundRequestInterceptor],
) {
	entry.Lock()
	if unbind, ok := entry.unbinds[tp]; ok {
		unbind()
		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) unbindInResInceptFrom(
	tp Transport,
	entry *tpInceptBinding[InboundResponseInterceptor],
) {
	entry.Lock()
	if unbind, ok := entry.unbinds[tp]; ok {
		unbind()
		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) unbindOutReqInceptFrom(
	tp Transport,
	entry *tpInceptBinding[OutboundRequestInterceptor],
) {
	entry.Lock()
	if unbind, ok := entry.unbinds[tp]; ok {
		unbind()
		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (*TransportManager) unbindOutResInceptFrom(
	tp Transport,
	entry *tpInceptBinding[OutboundResponseInterceptor],
) {
	entry.Lock()
	if unbind, ok := entry.unbinds[tp]; ok {
		unbind()
		delete(entry.unbinds, tp)
	}
	entry.Unlock()
}

func (tpm *TransportManager) admitReqTp(req *RequestEnvelope) (Transport, error) {
	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	if tpm.isClosing() {
		return nil, errors.Wrap(NewTransportManagerClosedError())
	}

	tp, ok := tpm.TransportFromRequest(req)
	if !ok {
		return nil, errors.Wrap(NewNoTransportError())
	}
	return tp, nil
}

func (tpm *TransportManager) admitResTp(res *ResponseEnvelope) (Transport, error) {
	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	if tpm.isClosing() {
		return nil, errors.Wrap(NewTransportManagerClosedError())
	}

	tp, ok := tpm.TransportFromResponse(res)
	if !ok {
		return nil, errors.Wrap(NewNoTransportError())
	}
	return tp, nil
}

func (tpm *TransportManager) SendRequest(
	ctx context.Context,
	req *RequestEnvelope,
	opts ...SendRequestOptions,
) error {
	tp, err := tpm.admitReqTp(req)
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
	tp, err := tpm.admitResTp(res)
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
	tp, err := tpm.admitReqTp(req)
	if err != nil {
		return errors.Wrap(err)
	}
	return errors.Wrap(tp.Respond(ctx, req, sts, opts...))
}

func (tpm *TransportManager) admitLisTp(proto TransportProto) (Transport, error) {
	tpm.lcMu.Lock()
	defer tpm.lcMu.Unlock()

	if tpm.isClosing() {
		return nil, errors.Wrap(NewTransportManagerClosedError())
	}

	tp, ok := tpm.TransportByProto(proto)
	if !ok {
		return nil, errors.Wrap(NewNoTransportError())
	}
	return tp, nil
}

func (tpm *TransportManager) Listen(
	ctx context.Context,
	proto TransportProto,
	addr string,
) (TransportListener, error) {
	tp, err := tpm.admitLisTp(proto)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	ls, err := tp.Listen(ctx, addr)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return ls, nil
}

func (tpm *TransportManager) MatchSentBy(addr Addr) bool {
	for _, tp := range tpm.tps.All() {
		if tp.MatchSentBy(addr) {
			return true
		}
	}
	return false
}
