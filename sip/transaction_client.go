package sip

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"iter"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/timeutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip/header"
)

// ClientTransaction represents a SIP client transaction.
// RFC 3261 Section 17.1.
type ClientTransaction interface {
	Transaction
	ResponseReceiver
	// Key returns the client transaction key.
	Key() ClientTransactionKey
	// Request returns the initial request that started this transaction.
	Request() *OutboundRequestEnvelope
	// LastResponse returns the last response received by the transaction.
	LastResponse() *InboundResponseEnvelope
	// Transport returns the transport used by the transaction.
	Transport() ClientTransport
	// OnResponse binds the callback to be called when the transaction receives a response.
	// The callback can be unbound by calling the returned unbind function.
	OnResponse(fn InboundResponseHandler) (unbind func())
}

// ClientTransport represents a SIP client transport used in the client transaction.
type ClientTransport interface {
	RequestSender
	// Reliable returns whether the transport is reliable or not.
	Reliable() bool
}

// ClientTransactionFactory is a factory for creating client transactions.
type ClientTransactionFactory interface {
	NewClientTransaction(
		ctx context.Context,
		req *OutboundRequestEnvelope,
		tp ClientTransport,
		opts *ClientTransactionOptions,
	) (ClientTransaction, error)
}

// ClientTransactionFactoryFunc is a function that implements [ClientTransactionFactory].
type ClientTransactionFactoryFunc func(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	tp ClientTransport,
	opts *ClientTransactionOptions,
) (ClientTransaction, error)

func (f ClientTransactionFactoryFunc) NewClientTransaction(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	tp ClientTransport,
	opts *ClientTransactionOptions,
) (ClientTransaction, error) {
	return errors.Wrap2(f(ctx, req, tp, opts))
}

// NewClientTransaction creates a new client transaction based on the request method.
// If the request method is INVITE, it creates an [InviteClientTransaction].
// Otherwise, it creates a [NonInviteClientTransaction].
func NewClientTransaction(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	tp ClientTransport,
	opts *ClientTransactionOptions,
) (ClientTransaction, error) {
	if req.Method().Equal(RequestMethodInvite) {
		return errors.Wrap2(NewInviteClientTransaction(ctx, req, tp, opts))
	}
	return errors.Wrap2(NewNonInviteClientTransaction(ctx, req, tp, opts))
}

// ClientTransactionOptions contains options for a client transaction.
type ClientTransactionOptions struct {
	// Key is the client transaction key that will be used with the transaction.
	// If zero, the transaction will be created with the key automatically filled from the request.
	// Key should be unique for the transaction and match responses on the request that created the transaction.
	Key ClientTransactionKey
	// Timings is the SIP timing config that will be used with the transaction.
	// If zero, [DefaultTimings] will be used.
	Timings TimingConfig
	// SendOptions are the options that will be used to send the requests.
	SendOptions *SendRequestOptions
	// Logger is the logger that will be used with the transaction.
	// If nil, the [log.Default] will be used.
	Logger *slog.Logger
}

func (o *ClientTransactionOptions) key() ClientTransactionKey {
	if o == nil {
		return ClientTransactionKey{}
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
	if o == nil || o.Logger == nil {
		return log.Default()
	}
	return o.Logger
}

type clientTransact struct {
	*baseTransact
	key      ClientTransactionKey
	tp       ClientTransport
	timings  TimingConfig
	req      *OutboundRequestEnvelope
	sendOpts *SendRequestOptions
	lastRes  atomic.Pointer[InboundResponseEnvelope]

	onRes       types.CallbackManager[InboundResponseHandler]
	pendingRess types.Queue[pendingResponse]
}

type pendingResponse struct {
	ctx context.Context
	res *InboundResponseEnvelope
}

func newClientTransact(
	typ TransactionType,
	impl clientTransactImpl,
	req *OutboundRequestEnvelope,
	tp ClientTransport,
	opts *ClientTransactionOptions,
) (*clientTransact, error) {
	if err := req.Validate(); err != nil {
		return nil, errors.NewInvalidArgumentErrorWrap(err)
	}

	if tp == nil {
		return nil, errors.NewInvalidArgumentErrorWrap("nil transport")
	}

	req.AccessMessage(func(r *Request) {
		via, _ := r.Headers.FirstViaHop()
		if branch, ok := via.Branch(); !ok || branch == "" || !strings.HasPrefix(branch, MagicCookie) {
			if via.Params == nil {
				via.Params = make(Values)
			}

			via.Params.Set("branch", GenerateBranch(0))
		}
	})

	key := opts.key()
	if key.IsValid() {
		key = key.Canonic()
	} else {
		var err error
		if key, err = MakeClientTransactionKey(req); err != nil {
			return nil, errors.NewInvalidArgumentErrorWrap(err)
		}
	}

	req.Metadata().Set("transaction_key", key)

	tx := &clientTransact{
		key:      key,
		tp:       tp,
		req:      req,
		sendOpts: opts.sendOpts(),
		timings:  opts.timings(),
	}
	tx.baseTransact = newBaseTransact(typ, impl, opts.log())

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
		return ClientTransactionKey{}
	}
	return tx.key
}

