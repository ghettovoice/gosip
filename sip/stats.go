package sip

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip/header"
)

// StatsReport is a snapshot of SIP stack statistics.
type StatsReport struct {
	Time         time.Time        `json:"time"`
	Transports   []TransportStats `json:"transports"`
	Transactions TransactionStats `json:"transactions"`
	Proxy        ProxyStats       `json:"proxy"`
}

// TransportStats contains statistics for a single transport.
type TransportStats struct {
	// Proto is a transport protocol.
	Proto TransportProto `json:"proto"`
	// Addr is a local address.
	Addr string `json:"addr"`
	// RequestsReceived is a number of received requests.
	RequestsReceived uint64 `json:"requests_received"`
	// RequestsSent is a number of sent requests.
	RequestsSent uint64 `json:"requests_sent"`
	// ResponsesReceived is a number of received responses.
	ResponsesReceived uint64 `json:"responses_received"`
	// ResponsesSent is a number of sent responses.
	ResponsesSent uint64 `json:"responses_sent"`
	// AvgRTT is an average round-trip time.
	AvgRTT time.Duration `json:"avg_rtt"`
	// NumRTT is a number of round-trip measurements.
	NumRTT uint64 `json:"num_rtt"`
}

// TransactionStats contains statistics for transactions.
type TransactionStats struct {
	// InviteClientTransactions is a number of active invite client transactions.
	InviteClientTransactions uint64 `json:"invite_client_transactions"`
	// NonInviteClientTransactions is a number of active non-invite client transactions.
	NonInviteClientTransactions uint64 `json:"non_invite_client_transactions"`
	// InviteServerTransactions is a number of active invite server transactions.
	InviteServerTransactions uint64 `json:"invite_server_transactions"`
	// NonInviteServerTransactions is a number of active non-invite server transactions.
	NonInviteServerTransactions uint64 `json:"non_invite_server_transactions"`
	// InviteClientTransactionsTotal is a total number of created invite client transactions.
	InviteClientTransactionsTotal uint64 `json:"invite_client_transactions_total"`
	// NonInviteClientTransactionsTotal is a total number of created non-invite client transactions.
	NonInviteClientTransactionsTotal uint64 `json:"non_invite_client_transactions_total"`
	// InviteServerTransactionsTotal is a total number of created invite server transactions.
	InviteServerTransactionsTotal uint64 `json:"invite_server_transactions_total"`
	// NonInviteServerTransactionsTotal is a total number of created non-invite server transactions.
	NonInviteServerTransactionsTotal uint64 `json:"non_invite_server_transactions_total"`
}

// ProxyStats contains cumulative statistics derived from [ProxyEvent]
// observations. The counters are subscription-lifetime observed totals:
// they count the events the recorder observed since it was bound, they
// keep no per-request or per-target labels, and they do not retain the
// history of requests that completed before the binding.
type ProxyStats struct {
	// RequestsHandled is a number of inbound requests claimed by a proxy
	// route that entered the forwarding pipeline.
	RequestsHandled uint64 `json:"requests_handled"`
	// RequestsForwarded is a number of proxied requests whose outbound
	// copy was emitted toward at least one target.
	RequestsForwarded uint64 `json:"requests_forwarded"`
	// ForwardAttemptsSucceeded is a number of settled downstream forward
	// attempts that produced no error.
	ForwardAttemptsSucceeded uint64 `json:"forward_attempts_succeeded"`
	// ForwardAttemptsFailed is a number of settled downstream forward
	// attempts that failed.
	ForwardAttemptsFailed uint64 `json:"forward_attempts_failed"`
	// ResponsesForwarded is a number of responses relayed toward the
	// prior hop.
	ResponsesForwarded uint64 `json:"responses_forwarded"`
	// RequestsCompleted is a number of proxied requests that reached a
	// committed outcome.
	RequestsCompleted uint64 `json:"requests_completed"`
	// RequestsRejected is a number of proxied requests completed by a
	// local rejection.
	RequestsRejected uint64 `json:"requests_rejected"`
	// RequestsTimedOut is a number of proxied requests completed by a
	// timeout without a usable response.
	RequestsTimedOut uint64 `json:"requests_timed_out"`
	// RequestsCancelled is a number of proxied requests completed by a
	// cancellation.
	RequestsCancelled uint64 `json:"requests_cancelled"`
}

