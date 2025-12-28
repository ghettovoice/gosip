package sip

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"strconv"
	"sync/atomic"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/internal/timeutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/log"
)

// ClientTransaction represents a SIP client transaction.
type ClientTransaction interface {
	Transaction
	// MatchResponse checks whether the response matches the client transaction.
	MatchResponse(res *InboundResponse) error
	// RecvResponse is called on each inbound response received by the transport layer.
	RecvResponse(ctx context.Context, res *InboundResponse) error
	// OnResponse registers a callback to be called when the transaction receives a response.
	OnResponse(fn TransactionResponseHandler) (cancel func())
}

type TransactionResponseHandler = func(ctx context.Context, tx ClientTransaction, res *InboundResponse)

type ClientTransactionStore = TransactionStore[ClientTransactionKey, ClientTransaction]

func NewMemoryClientTransactionStore() ClientTransactionStore {
	return NewMemoryTransactionStore[ClientTransactionKey, ClientTransaction]()
}

type ClientTransactionFactory interface {
	NewClientTransaction(
		ctx context.Context,
		req *OutboundRequest,
		tp ClientTransport,
		opts *ClientTransactionOptions,
	) (ClientTransaction, error)
}

type StdClientTransactionFactory struct{}

var defClnTxFactory = &StdClientTransactionFactory{}

func DefaultClientTransactionFactory() *StdClientTransactionFactory { return defClnTxFactory }

func (*StdClientTransactionFactory) NewClientTransaction(
	ctx context.Context,
	req *OutboundRequest,
	tp ClientTransport,
	opts *ClientTransactionOptions,
) (ClientTransaction, error) {
	if req.Method().Equal(RequestMethodInvite) {
		return errtrace.Wrap2(NewInviteClientTransaction(req, tp, opts))
	}
	return errtrace.Wrap2(NewNonInviteClientTransaction(req, tp, opts))
}

const clnTransactCtxKey types.ContextKey = "client_transaction"

func ClientTransactionFromContext(ctx context.Context) (ClientTransaction, bool) {
	tx, ok := ctx.Value(clnTransactCtxKey).(ClientTransaction)
	return tx, ok
}

// ClientTransactionOptions contains options for a client transaction.
type ClientTransactionOptions struct {
	// Key is the client transaction key that will be used with the transaction.
	// If zero, the transaction will be created with the key automatically filled from the request.
	// Key should be unique for the transaction and match responses on the request that created the transaction.
	Key ClientTransactionKey
	// Timings is the SIP timing config that will be used with the transaction.
	// If zero, the default SIP timing config will be used.
	Timings TimingConfig
	// SendOptions are the options that will be used to send the requests.
	SendOptions *SendRequestOptions
	// Log is the logger that will be used with the transaction.
	// If nil, the [log.Default] will be used.
	Log *slog.Logger
}

func (o *ClientTransactionOptions) key() ClientTransactionKey {
	if o == nil {
		return zeroClnTxKey
	}
	return o.Key
}

func (o *ClientTransactionOptions) timings() TimingConfig {
	if o == nil {
		return defTimingCfg
	}
	return o.Timings
}

func (o *ClientTransactionOptions) sendOpts() *SendRequestOptions {
	if o == nil {
		return nil
	}
	return o.SendOptions
}

func (o *ClientTransactionOptions) log() *slog.Logger {
	if o == nil || o.Log == nil {
		return log.Default()
	}
	return o.Log
}

type clientTransact struct {
	*baseTransact
	key      ClientTransactionKey
	tp       ClientTransport
	timings  TimingConfig
	req      *OutboundRequest
	sendOpts *SendRequestOptions
	lastRes  atomic.Pointer[InboundResponse]

	onRes       types.CallbackManager[TransactionResponseHandler]
	pendingRess types.Deque[*InboundResponse]
}

func newClientTransact(
	typ TransactionType,
	impl clientTransactImpl,
	req *OutboundRequest,
	tp ClientTransport,
	opts *ClientTransactionOptions,
) (*clientTransact, error) {
	if err := req.Validate(); err != nil {
		return nil, errtrace.Wrap(NewInvalidArgumentError(err))
	}
	if tp == nil {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid transport"))
	}

	key := opts.key()
	if !key.IsValid() {
		if err := key.FillFromMessage(req); err != nil {
			return nil, errtrace.Wrap(NewInvalidArgumentError(err))
		}
	}

	tx := &clientTransact{
		key:      key,
		tp:       tp,
		req:      req,
		sendOpts: opts.sendOpts(),
		timings:  opts.timings(),
	}
	ctx := context.WithValue(context.Background(), clnTransactCtxKey, impl)
	tx.baseTransact = newBaseTransact(ctx, typ, impl, opts.log())
	return tx, nil
}

