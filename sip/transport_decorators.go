package sip

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/log"
)

// StatsTypeTransport is the transport statistics type.
const StatsTypeTransport StatsType = "transport"

// TransportStats provides statistic values about the transport.
type TransportStats struct {
	// StatsID is a statistic StatsID.
	StatsID StatsID `json:"stats_id" yaml:"stats_id"`
	// StatsType is a statistic type.
	StatsType StatsType `json:"stats_type" yaml:"stats_type"`
	// StatsTime is a statistic timestamp.
	StatsTime time.Time `json:"stats_time" yaml:"stats_time"`

	// Proto is a transport protocol.
	Proto TransportProto `json:"proto" yaml:"proto"`
	// RequestsReceived is a number of received requests.
	RequestsReceived uint64 `json:"requests_received" yaml:"requests_received"`
	// RequestsSent is a number of sent requests.
	RequestsSent uint64 `json:"requests_sent" yaml:"requests_sent"`
	// ResponsesReceived is a number of received responses.
	ResponsesReceived uint64 `json:"responses_received" yaml:"responses_received"`
	// ResponsesSent is a number of sent responses.
	ResponsesSent uint64 `json:"responses_sent" yaml:"responses_sent"`
	// AvgRTT is an average round-trip time.
	AvgRTT time.Duration `json:"avg_rtt" yaml:"avg_rtt"`
	// NumRTT is a number of round-trip measurements.
	NumRTT uint64 `json:"num_rtt" yaml:"num_rtt"`
}

type statsTransp struct {
	*statsClientTransp
	*statsServerTransp
	statsID StatsID
	transp  Transport
}

type statsServerTransp struct {
	ServerTransport
	*statsServerValues
	cancOnReq func()
	onReq     types.CallbackManager[TransportRequestHandler]
}

type statsServerValues struct {
	inReqs,
	outRess atomic.Uint64
}

type statsClientTransp struct {
	ClientTransport
	*statsClientValues
	cancOnRes func()
	onRes     types.CallbackManager[TransportResponseHandler]
}

type statsClientValues struct {
	outReqs,
	inRess,
	avgRTT,
	rttNum atomic.Uint64
}

// NewStatsTransport decorates a transport with statistics reporting.
// See [TransportStats] for details which statistics are reported.
func NewStatsTransport(tp Transport) Transport {
	if tp == nil {
		return nil
	}

	if tp, ok := tp.(*statsTransp); ok {
		return tp
	}

	var statsID StatsID
	if v, ok := tp.(interface{ StatsID() StatsID }); ok {
		statsID = v.StatsID()
	} else {
		// TODO: handle empty proto and laddr, generate random ID
		proto, _ := GetTransportProto(tp)
		laddr, _ := GetTransportLocalAddr(tp)
		statsID = StatsID(string(proto) + ":" + laddr.String())
	}

	stp := &statsTransp{
		statsClientTransp: &statsClientTransp{
			ClientTransport:   tp,
			statsClientValues: &statsClientValues{},
		},
		statsServerTransp: &statsServerTransp{
			ServerTransport:   tp,
			statsServerValues: &statsServerValues{},
		},
		statsID: statsID,
		transp:  tp,
	}

	stp.cancOnReq = tp.OnRequest(stp.recvReq)
	stp.cancOnRes = tp.OnResponse(stp.recvRes)

	return stp
}

func (stp *statsServerTransp) recvReq(ctx context.Context, tp ServerTransport, req *InboundRequest) {
	stp.inReqs.Add(1)

	if tp, ok := ServerTransportFromContext(ctx); ok {
		if _, ok := tp.(*statsServerTransp); !ok {
			ctx = context.WithValue(ctx, srvTranspCtxKey, &statsServerTransp{
				ServerTransport:   tp,
				statsServerValues: stp.statsServerValues,
			})
		}
	}
	if _, ok := tp.(*statsServerTransp); !ok {
		tp = &statsServerTransp{
			ServerTransport:   tp,
			statsServerValues: stp.statsServerValues,
		}
	}

	var handled bool
	stp.onReq.Range(func(fn TransportRequestHandler) {
		handled = true
		fn(ctx, tp, req)
	})
	if handled {
		return
	}

	log.LoggerFromValues(ctx, tp).LogAttrs(ctx, slog.LevelWarn,
		"discarding inbound request due to missing request handlers",
		slog.Any("request", req),
	)
	respondStateless(ctx, tp, req, ResponseStatusServiceUnavailable)
}

