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
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/log"
)

// ClientTransaction represents a SIP client transaction.
// RFC 3261 Section 17.1.
type ClientTransaction interface {
	Transaction
	ResponseReceiver
	// Start starts the client transaction.
	// It must be called exactly once after the transaction is registered.
	Start(ctx context.Context) error
	// Key returns the client transaction key.
	Key() ClientTransactionKey
	// Request returns the initial request that started this transaction.
	Request() *RequestEnvelope
	// LastResponse returns the last response received by the transaction.
	LastResponse() *ResponseEnvelope
	// Transport returns the transport used by the transaction.
	Transport() ClientTransport
	// BindResponseHandler binds the callback to be called when the transaction receives a response.
	// The callback can be unbound by calling the returned unbind function.
	BindResponseHandler(fn InboundResponseHandler) (unbind func())
}

type InboundResponseHandler interface {
	HandleInboundResponse(ctx context.Context, res *ResponseEnvelope)
}

type InboundResponseHandlerFunc func(ctx context.Context, res *ResponseEnvelope)

func (f InboundResponseHandlerFunc) HandleInboundResponse(ctx context.Context, res *ResponseEnvelope) {
	f(ctx, res)
}

// ClientTransport represents a SIP client transport used in the client transaction.
type ClientTransport interface {
	Metadata() TransportMetadata
	RequestSender
}

// ClientTransactionFactory creates initialized client transactions.
// It must not start transaction processing, send the request, or start transaction timers.
type ClientTransactionFactory interface {
	NewClientTransaction(
		ctx context.Context,
		req *RequestEnvelope,
		tp ClientTransport,
		opts ...ClientTransactionOptions,
	) (ClientTransaction, error)
}

// ClientTransactionFactoryFunc is a function that implements [ClientTransactionFactory].
type ClientTransactionFactoryFunc func(
	ctx context.Context,
	req *RequestEnvelope,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (ClientTransaction, error)

func (f ClientTransactionFactoryFunc) NewClientTransaction(
	ctx context.Context,
	req *RequestEnvelope,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (ClientTransaction, error) {
	return errors.Wrap2(f(ctx, req, tp, opts...))
}

// NewClientTransaction creates an initialized client transaction based on the request method.
// If the request method is INVITE, it creates an [InviteClientTransaction].
// Otherwise, it creates a [NonInviteClientTransaction].
func NewClientTransaction(
	ctx context.Context,
	req *RequestEnvelope,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (ClientTransaction, error) {
	if req.Method().Equal(RequestMethodInvite) {
		return errors.Wrap2(NewInviteClientTransaction(ctx, req, tp, opts...))
	}
	return errors.Wrap2(NewNonInviteClientTransaction(ctx, req, tp, opts...))
}

// ClientTransactionOptions contains options for a client transaction.
type ClientTransactionOptions struct {
	// Timing is the SIP timing config that will be used with the transaction.
	// If zero, [DefaultTimings] will be used.
	Timing TimingConfig
	// SendOptions are the options that will be used to send the requests.
	SendOptions SendRequestOptions
	// Logger is the logger that will be used with the transaction.
	// If nil, the [log.Default] will be used.
	Logger *slog.Logger
}

func (o ClientTransactionOptions) log() *slog.Logger {
	if o.Logger == nil {
		return log.Default()
	}
	return o.Logger
}

type clientTransact struct {
	*baseTransact
	key      ClientTransactionKey
	tp       ClientTransport
	timing   TimingConfig
	req      *RequestEnvelope
	sendOpts SendRequestOptions
	started  atomic.Bool

	onRes       types.CallbackManager[InboundResponseHandler]
	pendingRess types.Queue[pendingResponse]
	lastRes     atomic.Pointer[ResponseEnvelope]
}

type pendingResponse struct {
	ctx context.Context
	res *ResponseEnvelope
}

func newClientTransact(
	typ TransactionType,
	impl clientTransactImpl,
	req *RequestEnvelope,
	tp ClientTransport,
	opts ClientTransactionOptions,
) (*clientTransact, error) {
	req.WithMessage(func(r *Request) { EnsureRequestVia(r, "", Addr{}) })

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

	key, err := ClientTransactionKeyFromMessage(req)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	tx := &clientTransact{
		key:      key,
		tp:       tp,
		req:      req,
		sendOpts: opts.SendOptions,
		timing:   opts.Timing,
	}
	tx.baseTransact = newBaseTransact(typ, impl, opts.log())

	req.Metadata().
		Set(txKeyMetaKey, key).
		Set(txTypeMetaKey, tx.typ)

	return tx, nil
}

type clientTransactImpl interface {
	transactImpl
	ClientTransaction
	takeSnapshot() *ClientTransactionSnapshot
}

func (tx *clientTransact) clnTxImpl() clientTransactImpl {
	return tx.impl.(clientTransactImpl) //nolint:forcetypeassert
}

// Key returns the transaction key.
func (tx *clientTransact) Key() ClientTransactionKey { return tx.key }

func (tx *clientTransact) Request() *RequestEnvelope {
	return tx.req.Clone().(*RequestEnvelope) //nolint:forcetypeassert
}

// LastResponse returns the last response received by the transaction.
func (tx *clientTransact) LastResponse() *ResponseEnvelope {
	res := tx.lastRes.Load()
	if res == nil {
		return nil
	}
	return res.Clone().(*ResponseEnvelope) //nolint:forcetypeassert
}

// Transport returns the transport used by the transaction.
func (tx *clientTransact) Transport() ClientTransport { return tx.tp }

// MatchMessage checks whether the message matches the client transaction.
// It implements the matching rules defined in RFC 3261 Section 17.1.3.
func (tx *clientTransact) MatchMessage(msg Message) bool {
	key, err := ClientTransactionKeyFromMessage(msg)
	if err != nil {
		return false
	}
	return tx.key.Equal(key)
}

// RecvResponse is called on each inbound response received by the transport layer.
func (tx *clientTransact) RecvResponse(ctx context.Context, res *ResponseEnvelope) error {
	if !tx.MatchMessage(res) {
		return errors.Wrap(ErrMessageNotMatched)
	}

	switch {
	case res.Status().IsProvisional():
		return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtRecv1xx, res))
	case res.Status().IsSuccessful():
		return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtRecv2xx, res))
	default:
		return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtRecv300699, res))
	}
}

