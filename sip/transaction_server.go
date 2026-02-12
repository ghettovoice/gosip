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
	"sync/atomic"
	"time"

	"braces.dev/errtrace"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/timeutil"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/log"
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
	Request() *InboundRequestEnvelope
	// LastResponse returns the last response sent by the transaction.
	LastResponse() *OutboundResponseEnvelope
	// Transport returns the transport used by the transaction.
	Transport() ServerTransport
}

// ServerTransport represents a SIP server transport used in the server transaction.
type ServerTransport interface {
	ResponseSender
	// Reliable returns whether the transport is reliable or not.
	Reliable() bool
}

// ServerTransactionFactory is a factory for creating server transactions.
type ServerTransactionFactory interface {
	NewServerTransaction(
		ctx context.Context,
		req *InboundRequestEnvelope,
		tp ServerTransport,
		opts *ServerTransactionOptions,
	) (ServerTransaction, error)
}

// ServerTransactionFactoryFunc is a function that implements [ServerTransactionFactory].
type ServerTransactionFactoryFunc func(
	ctx context.Context,
	req *InboundRequestEnvelope,
	tp ServerTransport,
	opts *ServerTransactionOptions,
) (ServerTransaction, error)

func (f ServerTransactionFactoryFunc) NewServerTransaction(
	ctx context.Context,
	req *InboundRequestEnvelope,
	tp ServerTransport,
	opts *ServerTransactionOptions,
) (ServerTransaction, error) {
	return errtrace.Wrap2(f(ctx, req, tp, opts))
}

// NewServerTransaction creates a new server transaction based on the request method.
// If the request method is INVITE, it creates an [InviteServerTransaction].
// Otherwise, it creates a [NonInviteServerTransaction].
func NewServerTransaction(
	ctx context.Context,
	req *InboundRequestEnvelope,
	tp ServerTransport,
	opts *ServerTransactionOptions,
) (ServerTransaction, error) {
	if req.Method().Equal(RequestMethodInvite) {
		return errtrace.Wrap2(NewInviteServerTransaction(ctx, req, tp, opts))
	}
	return errtrace.Wrap2(NewNonInviteServerTransaction(ctx, req, tp, opts))
}

// ServerTransactionOptions contains options for a server transaction.
type ServerTransactionOptions struct {
	// Key is the server transaction key that will be used with the transaction.
	// If zero, the transaction will be created with the key automatically filled from the request.
	// Key should be unique for the transaction and match the request that created the transaction.
	Key ServerTransactionKey
	// Timings is the SIP timing config that will be used with the transaction.
	// If zero, the default SIP timing config will be used.
	Timings TimingConfig
	// Logger is the logger that will be used with the transaction.
	// If nil, the [log.Default] will be used.
	Logger *slog.Logger
}

func (o *ServerTransactionOptions) key() ServerTransactionKey {
	if o == nil {
		return zeroSrvTxKey
	}
	return o.Key
}

func (o *ServerTransactionOptions) timings() TimingConfig {
	if o == nil {
		return defTimingCfg
	}
	return o.Timings
}

func (o *ServerTransactionOptions) log() *slog.Logger {
	if o == nil || o.Logger == nil {
		return log.Default()
	}
	return o.Logger
}

type serverTransact struct {
	*baseTransact
	key      ServerTransactionKey
	tp       ServerTransport
	timings  TimingConfig
	req      *InboundRequestEnvelope
	lastRes  atomic.Pointer[OutboundResponseEnvelope]
	sendOpts atomic.Pointer[SendResponseOptions]
}