func (tx *clientTransact) Request() *OutboundRequestEnvelope {
	if tx == nil {
		return nil
	}
	return tx.req
}

// LastResponse returns the last response received by the transaction.
func (tx *clientTransact) LastResponse() *InboundResponseEnvelope {
	if tx == nil {
		return nil
	}
	return tx.lastRes.Load()
}

// Transport returns the transport used by the transaction.
func (tx *clientTransact) Transport() ClientTransport {
	if tx == nil {
		return nil
	}
	return tx.tp
}

// MatchMessage checks whether the message matches the client transaction.
// It implements the matching rules defined in RFC 3261 Section 17.1.3.
func (tx *clientTransact) MatchMessage(msg Message) bool {
	key, err := MakeClientTransactionKey(msg)
	if err != nil {
		return false
	}

	return tx.key.Equal(key)
}

// RecvResponse is called on each inbound response received by the transport layer.
func (tx *clientTransact) RecvResponse(ctx context.Context, res *InboundResponseEnvelope) error {
	if !tx.MatchMessage(res) {
		return errors.Wrap(ErrMessageNotMatched)
	}

	ctx = ContextWithTransaction(ctx, tx.impl)

	switch {
	case res.Status().IsProvisional():
		return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtRecv1xx, res))
	case res.Status().IsSuccessful():
		return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtRecv2xx, res))
	default:
		return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtRecv300699, res))
	}
}

func (tx *clientTransact) sendReq(ctx context.Context, req *OutboundRequestEnvelope) error {
	if err := tx.tp.SendRequest(ctx, req, tx.sendOpts); err != nil {
		if err := tx.fsm.FireCtx(ctx, txEvtTranspErr, errors.ErrorfWrap("send %q request: %w", req.Method(), err)); err != nil {
			panic(errors.ErrorfWrap("fire %q in state %q: %w", txEvtTranspErr, tx.State(), err))
		}

		return errors.Wrap(err)
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
		return errors.Wrap(err)
	}

	tx.fsm.SetTriggerParameters(txEvtRecv1xx, reflect.TypeFor[*InboundResponseEnvelope]())
	tx.fsm.SetTriggerParameters(txEvtRecv2xx, reflect.TypeFor[*InboundResponseEnvelope]())
	tx.fsm.SetTriggerParameters(txEvtRecv300699, reflect.TypeFor[*InboundResponseEnvelope]())

	return nil
}

func (tx *clientTransact) actSendReq(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "send request",
		slog.Any("transaction", tx.impl),
		slog.Any("request", tx.req),
	)

	tx.sendReq(ctx, tx.req) //nolint:errcheck

	return nil
}