func (stp *statsClientTransp) recvRes(ctx context.Context, tp ClientTransport, res *InboundResponse) {
	stp.inRess.Add(1)

	if tp, ok := ClientTransportFromContext(ctx); ok {
		if _, ok := tp.(*statsClientTransp); !ok {
			ctx = context.WithValue(ctx, clnTranspCtxKey, &statsClientTransp{
				ClientTransport:   tp,
				statsClientValues: stp.statsClientValues,
			})
		}
	}
	if _, ok := tp.(*statsClientTransp); !ok {
		tp = &statsClientTransp{
			ClientTransport:   tp,
			statsClientValues: stp.statsClientValues,
		}
	}

	if hdr, ok := res.Headers().Timestamp(); ok && !hdr.RequestTime.IsZero() {
		if resTime := res.MessageTime(); !resTime.After(hdr.RequestTime.Add(hdr.ResponseDelay)) {
			n := stp.rttNum.Add(1)
			rtt := uint64(resTime.Sub(hdr.RequestTime) - hdr.ResponseDelay)
			stp.avgRTT.Store((stp.avgRTT.Load()*(n-1) + rtt) / n)
		}
	}

	var handled bool
	stp.onRes.Range(func(fn TransportResponseHandler) {
		handled = true
		fn(ctx, tp, res)
	})
	if handled {
		return
	}

	log.LoggerFromValues(ctx, tp).LogAttrs(ctx, slog.LevelWarn,
		"discarding inbound response due to missing response handlers",
		slog.Any("response", res),
	)
}

// SendRequestOptions sends a request to the specified remote address with options.
func (stp *statsClientTransp) SendRequest(ctx context.Context, req *OutboundRequest, opts *SendRequestOptions) error {
	req.UpdateMessage(func(msg *Request) {
		msg.Headers.Set(&header.Timestamp{RequestTime: time.Now()})
	})

	if err := stp.ClientTransport.SendRequest(ctx, req, opts); err != nil {
		return errtrace.Wrap(err)
	}

	stp.outReqs.Add(1)
	return nil
}

// SendResponseOptions sends a response to a remote address.
func (stp *statsServerTransp) SendResponse(ctx context.Context, res *OutboundResponse, opts *SendResponseOptions) error {
	res.UpdateMessage(func(msg *Response) {
		if hdr, ok := msg.Headers.Timestamp(); ok {
			if val, ok := res.Metadata().Get(reqTimeDataKey); ok {
				if reqTS, ok := val.(time.Time); ok && !reqTS.IsZero() {
					hdr.ResponseDelay = time.Since(reqTS)
				}
			}
		}
	})

	if err := stp.ServerTransport.SendResponse(ctx, res, opts); err != nil {
		return errtrace.Wrap(err)
	}

	stp.outRess.Add(1)
	return nil
}

func (stp *statsServerTransp) OnRequest(fn TransportRequestHandler) (cancel func()) {
	return stp.onReq.Add(fn)
}

func (stp *statsClientTransp) OnResponse(fn TransportResponseHandler) (cancel func()) {
	return stp.onRes.Add(fn)
}

func (stp *statsTransp) Serve() error { return errtrace.Wrap(stp.transp.Serve()) }

func (stp *statsTransp) Close() error {
	stp.cancOnReq()
	stp.cancOnRes()
	return errtrace.Wrap(stp.transp.Close())
}

// StatsID returns a statistics ID.
func (stp *statsTransp) StatsID() StatsID { return stp.statsID }

// CollectStats returns a statistics report.
// Call it periodically to collect statistics.
func (stp *statsTransp) CollectStats(ctx context.Context, rcdr StatsRecorder) error {
	proto, _ := GetTransportProto(stp.transp)
	return errtrace.Wrap(rcdr.RecordStats(ctx, stp.statsID, TransportStats{
		StatsID:           stp.statsID,
		StatsType:         StatsTypeTransport,
		StatsTime:         time.Now(),
		Proto:             proto,
		RequestsReceived:  stp.inReqs.Load(),
		RequestsSent:      stp.outReqs.Load(),
		ResponsesReceived: stp.inRess.Load(),
		ResponsesSent:     stp.outRess.Load(),
		AvgRTT:            time.Duration(stp.avgRTT.Load()),
		NumRTT:            stp.rttNum.Load(),
	}))
}

func (stp *statsTransp) LogValue() slog.Value {
	if stp == nil || stp.transp == nil {
		return slog.Value{}
	}

	proto, _ := GetTransportProto(stp.transp)
	netw, _ := GetTransportNetwork(stp.transp)
	laddr, _ := GetTransportLocalAddr(stp.transp)

	return slog.GroupValue(
		slog.Any("proto", proto),
		slog.Any("network", netw),
		slog.Any("local_addr", laddr),
	)
}

type logMsgTransp struct {
	logMsgServerTransp
	logMsgClientTransp
	transp Transport
}

type logMsgServerTransp struct {
	ServerTransport
	log       *slog.Logger
	lvl       slog.Level
	cancOnReq func()
	onReq     types.CallbackManager[TransportRequestHandler]
}

type logMsgClientTransp struct {
	ClientTransport
	log       *slog.Logger
	lvl       slog.Level
	cancOnRes func()
	onRes     types.CallbackManager[TransportResponseHandler]
}