func newServerTransact(
	typ TransactionType,
	impl serverTransactImpl,
	req *InboundRequestEnvelope,
	tp ServerTransport,
	opts *ServerTransactionOptions,
) (*serverTransact, error) {
	if err := req.Validate(); err != nil {
		return nil, errtrace.Wrap(NewInvalidArgumentError(err))
	}
	if tp == nil {
		return nil, errtrace.Wrap(NewInvalidArgumentError("invalid transport"))
	}
	if opts == nil {
		opts = &ServerTransactionOptions{}
	}

	key := opts.key()
	if !key.IsValid() {
		var err error
		if key, err = MakeServerTransactionKey(req); err != nil {
			return nil, errtrace.Wrap(NewInvalidArgumentError(err))
		}
	}
	req.Metadata().Set("transaction_key", key)

	tx := &serverTransact{
		key:     key,
		tp:      tp,
		timings: opts.timings(),
		req:     req,
	}
	tx.baseTransact = newBaseTransact(typ, impl, opts.log())
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

// LogValue implements [slog.LogValuer].
func (tx *serverTransact) LogValue() slog.Value {
	if tx == nil {
		return zeroSlogValue
	}
	return slog.GroupValue(
		slog.Any("key", tx.key),
		slog.Any("type", tx.typ),
		slog.Any("state", tx.State()),
	)
}

// Key returns the transaction key.
func (tx *serverTransact) Key() ServerTransactionKey {
	if tx == nil {
		return zeroSrvTxKey
	}
	return tx.key
}

// Request returns the initial request that started this transaction.
func (tx *serverTransact) Request() *InboundRequestEnvelope {
	if tx == nil {
		return nil
	}
	return tx.req
}

// LastResponse returns the last response sent by the transaction.
func (tx *serverTransact) LastResponse() *OutboundResponseEnvelope {
	if tx == nil {
		return nil
	}
	return tx.lastRes.Load()
}

// Transport returns the transport used by the transaction.
func (tx *serverTransact) Transport() ServerTransport {
	if tx == nil {
		return nil
	}
	return tx.tp
}

// MatchMessage checks whether the message matches the server transaction.
// It implements the matching rules defined in RFC 3261 section 17.2.3.
func (tx *serverTransact) MatchMessage(msg Message) bool {
	var (
		isReq, isRes bool
		mtd          RequestMethod
	)
	switch m := msg.(type) {
	case *InboundRequestEnvelope:
		isReq = true
		mtd = m.Method()
	case *Request:
		isRes = true
		mtd = m.Method
	case *OutboundResponseEnvelope:
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

//nolint:gocognit
func (tx *serverTransact) matchRes(res Message) error {
	if tx == nil {
		return errtrace.Wrap(NewInvalidArgumentError("invalid transaction"))
	}
	if tx.req == nil {
		return errtrace.Wrap(NewInvalidArgumentError("missing transaction request"))
	}
	if res == nil {
		return errtrace.Wrap(NewInvalidArgumentError("invalid response"))
	}

	reqHdrs := tx.req.Headers()
	resHdrs := GetMessageHeaders(res)

	reqVia, ok := reqHdrs.FirstVia()
	if !ok || reqVia == nil {
		return errtrace.Wrap(NewInvalidArgumentError("missing request Via"))
	}
	resVia, ok := resHdrs.FirstVia()
	if !ok || resVia == nil {
		return errtrace.Wrap(NewInvalidArgumentError("missing response Via"))
	}
	if !reqVia.Equal(resVia) {
		return errtrace.Wrap(NewInvalidArgumentError("response Via does not match transaction request"))
	}

	reqCallID, ok := reqHdrs.CallID()
	if !ok {
		return errtrace.Wrap(NewInvalidArgumentError("missing request Call-ID"))
	}
	resCallID, ok := resHdrs.CallID()
	if !ok {
		return errtrace.Wrap(NewInvalidArgumentError("missing response Call-ID"))
	}
	if reqCallID != resCallID {
		return errtrace.Wrap(NewInvalidArgumentError("response Call-ID does not match transaction request"))
	}

	reqFrom, ok := reqHdrs.From()
	if !ok || reqFrom == nil {
		return errtrace.Wrap(NewInvalidArgumentError("missing request From"))
	}
	resFrom, ok := resHdrs.From()
	if !ok || resFrom == nil {
		return errtrace.Wrap(NewInvalidArgumentError("missing response From"))
	}
	if !reqFrom.Equal(resFrom) {
		return errtrace.Wrap(NewInvalidArgumentError("response From does not match transaction request"))
	}

	reqTo, ok := reqHdrs.To()
	if !ok || reqTo == nil {
		return errtrace.Wrap(NewInvalidArgumentError("missing request To"))
	}
	resTo, ok := resHdrs.To()
	if !ok || resTo == nil {
		return errtrace.Wrap(NewInvalidArgumentError("missing response To"))
	}
	if !equalNameAddrWithoutTag(header.NameAddr(*reqTo), header.NameAddr(*resTo)) {
		return errtrace.Wrap(NewInvalidArgumentError("response To does not match transaction request"))
	}
	if reqTag, ok := reqTo.Tag(); ok && reqTag != "" {
		resTag, _ := resTo.Tag()
		if reqTag != resTag {
			return errtrace.Wrap(NewInvalidArgumentError("response To tag does not match transaction request"))
		}
	}

	reqCSeq, ok := reqHdrs.CSeq()
	if !ok || reqCSeq == nil {
		return errtrace.Wrap(NewInvalidArgumentError("missing request CSeq"))
	}
	resCSeq, ok := resHdrs.CSeq()
	if !ok || resCSeq == nil {
		return errtrace.Wrap(NewInvalidArgumentError("missing response CSeq"))
	}
	if reqCSeq.SeqNum != resCSeq.SeqNum {
		return errtrace.Wrap(NewInvalidArgumentError("response CSeq number does not match transaction request"))
	}
	if !resCSeq.Method.Equal(reqCSeq.Method) {
		return errtrace.Wrap(NewInvalidArgumentError("response CSeq method does not match transaction request"))
	}
	return nil
}

func equalNameAddrWithoutTag(a, b header.NameAddr) bool {
	a = a.Clone()
	b = b.Clone()
	if a.Params != nil {
		a.Params.Del("tag")
	}
	if b.Params != nil {
		b.Params.Del("tag")
	}
	return a.Equal(b)
}

// RecvRequest is called on each inbound request received by the transport layer.
func (tx *serverTransact) RecvRequest(ctx context.Context, req *InboundRequestEnvelope) error {
	if !tx.MatchMessage(req) {
		return errtrace.Wrap(NewInvalidArgumentError(ErrMessageNotMatched))
	}

	ctx = ContextWithTransaction(ctx, tx.impl)

	if v, ok := tx.impl.(interface {
		recvReq(ctx context.Context, req *InboundRequestEnvelope) error
	}); ok {
		return errtrace.Wrap(v.recvReq(ctx, req))
	}
	return errtrace.Wrap(tx.recvReq(ctx, req))
}

func (tx *serverTransact) recvReq(ctx context.Context, req *InboundRequestEnvelope) error {
	switch {
	case tx.req.Method().Equal(req.Method()):
		return errtrace.Wrap(tx.fsm.FireCtx(ctx, txEvtRecvReq, req))
	default:
		return errtrace.Wrap(NewInvalidArgumentError(ErrMethodNotAllowed))
	}
}

// Respond sends a response to the remote address with specified options.
// Response will be passed to the transport layer by the transaction's FSM.
func (tx *serverTransact) Respond(ctx context.Context, sts ResponseStatus, opts *RespondOptions) error {
	res, err := tx.req.NewResponse(sts, opts.resOpts())
	if err != nil {
		return errtrace.Wrap(err)
	}
	return errtrace.Wrap(tx.SendResponse(ctx, res, opts.sendOpts()))
}

// SendResponse sends a response to the remote address with specified options.
// Response will be passed to the transport layer by the transaction's FSM.
func (tx *serverTransact) SendResponse(
	ctx context.Context,
	res *OutboundResponseEnvelope,
	opts *SendResponseOptions,
) error {
	if err := res.Validate(); err != nil {
		return errtrace.Wrap(err)
	}
	if err := tx.matchRes(res); err != nil {
		return errtrace.Wrap(err)
	}

	ctx = ContextWithTransaction(ctx, tx.impl)

	switch sts := res.Status(); {
	case sts.IsProvisional():
		return errtrace.Wrap(tx.fsm.FireCtx(ctx, txEvtSend1xx, res, opts))
	case sts.IsSuccessful():
		return errtrace.Wrap(tx.fsm.FireCtx(ctx, txEvtSend2xx, res, opts))
	default:
		return errtrace.Wrap(tx.fsm.FireCtx(ctx, txEvtSend300699, res, opts))
	}
}

func (tx *serverTransact) sendRes(
	ctx context.Context,
	res *OutboundResponseEnvelope,
	opts *SendResponseOptions,
) error {
	if err := tx.tp.SendResponse(ctx, res, opts); err != nil {
		err = fmt.Errorf("send %q response: %w", res.Status(), err)
		if err := tx.fsm.FireCtx(ctx, txEvtTranspErr, errtrace.Wrap(err)); err != nil {
			panic(fmt.Errorf("fire %q in state %q: %w", txEvtTranspErr, tx.State(), err))
		}
		return errtrace.Wrap(err)
	}
	return nil
}

const (
	txEvtRecvReq    = "recv_req"
	txEvtSend1xx    = "send_1xx"
	txEvtSend2xx    = "send_2xx"
	txEvtSend300699 = "send_300-699"
)

func (tx *serverTransact) initFSM(start TransactionState) error {
	if err := tx.baseTransact.initFSM(start); err != nil {
		return errtrace.Wrap(err)
	}

	tx.fsm.SetTriggerParameters(txEvtRecvReq, reflect.TypeFor[*InboundRequestEnvelope]())
	tx.fsm.SetTriggerParameters(txEvtSend1xx,
		reflect.TypeFor[*OutboundResponseEnvelope](),
		reflect.TypeFor[*SendResponseOptions](),
	)
	tx.fsm.SetTriggerParameters(txEvtSend2xx,
		reflect.TypeFor[*OutboundResponseEnvelope](),
		reflect.TypeFor[*SendResponseOptions](),
	)
	tx.fsm.SetTriggerParameters(txEvtSend300699,
		reflect.TypeFor[*OutboundResponseEnvelope](),
		reflect.TypeFor[*SendResponseOptions](),
	)

	return nil
}

func (tx *serverTransact) actSendRes(ctx context.Context, args ...any) error {
	res := args[0].(*OutboundResponseEnvelope) //nolint:forcetypeassert
	opts := args[1].(*SendResponseOptions)     //nolint:forcetypeassert
	defer func() {
		tx.lastRes.Store(res)
		tx.sendOpts.Store(cloneSendResOpts(opts))
	}()

	tx.log.LogAttrs(ctx, slog.LevelDebug, "send response",
		slog.Any("transaction", tx.impl),
		slog.Any("response", res),
	)

	tx.sendRes(ctx, res, opts) //nolint:errcheck
	return nil
}

func (tx *serverTransact) actResendRes(ctx context.Context, _ ...any) error {
	res := tx.LastResponse()
	if res == nil {
		return nil
	}
	opts := tx.sendOpts.Load()

	tx.log.LogAttrs(ctx, slog.LevelDebug, "re-send response",
		slog.Any("transaction", tx.impl),
		slog.Any("response", res),
	)

	tx.sendRes(ctx, res, opts) //nolint:errcheck
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
	if tx == nil {
		return nil
	}
	return tx.srvTxImpl().takeSnapshot()
}

// MarshalJSON implements [json.Marshaler].
func (tx *serverTransact) MarshalJSON() ([]byte, error) {
	return errtrace.Wrap2(json.Marshal(tx.Snapshot()))
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
	Request *InboundRequestEnvelope `json:"request"`
	// LastResponse is the last response sent by the transaction.
	LastResponse *OutboundResponseEnvelope `json:"last_response,omitempty"`
	// SendOptions are the options used to send the last response.
	SendOptions *SendResponseOptions `json:"send_options,omitempty"`
	// Timings are the timing configuration used to create the transaction.
	Timings TimingConfig `json:"timing_config,omitzero"`

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
		snap.Type != "" &&
		snap.State != "" &&
		snap.Key.IsValid() &&
		snap.Request.IsValid() &&
		(snap.LastResponse == nil || snap.LastResponse.IsValid())
}

// ServerTransactionKey is a key used to identify a server transaction.
//
// The key implements the matching rules defined in RFC 3261 section 17.2.3.
// Branch, SentBy and Method are used for RFC 3261 transactions.
// Method, URI, FromTag, ToTag, CallID, CSeqNum and Via are used for RFC 2543 transactions.
//
//nolint:recvcheck
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

var zeroSrvTxKey ServerTransactionKey

// MakeServerTransactionKey builds server transaction key from the given message.
func MakeServerTransactionKey(msg Message) (ServerTransactionKey, error) {
	if msg == nil {
		return zeroSrvTxKey, errtrace.Wrap(NewInvalidArgumentError("invalid message"))
	}
	if err := msg.Validate(); err != nil {
		return zeroSrvTxKey, errtrace.Wrap(NewInvalidArgumentError(err))
	}

	hdrs := GetMessageHeaders(msg)
	via, _ := hdrs.FirstVia()
	if branch, _ := via.Branch(); IsRFC3261Branch(branch) {
		return makeSrvTransactKey3261(hdrs, via), nil
	}
	return errtrace.Wrap2(makeSrvTransactKey2543(msg, hdrs, via))
}

func makeSrvTransactKey3261(hdrs Headers, via *header.ViaHop) ServerTransactionKey {
	var k ServerTransactionKey
	k.Branch, _ = via.Branch()
	k.SentBy = util.LCase(via.Addr.String())

	cseq, _ := hdrs.CSeq()
	if cseq.Method.Equal(RequestMethodAck) {
		k.Method = string(RequestMethodInvite)
	} else {
		k.Method = string(cseq.Method.ToUpper())
	}
	return k
}

func makeSrvTransactKey2543(msg Message, hdrs Headers, via *header.ViaHop) (ServerTransactionKey, error) {
	var k ServerTransactionKey
	k.Via = util.LCase(via.String())

	callID, _ := hdrs.CallID()
	k.CallID = string(callID)

	switch m := msg.(type) {
	case *Request:
		k.URI = util.LCase(m.URI.Render(nil))
	case interface{ URI() URI }:
		k.URI = util.LCase(m.URI().Render(nil))
	}

	from, _ := hdrs.From()
	k.FromTag, _ = from.Tag()
	if k.FromTag == "" {
		return zeroSrvTxKey, errtrace.Wrap(NewInvalidArgumentError("missing From tag"))
	}

	to, _ := hdrs.To()
	k.ToTag, _ = to.Tag()

	cseq, _ := hdrs.CSeq()
	k.SeqNum = cseq.SeqNum
	if cseq.Method.Equal(RequestMethodAck) {
		k.Method = string(RequestMethodInvite)
	} else {
		k.Method = string(cseq.Method.ToUpper())
	}
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
	if IsRFC3261Branch(k.Branch) {
		return k.marshal3261(), nil
	}
	return k.marshal2543(), nil
}

func (k ServerTransactionKey) marshal3261() []byte {
	sentBy := util.LCase(k.SentBy)
	method := util.UCase(k.Method)

	size := 1 +
		util.SizePrefixedString(k.Branch) +
		util.SizePrefixedString(sentBy) +
		util.SizePrefixedString(method)

	buf := make([]byte, 0, size)
	buf = append(buf, srvTxKeyHash3261)
	buf = util.AppendPrefixedString(buf, k.Branch)
	buf = util.AppendPrefixedString(buf, sentBy)
	buf = util.AppendPrefixedString(buf, method)
	return buf
}

func (k ServerTransactionKey) marshal2543() []byte {
	method := util.UCase(k.Method)
	uri := util.LCase(k.URI)
	via := util.LCase(k.Via)

	size := 1 +
		util.SizePrefixedString(uri) +
		util.SizePrefixedString(k.FromTag) +
		util.SizePrefixedString(k.ToTag) +
		util.SizePrefixedString(k.CallID) +
		util.SizeUVarInt(uint64(k.SeqNum)) +
		util.SizePrefixedString(method) +
		util.SizePrefixedString(via)

	buf := make([]byte, 0, size)
	buf = append(buf, srvTxKeyHash2543)
	buf = util.AppendPrefixedString(buf, uri)
	buf = util.AppendPrefixedString(buf, k.FromTag)
	buf = util.AppendPrefixedString(buf, k.ToTag)
	buf = util.AppendPrefixedString(buf, k.CallID)
	buf = util.AppendUVarInt(buf, uint64(k.SeqNum))
	buf = util.AppendPrefixedString(buf, method)
	buf = util.AppendPrefixedString(buf, via)
	return buf
}

// UnmarshalBinary populates the key fields from a binary representation
// produced by [ServerTransactionKey.MarshalBinary].
func (k *ServerTransactionKey) UnmarshalBinary(data []byte) error {
	if len(data) == 0 {
		return errtrace.Wrap(NewInvalidArgumentError("invalid data"))
	}

	var (
		rest = data[1:]
		err  error
		key  ServerTransactionKey
	)

	switch data[0] {
	case srvTxKeyHash3261:
		if key.Branch, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return errtrace.Wrap(err)
		}
		if key.SentBy, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return errtrace.Wrap(err)
		}
		if key.Method, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return errtrace.Wrap(err)
		}
	case srvTxKeyHash2543:
		if key.URI, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return errtrace.Wrap(err)
		}
		if key.FromTag, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return errtrace.Wrap(err)
		}
		if key.ToTag, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return errtrace.Wrap(err)
		}
		if key.CallID, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return errtrace.Wrap(err)
		}
		var seqNum uint64
		if seqNum, rest, err = util.ConsumeUVarInt(rest); err != nil {
			return errtrace.Wrap(err)
		}
		key.SeqNum = uint(seqNum)
		if key.Method, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return errtrace.Wrap(err)
		}
		if key.Via, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return errtrace.Wrap(err)
		}
	default:
		return errtrace.Wrap(NewInvalidArgumentError("unknown key format"))
	}

	if len(rest) != 0 {
		return errtrace.Wrap(NewInvalidArgumentError("unexpected trailing data"))
	}

	*k = key
	return nil
}

