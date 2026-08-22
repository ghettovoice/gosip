package sip

import (
	"context"
	"encoding/json"
	"log/slog"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/timeutil"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip/header"
)

// ServerTransaction represents a SIP server transaction.
// RFC 3261 Section 17.2.
type ServerTransaction interface {
	Transaction
	RequestReceiver
	ResponseSender
	// Start starts the server transaction.
	// It must be called exactly once after the transaction is registered.
	Start(ctx context.Context) error
	// Key returns the server transaction key.
	Key() ServerTransactionKey
	// Request returns the initial request that started this transaction.
	Request() *RequestEnvelope
	// LastResponse returns the last response sent by the transaction.
	LastResponse() *ResponseEnvelope
	// Transport returns the transport used by the transaction.
	Transport() ServerTransport
	// Respond sends a response to the transaction.
	Respond(ctx context.Context, sts ResponseStatus, opts ...RespondOptions) error
}

// ServerTransport represents a SIP server transport used in the server transaction.
type ServerTransport interface {
	Metadata() TransportMetadata
	ResponseSender
}

// ServerTransactionFactory creates initialized server transactions.
// It must not start transaction processing, send responses, or start transaction timers.
type ServerTransactionFactory interface {
	NewServerTransaction(
		ctx context.Context,
		req *RequestEnvelope,
		tp ServerTransport,
		opts ...ServerTransactionOptions,
	) (ServerTransaction, error)
}

// ServerTransactionFactoryFunc is a function that implements [ServerTransactionFactory].
type ServerTransactionFactoryFunc func(
	ctx context.Context,
	req *RequestEnvelope,
	tp ServerTransport,
	opts ...ServerTransactionOptions,
) (ServerTransaction, error)

func (f ServerTransactionFactoryFunc) NewServerTransaction(
	ctx context.Context,
	req *RequestEnvelope,
	tp ServerTransport,
	opts ...ServerTransactionOptions,
) (ServerTransaction, error) {
	return errors.Wrap2(f(ctx, req, tp, opts...))
}

// NewServerTransaction creates a new server transaction based on the request method.
// If the request method is INVITE, it creates an [InviteServerTransaction].
// Otherwise, it creates a [NonInviteServerTransaction].
func NewServerTransaction(
	ctx context.Context,
	req *RequestEnvelope,
	tp ServerTransport,
	opts ...ServerTransactionOptions,
) (ServerTransaction, error) {
	if req.Method().Equal(RequestMethodInvite) {
		return errors.Wrap2(NewInviteServerTransaction(ctx, req, tp, opts...))
	}
	return errors.Wrap2(NewNonInviteServerTransaction(ctx, req, tp, opts...))
}

// ServerTransactionOptions contains options for a server transaction.
type ServerTransactionOptions struct {
	// Timing is the SIP timing config that will be used with the transaction.
	// If zero, [DefaultTimings] will be used.
	Timing TimingConfig
	// Logger is the logger that will be used with the transaction.
	// If nil, the [log.Default] will be used.
	Logger *slog.Logger
}

func (o ServerTransactionOptions) log() *slog.Logger {
	if o.Logger == nil {
		return log.Default()
	}
	return o.Logger
}

type serverTransact struct {
	*baseTransact
	key      ServerTransactionKey
	tp       ServerTransport
	timing   TimingConfig
	req      *RequestEnvelope
	lastRes  atomic.Pointer[ResponseEnvelope]
	sendOpts atomic.Value // SendResponseOptions
	started  atomic.Bool
}

func newServerTransact(
	typ TransactionType,
	impl serverTransactImpl,
	req *RequestEnvelope,
	tp ServerTransport,
	opts ServerTransactionOptions,
) (*serverTransact, error) {
	if err := req.Validate(); err != nil {
		return nil, errors.Wrap(err)
	}
	if !req.Transport().IsValid() {
		return nil, errors.ErrorWrap("invalid request transport")
	}
	if !req.RemoteAddr().IsValid() {
		return nil, errors.ErrorWrap("invalid request remote address")
	}
	if tp == nil {
		return nil, errors.ErrorWrap("nil transport")
	}

	key, err := ServerTransactionKeyFromMessage(req)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	tx := &serverTransact{
		key:    key,
		tp:     tp,
		timing: opts.Timing,
		req:    req,
	}
	tx.baseTransact = newBaseTransact(typ, impl, opts.log())
	tx.sendOpts.Store(SendResponseOptions{})

	req.Metadata().
		Set(txKeyMetaKey, key).
		Set(txTypeMetaKey, tx.typ)

	return tx, nil
}