func (tx *clientTransact) actPassRes(ctx context.Context, args ...any) error {
	res := args[0].(*InboundResponseEnvelope) //nolint:forcetypeassert
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

	for fn := range tx.onRes.All() {
		for _, e := range resps {
			fn(e.ctx, e.res)
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

// OnResponse binds the callback to be called when the transaction receives a response.
//
// The callback can be unbound by calling the returned cancel function.
// Multiple callbacks can be registered, they will be called in the order they were registered.
// Context passed to the callback is the context passed to [ClientTransport.RecvResponse].
func (tx *clientTransact) OnResponse(fn InboundResponseHandler) (unbind func()) {
	defer tx.deliverPendingRess()
	return tx.onRes.Add(fn)
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
	Request *OutboundRequestEnvelope `json:"request"`
	// SendOptions are the options used to send the request.
	SendOptions *SendRequestOptions `json:"send_options,omitempty"`
	// LastResponse is the last response received by the transaction.
	LastResponse *InboundResponseEnvelope `json:"last_response,omitempty"`
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

// MakeClientTransactionKey creates a client transaction key from the given message.
func MakeClientTransactionKey(msg Message) (ClientTransactionKey, error) {
	if msg == nil {
		return ClientTransactionKey{}, errors.NewInvalidArgumentErrorWrap("nil message")
	}

	if err := msg.Validate(); err != nil {
		return ClientTransactionKey{}, errors.NewInvalidArgumentErrorWrap(err)
	}

	hdrs := GetMessageHeaders(msg)
	via, _ := hdrs.FirstViaHop()
	cseq, _ := hdrs.CSeq()

	var k ClientTransactionKey

	k.Branch, _ = via.Branch()
	k.Method = string(cseq.Method.ToUpper())

	return k, nil
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

func (k ClientTransactionKey) Canonic() ClientTransactionKey {
	k.Method = util.UCase(k.Method)
	return k
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

func (k ClientTransactionKey) AppendBinary(b []byte) ([]byte, error) {
	data, err := k.MarshalBinary()
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return append(b, data...), nil
}

func (k *ClientTransactionKey) UnmarshalBinary(data []byte) error {
	if k == nil {
		return errors.NewInvalidArgumentErrorWrap("nil transaction key")
	}

	if len(data) == 0 {
		*k = ClientTransactionKey{}
		return nil
	}

	key, ok := parseClientTransactKey(data)
	if !ok {
		*k = ClientTransactionKey{}
		return errors.NewInvalidArgumentErrorWrap("invalid transaction key payload")
	}

	*k = key

	return nil
}

func parseClientTransactKey(data []byte) (ClientTransactionKey, bool) {
	var (
		rest = data
		err  error
		key  ClientTransactionKey
	)
	if key.Branch, rest, err = util.ConsumePrefixedString(rest); err != nil {
		return ClientTransactionKey{}, false
	}

	if key.Method, rest, err = util.ConsumePrefixedString(rest); err != nil {
		return ClientTransactionKey{}, false
	}

	if len(rest) != 0 {
		return ClientTransactionKey{}, false
	}

	return key, true
}

func (k ClientTransactionKey) String() string {
	data, _ := k.MarshalBinary()
	return hex.EncodeToString(data)
}

func (k ClientTransactionKey) Format(f fmt.State, verb rune) {
	switch verb {
	case 's':
		f.Write([]byte(k.String())) //nolint:errcheck
		return
	case 'q':
		f.Write([]byte(strconv.Quote(k.String()))) //nolint:errcheck
		return
	default:
		if !f.Flag('+') && !f.Flag('#') {
			f.Write([]byte(k.String())) //nolint:errcheck
			return
		}

		type (
			hideMethods          ClientTransactionKey
			ClientTransactionKey hideMethods
		)

		fmt.Fprintf(f, fmt.FormatString(f, verb), ClientTransactionKey(k))

		return
	}
}

type ClientTransactionStore interface {
	Load(ctx context.Context, key ClientTransactionKey) (ClientTransaction, error)
	MatchMessage(ctx context.Context, msg Message) (ClientTransaction, error)
	Store(ctx context.Context, tx ClientTransaction) error
	Delete(ctx context.Context, tx ClientTransaction) error
	All(ctx context.Context) (iter.Seq[ClientTransaction], error)
}

type MemoryClientTransactionStore struct {
	// store for matching responses
	main *syncutil.ShardMap[ClientTransactionKey, ClientTransaction]
}

// NewMemoryClientTransactionStore creates a new in-memory client transaction store.
func NewMemoryClientTransactionStore() *MemoryClientTransactionStore {
	return &MemoryClientTransactionStore{
		main: syncutil.NewShardMap[ClientTransactionKey, ClientTransaction](),
	}
}

func (s *MemoryClientTransactionStore) Load(_ context.Context, key ClientTransactionKey) (ClientTransaction, error) {
	tx, ok := s.main.Load(key)
	if !ok {
		return nil, errors.Wrap(ErrTransactionNotFound)
	}

	return tx, nil
}

func (s *MemoryClientTransactionStore) MatchMessage(ctx context.Context, msg Message) (ClientTransaction, error) {
	key, err := MakeClientTransactionKey(msg)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	tx, err := s.Load(ctx, key)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	if !tx.MatchMessage(msg) {
		return nil, errors.Wrap(ErrTransactionNotFound)
	}

	return tx, nil
}

// Store stores a new one if it does not exist.
func (s *MemoryClientTransactionStore) Store(_ context.Context, tx ClientTransaction) error {
	s.main.Store(tx.Key(), tx)
	return nil
}

func (s *MemoryClientTransactionStore) Delete(_ context.Context, tx ClientTransaction) error {
	s.main.Delete(tx.Key())
	return nil
}

func (s *MemoryClientTransactionStore) All(_ context.Context) (iter.Seq[ClientTransaction], error) {
	return util.SeqValues(s.main.All()), nil
}

// InviteClientTransaction represents a SIP client transaction for INVITE requests.
// It implements the client transaction FSM defined in RFC 3261 section 17.1.1
// and patches from RFC 6026.
type InviteClientTransaction struct {
	*clientTransact

	tmrA atomic.Pointer[timeutil.SerializableTimer]
	tmrB atomic.Pointer[timeutil.SerializableTimer]
	tmrD atomic.Pointer[timeutil.SerializableTimer]
	tmrM atomic.Pointer[timeutil.SerializableTimer]

	ack atomic.Pointer[OutboundRequestEnvelope]
}

// NewInviteClientTransaction creates a new invite client transaction and starts its state machine.
//
// Context does not affect the transaction lifecycle, it is passed to the initial FSM actions and transitions.
// Request expected to be a valid SIP request with INVITE method.
// Transport expected to be a non-nil client transport.
// Options are optional and can be nil, in which case default options will be used.
func NewInviteClientTransaction(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	tp ClientTransport,
	opts *ClientTransactionOptions,
) (*InviteClientTransaction, error) {
	if err := req.Validate(); err != nil {
		return nil, errors.NewInvalidArgumentErrorWrap(err)
	}

	if !req.Method().Equal(RequestMethodInvite) {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrMethodNotAllowed)
	}

	tx := new(InviteClientTransaction)

	clnTx, err := newClientTransact(TransactionTypeClientInvite, tx, req, tp, opts)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	tx.clientTransact = clnTx

	ctx = ContextWithTransaction(ctx, tx)

	if err := tx.initFSM(TransactionStateCalling); err != nil {
		return nil, errors.Wrap(err)
	}

	if err := tx.actCalling(ctx); err != nil {
		_ = tx.Terminate(ctx)
		return nil, errors.Wrap(err)
	}

	return tx, nil
}

const (
	txEvtTimerA = "timer_a"
	txEvtTimerB = "timer_b"
	txEvtTimerD = "timer_d"
	txEvtTimerM = "timer_m"
)

func (tx *InviteClientTransaction) initFSM(start TransactionState) error {
	if err := tx.clientTransact.initFSM(start); err != nil {
		return errors.Wrap(err)
	}

	tx.fsm.Configure(TransactionStateCalling).
		InternalTransition(txEvtTimerA, tx.actSendReq).
		Permit(txEvtRecv1xx, TransactionStateProceeding).
		Permit(txEvtRecv2xx, TransactionStateAccepted).
		Permit(txEvtRecv300699, TransactionStateCompleted).
		Permit(txEvtTimerB, TransactionStateTerminated).
		Permit(txEvtTranspErr, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateProceeding).
		OnEntry(tx.actProceeding).
		OnEntryFrom(txEvtRecv1xx, tx.actPassRes).
		InternalTransition(txEvtRecv1xx, tx.actPassRes).
		Permit(txEvtRecv2xx, TransactionStateAccepted).
		Permit(txEvtRecv300699, TransactionStateCompleted).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateCompleted).
		OnEntry(tx.actCompleted).
		OnEntryFrom(txEvtRecv300699, tx.actPassResSendAck).
		InternalTransition(txEvtRecv300699, tx.actSendAck).
		Permit(txEvtTimerD, TransactionStateTerminated).
		Permit(txEvtTranspErr, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateAccepted).
		OnEntry(tx.actAccepted).
		OnEntryFrom(txEvtRecv2xx, tx.actPassRes).
		InternalTransition(txEvtRecv2xx, tx.actPassRes).
		Permit(txEvtTimerM, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateTerminated).
		OnEntry(tx.actTerminated).
		OnEntryFrom(txEvtTimerB, tx.actTimedOut).
		OnEntryFrom(txEvtTranspErr, tx.actTranspErr).
		InternalTransition(txEvtTerminate, tx.actNoop)

	return nil
}

func (tx *InviteClientTransaction) actPassResSendAck(ctx context.Context, args ...any) error {
	tx.actPassRes(ctx, args...) //nolint:errcheck
	tx.actSendAck(ctx, args...) //nolint:errcheck
	return nil
}

func (tx *InviteClientTransaction) actSendAck(ctx context.Context, _ ...any) error {
	ack := tx.ack.Load()
	if ack == nil {
		ack = tx.req.Clone().(*OutboundRequestEnvelope) //nolint:forcetypeassert
		ack.msg.Method = RequestMethodAck

		via, _ := ack.msg.Headers.FirstViaHop()
		ack.msg.Headers.Set(header.Via{*via})

		cseq, _ := ack.msg.Headers.CSeq()
		cseq.Method = RequestMethodAck

		to, _ := tx.LastResponse().Headers().To()
		ack.msg.Headers.Set(to)

		ack.msg.Headers.Set(header.MaxForwards(70))

		tx.ack.Store(ack)
	}

	tx.log.LogAttrs(ctx, slog.LevelDebug, "send request",
		slog.Any("transaction", tx.impl),
		slog.Any("request", ack),
	)

	tx.sendReq(ctx, ack) //nolint:errcheck

	return nil
}

func (tx *InviteClientTransaction) actCalling(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction calling", slog.Any("transaction", tx))

	if err := tx.sendReq(ctx, tx.req); err != nil {
		return errors.Wrap(err)
	}

	if !tx.tp.Reliable() {
		tmr := timeutil.AfterFunc(tx.timings.TimeA(), tx.timerAHdlr(ctx))
		tx.tmrA.Store(tmr)

		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	tmr := timeutil.AfterFunc(tx.timings.TimeB(), tx.timerBHdlr(ctx))
	tx.tmrB.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *InviteClientTransaction) timerAHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A expired", slog.Any("transaction", tx))

		if tx.State() != TransactionStateCalling {
			tx.tmrA.Store(nil)
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerA); err != nil {
			panic(errors.ErrorfWrap("fire %q in state %q: %w", txEvtTimerA, tx.State(), err))
		}

		if tmr := tx.tmrA.Load(); tmr != nil {
			tmr.Reset(2 * tmr.Duration())

			tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A reset",
				slog.Any("transaction", tx),
				slog.Time("expires_at", time.Now().Add(tmr.Left())),
			)
		}
	}
}

func (tx *InviteClientTransaction) timerBHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B expired", slog.Any("transaction", tx))

		tx.tmrB.Store(nil)

		if tx.State() != TransactionStateCalling {
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerB); err != nil {
			panic(errors.ErrorfWrap("fire %q in state %q: %w", txEvtTimerB, tx.State(), err))
		}
	}
}