func (k ServerTransactionKey) String() string {
	data, _ := k.MarshalBinary()
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

		type hideMethods ServerTransactionKey
		type ServerTransactionKey hideMethods
		fmt.Fprintf(f, fmt.FormatString(f, verb), ServerTransactionKey(k))
		return
	}
}

type ServerTransactionStore interface {
	Load(ctx context.Context, key ServerTransactionKey) (ServerTransaction, error)
	LookupMatched(ctx context.Context, msg Message) (ServerTransaction, error)
	LookupMerged(ctx context.Context, key ServerTransactionKey) (ServerTransaction, error)
	Store(ctx context.Context, tx ServerTransaction) error
	Delete(ctx context.Context, tx ServerTransaction) error
	All(ctx context.Context) (iter.Seq[ServerTransaction], error)
}

type MemoryServerTransactionStore struct {
	keyLocks syncutil.KeyMutex[string]
	// store for matching request re-transmits (3261/2345)
	main *syncutil.ShardMap[string, ServerTransaction]
	// store for checking on merged requests, loop detection (3261/2345)
	merged *syncutil.ShardMap[string, ServerTransaction]
}

// NewMemoryServerTransactionStore creates a new in-memory server transaction store.
func NewMemoryServerTransactionStore() *MemoryServerTransactionStore {
	return &MemoryServerTransactionStore{
		main:   syncutil.NewShardMap[string, ServerTransaction](),
		merged: syncutil.NewShardMap[string, ServerTransaction](),
	}
}