type serverTransactImpl interface {
	transactImpl
	ServerTransaction
	takeSnapshot() *ServerTransactionSnapshot
}

func (tx *serverTransact) srvTxImpl() serverTransactImpl {
	return tx.impl.(serverTransactImpl) //nolint:forcetypeassert
}

// Key returns the transaction key.
func (tx *serverTransact) Key() ServerTransactionKey { return tx.key }

// Request returns the initial request that started this transaction.
func (tx *serverTransact) Request() *RequestEnvelope {
	return tx.req.Clone().(*RequestEnvelope) //nolint:forcetypeassert
}

// LastResponse returns the last response sent by the transaction.
func (tx *serverTransact) LastResponse() *ResponseEnvelope {
	res := tx.lastRes.Load()
	if res == nil {
		return nil
	}
	return res.Clone().(*ResponseEnvelope) //nolint:forcetypeassert
}

// Transport returns the transport used by the transaction.
func (tx *serverTransact) Transport() ServerTransport { return tx.tp }

// MatchMessage checks whether the message matches the server transaction.
// It implements the matching rules defined in RFC 3261 section 17.2.3.
func (tx *serverTransact) MatchMessage(msg Message) bool {
	var (
		isReq, isRes bool
		mtd          RequestMethod
	)
	switch m := msg.(type) {
	case *RequestEnvelope:
		isReq = true
		mtd = m.Method()
	case *Request:
		isReq = true
		mtd = m.Method
	case *ResponseEnvelope:
		isRes = true
	case *Response:
		isRes = true
	}

	if isReq {
		reqKey, err := ServerTransactionKeyFromMessage(msg)
		if err != nil {
			return false
		}

		txKey := tx.key
		if v, ok := tx.impl.(interface {
			adjustKeys(txKey, reqKey *ServerTransactionKey, mtd RequestMethod)
		}); ok {
			v.adjustKeys(&txKey, &reqKey, mtd)
		}

		return txKey.Equal(reqKey)
	} else if isRes {
		return tx.matchRes(msg) == nil
	}

	return false
}

func (tx *serverTransact) matchRes(res Message) error {
	if err := res.Validate(); err != nil {
		return errors.Wrap(err)
	}

	reqHdrs := tx.req.Headers()
	resHdrs, _ := GetMessageHeaders(res)

	reqVia, _ := reqHdrs.FirstVia()
	resVia, _ := resHdrs.FirstVia()
	if !reqVia.Equal(resVia) {
		return errors.PrefixWrap(ErrMessageNotMatched, "response Via doesn't match transaction request")
	}

	reqCallID, _ := reqHdrs.CallID()
	resCallID, _ := resHdrs.CallID()
	if !reqCallID.Equal(resCallID) {
		return errors.PrefixWrap(ErrMessageNotMatched, "response Call-ID doesn't match transaction request")
	}

	reqFrom, _ := reqHdrs.From()
	resFrom, _ := resHdrs.From()
	if !reqFrom.Equal(resFrom) {
		return errors.PrefixWrap(ErrMessageNotMatched, "response From doesn't match transaction request")
	}

	reqTo, _ := reqHdrs.To()
	resTo, _ := resHdrs.To()
	if !compareNameAddrWithoutTag(header.NameAddr(*reqTo), header.NameAddr(*resTo)) {
		return errors.PrefixWrap(ErrMessageNotMatched, "response To doesn't match transaction request")
	}

	if reqTag, ok := reqTo.Tag(); ok && reqTag != "" {
		resTag, _ := resTo.Tag()
		if reqTag != resTag {
			return errors.PrefixWrap(ErrMessageNotMatched, "response To tag doesn't match transaction request")
		}
	}

	reqCSeq, _ := reqHdrs.CSeq()
	resCSeq, _ := resHdrs.CSeq()
	if !reqCSeq.Equal(resCSeq) {
		return errors.PrefixWrap(ErrMessageNotMatched, "response CSeq doesn't match transaction request")
	}

	return nil
}