func (tx *clientTransact) sendReq(ctx context.Context, req *RequestEnvelope) error {
	if err := tx.tp.SendRequest(ctx, req, tx.sendOpts); err != nil {
		if err := tx.fsm.FireCtx(ctx, txEvtTranspErr, errors.ErrorfWrap("send %q request: %w", req.Method(), err)); err != nil {
			panic(errors.Wrap(newTxTriggerErr(txEvtTranspErr, tx.State(), err)))
		}
		return errors.Wrap(err)
	}
	return nil
}

func (tx *clientTransact) Terminate(ctx context.Context, cause error) error {
	return errors.Wrap(tx.baseTransact.Terminate(ctx, errors.Wrap(cause)))
}

const (
	txEvtRecv1xx    = "recv_1xx"
	txEvtRecv2xx    = "recv_2xx"
	txEvtRecv300699 = "recv_300-699"
)

func (tx *clientTransact) initFSM(start TransactionState) error {
	if err := tx.baseTransact.initFSM(start); err != nil {
		return errors.Wrap(err)
	}

	tx.fsm.SetTriggerParameters(txEvtRecv1xx, reflect.TypeFor[*ResponseEnvelope]())
	tx.fsm.SetTriggerParameters(txEvtRecv2xx, reflect.TypeFor[*ResponseEnvelope]())
	tx.fsm.SetTriggerParameters(txEvtRecv300699, reflect.TypeFor[*ResponseEnvelope]())
	return nil
}

func (tx *clientTransact) actSendReq(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "send request",
		slog.Any("transaction", tx.impl),
		slog.Any("request", tx.req),
	)

	_ = tx.sendReq(ctx, tx.req)
	return nil
}

func (tx *clientTransact) actPassRes(ctx context.Context, args ...any) error {
	res := args[0].(*ResponseEnvelope) //nolint:forcetypeassert
	tx.lastRes.Store(res)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "pass response",
		slog.Any("transaction", tx.impl),
		slog.Any("response", res),
	)

	tx.pendingRess.Push(pendingResponse{ctx, res})
	if tx.onRes.Len() > 0 {
		tx.deliverPendingRess()
	}
	return nil
}