func (s *MemoryServerTransactionStore) Load(
	_ context.Context,
	key ServerTransactionKey,
) (ServerTransaction, error) {
	// match inbound RFC 3261/2345 request retransmits
	hash := key.String()
	unlock := s.keyLocks.Lock(hash)
	tx, ok := s.main.Get(hash)
	unlock()
	if ok {
		return tx, nil
	}

	if IsRFC3261Branch(key.Branch) || !util.EqFold(key.Method, string(RequestMethodAck)) {
		return nil, errtrace.Wrap(ErrTransactionNotFound)
	}

	key.ToTag = ""
	hash = key.String()
	unlock = s.keyLocks.Lock(hash)
	tx, ok = s.main.Get(hash)
	unlock()
	if !ok {
		return nil, errtrace.Wrap(ErrTransactionNotFound)
	}
	return tx, nil
}

func (s *MemoryServerTransactionStore) LookupMatched(
	ctx context.Context,
	msg Message,
) (ServerTransaction, error) {
	key, err := MakeServerTransactionKey(msg)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}

	tx, err := s.Load(ctx, key)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}

	if !tx.MatchMessage(msg) {
		return nil, errtrace.Wrap(ErrTransactionNotFound)
	}
	return tx, nil
}

func (s *MemoryServerTransactionStore) LookupMerged(
	_ context.Context,
	key ServerTransactionKey,
) (ServerTransaction, error) {
	key.Branch = ""
	key.SentBy = ""
	key.URI = ""
	key.ToTag = ""
	key.Via = ""
	hash := key.String()
	unlock := s.keyLocks.Lock(hash)
	tx, ok := s.merged.Get(hash)
	unlock()
	if !ok {
		return nil, errtrace.Wrap(ErrTransactionNotFound)
	}
	return tx, nil
}