type clientTransactImpl interface {
	transactImpl
	takeSnapshot() *ClientTransactionSnapshot
}

func (tx *clientTransact) clnTxImpl() clientTransactImpl {
	return tx.impl.(clientTransactImpl) //nolint:forcetypeassert
}

// LogValue implements [slog.LogValuer].
func (tx *clientTransact) LogValue() slog.Value {
	if tx == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.Any("key", tx.key),
		slog.Any("type", tx.typ),
		slog.Any("state", tx.State()),
	)
}

// Key returns the transaction key.
func (tx *clientTransact) Key() ClientTransactionKey {
	if tx == nil {
		return zeroClnTxKey
	}
	return tx.key
}

func (tx *clientTransact) Request() *OutboundRequest {
	if tx == nil {
		return nil
	}
	return tx.req
}

// LastResponse returns the last response received by the transaction.
func (tx *clientTransact) LastResponse() *InboundResponse {
	if tx == nil {
		return nil
	}
	return tx.lastRes.Load()
}

// MatchResponse checks whether the response matches the client transaction.
// It implements the matching rules defined in RFC 3261 Section 17.1.3.
func (tx *clientTransact) MatchResponse(res *InboundResponse) error {
	var resKey ClientTransactionKey
	if err := resKey.FillFromMessage(res); err != nil {
		return errtrace.Wrap(NewInvalidArgumentError(err))
	}

	if !tx.key.Equal(resKey) {
		return errtrace.Wrap(ErrTransactionNotMatched)
	}
	return nil
}

// RecvResponse is called on each inbound response received by the transport layer.
func (tx *clientTransact) RecvResponse(ctx context.Context, res *InboundResponse) error {
	if err := tx.MatchResponse(res); err != nil {
		return errtrace.Wrap(err)
	}

	switch {
	case res.Status().IsProvisional():
		return errtrace.Wrap(tx.fsm.FireCtx(ctx, txEvtRecv1xx, res))
	case res.Status().IsSuccessful():
		return errtrace.Wrap(tx.fsm.FireCtx(ctx, txEvtRecv2xx, res))
	default:
		return errtrace.Wrap(tx.fsm.FireCtx(ctx, txEvtRecv300699, res))
	}
}

func (tx *clientTransact) sendReq(ctx context.Context, req *OutboundRequest) error {
	if err := tx.tp.SendRequest(ctx, req, tx.sendOpts); err != nil {
		err = fmt.Errorf("send %q request: %w", req.Method(), err)
		if err := tx.fsm.FireCtx(ctx, txEvtTranspErr, errtrace.Wrap(err)); err != nil {
			panic(fmt.Errorf("fire %q in state %q: %w", txEvtTranspErr, tx.State(), err))
		}
		return errtrace.Wrap(err)
	}
	return nil
}

const (
	txEvtRecv1xx    = "recv_1xx"
	txEvtRecv2xx    = "recv_2xx"
	txEvtRecv300699 = "recv_300-699"
)

func (tx *clientTransact) initFSM(start TransactionState) error {
	if err := tx.baseTransact.initFSM(start); err != nil {
		return errtrace.Wrap(err)
	}

	tx.fsm.SetTriggerParameters(txEvtRecv1xx, reflect.TypeOf((*InboundResponse)(nil)))
	tx.fsm.SetTriggerParameters(txEvtRecv2xx, reflect.TypeOf((*InboundResponse)(nil)))
	tx.fsm.SetTriggerParameters(txEvtRecv300699, reflect.TypeOf((*InboundResponse)(nil)))

	return nil
}

func (tx *clientTransact) actSendReq(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "send request", slog.Any("transaction", tx.impl), slog.Any("request", tx.req))

	tx.sendReq(ctx, tx.req) //nolint:errcheck
	return nil
}

func (tx *clientTransact) actPassRes(ctx context.Context, args ...any) error {
	res := args[0].(*InboundResponse) //nolint:forcetypeassert
	tx.lastRes.Store(res)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "pass response", slog.Any("transaction", tx.impl), slog.Any("response", res))

	tx.pendingRess.Append(res)
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

	tx.onRes.Range(func(fn TransactionResponseHandler) {
		for _, res := range resps {
			fn(tx.ctx, tx.impl.(ClientTransaction), res)
		}
	})
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

// OnResponse registers a callback to be called when the transaction receives a response.
//
// The callback will be called with the transaction's context, see [Transaction.Context].
// The transaction can be retrieved from the context using [TransactionFromContext].
//
// The callback can be canceled by calling the returned cancel function.
// Multiple callbacks can be registered.
func (tx *clientTransact) OnResponse(fn TransactionResponseHandler) (cancel func()) {
	cancel = tx.onRes.Add(fn)
	tx.deliverPendingRess()
	return cancel
}

