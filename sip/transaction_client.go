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

	"github.com/ghettovoice/timeutil"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/pkg/log"
)

// ClientTransaction represents a SIP client transaction.
// RFC 3261 Section 17.1.
type ClientTransaction interface {
	Transaction
	ResponseReceiver
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
		req *RequestEnvelope,
		tp ClientTransport,
		opts ...ClientTransactionOptions,
	) (ClientTransaction, error)
}

// ClientTransactionFactoryFunc is a function that implements [ClientTransactionFactory].
type ClientTransactionFactoryFunc func(
	req *RequestEnvelope,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (ClientTransaction, error)

func (f ClientTransactionFactoryFunc) NewClientTransaction(
	req *RequestEnvelope,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (ClientTransaction, error) {
	return errors.Wrap2(f(req, tp, opts...))
}

// NewClientTransaction creates an initialized client transaction based on the request method.
// If the request method is INVITE, it creates an [InviteClientTransaction].
// Otherwise, it creates a [NonInviteClientTransaction].
func NewClientTransaction(
	req *RequestEnvelope,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (ClientTransaction, error) {
	if req.Method().Equal(RequestMethodInvite) {
		return errors.Wrap2(NewInviteClientTransaction(req, tp, opts...))
	}
	return errors.Wrap2(NewNonInviteClientTransaction(req, tp, opts...))
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

	onRes       types.CallbackManager[InboundResponseHandler]
	pendingRess types.Queue[pendingResponse]
	lastRes     atomic.Pointer[ResponseEnvelope]

	snapshot atomic.Pointer[ClientTransactionSnapshot]
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

	key, err := MakeClientTransactionKey(req)
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
	key, err := MakeClientTransactionKey(msg)
	if err != nil {
		return false
	}
	return tx.key.Equal(key)
}

// RecvResponse is called on each inbound response received by the transport layer.
// A matched response received while the transaction is still prepared waits for
// activation, termination, or context cancellation.
func (tx *clientTransact) RecvResponse(ctx context.Context, res *ResponseEnvelope) error {
	if !tx.MatchMessage(res) {
		return errors.Wrap(NewMessageNotMatched())
	}

	if err := tx.waitActive(ctx); err != nil {
		return errors.Wrap(err)
	}
	defer tx.saveSnapshot()

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
		err = errors.ErrorfWrap("send %q request: %w", req.Method(), err)
		if ferr := tx.fsm.FireCtx(ctx, txEvtTranspErr, err); ferr != nil &&
			!errors.Is(ferr, ErrTransactionActionNotAllowed) {
			panic(errors.Wrap(newTxTriggerErr(txEvtTranspErr, tx.State(), ferr)))
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
	tx.log.LogAttrs(
		ctx, slog.LevelDebug, "send request",
		slog.Any("transaction", tx.impl),
		slog.Any("request", tx.req),
	)

	_ = tx.sendReq(ctx, tx.req)
	return nil
}

func (tx *clientTransact) actPassRes(ctx context.Context, args ...any) error {
	res := args[0].(*ResponseEnvelope) //nolint:forcetypeassert
	tx.lastRes.Store(res)

	tx.log.LogAttrs(
		ctx, slog.LevelDebug, "pass response",
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

func (tx *clientTransact) saveSnapshotLocked() {
	tx.snapshot.Store(tx.clnTxImpl().takeSnapshot())
}

// Snapshot returns a snapshot of the transaction state that can be serialized.
// The snapshot contains all the data needed to restore the transaction after a restart.
// A transaction cannot be snapshotted before Start completes, except when restored as terminated.
func (tx *clientTransact) Snapshot() (*ClientTransactionSnapshot, error) {
	tx.lcMu.Lock()
	defer tx.lcMu.Unlock()

	state := tx.State()
	terminalRestore := tx.restored && state == TransactionStateTerminated
	if tx.starting || (!tx.started && !terminalRestore) ||
		(!tx.isActive() && state != TransactionStateTerminated) {
		return nil, errors.Wrap(NewTransactionActionNotAllowedError())
	}
	snap := tx.snapshot.Load()
	if snap == nil {
		return nil, errors.Wrap(NewTransactionActionNotAllowedError())
	}
	return snap.Clone(), nil
}

// MarshalJSON implements [json.Marshaler].
func (tx *clientTransact) MarshalJSON() ([]byte, error) {
	if tx == nil {
		return jsonNull, nil
	}
	snap, err := tx.Snapshot()
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return errors.Wrap2(json.Marshal(snap))
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

// Clone returns an independent copy of the snapshot.
func (snap *ClientTransactionSnapshot) Clone() *ClientTransactionSnapshot {
	if snap == nil {
		return nil
	}

	clone := *snap
	clone.Request = cloneReqEnvelope(snap.Request)
	clone.LastResponse = cloneResEnvelope(snap.LastResponse)
	clone.TimerA = cloneTmrSnapshot(snap.TimerA)
	clone.TimerB = cloneTmrSnapshot(snap.TimerB)
	clone.TimerD = cloneTmrSnapshot(snap.TimerD)
	clone.TimerM = cloneTmrSnapshot(snap.TimerM)
	clone.TimerE = cloneTmrSnapshot(snap.TimerE)
	clone.TimerF = cloneTmrSnapshot(snap.TimerF)
	clone.TimerK = cloneTmrSnapshot(snap.TimerK)
	return &clone
}

func (snap *ClientTransactionSnapshot) IsValid() bool {
	return snap != nil &&
		snap.Type.IsValid() &&
		snap.State.IsValid() &&
		snap.Key.IsValid() &&
		snap.Request.IsValid() &&
		(snap.LastResponse == nil || snap.LastResponse.IsValid())
}

func (snap *ClientTransactionSnapshot) validate(wantType TransactionType) error {
	if !snap.IsValid() || snap.Type != wantType {
		return errors.Wrap(NewInvalidTransactionSnapshotError())
	}

	mtd := snap.Request.Method()
	switch wantType {
	case TransactionTypeClientInvite:
		if !mtd.Equal(RequestMethodInvite) {
			return errors.Wrap(NewInvalidTransactionSnapshotError("request method is not INVITE"))
		}

		if !isTxStateIn(
			snap.State,
			TransactionStateCalling,
			TransactionStateProceeding,
			TransactionStateAccepted,
			TransactionStateCompleted,
			TransactionStateTerminated,
		) {
			return errors.Wrap(NewInvalidTransactionSnapshotError("unexpected state"))
		}
	case TransactionTypeClientNonInvite:
		if mtd.Equal(RequestMethodInvite) || mtd.Equal(RequestMethodAck) {
			return errors.Wrap(NewInvalidTransactionSnapshotError("unexpected request method"))
		}

		if !isTxStateIn(
			snap.State,
			TransactionStateTrying,
			TransactionStateProceeding,
			TransactionStateCompleted,
			TransactionStateTerminated,
		) {
			return errors.Wrap(NewInvalidTransactionSnapshotError("unexpected state"))
		}
	default:
		return errors.Wrap(NewInvalidTransactionSnapshotError())
	}

	key, err := MakeClientTransactionKey(snap.Request)
	if err != nil {
		return errors.Wrap(err)
	}
	if !snap.Key.Equal(key) {
		return errors.Wrap(NewInvalidTransactionSnapshotError("key mismatch"))
	}

	if res := snap.LastResponse; res != nil {
		if err := matchTxResHdrs(snap.Request, res, false); err != nil {
			return errors.Wrap(err)
		}
		if resKey, err := MakeClientTransactionKey(res); err != nil || !snap.Key.Equal(resKey) {
			return errors.Wrap(NewInvalidTransactionSnapshotError("response key mismatch"))
		}
	}
	if err := snap.validateLastRes(); err != nil {
		return errors.Wrap(err)
	}

	return errors.Wrap(snap.validateTimers())
}

func (snap *ClientTransactionSnapshot) validateLastRes() error {
	res := snap.LastResponse

	switch snap.State {
	case TransactionStateCalling, TransactionStateTrying:
		if res != nil {
			return errors.Wrap(NewInvalidTransactionSnapshotError("unexpected last response"))
		}
	case TransactionStateProceeding:
		if res == nil || !res.Status().IsProvisional() {
			return errors.Wrap(NewInvalidTransactionSnapshotError("provisional response required"))
		}
	case TransactionStateAccepted:
		if res == nil || !res.Status().IsSuccessful() {
			return errors.Wrap(NewInvalidTransactionSnapshotError("success response required"))
		}
	case TransactionStateCompleted:
		if res == nil || res.Status().IsProvisional() {
			return errors.Wrap(NewInvalidTransactionSnapshotError("final response required"))
		}
		if snap.Type == TransactionTypeClientInvite && res.Status().IsSuccessful() {
			return errors.Wrap(NewInvalidTransactionSnapshotError("non-2xx final response required"))
		}
	case TransactionStateConfirmed, TransactionStateTerminated:
	}

	return nil
}

func (snap *ClientTransactionSnapshot) validateTimers() error {
	type tmrSpec struct {
		snap    *timeutil.TimerSnapshot
		allowed []TransactionState
	}

	var specs []tmrSpec

	switch snap.Type {
	case TransactionTypeClientInvite:
		specs = []tmrSpec{
			{snap.TimerA, []TransactionState{TransactionStateCalling}},
			{snap.TimerB, []TransactionState{TransactionStateCalling}},
			{snap.TimerD, []TransactionState{TransactionStateCompleted}},
			{snap.TimerM, []TransactionState{TransactionStateAccepted}},
		}
	case TransactionTypeClientNonInvite:
		specs = []tmrSpec{
			{snap.TimerE, []TransactionState{TransactionStateTrying, TransactionStateProceeding}},
			{snap.TimerF, []TransactionState{TransactionStateTrying, TransactionStateProceeding}},
			{snap.TimerK, []TransactionState{TransactionStateCompleted}},
		}
	default:
		return errors.Wrap(NewInvalidTransactionSnapshotError("unexpected type"))
	}

	var foreign []*timeutil.TimerSnapshot
	if snap.Type == TransactionTypeClientInvite {
		foreign = []*timeutil.TimerSnapshot{snap.TimerE, snap.TimerF, snap.TimerK}
	} else {
		foreign = []*timeutil.TimerSnapshot{snap.TimerA, snap.TimerB, snap.TimerD, snap.TimerM}
	}

	for _, spec := range specs {
		if err := validateTxTmrSnap(spec.snap, snap.State, spec.allowed...); err != nil {
			return errors.Wrap(err)
		}
	}

	for _, tmr := range foreign {
		if tmr == nil {
			continue
		}
		if err := tmr.Validate(); err != nil {
			return errors.Wrap(err)
		}
		if tmr.State == timeutil.TimerStateRunning {
			return errors.Wrap(NewInvalidTransactionSnapshotError("unexpected active timer"))
		}
	}

	return nil
}

func RestoreClientTransaction(
	snap *ClientTransactionSnapshot,
	tp ClientTransport,
	opts ...ClientTransactionOptions,
) (ClientTransaction, error) {
	if snap == nil {
		return nil, errors.Wrap(NewInvalidTransactionSnapshotError())
	}

	switch snap.Type {
	case TransactionTypeClientInvite:
		return errors.Wrap2(RestoreInviteClientTransaction(snap, tp, opts...))
	case TransactionTypeClientNonInvite:
		return errors.Wrap2(RestoreNonInviteClientTransaction(snap, tp, opts...))
	default:
		return nil, errors.Wrap(NewInvalidTransactionSnapshotError())
	}
}

// ClientTransactionKey is the key of a client transaction.
// It is used for matching responses to the request that created the transaction.
type ClientTransactionKey struct {
	// Branch parameter of the topmost Via header field.
	Branch string `json:"branch"`
	// Method of the request that created the transaction.
	Method string `json:"method"`
}

// MakeClientTransactionKey creates a client transaction key from the given message.
func MakeClientTransactionKey(msg Message) (ClientTransactionKey, error) {
	if err := msg.Validate(); err != nil {
		return ClientTransactionKey{}, errors.Wrap(err)
	}

	hdrs, ok := GetMessageHeaders(msg)
	if !ok {
		return ClientTransactionKey{}, errors.Wrap(newUnexpectMsgTypeErr(msg))
	}

	var k ClientTransactionKey
	via, _ := hdrs.FirstVia()
	k.Branch, _ = via.Branch()
	if !IsRFC3261Branch(k.Branch) {
		return ClientTransactionKey{}, errors.Wrap(NewInvalidMessageError("invalid Via branch"))
	}

	cseq, _ := hdrs.CSeq()
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
	return IsRFC3261Branch(k.Branch) && k.Method != ""
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
	if !k.IsValid() {
		return nil, errors.ErrorWrap("invalid transaction key")
	}

	k = k.Canonic() //nolint:revive

	size := util.SizePrefixedString(k.Branch) +
		util.SizePrefixedString(k.Method)

	buf := make([]byte, 0, size)
	buf = util.AppendPrefixedString(buf, k.Branch)
	buf = util.AppendPrefixedString(buf, k.Method)
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
	if len(data) == 0 {
		*k = ClientTransactionKey{}
		return nil
	}

	key, ok := parseClnTxKey(data)
	if !ok {
		return errors.ErrorWrap("invalid transaction key payload")
	}

	*k = key
	return nil
}

func parseClnTxKey(data []byte) (ClientTransactionKey, bool) {
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
	data, err := k.MarshalBinary()
	if err != nil {
		return "invalid transaction key"
	}
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

		type (
			hideMethods          ClientTransactionKey
			ClientTransactionKey hideMethods
		)
		fmt.Fprintf(f, fmt.FormatString(f, verb), ClientTransactionKey(k))
		return
	}
}