// NewLogMessageTransport decorates a transport with message logging.
func NewLogMessageTransport(tp Transport, logger *slog.Logger, lvl slog.Level) Transport {
	if tp == nil {
		return nil
	}

	if _, ok := tp.(*logMsgTransp); ok {
		return tp
	}

	logger = logger.With("transport", tp)
	ltp := &logMsgTransp{
		logMsgServerTransp: logMsgServerTransp{
			ServerTransport: tp,
			log:             logger,
			lvl:             lvl,
		},
		logMsgClientTransp: logMsgClientTransp{
			ClientTransport: tp,
			log:             logger,
			lvl:             lvl,
		},
		transp: tp,
	}

	ltp.cancOnReq = tp.OnRequest(ltp.recvReq)
	ltp.cancOnRes = tp.OnResponse(ltp.recvRes)

	return ltp
}

func (ltp *logMsgServerTransp) recvReq(ctx context.Context, tp ServerTransport, req *InboundRequest) {
	ltp.log.LogAttrs(ctx, ltp.lvl, "received the request", slog.Any("request", req))

	if tp, ok := ServerTransportFromContext(ctx); ok {
		if _, ok := tp.(*logMsgServerTransp); !ok {
			ctx = context.WithValue(ctx, srvTranspCtxKey, &logMsgServerTransp{
				ServerTransport: tp,
				log:             ltp.log,
				lvl:             ltp.lvl,
			})
		}
	}
	if _, ok := tp.(*logMsgServerTransp); !ok {
		tp = &logMsgServerTransp{
			ServerTransport: tp,
			log:             ltp.log,
			lvl:             ltp.lvl,
		}
	}

	var handled bool
	ltp.onReq.Range(func(fn TransportRequestHandler) {
		handled = true
		fn(ctx, tp, req)
	})
	if handled {
		return
	}

	log.LoggerFromValues(ctx, tp).LogAttrs(ctx, slog.LevelWarn,
		"discarding inbound request due to missing request handlers",
		slog.Any("request", req),
	)
	respondStateless(ctx, tp, req, ResponseStatusServiceUnavailable)
}

func (ltp *logMsgClientTransp) recvRes(ctx context.Context, tp ClientTransport, res *InboundResponse) {
	ltp.log.LogAttrs(ctx, ltp.lvl, "received the response", slog.Any("response", res))

	if tp, ok := ClientTransportFromContext(ctx); ok {
		if _, ok := tp.(*logMsgClientTransp); !ok {
			ctx = context.WithValue(ctx, clnTranspCtxKey, &logMsgClientTransp{
				ClientTransport: tp,
				log:             ltp.log,
				lvl:             ltp.lvl,
			})
		}
	}
	if _, ok := tp.(*logMsgClientTransp); !ok {
		tp = &logMsgClientTransp{
			ClientTransport: tp,
			log:             ltp.log,
			lvl:             ltp.lvl,
		}
	}

	var handled bool
	ltp.onRes.Range(func(fn TransportResponseHandler) {
		handled = true
		fn(ctx, tp, res)
	})
	if handled {
		return
	}

	log.LoggerFromValues(ctx, tp).LogAttrs(ctx, slog.LevelWarn,
		"discarding inbound response due to missing response handlers",
		slog.Any("response", res),
	)
}

func (ltp *logMsgClientTransp) SendRequest(ctx context.Context, req *OutboundRequest, opts *SendRequestOptions) error {
	if err := ltp.ClientTransport.SendRequest(ctx, req, opts); err != nil {
		return errtrace.Wrap(err)
	}

	ltp.log.LogAttrs(ctx, ltp.lvl, "sent the request", slog.Any("request", req))

	return nil
}

func (ltp *logMsgServerTransp) SendResponse(ctx context.Context, res *OutboundResponse, opts *SendResponseOptions) error {
	if err := ltp.ServerTransport.SendResponse(ctx, res, opts); err != nil {
		return errtrace.Wrap(err)
	}

	ltp.log.LogAttrs(ctx, ltp.lvl, "sent the response", slog.Any("response", res))

	return nil
}

func (ltp *logMsgServerTransp) OnRequest(fn TransportRequestHandler) (cancel func()) {
	return ltp.onReq.Add(fn)
}

func (ltp *logMsgClientTransp) OnResponse(fn TransportResponseHandler) (cancel func()) {
	return ltp.onRes.Add(fn)
}

func (ltp *logMsgTransp) Serve() error { return errtrace.Wrap(ltp.transp.Serve()) }

func (ltp *logMsgTransp) Close() error {
	ltp.cancOnReq()
	ltp.cancOnRes()
	return errtrace.Wrap(ltp.transp.Close())
}

func (ltp *logMsgTransp) LogValue() slog.Value {
	if ltp == nil || ltp.transp == nil {
		return slog.Value{}
	}

	proto, _ := GetTransportProto(ltp.transp)
	netw, _ := GetTransportNetwork(ltp.transp)
	laddr, _ := GetTransportLocalAddr(ltp.transp)

	return slog.GroupValue(
		slog.Any("proto", proto),
		slog.Any("network", netw),
		slog.Any("local_addr", laddr),
	)
}