func (tx *clientTransact) deliverPendingRess() {
	resps := tx.pendingRess.Drain()
	if len(resps) == 0 {
		return
	}

	for _, v := range resps {
		for fn := range tx.onRes.All() {
			fn.HandleInboundResponse(v.ctx, v.res)
		}
	}
}

func (tx *clientTransact) actProceeding(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction proceeding", slog.Any("transaction", tx))

	return nil
}

//nolint:unparam
func (tx *clientTransact) actCompleted(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction completed", slog.Any("transaction", tx))

	return nil
}

// BindResponseHandler binds the callback to be called when the transaction receives a response.
//
// The callback can be unbound by calling the returned cancel function.
// Multiple callbacks can be registered, they will be called in the order they were registered.
// Context passed to the callback is the context passed to [ClientTransport.RecvResponse].
func (tx *clientTransact) BindResponseHandler(fn InboundResponseHandler) (unbind func()) {
	defer tx.deliverPendingRess()
	return tx.onRes.Add(fn)
}

// Snapshot returns a snapshot of the transaction state that can be serialized.
// The snapshot contains all the data needed to restore the transaction after a restart.
func (tx *clientTransact) Snapshot() *ClientTransactionSnapshot {
	return tx.clnTxImpl().takeSnapshot()
}

// MarshalJSON implements [json.Marshaler].
func (tx *clientTransact) MarshalJSON() ([]byte, error) {
	if tx == nil {
		return jsonNull, nil
	}
	return errors.Wrap2(json.Marshal(tx.Snapshot()))
}

// ClientTransactionSnapshot represents a snapshot of a client transaction state.
// It contains all the data needed to serialize and restore a transaction.
type ClientTransactionSnapshot struct {
	// Time is the snapshot timestamp.
	Time time.Time `json:"time"`
	// Type is the transaction type.
	Type TransactionType `json:"type"`
	// State is the current transaction state.
	State TransactionState `json:"state"`
	// Key is the transaction key.
	Key ClientTransactionKey `json:"key"`
	// Request is the request that created the transaction.
	Request *RequestEnvelope `json:"request"`
	// SendOptions are the options used to send the request.
	SendOptions SendRequestOptions `json:"send_options,omitzero"`
	// LastResponse is the last response received by the transaction.
	LastResponse *ResponseEnvelope `json:"last_response,omitempty"`
	// Timing are the timing configuration used to create the transaction.
	Timing TimingConfig `json:"timing,omitzero"`

	// TimerA is the request retransmission timer (INVITE only).
	TimerA *timeutil.TimerSnapshot `json:"timer_a,omitempty"`
	// TimerB is the INVITE client transaction timeout (INVITE only).
	TimerB *timeutil.TimerSnapshot `json:"timer_b,omitempty"`
	// TimerD waits for final-response retransmits on unreliable transports (INVITE only).
	TimerD *timeutil.TimerSnapshot `json:"timer_d,omitempty"`
	// TimerM waits for 2xx retransmits before terminating an accepted INVITE (INVITE only).
	TimerM *timeutil.TimerSnapshot `json:"timer_m,omitempty"`

	// TimerE is the request retransmission timer (non-INVITE only).
	TimerE *timeutil.TimerSnapshot `json:"timer_e,omitempty"`
	// TimerF is the overall non-INVITE client transaction timeout (non-INVITE only).
	TimerF *timeutil.TimerSnapshot `json:"timer_f,omitempty"`
	// TimerK waits for final-response retransmits on unreliable transports (non-INVITE only).
	TimerK *timeutil.TimerSnapshot `json:"timer_k,omitempty"`
}

func (snap *ClientTransactionSnapshot) IsValid() bool {
	return snap != nil &&
		snap.Type.IsValid() &&
		snap.State.IsValid() &&
		snap.Key.IsValid() &&
		snap.Request.IsValid() &&
		(snap.LastResponse == nil || snap.LastResponse.IsValid())
}
