package stats

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/proxy"
)

// Report is a snapshot of SIP stack statistics.
type Report struct {
	Time         time.Time   `json:"time"`
	Transports   []Transport `json:"transports"`
	Transactions Transaction `json:"transactions"`
	Proxy        Proxy       `json:"proxy"`
}

// Transport contains statistics for a single transport.
type Transport struct {
	// Proto is a transport protocol.
	Proto sip.TransportProto `json:"proto"`
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

// Transaction contains statistics for transactions.
type Transaction struct {
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

// Proxy contains cumulative statistics derived from [proxy.Event]
// observations. The counters are subscription-lifetime observed totals:
// they count the events the recorder observed since it was bound, they
// keep no per-request or per-target labels, and they do not retain the
// history of requests that completed before the binding.
type Proxy struct {
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

// Recorder records various SIP statistics.
// It implements [sip.MessageInterceptor], [sip.TransactionHandler] and
// [proxy.EventHandler] interfaces: it can be used as a middleware for
// Element and as a proxy observation event handler at the same time —
// the proxy counters count committed forwarding events only, so one
// recorder serving both roles never counts a logical event twice.
type Recorder struct {
	transpsStats
	transactStats
	proxyStats
}

var (
	_ sip.MessageInterceptor = (*Recorder)(nil)
	_ sip.TransactionHandler = (*Recorder)(nil)
	_ sip.ElementMiddleware  = (*Recorder)(nil)
	_ proxy.EventHandler     = (*Recorder)(nil)
)

type transpsStats struct {
	stats sync.Map // map[transpKey]*transpStats
}

type transpKey struct {
	proto sip.TransportProto
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

type proxyStats struct {
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

func (rcdr *Recorder) LogValue() slog.Value {
	if rcdr == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.String("ptr", fmt.Sprintf("%p", rcdr)),
	)
}

// Report returns statistics report about various SIP layers.
// Call this function periodically to get updated values.
func (rcdr *Recorder) Report() Report {
	report := Report{
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

		report.Transports = append(report.Transports, Transport{
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

	report.Transactions = Transaction{
		InviteClientTransactions:         clampToUint64(rcdr.invClnTxs.Load()),
		NonInviteClientTransactions:      clampToUint64(rcdr.ninvClnTxs.Load()),
		InviteServerTransactions:         clampToUint64(rcdr.invSrvTxs.Load()),
		NonInviteServerTransactions:      clampToUint64(rcdr.ninvSrvTxs.Load()),
		InviteClientTransactionsTotal:    rcdr.invClnTxsTotal.Load(),
		NonInviteClientTransactionsTotal: rcdr.ninvClnTxsTotal.Load(),
		InviteServerTransactionsTotal:    rcdr.invSrvTxsTotal.Load(),
		NonInviteServerTransactionsTotal: rcdr.ninvSrvTxsTotal.Load(),
	}

	report.Proxy = Proxy{
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

func (rcdr *Recorder) getTpStats(key transpKey) *transpStats {
	stats, _ := rcdr.stats.LoadOrStore(key, &transpStats{})
	return stats.(*transpStats) //nolint:forcetypeassert // the concrete type is fixed by construction
}

// InterceptInboundRequest intercepts inbound requests and records statistics.
func (rcdr *Recorder) InterceptInboundRequest(
	ctx context.Context,
	next sip.RequestReceiver,
	req *sip.RequestEnvelope,
) error {
	rcdr.handleReqReceived(req)
	return errors.Wrap(next.RecvRequest(ctx, req))
}

func (rcdr *Recorder) handleReqReceived(req *sip.RequestEnvelope) {
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
func (rcdr *Recorder) InterceptInboundResponse(
	ctx context.Context,
	next sip.ResponseReceiver,
	res *sip.ResponseEnvelope,
) error {
	rcdr.handleResReceived(res)
	return errors.Wrap(next.RecvResponse(ctx, res))
}

func (rcdr *Recorder) handleResReceived(res *sip.ResponseEnvelope) {
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
func (rcdr *Recorder) InterceptOutboundRequest(
	ctx context.Context,
	next sip.RequestSender,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	rcdr.handleReqBeforeSend(req)

	if err := next.SendRequest(ctx, req, opts...); err != nil {
		return errors.Wrap(err)
	}

	rcdr.handleReqSent(req)
	return nil
}

func (*Recorder) handleReqBeforeSend(req *sip.RequestEnvelope) {
	if req == nil {
		return
	}

	req.WithMessage(func(r *sip.Request) {
		if hdrs := r.Headers.Get("Timestamp"); len(hdrs) == 0 {
			r.Headers.Set(&header.Timestamp{RequestTime: time.Now()})
		}
	})
}

func (rcdr *Recorder) handleReqSent(req *sip.RequestEnvelope) {
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
func (rcdr *Recorder) InterceptOutboundResponse(
	ctx context.Context,
	next sip.ResponseSender,
	res *sip.ResponseEnvelope,
	opts ...sip.SendResponseOptions,
) error {
	rcdr.handleResBeforeSend(res)

	if err := next.SendResponse(ctx, res, opts...); err != nil {
		return errors.Wrap(err)
	}

	rcdr.handleResSent(res)
	return nil
}

func (*Recorder) handleResBeforeSend(res *sip.ResponseEnvelope) {
	if res == nil {
		return
	}

	// The request timestamp lives in the envelope metadata, so read it
	// before entering the message mutation lock.
	reqTS, ok := res.RequestTime()
	if !ok || reqTS.IsZero() {
		return
	}

	res.WithMessage(func(r *sip.Response) {
		if hdrs := r.Headers.Get("Timestamp"); len(hdrs) > 0 {
			if ts, ok := hdrs[0].(*header.Timestamp); ok && !ts.RequestTime.IsZero() && ts.ResponseDelay == 0 {
				ts.ResponseDelay = time.Since(reqTS)
			}
		}
	})
}

func (rcdr *Recorder) handleResSent(res *sip.ResponseEnvelope) {
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

// clampToUint64 converts a non-negative counter to uint64, clamping
// negative values to zero.
func clampToUint64(value int64) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}

func (rcdr *Recorder) handleTx(tx sip.Transaction) {
	if tx == nil {
		return
	}

	switch tx.Type() {
	case sip.TransactionTypeClientInvite:
		rcdr.invClnTxs.Add(1)
		rcdr.invClnTxsTotal.Add(1)
	case sip.TransactionTypeClientNonInvite:
		rcdr.ninvClnTxs.Add(1)
		rcdr.ninvClnTxsTotal.Add(1)
	case sip.TransactionTypeServerInvite:
		rcdr.invSrvTxs.Add(1)
		rcdr.invSrvTxsTotal.Add(1)
	case sip.TransactionTypeServerNonInvite:
		rcdr.ninvSrvTxs.Add(1)
		rcdr.ninvSrvTxsTotal.Add(1)
	}

	var once sync.Once
	dec := func() {
		once.Do(func() {
			switch tx.Type() {
			case sip.TransactionTypeClientInvite:
				rcdr.invClnTxs.Add(-1)
			case sip.TransactionTypeClientNonInvite:
				rcdr.ninvClnTxs.Add(-1)
			case sip.TransactionTypeServerInvite:
				rcdr.invSrvTxs.Add(-1)
			case sip.TransactionTypeServerNonInvite:
				rcdr.ninvSrvTxs.Add(-1)
			}
		})
	}

	unbind := tx.BindStateHandler(sip.TransactionStateHandlerFunc(func(_ context.Context, _, to sip.TransactionState) {
		if to == sip.TransactionStateTerminated {
			dec()
		}
	}))

	if tx.State() == sip.TransactionStateTerminated {
		unbind()
		dec()
	}
}

// HandleClientTransaction handles new client transactions for statistics.
func (rcdr *Recorder) HandleClientTransaction(_ context.Context, tx sip.ClientTransaction) {
	rcdr.handleTx(tx)
}

// HandleServerTransaction handles new server transactions for statistics.
func (rcdr *Recorder) HandleServerTransaction(_ context.Context, tx sip.ServerTransaction) {
	rcdr.handleTx(tx)
}

// HandleEvent records one proxy observation event.
//
// Each counter increments only at its committed event kind: an attempt
// counts as succeeded or failed by the presence of the event error, and
// the completion reason counters derive solely from the committed
// outcome carried by a completion event — a downstream error response is
// a response decision, not an attempt failure and not a rejection.
func (rcdr *Recorder) HandleEvent(_ context.Context, event proxy.Event) {
	switch event.Kind {
	case proxy.EventKindRequestHandled:
		rcdr.reqsHandled.Add(1)
	case proxy.EventKindForwardAttemptFinished:
		if event.Error != nil {
			rcdr.attFail.Add(1)
		} else {
			rcdr.attSucc.Add(1)
		}
	case proxy.EventKindRequestForwarded:
		rcdr.reqsFwd.Add(1)
	case proxy.EventKindResponseForwarded:
		rcdr.resFwd.Add(1)
	case proxy.EventKindRequestCompleted:
		rcdr.reqsCompl.Add(1)
		switch event.Outcome {
		case proxy.OutcomeRejected:
			rcdr.reqsRej.Add(1)
		case proxy.OutcomeTimeout:
			rcdr.reqsTmo.Add(1)
		case proxy.OutcomeCancelled:
			rcdr.reqsCnc.Add(1)
		case proxy.OutcomeResponse, proxy.OutcomeSendFailure,
			proxy.OutcomeAbandoned, proxy.OutcomeSent:
			// Counted only in the completion total.
		}
	}
}