func (tx *InviteClientTransaction) actProceeding(ctx context.Context, args ...any) error {
	tx.clientTransact.actProceeding(ctx, args...) //nolint:errcheck

	if tmr := tx.tmrA.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrB.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B stopped", slog.Any("transaction", tx))
	}

	return nil
}

func (tx *InviteClientTransaction) actCompleted(ctx context.Context, args ...any) error {
	tx.clientTransact.actCompleted(ctx, args...) //nolint:errcheck

	if tmr := tx.tmrA.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrB.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B stopped", slog.Any("transaction", tx))
	}

	tmr := timeutil.AfterFunc(tx.timings.TimeD(), tx.timerDHdlr(ctx))
	tx.tmrD.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer D started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *InviteClientTransaction) timerDHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer D expired", slog.Any("transaction", tx))

		tx.tmrD.Store(nil)

		if tx.State() != TransactionStateCompleted {
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerD); err != nil {
			panic(errors.ErrorfWrap("fire %q in state %q: %w", txEvtTimerD, tx.State(), err))
		}
	}
}

func (tx *InviteClientTransaction) actAccepted(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction accepted", slog.Any("transaction", tx))

	if tmr := tx.tmrA.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrB.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B stopped", slog.Any("transaction", tx))
	}

	tmr := timeutil.AfterFunc(tx.timings.TimeM(), tx.timerMHdlr(ctx))
	tx.tmrM.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer M started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *InviteClientTransaction) timerMHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer M expired", slog.Any("transaction", tx))

		tx.tmrM.Store(nil)

		if tx.State() != TransactionStateAccepted {
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerM); err != nil {
			panic(errors.ErrorfWrap("fire %q in state %q: %w", txEvtTimerM, tx.State(), err))
		}
	}
}

