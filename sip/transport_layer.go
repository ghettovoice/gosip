package sip

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"net/netip"
	"sync"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/log"
)

// TransportLayer represents a transport layer.
// It is responsible for tracking transports and serving them and can be used as
// single point of sending/receiving messages.
type TransportLayer struct {
	mu      sync.RWMutex
	transps transpsByProto
	defTp   Transport
	tpsWg   sync.WaitGroup
	closed  bool
	srvOnce sync.Once
	srvErr  error
	serving bool
	srvErrs []error

	onReq types.CallbackManager[TransportRequestHandler]
	onRes types.CallbackManager[TransportResponseHandler]

	closeOnce sync.Once
	closeErr  error
}

type (
	transpsByProto = map[TransportProto]transpsByAddr
	transpsByAddr  = map[netip.AddrPort]*trackedTransp
)

type trackedTransp struct {
	Transport
	cancOnReq,
	cancOnRes func()
}

func (tpl *TransportLayer) TrackTransport(tp Transport, isDef bool) error {
	proto, ok := GetTransportProto(tp)
	if !ok {
		return errtrace.Wrap(NewInvalidArgumentError("invalid transport"))
	}
	laddr, ok := GetTransportLocalAddr(tp)
	if !ok {
		return errtrace.Wrap(NewInvalidArgumentError("invalid transport"))
	}

	tpl.mu.Lock()
	if tpl.closed {
		tpl.mu.Unlock()
		return errtrace.Wrap(ErrTransportClosed)
	}

	if isDef {
		tpl.defTp = tp
	}

	if tpl.transps == nil {
		tpl.transps = make(transpsByProto)
	}
	byAddr := tpl.transps[proto]
	if byAddr == nil {
		byAddr = make(transpsByAddr)
		tpl.transps[proto] = byAddr
	}
	if _, ok := byAddr[laddr]; ok {
		tpl.mu.Unlock()
		return nil
	}
	tracked := &trackedTransp{
		Transport: tp,
		cancOnReq: tp.OnRequest(tpl.recvReq),
		cancOnRes: tp.OnResponse(tpl.recvRes),
	}
	byAddr[laddr] = tracked
	shouldServe := tpl.serving
	tpl.mu.Unlock()

	if shouldServe {
		tpl.serveTransps([]*trackedTransp{tracked})
	}

	return nil
}

func (tpl *TransportLayer) recvReq(ctx context.Context, tp ServerTransport, req *InboundRequest) {
	// ctx = context.WithValue(ctx, transpLayerCtxKey, tpl)
	var handled bool
	tpl.onReq.Range(func(fn TransportRequestHandler) {
		handled = true
		fn(ctx, tp, req)
	})
	if handled {
		return
	}

	log.LoggerFromValues(ctx, tp).LogAttrs(ctx, slog.LevelWarn,
		"discarding inbound request due to missing transport layer request handlers",
		slog.Any("request", req),
	)
	respondStateless(ctx, tp, req, ResponseStatusServiceUnavailable)
}

func (tpl *TransportLayer) recvRes(ctx context.Context, tp ClientTransport, res *InboundResponse) {
	// ctx = context.WithValue(ctx, transpLayerCtxKey, tpl)
	var handled bool
	tpl.onRes.Range(func(fn TransportResponseHandler) {
		handled = true
		fn(ctx, tp, res)
	})
	if handled {
		return
	}

	log.LoggerFromValues(ctx, tp).LogAttrs(ctx, slog.LevelWarn,
		"discarding inbound response due to missing transport layer response handlers",
		slog.Any("response", res),
	)
}