// StatsRecorder records various SIP statistics.
// It implements MessageInterceptor, TransactionHandler and
// ProxyEventHandler interfaces: it can be used as a middleware for
// Element and as a proxy observation event handler at the same time —
// the proxy counters count committed forwarding events only, so one
// recorder serving both roles never counts a logical event twice.
type StatsRecorder struct {
	transpsStats
	transactStats
	prxStats
}

var (
	_ MessageInterceptor = (*StatsRecorder)(nil)
	_ TransactionHandler = (*StatsRecorder)(nil)
	_ ElementMiddleware  = (*StatsRecorder)(nil)
	_ ProxyEventHandler  = (*StatsRecorder)(nil)
)

type transpsStats struct {
	stats sync.Map // map[transpKey]*transpStats
}

type transpKey struct {
	proto TransportProto
	laddr netip.AddrPort
}

type transpStats struct {
	inReqs,
	inRess,
	outRess,
	outReqs,
	rttSum,
	rttNum atomic.Uint64
}

type transactStats struct {
	invClnTxs,
	invSrvTxs,
	ninvClnTxs,
	ninvSrvTxs atomic.Int64

	invClnTxsTotal,
	invSrvTxsTotal,
	ninvClnTxsTotal,
	ninvSrvTxsTotal atomic.Uint64
}

type prxStats struct {
	reqsHandled,
	reqsFwd,
	attSucc,
	attFail,
	resFwd,
	reqsCompl,
	reqsRej,
	reqsTmo,
	reqsCnc atomic.Uint64
}

func (rcdr *StatsRecorder) LogValue() slog.Value {
	if rcdr == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.String("ptr", fmt.Sprintf("%p", rcdr)),
	)
}

// Report returns statistics report about various SIP layers.
// Call this function periodically to get updated values.
func (rcdr *StatsRecorder) Report() StatsReport {
	report := StatsReport{
		Time: time.Now(),
	}

	rcdr.stats.Range(func(key, value any) bool {
		stats, ok := value.(*transpStats)
		if !ok {
			return true
		}

		tpKey, ok := key.(transpKey)
		if !ok {
			return true
		}

		rttNum := stats.rttNum.Load()
		rttSum := stats.rttSum.Load()
		avgRTT := time.Duration(0)
		if rttNum > 0 {
			avgRTT = time.Duration(rttSum / rttNum)
		}

		report.Transports = append(report.Transports, TransportStats{
			Proto:             tpKey.proto,
			Addr:              tpKey.laddr.String(),
			RequestsReceived:  stats.inReqs.Load(),
			RequestsSent:      stats.outReqs.Load(),
			ResponsesReceived: stats.inRess.Load(),
			ResponsesSent:     stats.outRess.Load(),
			AvgRTT:            avgRTT,
			NumRTT:            rttNum,
		})
		return true
	})

	report.Transactions = TransactionStats{
		InviteClientTransactions:         clampToUint64(rcdr.invClnTxs.Load()),
		NonInviteClientTransactions:      clampToUint64(rcdr.ninvClnTxs.Load()),
		InviteServerTransactions:         clampToUint64(rcdr.invSrvTxs.Load()),
		NonInviteServerTransactions:      clampToUint64(rcdr.ninvSrvTxs.Load()),
		InviteClientTransactionsTotal:    rcdr.invClnTxsTotal.Load(),
		NonInviteClientTransactionsTotal: rcdr.ninvClnTxsTotal.Load(),
		InviteServerTransactionsTotal:    rcdr.invSrvTxsTotal.Load(),
		NonInviteServerTransactionsTotal: rcdr.ninvSrvTxsTotal.Load(),
	}

	report.Proxy = ProxyStats{
		RequestsHandled:          rcdr.reqsHandled.Load(),
		RequestsForwarded:        rcdr.reqsFwd.Load(),
		ForwardAttemptsSucceeded: rcdr.attSucc.Load(),
		ForwardAttemptsFailed:    rcdr.attFail.Load(),
		ResponsesForwarded:       rcdr.resFwd.Load(),
		RequestsCompleted:        rcdr.reqsCompl.Load(),
		RequestsRejected:         rcdr.reqsRej.Load(),
		RequestsTimedOut:         rcdr.reqsTmo.Load(),
		RequestsCancelled:        rcdr.reqsCnc.Load(),
	}
	return report
}