func (tx *InviteClientTransaction) actTerminated(ctx context.Context, args ...any) error {
	tx.clientTransact.actTerminated(ctx, args...) //nolint:errcheck

	if tmr := tx.tmrA.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer A stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrB.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer B stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrD.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer D stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrM.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer M stopped", slog.Any("transaction", tx))
	}

	return nil
}

func (tx *InviteClientTransaction) takeSnapshot() *ClientTransactionSnapshot {
	return &ClientTransactionSnapshot{
		Time:         time.Now(),
		Type:         tx.typ,
		State:        tx.State(),
		Key:          tx.key,
		Request:      tx.req,
		LastResponse: tx.LastResponse(),
		SendOptions:  cloneSendReqOpts(tx.sendOpts),
		Timings:      tx.timings,
		TimerA:       tx.tmrA.Load().Snapshot(),
		TimerB:       tx.tmrB.Load().Snapshot(),
		TimerD:       tx.tmrD.Load().Snapshot(),
		TimerM:       tx.tmrM.Load().Snapshot(),
	}
}

func RestoreInviteClientTransaction(
	ctx context.Context,
	snap *ClientTransactionSnapshot,
	tp ClientTransport,
	opts *ClientTransactionOptions,
) (*InviteClientTransaction, error) {
	if !snap.IsValid() || snap.Type != TransactionTypeClientInvite {
		return nil, errors.NewInvalidArgumentErrorWrap("invalid snapshot")
	}

	var restoreOpts ClientTransactionOptions
	if opts != nil {
		restoreOpts = *opts
	}

	restoreOpts.Key = snap.Key
	restoreOpts.SendOptions = cloneSendReqOpts(snap.SendOptions)
	restoreOpts.Timings = snap.Timings

	tx := new(InviteClientTransaction)

	clnTx, err := newClientTransact(TransactionTypeClientInvite, tx, snap.Request, tp, &restoreOpts)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	tx.clientTransact = clnTx

	ctx = ContextWithTransaction(ctx, tx)

	if snap.LastResponse != nil {
		tx.lastRes.Store(snap.LastResponse)
	}

	if err := tx.initFSM(snap.State); err != nil {
		return nil, errors.Wrap(err)
	}

	tx.restoreTimers(ctx, snap)

	return tx, nil
}