func (tpl *TransportLayer) UntrackTransport(tp Transport) error {
	proto, ok := GetTransportProto(tp)
	if !ok {
		return errtrace.Wrap(NewInvalidArgumentError("invalid transport"))
	}
	laddr, ok := GetTransportLocalAddr(tp)
	if !ok {
		return errtrace.Wrap(NewInvalidArgumentError("invalid transport"))
	}

	tpl.mu.Lock()
	defer tpl.mu.Unlock()

	byAddr := tpl.transps[proto]
	if byAddr == nil {
		return nil
	}
	found, ok := byAddr[laddr]
	if !ok {
		return nil
	}

	found.cancOnReq()
	found.cancOnRes()

	delete(byAddr, laddr)
	if len(byAddr) == 0 {
		delete(tpl.transps, proto)
	}

	if tpl.defTp == tp {
		for _, addrs := range tpl.transps {
			for _, t := range addrs {
				tpl.defTp = t.Transport
				break
			}
			break
		}
	}

	return nil
}

func (tpl *TransportLayer) GetTransport(proto TransportProto, laddr netip.AddrPort) (Transport, bool) {
	tpl.mu.RLock()
	defer tpl.mu.RUnlock()

	found, ok := tpl.getTransp(proto, laddr)
	if !ok {
		return nil, false
	}
	return found.Transport, true
}

func (tpl *TransportLayer) getTransp(proto TransportProto, laddr netip.AddrPort) (*trackedTransp, bool) {
	byAddr := tpl.transps[proto]
	if byAddr == nil {
		return nil, false
	}
	found, ok := byAddr[laddr]
	if !ok {
		return nil, false
	}
	return found, true
}

func (tpl *TransportLayer) AllTransports() iter.Seq[Transport] {
	return func(yield func(tp Transport) bool) {
		tpl.mu.RLock()
		defer tpl.mu.RUnlock()

		for _, byAddr := range tpl.transps {
			for _, tp := range byAddr {
				if !yield(tp.Transport) {
					return
				}
			}
		}
	}
}

var errNoTransp Error = "no transport resolved"

func (tpl *TransportLayer) SendRequest(ctx context.Context, req *OutboundRequest, opts *SendRequestOptions) error {
	tpl.mu.RLock()
	if tpl.closed {
		tpl.mu.RUnlock()
		return errtrace.Wrap(ErrTransportClosed)
	}
	var tp Transport
	if proto, laddr := req.Transport(), req.LocalAddr(); proto != "" && laddr != zeroAddrPort {
		tp, _ = tpl.getTransp(proto, laddr)
	}
	if tp == nil {
		if tpl.defTp == nil {
			for _, byAddr := range tpl.transps {
				for _, tp = range byAddr {
					break
				}
				break
			}
		} else {
			tp = tpl.defTp
		}
	}
	tpl.mu.RUnlock()
	if tp == nil {
		return errtrace.Wrap(errNoTransp)
	}
	return errtrace.Wrap(tp.SendRequest(ctx, req, opts))
}

func (tpl *TransportLayer) SendResponse(ctx context.Context, res *OutboundResponse, opts *SendResponseOptions) error {
	tpl.mu.RLock()
	if tpl.closed {
		tpl.mu.RUnlock()
		return errtrace.Wrap(ErrTransportClosed)
	}
	var tp Transport
	if proto, laddr := res.Transport(), res.LocalAddr(); proto != "" && laddr != zeroAddrPort {
		tp, _ = tpl.getTransp(proto, laddr)
	}
	if tp == nil {
		if tpl.defTp == nil {
			for _, byAddr := range tpl.transps {
				for _, tp = range byAddr {
					break
				}
				break
			}
		} else {
			tp = tpl.defTp
		}
	}
	tpl.mu.RUnlock()
	if tp == nil {
		return errtrace.Wrap(errNoTransp)
	}
	return errtrace.Wrap(tp.SendResponse(ctx, res, opts))
}

func (tpl *TransportLayer) OnRequest(fn TransportRequestHandler) (cancel func()) {
	return tpl.onReq.Add(fn)
}

func (tpl *TransportLayer) OnResponse(fn TransportResponseHandler) (cancel func()) {
	return tpl.onRes.Add(fn)
}