func compareNameAddrWithoutTag(a, b header.NameAddr) bool {
	a = a.Clone()
	b = b.Clone()
	if a.Params != nil {
		a.Params.Delete("tag")
	}
	if b.Params != nil {
		b.Params.Delete("tag")
	}
	return a.Equal(b)
}

// RecvRequest is called on each inbound request received by the transport layer.
func (tx *serverTransact) RecvRequest(ctx context.Context, req *RequestEnvelope) error {
	if !tx.MatchMessage(req) {
		return errors.Wrap(ErrMessageNotMatched)
	}

	if v, ok := tx.impl.(interface {
		recvReq(ctx context.Context, req *RequestEnvelope) error
	}); ok {
		return errors.Wrap(v.recvReq(ctx, req))
	}
	return errors.Wrap(tx.recvReq(ctx, req))
}

func (tx *serverTransact) recvReq(ctx context.Context, req *RequestEnvelope) error {
	switch {
	case tx.req.Method().Equal(req.Method()):
		return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtRecvReq, req))
	default:
		return errors.Wrap(ErrMethodNotAllowed)
	}
}

// Respond sends a response to the remote address with specified options.
// Response will be passed to the transport layer by the transaction's FSM.
func (tx *serverTransact) Respond(ctx context.Context, sts ResponseStatus, opts ...RespondOptions) error {
	return errors.Wrap(Respond(ctx, tx.req, sts, tx, opts...))
}

// SendResponse sends a response to the remote address with specified options.
// Response will be passed to the transport layer by the transaction's FSM.
func (tx *serverTransact) SendResponse(
	ctx context.Context,
	res *ResponseEnvelope,
	opts ...SendResponseOptions,
) error {
	if err := tx.matchRes(res); err != nil {
		return errors.Wrap(err)
	}

	switch sts := res.Status(); {
	case sts.IsProvisional():
		return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtSend1xx, res, opts))
	case sts.IsSuccessful():
		return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtSend2xx, res, opts))
	default:
		return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtSend300699, res, opts))
	}
}

func (tx *serverTransact) sendRes(ctx context.Context, res *ResponseEnvelope, opts ...SendResponseOptions) error {
	if err := tx.tp.SendResponse(ctx, res, opts...); err != nil {
		if err := tx.fsm.FireCtx(ctx, txEvtTranspErr, errors.ErrorfWrap("send %q response: %w", res.Status(), err)); err != nil {
			panic(errors.Wrap(newTxTriggerErr(txEvtTranspErr, tx.State(), err)))
		}
		return errors.Wrap(err)
	}
	return nil
}

func (tx *serverTransact) Terminate(ctx context.Context, cause error) error {
	return errors.Wrap(tx.baseTransact.Terminate(ctx, errors.Wrap(cause)))
}

const (
	txEvtRecvReq    = "recv_req"
	txEvtSend1xx    = "send_1xx"
	txEvtSend2xx    = "send_2xx"
	txEvtSend300699 = "send_300-699"
)

func (tx *serverTransact) initFSM(start TransactionState) error {
	if err := tx.baseTransact.initFSM(start); err != nil {
		return errors.Wrap(err)
	}

	tx.fsm.SetTriggerParameters(txEvtRecvReq, reflect.TypeFor[*RequestEnvelope]())
	tx.fsm.SetTriggerParameters(txEvtSend1xx,
		reflect.TypeFor[*ResponseEnvelope](),
		reflect.TypeFor[[]SendResponseOptions](),
	)
	tx.fsm.SetTriggerParameters(txEvtSend2xx,
		reflect.TypeFor[*ResponseEnvelope](),
		reflect.TypeFor[[]SendResponseOptions](),
	)
	tx.fsm.SetTriggerParameters(txEvtSend300699,
		reflect.TypeFor[*ResponseEnvelope](),
		reflect.TypeFor[[]SendResponseOptions](),
	)

	return nil
}