func (tx *InviteClientTransaction) restoreTimers(ctx context.Context, snap *ClientTransactionSnapshot) {
	if tmr := snap.TimerA; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerAHdlr(ctx))
		tx.tmrA.Store(restored)
	}

	if tmr := snap.TimerB; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerBHdlr(ctx))
		tx.tmrB.Store(restored)
	}

	if tmr := snap.TimerD; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerDHdlr(ctx))
		tx.tmrD.Store(restored)
	}

	if tmr := snap.TimerM; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerMHdlr(ctx))
		tx.tmrM.Store(restored)
	}
}

type NonInviteClientTransaction struct {
	*clientTransact

	tmrE atomic.Pointer[timeutil.SerializableTimer]
	tmrF atomic.Pointer[timeutil.SerializableTimer]
	tmrK atomic.Pointer[timeutil.SerializableTimer]
}

func NewNonInviteClientTransaction(
	ctx context.Context,
	req *OutboundRequestEnvelope,
	tp ClientTransport,
	opts *ClientTransactionOptions,
) (*NonInviteClientTransaction, error) {
	if err := req.Validate(); err != nil {
		return nil, errors.NewInvalidArgumentErrorWrap(err)
	}

	if mtd := req.Method(); mtd.Equal(RequestMethodInvite) || mtd.Equal(RequestMethodAck) {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrMethodNotAllowed)
	}

	tx := new(NonInviteClientTransaction)

	clnTx, err := newClientTransact(TransactionTypeClientNonInvite, tx, req, tp, opts)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	tx.clientTransact = clnTx

	ctx = ContextWithTransaction(ctx, tx)

	if err := tx.initFSM(TransactionStateTrying); err != nil {
		return nil, errors.Wrap(err)
	}

	if err := tx.actTrying(ctx); err != nil {
		_ = tx.Terminate(ctx)
		return nil, errors.Wrap(err)
	}

	return tx, nil
}