func (rcdr *StatsRecorder) getTpStats(key transpKey) *transpStats {
	stats, _ := rcdr.stats.LoadOrStore(key, &transpStats{})
	return stats.(*transpStats) //nolint:forcetypeassert // the concrete type is fixed by construction
}

// InterceptInboundRequest intercepts inbound requests and records statistics.
func (rcdr *StatsRecorder) InterceptInboundRequest(
	ctx context.Context,
	next RequestReceiver,
	req *RequestEnvelope,
) error {
	rcdr.handleReqReceived(req)
	return errors.Wrap(next.RecvRequest(ctx, req))
}

func (rcdr *StatsRecorder) handleReqReceived(req *RequestEnvelope) {
	if req == nil {
		return
	}

	key := transpKey{
		proto: req.Transport().Proto,
		laddr: req.LocalAddr(),
	}
	if !key.laddr.IsValid() {
		return
	}

	stats := rcdr.getTpStats(key)
	stats.inReqs.Add(1)
}

// InterceptInboundResponse intercepts inbound responses and records statistics.
func (rcdr *StatsRecorder) InterceptInboundResponse(
	ctx context.Context,
	next ResponseReceiver,
	res *ResponseEnvelope,
) error {
	rcdr.handleResReceived(res)
	return errors.Wrap(next.RecvResponse(ctx, res))
}

func (rcdr *StatsRecorder) handleResReceived(res *ResponseEnvelope) {
	if res == nil {
		return
	}

	key := transpKey{
		proto: res.Transport().Proto,
		laddr: res.LocalAddr(),
	}
	if !key.laddr.IsValid() {
		return
	}

	stats := rcdr.getTpStats(key)
	stats.inRess.Add(1)

	// Calculate RTT using Timestamp header if present
	if r := res.Message(); r != nil && r.Headers != nil {
		if hdrs := r.Headers.Get("Timestamp"); len(hdrs) > 0 {
			if ts, ok := hdrs[0].(*header.Timestamp); ok && !ts.RequestTime.IsZero() {
				if resTime := res.MessageTime(); !resTime.Before(ts.RequestTime.Add(ts.ResponseDelay)) {
					stats.rttNum.Add(1)
					stats.rttSum.Add(uint64(resTime.Sub(ts.RequestTime) - ts.ResponseDelay))
				}
			}
		}
	}
}

// InterceptOutboundRequest intercepts outbound requests and records statistics.
// It also adds Timestamp header for RTT measurements.
func (rcdr *StatsRecorder) InterceptOutboundRequest(
	ctx context.Context,
	next RequestSender,
	req *RequestEnvelope,
	opts ...SendRequestOptions,
) error {
	rcdr.handleReqBeforeSend(req)

	if err := next.SendRequest(ctx, req, opts...); err != nil {
		return errors.Wrap(err)
	}

	rcdr.handleReqSent(req)
	return nil
}

func (*StatsRecorder) handleReqBeforeSend(req *RequestEnvelope) {
	if req == nil {
		return
	}

	req.WithMessage(func(r *Request) {
		if hdrs := r.Headers.Get("Timestamp"); len(hdrs) == 0 {
			r.Headers.Set(&header.Timestamp{RequestTime: time.Now()})
		}
	})
}

func (rcdr *StatsRecorder) handleReqSent(req *RequestEnvelope) {
	if req == nil {
		return
	}

	key := transpKey{
		proto: req.Transport().Proto,
		laddr: req.LocalAddr(),
	}
	if !key.laddr.IsValid() {
		return
	}

	stats := rcdr.getTpStats(key)
	stats.outReqs.Add(1)
}

// InterceptOutboundResponse intercepts outbound responses and records statistics.
// It also calculates response delay using Timestamp header.
func (rcdr *StatsRecorder) InterceptOutboundResponse(
	ctx context.Context,
	next ResponseSender,
	res *ResponseEnvelope,
	opts ...SendResponseOptions,
) error {
	rcdr.handleResBeforeSend(res)

	if err := next.SendResponse(ctx, res, opts...); err != nil {
		return errors.Wrap(err)
	}

	rcdr.handleResSent(res)
	return nil
}