// Store stores a new one if it does not exist.
func (s *MemoryServerTransactionStore) Store(_ context.Context, tx ServerTransaction) error {
	key := tx.Key()
	hash := key.String()
	unlock := s.keyLocks.Lock(hash)
	s.main.Set(hash, tx)
	unlock()

	key = ServerTransactionKey{
		FromTag: key.FromTag,
		CallID:  key.CallID,
		SeqNum:  key.SeqNum,
		Method:  key.Method,
	}
	hash = key.String()
	unlock = s.keyLocks.Lock(hash)
	s.merged.Set(hash, tx)
	unlock()
	return nil
}

func (s *MemoryServerTransactionStore) Delete(_ context.Context, tx ServerTransaction) error {
	key := tx.Key()
	hash := key.String()
	unlock := s.keyLocks.Lock(hash)
	s.main.Del(hash)
	unlock()

	key = ServerTransactionKey{
		FromTag: key.FromTag,
		CallID:  key.CallID,
		SeqNum:  key.SeqNum,
		Method:  key.Method,
	}
	hash = key.String()
	unlock = s.keyLocks.Lock(hash)
	s.merged.Del(hash)
	unlock()
	return nil
}

func (s *MemoryServerTransactionStore) All(_ context.Context) (iter.Seq[ServerTransaction], error) {
	return util.SeqValues(s.main.Items()), nil
}