const (
	txEvtTimerE = "timer_e"
	txEvtTimerF = "timer_f"
	txEvtTimerK = "timer_k"
)

func (tx *NonInviteClientTransaction) initFSM(start TransactionState) error {
	if err := tx.clientTransact.initFSM(start); err != nil {
		return errors.Wrap(err)
	}

	tx.fsm.Configure(TransactionStateTrying).
		InternalTransition(txEvtTimerE, tx.actSendReq).
		Permit(txEvtRecv1xx, TransactionStateProceeding).
		Permit(txEvtRecv2xx, TransactionStateCompleted).
		Permit(txEvtRecv300699, TransactionStateCompleted).
		Permit(txEvtTimerF, TransactionStateTerminated).
		Permit(txEvtTranspErr, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateProceeding).
		OnEntry(tx.actProceeding).
		OnEntryFrom(txEvtRecv1xx, tx.actPassRes).
		InternalTransition(txEvtTimerE, tx.actSendReq).
		InternalTransition(txEvtRecv1xx, tx.actPassRes).
		Permit(txEvtRecv2xx, TransactionStateCompleted).
		Permit(txEvtRecv300699, TransactionStateCompleted).
		Permit(txEvtTimerF, TransactionStateTerminated).
		Permit(txEvtTranspErr, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateCompleted).
		OnEntry(tx.actCompleted).
		OnEntryFrom(txEvtRecv2xx, tx.actPassRes).
		OnEntryFrom(txEvtRecv300699, tx.actPassRes).
		Permit(txEvtTimerK, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateTerminated).
		OnEntry(tx.actTerminated).
		OnEntryFrom(txEvtTimerF, tx.actTimedOut).
		OnEntryFrom(txEvtTranspErr, tx.actTranspErr).
		InternalTransition(txEvtTerminate, tx.actNoop)

	return nil
}

func (tx *NonInviteClientTransaction) actTrying(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction trying", slog.Any("transaction", tx))

	if err := tx.sendReq(ctx, tx.req); err != nil {
		return errors.Wrap(err)
	}

	if !tx.tp.Reliable() {
		tmr := timeutil.AfterFunc(tx.timings.TimeE(), tx.timerEHdlr(ctx))
		tx.tmrE.Store(tmr)

		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer E started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	tmr := timeutil.AfterFunc(tx.timings.TimeF(), tx.timerFHdlr(ctx))
	tx.tmrF.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer F started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *NonInviteClientTransaction) timerEHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer E expired", slog.Any("transaction", tx))

		if tx.State() != TransactionStateTrying && tx.State() != TransactionStateProceeding {
			tx.tmrE.Store(nil)
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerE); err != nil {
			panic(errors.ErrorfWrap("fire %q in state %q: %w", txEvtTimerE, tx.State(), err))
		}

		if tmr := tx.tmrE.Load(); tmr != nil {
			var dur time.Duration
			if tx.State() == TransactionStateTrying {
				dur = min(2*tmr.Duration(), tx.timings.T2())
			} else {
				dur = tx.timings.T2()
			}

			tmr.Reset(dur)

			tx.log.LogAttrs(ctx, slog.LevelDebug, "timer E reset",
				slog.Any("transaction", tx),
				slog.Time("expires_at", time.Now().Add(tmr.Left())),
			)
		}
	}
}