func (*StatsRecorder) handleResBeforeSend(res *ResponseEnvelope) {
	if res == nil {
		return
	}

	res.WithMessage(func(r *Response) {
		if hdrs := r.Headers.Get("Timestamp"); len(hdrs) > 0 {
			if ts, ok := hdrs[0].(*header.Timestamp); ok && !ts.RequestTime.IsZero() && ts.ResponseDelay == 0 {
				if val, ok := res.Metadata().Get(reqTimeMetaKey); ok {
					if reqTS, ok := val.(time.Time); ok && !reqTS.IsZero() {
						ts.ResponseDelay = time.Since(reqTS)
					}
				}
			}
		}
	})
}

func (rcdr *StatsRecorder) handleResSent(res *ResponseEnvelope) {
	if res == nil {
		return
	}

	key := transpKey{
		proto: res.Transport().Proto,
		laddr: res.LocalAddr(),
	}
	if !key.laddr.IsValid() {
		return
	}

	stats := rcdr.getTpStats(key)
	stats.outRess.Add(1)
}

func (rcdr *StatsRecorder) handleTx(tx Transaction) {
	if tx == nil {
		return
	}

	switch tx.Type() {
	case TransactionTypeClientInvite:
		rcdr.invClnTxs.Add(1)
		rcdr.invClnTxsTotal.Add(1)
	case TransactionTypeClientNonInvite:
		rcdr.ninvClnTxs.Add(1)
		rcdr.ninvClnTxsTotal.Add(1)
	case TransactionTypeServerInvite:
		rcdr.invSrvTxs.Add(1)
		rcdr.invSrvTxsTotal.Add(1)
	case TransactionTypeServerNonInvite:
		rcdr.ninvSrvTxs.Add(1)
		rcdr.ninvSrvTxsTotal.Add(1)
	}

	var once sync.Once
	dec := func() {
		once.Do(func() {
			switch tx.Type() {
			case TransactionTypeClientInvite:
				rcdr.invClnTxs.Add(-1)
			case TransactionTypeClientNonInvite:
				rcdr.ninvClnTxs.Add(-1)
			case TransactionTypeServerInvite:
				rcdr.invSrvTxs.Add(-1)
			case TransactionTypeServerNonInvite:
				rcdr.ninvSrvTxs.Add(-1)
			}
		})
	}

	unbind := tx.BindStateHandler(TransactionStateHandlerFunc(func(_ context.Context, _, to TransactionState) {
		if to == TransactionStateTerminated {
			dec()
		}
	}))

	if tx.State() == TransactionStateTerminated {
		unbind()
		dec()
	}
}

// HandleClientTransaction handles new client transactions for statistics.
func (rcdr *StatsRecorder) HandleClientTransaction(_ context.Context, tx ClientTransaction) {
	rcdr.handleTx(tx)
}

// HandleServerTransaction handles new server transactions for statistics.
func (rcdr *StatsRecorder) HandleServerTransaction(_ context.Context, tx ServerTransaction) {
	rcdr.handleTx(tx)
}

// HandleProxyEvent records one proxy observation event.
//
// Each counter increments only at its committed event kind: an attempt
// counts as succeeded or failed by the presence of the event error, and
// the completion reason counters derive solely from the committed
// outcome carried by a completion event — a downstream error response is
// a response decision, not an attempt failure and not a rejection.
func (rcdr *StatsRecorder) HandleProxyEvent(_ context.Context, event ProxyEvent) {
	switch event.Kind {
	case ProxyEventRequestHandled:
		rcdr.reqsHandled.Add(1)
	case ProxyEventForwardAttemptFinished:
		if event.Error != nil {
			rcdr.attFail.Add(1)
		} else {
			rcdr.attSucc.Add(1)
		}
	case ProxyEventRequestForwarded:
		rcdr.reqsFwd.Add(1)
	case ProxyEventResponseForwarded:
		rcdr.resFwd.Add(1)
	case ProxyEventRequestCompleted:
		rcdr.reqsCompl.Add(1)
		switch event.Outcome {
		case ProxyOutcomeRejected:
			rcdr.reqsRej.Add(1)
		case ProxyOutcomeTimeout:
			rcdr.reqsTmo.Add(1)
		case ProxyOutcomeCancelled:
			rcdr.reqsCnc.Add(1)
		case ProxyOutcomeResponse, ProxyOutcomeSendFailure,
			ProxyOutcomeAbandoned, ProxyOutcomeSent:
			// Counted only in the completion total.
		}
	}
}
