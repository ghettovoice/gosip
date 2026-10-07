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
	"github.com/ghettovoice/gosip/sip/header"
)

// ServerTransaction represents a SIP server transaction.
// RFC 3261 Section 17.2.
type ServerTransaction interface {
	Transaction
	RequestReceiver
	ResponseSender
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
		req *RequestEnvelope,
		tp ServerTransport,
		opts ...ServerTransactionOptions,
	) (ServerTransaction, error)
}

// ServerTransactionFactoryFunc is a function that implements [ServerTransactionFactory].
type ServerTransactionFactoryFunc func(
	req *RequestEnvelope,
	tp ServerTransport,
	opts ...ServerTransactionOptions,
) (ServerTransaction, error)

func (f ServerTransactionFactoryFunc) NewServerTransaction(
	req *RequestEnvelope,
	tp ServerTransport,
	opts ...ServerTransactionOptions,
) (ServerTransaction, error) {
	return errors.Wrap2(f(req, tp, opts...))
}

// NewServerTransaction creates a new server transaction based on the request method.
// If the request method is INVITE, it creates an [InviteServerTransaction].
// Otherwise, it creates a [NonInviteServerTransaction].
func NewServerTransaction(
	req *RequestEnvelope,
	tp ServerTransport,
	opts ...ServerTransactionOptions,
) (ServerTransaction, error) {
	if req.Method().Equal(RequestMethodInvite) {
		return errors.Wrap2(NewInviteServerTransaction(req, tp, opts...))
	}
	return errors.Wrap2(NewNonInviteServerTransaction(req, tp, opts...))
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
	snapshot atomic.Pointer[ServerTransactionSnapshot]
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

	key, err := MakeServerTransactionKey(req)
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
		reqKey, err := MakeServerTransactionKey(msg)
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
	return errors.Wrap(matchTxRes(tx.req, res))
}

func matchTxRes(req *RequestEnvelope, res Message) error {
	return errors.Wrap(matchTxResHdrs(req, res, true))
}