func (tx *NonInviteClientTransaction) timerFHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer F expired", slog.Any("transaction", tx))

		tx.tmrF.Store(nil)

		if tx.State() != TransactionStateTrying && tx.State() != TransactionStateProceeding {
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerF); err != nil {
			panic(errors.ErrorfWrap("fire %q in state %q: %w", txEvtTimerF, tx.State(), err))
		}
	}
}

func (tx *NonInviteClientTransaction) actCompleted(ctx context.Context, args ...any) error {
	tx.clientTransact.actCompleted(ctx, args...) //nolint:errcheck

	if tmr := tx.tmrE.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer E stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrF.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer F stopped", slog.Any("transaction", tx))
	}

	tmr := timeutil.AfterFunc(tx.timings.TimeK(), tx.timerKHdlr(ctx))
	tx.tmrK.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer K started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *NonInviteClientTransaction) timerKHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer K expired", slog.Any("transaction", tx))

		tx.tmrK.Store(nil)

		if tx.State() != TransactionStateCompleted {
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerK); err != nil {
			panic(errors.ErrorfWrap("fire %q in state %q: %w", txEvtTimerK, tx.State(), err))
		}
	}
}

func (tx *NonInviteClientTransaction) actTerminated(ctx context.Context, args ...any) error {
	tx.clientTransact.actTerminated(ctx, args...) //nolint:errcheck

	if tmr := tx.tmrE.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer E stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrF.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer F stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrK.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer K stopped", slog.Any("transaction", tx))
	}

	return nil
}

func (tx *NonInviteClientTransaction) takeSnapshot() *ClientTransactionSnapshot {
	return &ClientTransactionSnapshot{
		Time:         time.Now(),
		Type:         tx.typ,
		State:        tx.State(),
		Key:          tx.key,
		Request:      tx.req,
		LastResponse: tx.LastResponse(),
		SendOptions:  cloneSendReqOpts(tx.sendOpts),
		Timings:      tx.timings,
		TimerE:       tx.tmrE.Load().Snapshot(),
		TimerF:       tx.tmrF.Load().Snapshot(),
		TimerK:       tx.tmrK.Load().Snapshot(),
	}
}

func RestoreNonInviteClientTransaction(
	ctx context.Context,
	snap *ClientTransactionSnapshot,
	tp ClientTransport,
	opts *ClientTransactionOptions,
) (*NonInviteClientTransaction, error) {
	if !snap.IsValid() || snap.Type != TransactionTypeClientNonInvite {
		return nil, errors.NewInvalidArgumentErrorWrap("invalid snapshot")
	}

	var restoreOpts ClientTransactionOptions
	if opts != nil {
		restoreOpts = *opts
	}

	restoreOpts.Key = snap.Key
	restoreOpts.SendOptions = cloneSendReqOpts(snap.SendOptions)
	restoreOpts.Timings = snap.Timings

	tx := new(NonInviteClientTransaction)

	clnTx, err := newClientTransact(TransactionTypeClientNonInvite, tx, snap.Request, tp, &restoreOpts)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	tx.clientTransact = clnTx

	ctx = ContextWithTransaction(ctx, tx)

	if snap.LastResponse != nil {
		tx.lastRes.Store(snap.LastResponse)
	}

	if err := tx.initFSM(snap.State); err != nil {
		return nil, errors.Wrap(err)
	}

	tx.restoreTimers(ctx, snap)

	return tx, nil
}

func (tx *NonInviteClientTransaction) restoreTimers(ctx context.Context, snap *ClientTransactionSnapshot) {
	if tmr := snap.TimerE; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerEHdlr(ctx))
		tx.tmrE.Store(restored)
	}

	if tmr := snap.TimerF; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerFHdlr(ctx))
		tx.tmrF.Store(restored)
	}

	if tmr := snap.TimerK; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerKHdlr(ctx))
		tx.tmrK.Store(restored)
	}
}
