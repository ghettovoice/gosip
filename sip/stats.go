package sip

import (
	"context"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/header"
)

type StatsReport struct {
	Time         time.Time        `json:"time"`
	Transports   []TransportStats `json:"transports"`
	Transactions TransactionStats `json:"transactions"`
}

type TransportStats struct {
	// Proto is a transport protocol.
	Proto TransportProto `json:"proto"`
	// LocalAddr is a local address.
	LocalAddr string `json:"local_addr"`
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

// StatsRecorder records various SIP statistics.
type StatsRecorder struct {
	transpsStats
	transactStats
}

type transpsStats struct {
	stats sync.Map // map[transpKey]*transpStats
}

type transpKey struct {
	proto TransportProto
	laddr netip.AddrPort
}

func transpKeyFromContext(ctx context.Context) (key transpKey, got bool) {
	tp, ok := TransportFromContext(ctx)
	if !ok {
		return key, false
	}
	proto, ok := GetTransportProto(tp)
	if !ok {
		return key, false
	}
	laddr, ok := GetTransportLocalAddr(tp)
	if !ok {
		return key, false
	}
	return transpKey{proto, laddr}, true
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
			LocalAddr:         tpKey.laddr.String(),
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

	return report
}

func (rcdr *StatsRecorder) getTranspStats(key transpKey) *transpStats {
	stats, _ := rcdr.stats.LoadOrStore(key, &transpStats{})
	return stats.(*transpStats) //nolint:forcetypeassert
}

func clampToUint64(value int64) uint64 {
	if value <= 0 {
		return 0
	}
	return uint64(value)
}

// InboundRequestInterceptor returns an interceptor for inbound requests.
func (rcdr *StatsRecorder) InboundRequestInterceptor() InboundRequestInterceptor {
	return InboundRequestInterceptorFunc(
		func(ctx context.Context, req *InboundRequestEnvelope, next RequestReceiver) error {
			rcdr.handleReqRecvied(ctx, req)
			return errtrace.Wrap(next.RecvRequest(ctx, req))
		},
	)
}

func (rcdr *StatsRecorder) handleReqRecvied(ctx context.Context, _ *InboundRequestEnvelope) {
	key, ok := transpKeyFromContext(ctx)
	if !ok {
		return
	}

	stats := rcdr.getTranspStats(key)
	stats.inReqs.Add(1)
}

// InboundResponseInterceptor returns an interceptor for inbound responses.
func (rcdr *StatsRecorder) InboundResponseInterceptor() InboundResponseInterceptor {
	return InboundResponseInterceptorFunc(
		func(ctx context.Context, res *InboundResponseEnvelope, next ResponseReceiver) error {
			rcdr.handleResReceived(ctx, res)
			return errtrace.Wrap(next.RecvResponse(ctx, res))
		},
	)
}

func (rcdr *StatsRecorder) handleResReceived(ctx context.Context, res *InboundResponseEnvelope) {
	key, ok := transpKeyFromContext(ctx)
	if !ok {
		return
	}

	stats := rcdr.getTranspStats(key)
	stats.inRess.Add(1)

	if hdrs := res.Headers().Get("Timestamp"); len(hdrs) > 0 {
		if ts, ok := hdrs[0].(*header.Timestamp); ok && !ts.RequestTime.IsZero() {
			if resTime := res.MessageTime(); !resTime.Before(ts.RequestTime.Add(ts.ResponseDelay)) {
				stats.rttNum.Add(1)
				stats.rttSum.Add(uint64(resTime.Sub(ts.RequestTime) - ts.ResponseDelay))
			}
		}
	}
}

// OutboundRequestInterceptor returns an interceptor for outbound requests.
func (rcdr *StatsRecorder) OutboundRequestInterceptor() OutboundRequestInterceptor {
	return OutboundRequestInterceptorFunc(
		func(ctx context.Context, req *OutboundRequestEnvelope, opts *SendRequestOptions, next RequestSender) error {
			rcdr.handleReqBeforeSend(ctx, req)
			if err := next.SendRequest(ctx, req, opts); err != nil {
				return errtrace.Wrap(err)
			}
			rcdr.handleReqSent(ctx, req)
			return nil
		},
	)
}

func (*StatsRecorder) handleReqBeforeSend(_ context.Context, req *OutboundRequestEnvelope) {
	req.AccessMessage(func(r *Request) {
		if r == nil || r.Headers == nil {
			return
		}

		if hdrs := r.Headers.Get("Timestamp"); len(hdrs) == 0 {
			r.Headers.Set(&header.Timestamp{RequestTime: time.Now()})
		}
	})
}

func (rcdr *StatsRecorder) handleReqSent(ctx context.Context, _ *OutboundRequestEnvelope) {
	key, ok := transpKeyFromContext(ctx)
	if !ok {
		return
	}

	stats := rcdr.getTranspStats(key)
	stats.outReqs.Add(1)
}

// OutboundResponseInterceptor returns an interceptor for outbound responses.
func (rcdr *StatsRecorder) OutboundResponseInterceptor() OutboundResponseInterceptor {
	return OutboundResponseInterceptorFunc(
		func(ctx context.Context, res *OutboundResponseEnvelope, opts *SendResponseOptions, next ResponseSender) error {
			rcdr.handleResBeforeSend(ctx, res)
			if err := next.SendResponse(ctx, res, opts); err != nil {
				return errtrace.Wrap(err)
			}
			rcdr.handleResSent(ctx, res)
			return nil
		},
	)
}

func (*StatsRecorder) handleResBeforeSend(_ context.Context, res *OutboundResponseEnvelope) {
	res.AccessMessage(func(r *Response) {
		if r == nil || r.Headers == nil {
			return
		}

		if hdrs := r.Headers.Get("Timestamp"); len(hdrs) > 0 {
			if ts, ok := hdrs[0].(*header.Timestamp); ok && !ts.RequestTime.IsZero() && ts.ResponseDelay == 0 {
				if val, ok := res.Metadata().Get(reqTimeDataKey); ok {
					if reqTS, ok := val.(time.Time); ok && !reqTS.IsZero() {
						ts.ResponseDelay = time.Since(reqTS)
					}
				}
			}
		}
	})
}

func (rcdr *StatsRecorder) handleResSent(ctx context.Context, _ *OutboundResponseEnvelope) {
	key, ok := transpKeyFromContext(ctx)
	if !ok {
		return
	}

	stats := rcdr.getTranspStats(key)
	stats.outRess.Add(1)
}

func (rcdr *StatsRecorder) BindTransactionInitHandlers(hdlrs TransactionInitHandlerRegistry) (unbind func()) {
	unbind1 := hdlrs.OnNewClientTransaction(rcdr.handleNewClnTx)
	unbind2 := hdlrs.OnNewServerTransaction(rcdr.handleNewSrvTx)
	return func() {
		unbind1()
		unbind2()
	}
}

func (rcdr *StatsRecorder) handleNewClnTx(ctx context.Context, tx ClientTransaction) {
	//nolint:exhaustive
	switch tx.Type() {
	case TransactionTypeClientInvite:
		rcdr.invClnTxs.Add(1)
		rcdr.invClnTxsTotal.Add(1)
	case TransactionTypeClientNonInvite:
		rcdr.ninvClnTxs.Add(1)
		rcdr.ninvClnTxsTotal.Add(1)
	}

	tx.OnStateChanged(func(ctx context.Context, from, to TransactionState) {
		if to != TransactionStateTerminated {
			return
		}

		//nolint:exhaustive
		switch tx.Type() {
		case TransactionTypeClientInvite:
			rcdr.invClnTxs.Add(-1)
		case TransactionTypeClientNonInvite:
			rcdr.ninvClnTxs.Add(-1)
		}
	})
}

func (rcdr *StatsRecorder) handleNewSrvTx(ctx context.Context, tx ServerTransaction) {
	//nolint:exhaustive
	switch tx.Type() {
	case TransactionTypeServerInvite:
		rcdr.invSrvTxs.Add(1)
		rcdr.invSrvTxsTotal.Add(1)
	case TransactionTypeServerNonInvite:
		rcdr.ninvSrvTxs.Add(1)
		rcdr.ninvSrvTxsTotal.Add(1)
	}

	tx.OnStateChanged(func(ctx context.Context, from, to TransactionState) {
		if to != TransactionStateTerminated {
			return
		}

		//nolint:exhaustive
		switch tx.Type() {
		case TransactionTypeServerInvite:
			rcdr.invSrvTxs.Add(-1)
		case TransactionTypeServerNonInvite:
			rcdr.ninvSrvTxs.Add(-1)
		}
	})
}

func BindStatsRecorder(
	rcdr *StatsRecorder,
	msgChain MessageInterceptorChain,
	txHdlrs TransactionInitHandlerRegistry,
) (unbind func()) {
	unbinds := make([]func(), 0, 2)
	if msgChain != nil {
		unbinds = append(unbinds, msgChain.UseInterceptor(rcdr))
	}
	if txHdlrs != nil {
		unbinds = append(unbinds, rcdr.BindTransactionInitHandlers(txHdlrs))
	}
	return func() {
		for _, fn := range unbinds {
			fn()
		}
	}
}