func (tpl *TransportLayer) Serve() error {
	tpl.srvOnce.Do(func() {
		tpl.srvErr = tpl.serve()
	})
	return errtrace.Wrap(tpl.srvErr)
}

func (tpl *TransportLayer) serve() error {
	tpl.mu.Lock()
	if tpl.closed {
		tpl.mu.Unlock()
		return errtrace.Wrap(ErrTransportClosed)
	}
	if tpl.serving {
		// Serve already running; should not happen due to srvOnce but keep guard.
		tpl.mu.Unlock()
		return nil
	}
	tracked := tpl.snapshotTracked()
	tpl.serving = true
	tpl.mu.Unlock()

	tpl.serveTransps(tracked)
	tpl.tpsWg.Wait()

	tpl.mu.Lock()
	tpl.serving = false
	errs := append([]error(nil), tpl.srvErrs...)
	tpl.srvErrs = nil
	closed := tpl.closed
	tpl.mu.Unlock()

	if len(errs) > 0 {
		return errtrace.Wrap(errorutil.Join(errs...))
	}
	if closed {
		return errtrace.Wrap(ErrTransportClosed)
	}
	return nil
}

func (tpl *TransportLayer) Close() error {
	tpl.closeOnce.Do(func() {
		tpl.closeErr = tpl.close()
	})
	return errtrace.Wrap(tpl.closeErr)
}

func (tpl *TransportLayer) close() error {
	tpl.mu.Lock()
	if tpl.closed {
		tpl.mu.Unlock()
		return nil
	}
	tpl.closed = true
	tracked := tpl.snapshotTracked()
	tpl.transps = nil
	tpl.defTp = nil
	tpl.mu.Unlock()

	var errs []error
	for _, tp := range tracked {
		if tp.cancOnReq != nil {
			tp.cancOnReq()
		}
		if tp.cancOnRes != nil {
			tp.cancOnRes()
		}
		if err := tp.Transport.Close(); err != nil && !errors.Is(err, ErrTransportClosed) {
			errs = append(errs, err)
		}
	}

	tpl.tpsWg.Wait()

	if len(errs) == 0 {
		return nil
	}
	return errtrace.Wrap(errorutil.JoinPrefix("failed to close transports:", errs...))
}

func (tpl *TransportLayer) snapshotTracked() []*trackedTransp {
	var count int
	for _, addrs := range tpl.transps {
		count += len(addrs)
	}
	if count == 0 {
		return nil
	}

	tracked := make([]*trackedTransp, 0, count)
	for _, addrs := range tpl.transps {
		for _, tp := range addrs {
			tracked = append(tracked, tp)
		}
	}
	return tracked
}

func (tpl *TransportLayer) serveTransps(tracked []*trackedTransp) {
	if len(tracked) == 0 {
		return
	}

	tpl.tpsWg.Add(len(tracked))
	for _, tp := range tracked {
		go tpl.serveTransp(tp)
	}
}

func (tpl *TransportLayer) serveTransp(tp *trackedTransp) {
	defer tpl.tpsWg.Done()
	defer tpl.UntrackTransport(tp.Transport) //nolint:errcheck

	if err := tp.Transport.Serve(); err != nil && !errors.Is(err, ErrTransportClosed) {
		tpl.mu.Lock()
		tpl.srvErrs = append(tpl.srvErrs, err)
		tpl.mu.Unlock()
	}
}

func (*TransportLayer) StatsID() StatsID { return "transport_layer" }

// const transpLayerCtxKey types.ContextKey = "transport_layer"

// // TransportLayerFromContext returns the transport layer from the given context.
// func TransportLayerFromContext(ctx context.Context) (*TransportLayer, bool) {
// 	tpl, ok := ctx.Value(transpLayerCtxKey).(*TransportLayer)
// 	return tpl, ok
// }