// Snapshot returns a snapshot of the transaction state that can be serialized.
// The snapshot contains all the data needed to restore the transaction after a restart.
func (tx *clientTransact) Snapshot() *ClientTransactionSnapshot {
	if tx == nil {
		return nil
	}
	return tx.clnTxImpl().takeSnapshot()
}

// MarshalJSON implements [json.Marshaler].
func (tx *clientTransact) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(json.Marshal(tx.Snapshot()))
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
	Request *OutboundRequest `json:"request"`
	// SendOptions are the options used to send the request.
	SendOptions *SendRequestOptions `json:"send_options,omitempty"`
	// LastResponse is the last response received by the transaction.
	LastResponse *InboundResponse `json:"last_response,omitempty"`
	// Timings are the timing configuration used to create the transaction.
	Timings TimingConfig `json:"timing_config,omitzero"`

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
		snap.Type != "" &&
		snap.State != "" &&
		snap.Key.IsValid() &&
		snap.Request.IsValid() &&
		(snap.LastResponse == nil || snap.LastResponse.IsValid())
}

// ClientTransactionKey is the key of a client transaction.
// It is used for matching responses to the request that created the transaction.
//
//nolint:recvcheck
type ClientTransactionKey struct {
	// Branch parameter of the topmost Via header field.
	Branch string `json:"branch"`
	// Method of the request that created the transaction.
	Method string `json:"method"`
}

var zeroClnTxKey ClientTransactionKey

// FillFromMessage populates the key fields from the given message.
func (k *ClientTransactionKey) FillFromMessage(msg Message) error {
	if msg == nil {
		return errtrace.Wrap(NewInvalidArgumentError("invalid message"))
	}
	if err := msg.Validate(); err != nil {
		return errtrace.Wrap(NewInvalidArgumentError(err))
	}

	hdrs := GetMessageHeaders(msg)
	via, _ := hdrs.FirstVia()
	cseq, _ := hdrs.CSeq()

	k.Branch, _ = via.Branch()
	k.Method = util.UCase(string(cseq.Method))
	return nil
}

// Equal checks whether the key is equal to another key.
func (k ClientTransactionKey) Equal(val any) bool {
	var other ClientTransactionKey
	switch v := val.(type) {
	case ClientTransactionKey:
		other = v
	case *ClientTransactionKey:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}

	return k.Branch == other.Branch && util.EqFold(k.Method, other.Method)
}

// IsValid checks whether the key is valid.
func (k ClientTransactionKey) IsValid() bool {
	return k.Branch != "" && k.Method != ""
}

// IsZero checks whether the key is zero.
func (k ClientTransactionKey) IsZero() bool {
	return k.Branch == "" && k.Method == ""
}

// LogValue returns a [slog.Value] for the key.
func (k ClientTransactionKey) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Any("branch", k.Branch),
		slog.Any("method", k.Method),
	)
}

func (k ClientTransactionKey) MarshalBinary() ([]byte, error) {
	method := util.UCase(k.Method)

	size := util.SizePrefixedString(k.Branch) +
		util.SizePrefixedString(method)

	buf := make([]byte, 0, size)
	buf = util.AppendPrefixedString(buf, k.Branch)
	buf = util.AppendPrefixedString(buf, method)
	return buf, nil
}

func (k *ClientTransactionKey) UnmarshalBinary(data []byte) error {
	if len(data) == 0 {
		return errtrace.Wrap(NewInvalidArgumentError("invalid data"))
	}

	var (
		rest = data
		err  error
		key  ClientTransactionKey
	)
	if key.Branch, rest, err = util.ConsumePrefixedString(rest); err != nil {
		return errtrace.Wrap(err)
	}
	if key.Method, rest, err = util.ConsumePrefixedString(rest); err != nil {
		return errtrace.Wrap(err)
	}

	if len(rest) != 0 {
		return errtrace.Wrap(NewInvalidArgumentError("unexpected trailing data"))
	}

	*k = key
	return nil
}

func (k ClientTransactionKey) String() string {
	data, _ := k.MarshalBinary()
	return hex.EncodeToString(data)
}

func (k ClientTransactionKey) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		f.Write([]byte(k.String()))
		return
	case 'q':
		f.Write([]byte(strconv.Quote(k.String())))
		return
	default:
		if !f.Flag('+') && !f.Flag('#') {
			f.Write([]byte(k.String()))
			return
		}

		type hideMethods ClientTransactionKey
		type ClientTransactionKey hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), ClientTransactionKey(k))
		return
	}
}

func GetClientTransactionKey(tx ClientTransaction) (ClientTransactionKey, bool) {
	if v, ok := tx.(interface{ Key() ClientTransactionKey }); ok {
		return v.Key(), true
	}
	return zeroClnTxKey, false
}