func matchTxResHdrs(req *RequestEnvelope, res Message, cmpVia bool) error {
	if err := res.Validate(); err != nil {
		return errors.Wrap(err)
	}

	reqHdrs := req.Headers()
	resHdrs, _ := GetMessageHeaders(res)

	if cmpVia {
		reqVia, _ := reqHdrs.FirstVia()
		resVia, _ := resHdrs.FirstVia()
		if !reqVia.Equal(resVia) {
			return errors.Wrap(NewMessageNotMatched("response Via doesn't match transaction request"))
		}
	}

	reqCallID, _ := reqHdrs.CallID()
	resCallID, _ := resHdrs.CallID()
	if !reqCallID.Equal(resCallID) {
		return errors.Wrap(NewMessageNotMatched("response Call-ID doesn't match transaction request"))
	}

	reqFrom, _ := reqHdrs.From()
	resFrom, _ := resHdrs.From()
	if !reqFrom.Equal(resFrom) {
		return errors.Wrap(NewMessageNotMatched("response From doesn't match transaction request"))
	}

	reqTo, _ := reqHdrs.To()
	resTo, _ := resHdrs.To()
	if !compareNameAddrWithoutTag(header.NameAddr(*reqTo), header.NameAddr(*resTo)) {
		return errors.Wrap(NewMessageNotMatched("response To doesn't match transaction request"))
	}

	if reqTag, ok := reqTo.Tag(); ok && reqTag != "" {
		resTag, _ := resTo.Tag()
		if reqTag != resTag {
			return errors.Wrap(NewMessageNotMatched("response To tag doesn't match transaction request"))
		}
	}

	reqCSeq, _ := reqHdrs.CSeq()
	resCSeq, _ := resHdrs.CSeq()
	if !reqCSeq.Equal(resCSeq) {
		return errors.Wrap(NewMessageNotMatched("response CSeq doesn't match transaction request"))
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
// A matched request received while the transaction is still prepared waits for
// activation, termination, or context cancellation.
func (tx *serverTransact) RecvRequest(ctx context.Context, req *RequestEnvelope) error {
	if !tx.MatchMessage(req) {
		return errors.Wrap(NewMessageNotMatched())
	}

	if err := tx.waitActive(ctx); err != nil {
		return errors.Wrap(err)
	}
	defer tx.saveSnapshot()

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
		return errors.Wrap(NewRequestMethodNotAllowedError())
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

	if !tx.isActive() {
		return errors.Wrap(NewTransactionActionNotAllowedError())
	}
	defer tx.saveSnapshot()

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
		err = errors.ErrorfWrap("send %q response: %w", res.Status(), err)
		if ferr := tx.fsm.FireCtx(ctx, txEvtTranspErr, err); ferr != nil &&
			!errors.Is(ferr, ErrTransactionActionNotAllowed) {
			panic(errors.Wrap(newTxTriggerErr(txEvtTranspErr, tx.State(), ferr)))
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
	tx.fsm.SetTriggerParameters(
		txEvtSend1xx,
		reflect.TypeFor[*ResponseEnvelope](),
		reflect.TypeFor[[]SendResponseOptions](),
	)
	tx.fsm.SetTriggerParameters(
		txEvtSend2xx,
		reflect.TypeFor[*ResponseEnvelope](),
		reflect.TypeFor[[]SendResponseOptions](),
	)
	tx.fsm.SetTriggerParameters(
		txEvtSend300699,
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

	tx.log.LogAttrs(
		ctx, slog.LevelDebug, "send response",
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

	tx.log.LogAttrs(
		ctx, slog.LevelDebug, "re-send response",
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

func (tx *serverTransact) saveSnapshotLocked() {
	tx.snapshot.Store(tx.srvTxImpl().takeSnapshot())
}

// Snapshot returns a snapshot of the transaction state that can be serialized.
// The snapshot contains all the data needed to restore the transaction after a restart.
// A transaction cannot be snapshotted before Start completes, except when restored as terminated.
func (tx *serverTransact) Snapshot() (*ServerTransactionSnapshot, error) {
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
func (tx *serverTransact) MarshalJSON() ([]byte, error) {
	if tx == nil {
		return jsonNull, nil
	}
	snap, err := tx.Snapshot()
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return errors.Wrap2(json.Marshal(snap))
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

// Clone returns an independent copy of the snapshot.
func (snap *ServerTransactionSnapshot) Clone() *ServerTransactionSnapshot {
	if snap == nil {
		return nil
	}

	clone := *snap
	clone.Request = cloneReqEnvelope(snap.Request)
	clone.LastResponse = cloneResEnvelope(snap.LastResponse)
	clone.Timer1xx = cloneTmrSnapshot(snap.Timer1xx)
	clone.TimerG = cloneTmrSnapshot(snap.TimerG)
	clone.TimerH = cloneTmrSnapshot(snap.TimerH)
	clone.TimerI = cloneTmrSnapshot(snap.TimerI)
	clone.TimerL = cloneTmrSnapshot(snap.TimerL)
	clone.TimerJ = cloneTmrSnapshot(snap.TimerJ)
	return &clone
}

func (snap *ServerTransactionSnapshot) IsValid() bool {
	return snap != nil &&
		snap.Type.IsValid() &&
		snap.State.IsValid() &&
		snap.Key.IsValid() &&
		snap.Request.IsValid() &&
		(snap.LastResponse == nil || snap.LastResponse.IsValid())
}

func (snap *ServerTransactionSnapshot) validate(wantType TransactionType) error {
	if !snap.IsValid() || snap.Type != wantType {
		return errors.Wrap(NewInvalidTransactionSnapshotError())
	}

	mtd := snap.Request.Method()
	switch wantType {
	case TransactionTypeServerInvite:
		if !mtd.Equal(RequestMethodInvite) {
			return errors.Wrap(NewInvalidTransactionSnapshotError("request method is not INVITE"))
		}

		if !isTxStateIn(
			snap.State,
			TransactionStateProceeding,
			TransactionStateAccepted,
			TransactionStateCompleted,
			TransactionStateConfirmed,
			TransactionStateTerminated,
		) {
			return errors.Wrap(NewInvalidTransactionSnapshotError("unexpected state"))
		}
	case TransactionTypeServerNonInvite:
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

	key, err := MakeServerTransactionKey(snap.Request)
	if err != nil {
		return errors.Wrap(err)
	}
	if !snap.Key.Equal(key) {
		return errors.Wrap(NewInvalidTransactionSnapshotError("key mismatch"))
	}

	if res := snap.LastResponse; res != nil {
		if err := matchTxRes(snap.Request, res); err != nil {
			return errors.Wrap(err)
		}
	}
	if err := snap.validateLastRes(); err != nil {
		return errors.Wrap(err)
	}

	return errors.Wrap(snap.validateTimers())
}

func (snap *ServerTransactionSnapshot) validateLastRes() error {
	res := snap.LastResponse

	switch snap.State {
	case TransactionStateTrying:
		if res != nil {
			return errors.Wrap(NewInvalidTransactionSnapshotError("unexpected last response"))
		}
	case TransactionStateProceeding:
		if res != nil && !res.Status().IsProvisional() {
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
		if snap.Type == TransactionTypeServerInvite && res.Status().IsSuccessful() {
			return errors.Wrap(NewInvalidTransactionSnapshotError("non-2xx final response required"))
		}
	case TransactionStateConfirmed:
		if res == nil || res.Status().IsProvisional() || res.Status().IsSuccessful() {
			return errors.Wrap(NewInvalidTransactionSnapshotError("non-2xx final response required"))
		}
	case TransactionStateCalling, TransactionStateTerminated:
	}

	return nil
}

func (snap *ServerTransactionSnapshot) validateTimers() error {
	type tmrSpec struct {
		snap    *timeutil.TimerSnapshot
		allowed []TransactionState
	}

	var specs []tmrSpec

	switch snap.Type {
	case TransactionTypeServerInvite:
		specs = []tmrSpec{
			{snap.Timer1xx, []TransactionState{TransactionStateProceeding}},
			{snap.TimerG, []TransactionState{TransactionStateCompleted}},
			{snap.TimerH, []TransactionState{TransactionStateCompleted}},
			{snap.TimerI, []TransactionState{TransactionStateConfirmed}},
			{snap.TimerL, []TransactionState{TransactionStateAccepted}},
		}
	case TransactionTypeServerNonInvite:
		specs = []tmrSpec{
			{snap.TimerJ, []TransactionState{TransactionStateCompleted}},
		}
	default:
		return errors.Wrap(NewInvalidTransactionSnapshotError("unexpected type"))
	}

	var foreign []*timeutil.TimerSnapshot
	if snap.Type == TransactionTypeServerInvite {
		foreign = []*timeutil.TimerSnapshot{snap.TimerJ}
	} else {
		foreign = []*timeutil.TimerSnapshot{snap.Timer1xx, snap.TimerG, snap.TimerH, snap.TimerI, snap.TimerL}
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

func RestoreServerTransaction(
	snap *ServerTransactionSnapshot,
	tp ServerTransport,
	opts ...ServerTransactionOptions,
) (ServerTransaction, error) {
	if snap == nil {
		return nil, errors.Wrap(NewInvalidTransactionSnapshotError())
	}

	switch snap.Type {
	case TransactionTypeServerInvite:
		return errors.Wrap2(RestoreInviteServerTransaction(snap, tp, opts...))
	case TransactionTypeServerNonInvite:
		return errors.Wrap2(RestoreNonInviteServerTransaction(snap, tp, opts...))
	default:
		return nil, errors.Wrap(NewInvalidTransactionSnapshotError())
	}
}

// ServerTransactionKey is a key used to identify a server transaction.
//
// The key implements the matching rules defined in RFC 3261 section 17.2.3.
// Branch, SentBy and Method are used for RFC 3261 transactions.
// Method, URI, FromTag, ToTag, CallID, CSeqNum and Via are used for RFC 2543 transactions.
type ServerTransactionKey struct {
	// Branch parameter of the topmost Via header field.
	// RFC 3261 transactions.
	Branch string `json:"branch,omitempty"`
	// Host and port of the topmost Via header field.
	// RFC 3261 transactions.
	SentBy string `json:"sent_by,omitempty"`
	// Method of the request that created the transaction.
	// RFC 3261/2543 transactions.
	Method string `json:"method,omitempty"`

	// Request-URI of the request that created the transaction.
	// RFC 2543 transactions.
	URI string `json:"uri,omitempty"`
	// Tag parameter of the From header field of the request that created the transaction.
	// RFC 2543 transactions.
	FromTag string `json:"from_tag,omitempty"`
	// Tag parameter of the To header field of the request that created the transaction.
	// RFC 2543 transactions.
	ToTag string `json:"to_tag,omitempty"`
	// Call-ID of the request that created the transaction.
	// RFC 2543 transactions.
	CallID string `json:"call_id,omitempty"`
	// SeqNum is the CSeq number of the request that created the transaction.
	// RFC 2543 transactions.
	SeqNum uint `json:"seq_num,omitempty"`
	// Topmost Via header field of the request that created the transaction.
	// RFC 2543 transactions.
	Via string `json:"via,omitempty"`
}

// MakeServerTransactionKey builds server transaction key from the given message.
func MakeServerTransactionKey(msg Message) (ServerTransactionKey, error) {
	if err := msg.Validate(); err != nil {
		return ServerTransactionKey{}, errors.Wrap(err)
	}

	hdrs, ok := GetMessageHeaders(msg)
	if !ok {
		return ServerTransactionKey{}, errors.Wrap(newUnexpectMsgTypeErr(msg))
	}

	via, _ := hdrs.FirstVia()
	if branch, ok := via.Branch(); ok && IsRFC3261Branch(branch) {
		return makeSrvTxKey3261(hdrs, via), nil
	}
	return errors.Wrap2(makeSrvTxKey2543(msg, hdrs, via))
}

func makeSrvTxKey3261(hdrs Headers, via *header.ViaHop) ServerTransactionKey {
	var k ServerTransactionKey
	k.Branch, _ = via.Branch()
	k.SentBy = util.LCase(via.Addr.String())

	cseq, _ := hdrs.CSeq()
	k.Method = string(cseq.Method.ToUpper())

	return k
}

func makeSrvTxKey2543(msg Message, hdrs Headers, via *header.ViaHop) (ServerTransactionKey, error) {
	var k ServerTransactionKey

	k.Via = util.LCase(via.String())

	callID, _ := hdrs.CallID()
	k.CallID = string(callID)

	switch m := msg.(type) {
	case *Request:
		k.URI = util.LCase(types.Render(m.URI))
	case interface{ URI() AnyURI }:
		k.URI = util.LCase(types.Render(m.URI()))
	}

	from, _ := hdrs.From()
	k.FromTag, _ = from.Tag()
	if k.FromTag == "" {
		return ServerTransactionKey{}, errors.Wrap(NewInvalidMessageError("missing From tag"))
	}

	to, _ := hdrs.To()
	k.ToTag, _ = to.Tag()

	cseq, _ := hdrs.CSeq()
	k.SeqNum = cseq.SeqNum
	k.Method = string(cseq.Method.ToUpper())

	return k, nil
}

// Equal checks whether the key is equal to another key.
func (k ServerTransactionKey) Equal(val any) bool {
	var other ServerTransactionKey
	switch v := val.(type) {
	case ServerTransactionKey:
		other = v
	case *ServerTransactionKey:
		if v == nil {
			return false
		}
		other = *v
	default:
		return false
	}

	if IsRFC3261Branch(k.Branch) {
		return k.Branch == other.Branch &&
			util.EqFold(k.SentBy, other.SentBy) &&
			util.EqFold(k.Method, other.Method)
	}

	return util.EqFold(k.Method, other.Method) &&
		util.EqFold(k.URI, other.URI) &&
		k.FromTag == other.FromTag &&
		k.ToTag == other.ToTag &&
		k.CallID == other.CallID &&
		k.SeqNum == other.SeqNum &&
		util.EqFold(k.Via, other.Via)
}

// IsValid checks whether the key is valid.
func (k ServerTransactionKey) IsValid() bool {
	if IsRFC3261Branch(k.Branch) {
		return k.SentBy != "" && k.Method != ""
	}
	return k.Method != "" &&
		k.URI != "" &&
		k.FromTag != "" &&
		k.CallID != "" &&
		k.SeqNum > 0 &&
		k.Via != ""
}

func (k ServerTransactionKey) IsZero() bool {
	return k.Branch == "" &&
		k.SentBy == "" &&
		k.Method == "" &&
		k.URI == "" &&
		k.FromTag == "" &&
		k.ToTag == "" &&
		k.CallID == "" &&
		k.SeqNum == 0 &&
		k.Via == ""
}

// LogValue returns a slog.Value for the key.
func (k ServerTransactionKey) LogValue() slog.Value {
	if IsRFC3261Branch(k.Branch) {
		return slog.GroupValue(
			slog.Any("branch", k.Branch),
			slog.Any("sent_by", k.SentBy),
			slog.Any("method", k.Method),
		)
	}

	return slog.GroupValue(
		slog.Any("method", k.Method),
		slog.Any("uri", k.URI),
		slog.Any("from_tag", k.FromTag),
		slog.Any("to_tag", k.ToTag),
		slog.Any("call_id", k.CallID),
		slog.Any("seq_num", k.SeqNum),
		slog.Any("via", k.Via),
	)
}

func (k ServerTransactionKey) Canonic() ServerTransactionKey {
	if IsRFC3261Branch(k.Branch) {
		k.SentBy = util.LCase(k.SentBy)
		k.Method = util.UCase(k.Method)
		return k
	}

	k.Method = util.UCase(k.Method)
	k.URI = util.LCase(k.URI)
	k.Via = util.LCase(k.Via)
	return k
}

const (
	srvTxKeyHash3261 byte = 1
	srvTxKeyHash2543 byte = 2
)

// MarshalBinary returns a canonical binary representation of the key that can be used as
// a stable hash.
//
// The representation keeps all significant fields for transaction matching and
// uses case-folded values for case-insensitive fields. The encoding is
// lossless for the canonical form, so a key can be reconstructed from the
// resulting bytes if needed.
func (k ServerTransactionKey) MarshalBinary() ([]byte, error) {
	if !k.IsValid() {
		return nil, errors.ErrorWrap("invalid transaction key")
	}

	if IsRFC3261Branch(k.Branch) {
		return k.marshal3261(), nil
	}
	return k.marshal2543(), nil
}

func (k ServerTransactionKey) AppendBinary(b []byte) ([]byte, error) {
	data, err := k.MarshalBinary()
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return append(b, data...), nil
}

func (k ServerTransactionKey) marshal3261() []byte {
	k = k.Canonic() //nolint:revive

	size := 1 +
		util.SizePrefixedString(k.Branch) +
		util.SizePrefixedString(k.SentBy) +
		util.SizePrefixedString(k.Method)

	buf := make([]byte, 0, size)
	buf = append(buf, srvTxKeyHash3261)
	buf = util.AppendPrefixedString(buf, k.Branch)
	buf = util.AppendPrefixedString(buf, k.SentBy)
	buf = util.AppendPrefixedString(buf, k.Method)
	return buf
}

func (k ServerTransactionKey) marshal2543() []byte {
	k = k.Canonic() //nolint:revive

	size := 1 +
		util.SizePrefixedString(k.URI) +
		util.SizePrefixedString(k.FromTag) +
		util.SizePrefixedString(k.ToTag) +
		util.SizePrefixedString(k.CallID) +
		util.SizeUVarInt(uint64(k.SeqNum)) +
		util.SizePrefixedString(k.Method) +
		util.SizePrefixedString(k.Via)

	buf := make([]byte, 0, size)
	buf = append(buf, srvTxKeyHash2543)
	buf = util.AppendPrefixedString(buf, k.URI)
	buf = util.AppendPrefixedString(buf, k.FromTag)
	buf = util.AppendPrefixedString(buf, k.ToTag)
	buf = util.AppendPrefixedString(buf, k.CallID)
	buf = util.AppendUVarInt(buf, uint64(k.SeqNum))
	buf = util.AppendPrefixedString(buf, k.Method)
	buf = util.AppendPrefixedString(buf, k.Via)
	return buf
}

// UnmarshalBinary populates the key fields from a binary representation
// produced by [ServerTransactionKey.MarshalBinary].
func (k *ServerTransactionKey) UnmarshalBinary(data []byte) error {
	if len(data) == 0 {
		*k = ServerTransactionKey{}
		return nil
	}

	key, ok := parseSrvTxKey(data)
	if !ok {
		return errors.ErrorWrap("invalid transaction key payload")
	}

	*k = key
	return nil
}

func parseSrvTxKey(data []byte) (ServerTransactionKey, bool) {
	var (
		rest = data[1:]
		err  error
		key  ServerTransactionKey
	)

	switch data[0] {
	case srvTxKeyHash3261:
		if key.Branch, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return ServerTransactionKey{}, false
		}

		if key.SentBy, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return ServerTransactionKey{}, false
		}

		if key.Method, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return ServerTransactionKey{}, false
		}
	case srvTxKeyHash2543:
		if key.URI, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return ServerTransactionKey{}, false
		}

		if key.FromTag, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return ServerTransactionKey{}, false
		}

		if key.ToTag, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return ServerTransactionKey{}, false
		}

		if key.CallID, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return ServerTransactionKey{}, false
		}

		var seqNum uint64
		if seqNum, rest, err = util.ConsumeUVarInt(rest); err != nil {
			return ServerTransactionKey{}, false
		}
		key.SeqNum = uint(seqNum)

		if key.Method, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return ServerTransactionKey{}, false
		}

		if key.Via, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return ServerTransactionKey{}, false
		}
	default:
		return ServerTransactionKey{}, false
	}

	if len(rest) != 0 {
		return ServerTransactionKey{}, false
	}
	return key, true
}

func (k ServerTransactionKey) String() string {
	data, err := k.MarshalBinary()
	if err != nil {
		return "invalid transaction key"
	}
	return hex.EncodeToString(data)
}

func (k ServerTransactionKey) Format(f fmt.State, verb rune) {
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
			hideMethods          ServerTransactionKey
			ServerTransactionKey hideMethods
		)
		fmt.Fprintf(f, fmt.FormatString(f, verb), ServerTransactionKey(k))
		return
	}
}