func (tx *serverTransact) actSendRes(ctx context.Context, args ...any) error {
	res := args[0].(*ResponseEnvelope)                                                   //nolint:forcetypeassert
	opts := util.LastSliceElemOr(args[1].([]SendResponseOptions), SendResponseOptions{}) //nolint:forcetypeassert

	defer func() {
		tx.lastRes.Store(res)
		tx.sendOpts.Store(opts)
	}()

	tx.log.LogAttrs(ctx, slog.LevelDebug, "send response",
		slog.Any("transaction", tx.impl),
		slog.Any("response", res),
	)

	_ = tx.sendRes(ctx, res, opts)
	return nil
}

func (tx *serverTransact) actResendRes(ctx context.Context, _ ...any) error {
	res := tx.LastResponse()
	if res == nil {
		return nil
	}

	opts := tx.sendOpts.Load().(SendResponseOptions) //nolint:forcetypeassert

	tx.log.LogAttrs(ctx, slog.LevelDebug, "re-send response",
		slog.Any("transaction", tx.impl),
		slog.Any("response", res),
	)

	_ = tx.sendRes(ctx, res, opts)
	return nil
}

func (tx *serverTransact) actProceeding(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction proceeding", slog.Any("transaction", tx.impl))

	return nil
}

//nolint:unparam
func (tx *serverTransact) actCompleted(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction completed", slog.Any("transaction", tx.impl))

	return nil
}

// Snapshot returns a snapshot of the transaction state that can be serialized.
// The snapshot contains all the data needed to restore the transaction after a restart.
func (tx *serverTransact) Snapshot() *ServerTransactionSnapshot {
	return tx.srvTxImpl().takeSnapshot()
}

// MarshalJSON implements [json.Marshaler].
func (tx *serverTransact) MarshalJSON() ([]byte, error) {
	if tx == nil {
		return jsonNull, nil
	}
	return errors.Wrap2(json.Marshal(tx.Snapshot()))
}

// ServerTransactionSnapshot represents a snapshot of a server transaction state.
// It contains all the data needed to serialize and restore a transaction.
type ServerTransactionSnapshot struct {
	// Time is the snapshot timestamp.
	Time time.Time `json:"time"`
	// Type is the transaction type.
	Type TransactionType `json:"type"`
	// State is the current transaction state.
	State TransactionState `json:"state"`
	// Key is the transaction key.
	Key ServerTransactionKey `json:"key"`
	// Request is the request that created the transaction.
	Request *RequestEnvelope `json:"request"`
	// LastResponse is the last response sent by the transaction.
	LastResponse *ResponseEnvelope `json:"last_response,omitempty"`
	// SendOptions are the options used to send the last response.
	SendOptions SendResponseOptions `json:"send_options,omitzero"`
	// Timing are the timing configuration used to create the transaction.
	Timing TimingConfig `json:"timing,omitzero"`

	// Timer1xx is the 1xx provisional response timer (INVITE only).
	Timer1xx *timeutil.TimerSnapshot `json:"timer_1xx,omitempty"`
	// TimerG is the response retransmission timer (INVITE only, unreliable transport).
	TimerG *timeutil.TimerSnapshot `json:"timer_g,omitempty"`
	// TimerH is the timeout timer for waiting for ACK (INVITE only).
	TimerH *timeutil.TimerSnapshot `json:"timer_h,omitempty"`
	// TimerI is the wait timer after ACK received (INVITE only, unreliable transport).
	TimerI *timeutil.TimerSnapshot `json:"timer_i,omitempty"`
	// TimerL is the wait timer for 2xx response retransmission (INVITE only).
	TimerL *timeutil.TimerSnapshot `json:"timer_l,omitempty"`

	// TimerJ is the wait timer after final response sent (non-INVITE only).
	TimerJ *timeutil.TimerSnapshot `json:"timer_j,omitempty"`
}

func (snap *ServerTransactionSnapshot) IsValid() bool {
	return snap != nil &&
		snap.Type.IsValid() &&
		snap.State.IsValid() &&
		snap.Key.IsValid() &&
		snap.Request.IsValid() &&
		(snap.LastResponse == nil || snap.LastResponse.IsValid())
}
