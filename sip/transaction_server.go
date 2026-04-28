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

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/timeutil"
	"github.com/ghettovoice/gosip/internal/types"
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
	return errors.Wrap2(f(ctx, req, tp, opts))
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
		return errors.Wrap2(NewInviteServerTransaction(ctx, req, tp, opts))
	}
	return errors.Wrap2(NewNonInviteServerTransaction(ctx, req, tp, opts))
}

// ServerTransactionOptions contains options for a server transaction.
type ServerTransactionOptions struct {
	// Key is the server transaction key that will be used with the transaction.
	// If zero, the transaction will be created with the key automatically filled from the request.
	// Key should be unique for the transaction and match the request that created the transaction.
	Key ServerTransactionKey
	// Timings is the SIP timing config that will be used with the transaction.
	// If zero, [DefaultTimings] will be used.
	Timings TimingConfig
	// Logger is the logger that will be used with the transaction.
	// If nil, the [log.Default] will be used.
	Logger *slog.Logger
}

func (o *ServerTransactionOptions) key() ServerTransactionKey {
	if o == nil {
		return ServerTransactionKey{}
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
		return nil, errors.NewInvalidArgumentErrorWrap(err)
	}

	if tp == nil {
		return nil, errors.NewInvalidArgumentErrorWrap("nil transport")
	}

	if opts == nil {
		opts = &ServerTransactionOptions{}
	}

	key := opts.key()
	if key.IsValid() {
		key = key.Canonic()
	} else {
		var err error
		if key, err = MakeServerTransactionKey(req); err != nil {
			return nil, errors.NewInvalidArgumentErrorWrap(err)
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
		return slog.Value{}
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
		return ServerTransactionKey{}
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
		return errors.NewInvalidArgumentErrorWrap("nil transaction")
	}

	if tx.req == nil {
		return errors.NewInvalidArgumentErrorWrap("missing transaction request")
	}

	if res == nil {
		return errors.NewInvalidArgumentErrorWrap("nil response")
	}

	reqHdrs := tx.req.Headers()
	resHdrs := GetMessageHeaders(res)

	reqVia, ok := reqHdrs.FirstViaHop()
	if !ok || reqVia == nil {
		return errors.NewInvalidArgumentErrorWrap("missing request Via")
	}

	resVia, ok := resHdrs.FirstViaHop()
	if !ok || resVia == nil {
		return errors.NewInvalidArgumentErrorWrap("missing response Via")
	}

	if !reqVia.Equal(resVia) {
		return errors.NewInvalidArgumentErrorWrap("response Via does not match transaction request")
	}

	reqCallID, ok := reqHdrs.CallID()
	if !ok {
		return errors.NewInvalidArgumentErrorWrap("missing request Call-ID")
	}

	resCallID, ok := resHdrs.CallID()
	if !ok {
		return errors.NewInvalidArgumentErrorWrap("missing response Call-ID")
	}

	if reqCallID != resCallID {
		return errors.NewInvalidArgumentErrorWrap("response Call-ID does not match transaction request")
	}

	reqFrom, ok := reqHdrs.From()
	if !ok || reqFrom == nil {
		return errors.NewInvalidArgumentErrorWrap("missing request From")
	}

	resFrom, ok := resHdrs.From()
	if !ok || resFrom == nil {
		return errors.NewInvalidArgumentErrorWrap("missing response From")
	}

	if !reqFrom.Equal(resFrom) {
		return errors.NewInvalidArgumentErrorWrap("response From does not match transaction request")
	}

	reqTo, ok := reqHdrs.To()
	if !ok || reqTo == nil {
		return errors.NewInvalidArgumentErrorWrap("missing request To")
	}

	resTo, ok := resHdrs.To()
	if !ok || resTo == nil {
		return errors.NewInvalidArgumentErrorWrap("missing response To")
	}

	if !equalNameAddrWithoutTag(header.NameAddr(*reqTo), header.NameAddr(*resTo)) {
		return errors.NewInvalidArgumentErrorWrap("response To does not match transaction request")
	}

	if reqTag, ok := reqTo.Tag(); ok && reqTag != "" {
		resTag, _ := resTo.Tag()
		if reqTag != resTag {
			return errors.NewInvalidArgumentErrorWrap("response To tag does not match transaction request")
		}
	}

	reqCSeq, ok := reqHdrs.CSeq()
	if !ok || reqCSeq == nil {
		return errors.NewInvalidArgumentErrorWrap("missing request CSeq")
	}

	resCSeq, ok := resHdrs.CSeq()
	if !ok || resCSeq == nil {
		return errors.NewInvalidArgumentErrorWrap("missing response CSeq")
	}

	if reqCSeq.SeqNum != resCSeq.SeqNum {
		return errors.NewInvalidArgumentErrorWrap("response CSeq number does not match transaction request")
	}

	if !resCSeq.Method.Equal(reqCSeq.Method) {
		return errors.NewInvalidArgumentErrorWrap("response CSeq method does not match transaction request")
	}

	return nil
}

func equalNameAddrWithoutTag(a, b header.NameAddr) bool {
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
func (tx *serverTransact) RecvRequest(ctx context.Context, req *InboundRequestEnvelope) error {
	if !tx.MatchMessage(req) {
		return errors.NewInvalidArgumentErrorWrap(ErrMessageNotMatched)
	}

	ctx = ContextWithTransaction(ctx, tx.impl)

	if v, ok := tx.impl.(interface {
		recvReq(ctx context.Context, req *InboundRequestEnvelope) error
	}); ok {
		return errors.Wrap(v.recvReq(ctx, req))
	}

	return errors.Wrap(tx.recvReq(ctx, req))
}

func (tx *serverTransact) recvReq(ctx context.Context, req *InboundRequestEnvelope) error {
	switch {
	case tx.req.Method().Equal(req.Method()):
		return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtRecvReq, req))
	default:
		return errors.NewInvalidArgumentErrorWrap(ErrMethodNotAllowed)
	}
}

// Respond sends a response to the remote address with specified options.
// Response will be passed to the transport layer by the transaction's FSM.
func (tx *serverTransact) Respond(ctx context.Context, sts ResponseStatus, opts *RespondOptions) error {
	res, err := tx.req.NewResponse(sts, opts.resOpts())
	if err != nil {
		return errors.Wrap(err)
	}

	return errors.Wrap(tx.SendResponse(ctx, res, opts.sendOpts()))
}

// SendResponse sends a response to the remote address with specified options.
// Response will be passed to the transport layer by the transaction's FSM.
func (tx *serverTransact) SendResponse(ctx context.Context, res *OutboundResponseEnvelope, opts *SendResponseOptions) error {
	if err := res.Validate(); err != nil {
		return errors.Wrap(err)
	}

	if err := tx.matchRes(res); err != nil {
		return errors.Wrap(err)
	}

	ctx = ContextWithTransaction(ctx, tx.impl)

	switch sts := res.Status(); {
	case sts.IsProvisional():
		return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtSend1xx, res, opts))
	case sts.IsSuccessful():
		return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtSend2xx, res, opts))
	default:
		return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtSend300699, res, opts))
	}
}

func (tx *serverTransact) sendRes(ctx context.Context, res *OutboundResponseEnvelope, opts *SendResponseOptions) error {
	if err := tx.tp.SendResponse(ctx, res, opts); err != nil {
		if err := tx.fsm.FireCtx(ctx, txEvtTranspErr, errors.ErrorfWrap("send %q response: %w", res.Status(), err)); err != nil {
			panic(errors.ErrorfWrap("fire %q in state %q: %w", txEvtTranspErr, tx.State(), err))
		}

		return errors.Wrap(err)
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
		return errors.Wrap(err)
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

	opts := args[1].(*SendResponseOptions) //nolint:forcetypeassert
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

// MakeServerTransactionKey builds server transaction key from the given message.
func MakeServerTransactionKey(msg Message) (ServerTransactionKey, error) {
	if msg == nil {
		return ServerTransactionKey{}, errors.NewInvalidArgumentErrorWrap("nil message")
	}

	if err := msg.Validate(); err != nil {
		return ServerTransactionKey{}, errors.NewInvalidArgumentErrorWrap(err)
	}

	hdrs := GetMessageHeaders(msg)

	via, _ := hdrs.FirstViaHop()
	if branch, _ := via.Branch(); IsRFC3261Branch(branch) {
		return makeSrvTransactKey3261(hdrs, via), nil
	}

	return errors.Wrap2(makeSrvTransactKey2543(msg, hdrs, via))
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
		return ServerTransactionKey{}, errors.NewInvalidArgumentErrorWrap("missing From tag")
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
	if IsRFC3261Branch(k.Branch) {
		return k.Canonic().marshal3261(), nil
	}
	return k.Canonic().marshal2543(), nil
}

func (k ServerTransactionKey) AppendBinary(b []byte) ([]byte, error) {
	data, err := k.MarshalBinary()
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return append(b, data...), nil
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
	if k == nil {
		return errors.NewInvalidArgumentErrorWrap("nil transaction key")
	}

	if len(data) == 0 {
		*k = ServerTransactionKey{}
		return nil
	}

	key, ok := parseServerTransactKey(data)
	if !ok {
		*k = ServerTransactionKey{}
		return errors.NewInvalidArgumentErrorWrap("invalid transaction key payload")
	}

	*k = key

	return nil
}

func parseServerTransactKey(data []byte) (ServerTransactionKey, bool) {
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
	data, _ := k.MarshalBinary()
	return hex.EncodeToString(data)
}

func (k ServerTransactionKey) Format(f fmt.State, verb rune) {
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
			hideMethods          ServerTransactionKey
			ServerTransactionKey hideMethods
		)

		fmt.Fprintf(f, fmt.FormatString(f, verb), ServerTransactionKey(k))

		return
	}
}

type ServerTransactionStore interface {
	Load(ctx context.Context, key ServerTransactionKey) (ServerTransaction, error)
	MatchMessage(ctx context.Context, msg Message) (ServerTransaction, error)
	LookupMerged(ctx context.Context, key ServerTransactionKey) (ServerTransaction, error)
	Store(ctx context.Context, tx ServerTransaction) error
	Delete(ctx context.Context, tx ServerTransaction) error
	All(ctx context.Context) (iter.Seq[ServerTransaction], error)
}

type MemoryServerTransactionStore struct {
	// store for matching request re-transmits (3261/2345)
	main *syncutil.ShardMap[ServerTransactionKey, ServerTransaction]
	// store for checking on merged requests, loop detection (3261/2345)
	merged *syncutil.ShardMap[ServerTransactionKey, ServerTransaction]
}

// NewMemoryServerTransactionStore creates a new in-memory server transaction store.
func NewMemoryServerTransactionStore() *MemoryServerTransactionStore {
	return &MemoryServerTransactionStore{
		main:   syncutil.NewShardMap[ServerTransactionKey, ServerTransaction](),
		merged: syncutil.NewShardMap[ServerTransactionKey, ServerTransaction](),
	}
}

func (s *MemoryServerTransactionStore) Load(_ context.Context, key ServerTransactionKey) (ServerTransaction, error) {
	// match inbound RFC 3261/2345 request retransmits
	if tx, ok := s.main.Load(key); ok {
		return tx, nil
	}

	if IsRFC3261Branch(key.Branch) || !util.EqFold(key.Method, string(RequestMethodAck)) {
		return nil, errors.Wrap(ErrTransactionNotFound)
	}

	key.ToTag = ""

	tx, ok := s.main.Load(key)
	if !ok {
		return nil, errors.Wrap(ErrTransactionNotFound)
	}

	return tx, nil
}

func (s *MemoryServerTransactionStore) MatchMessage(ctx context.Context, msg Message) (ServerTransaction, error) {
	key, err := MakeServerTransactionKey(msg)
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

func (s *MemoryServerTransactionStore) LookupMerged(_ context.Context, key ServerTransactionKey) (ServerTransaction, error) {
	key.Branch = ""
	key.SentBy = ""
	key.URI = ""
	key.ToTag = ""
	key.Via = ""

	tx, ok := s.merged.Load(key)
	if !ok {
		return nil, errors.Wrap(ErrTransactionNotFound)
	}

	return tx, nil
}

// Store stores a new one if it does not exist.
func (s *MemoryServerTransactionStore) Store(_ context.Context, tx ServerTransaction) error {
	key := tx.Key()
	s.main.Store(key, tx)

	key = ServerTransactionKey{
		FromTag: key.FromTag,
		CallID:  key.CallID,
		SeqNum:  key.SeqNum,
		Method:  key.Method,
	}
	s.merged.Store(key, tx)

	return nil
}

func (s *MemoryServerTransactionStore) Delete(_ context.Context, tx ServerTransaction) error {
	key := tx.Key()
	s.main.Delete(key)

	key = ServerTransactionKey{
		FromTag: key.FromTag,
		CallID:  key.CallID,
		SeqNum:  key.SeqNum,
		Method:  key.Method,
	}
	s.merged.Delete(key)

	return nil
}

func (s *MemoryServerTransactionStore) All(_ context.Context) (iter.Seq[ServerTransaction], error) {
	return util.SeqValues(s.main.All()), nil
}

// InviteServerTransaction represents an invite server transaction.
// It implements the server transaction FSM defined in RFC 3261 section 17.2.1
// and patches from RFC 6026.
type InviteServerTransaction struct {
	*serverTransact

	tmr1xx atomic.Pointer[timeutil.SerializableTimer]
	tmrG   atomic.Pointer[timeutil.SerializableTimer]
	tmrH   atomic.Pointer[timeutil.SerializableTimer]
	tmrI   atomic.Pointer[timeutil.SerializableTimer]
	tmrL   atomic.Pointer[timeutil.SerializableTimer]

	onAck       types.CallbackManager[InboundRequestHandler]
	pendingAcks types.Queue[pendingAck]
}

type pendingAck struct {
	ctx context.Context
	ack *InboundRequestEnvelope
}

// NewInviteServerTransaction creates a new invite server transaction and starts its state machine.
//
// Context does not affect the transaction lifecycle, it can be used to pass
// additional information to the transaction.
// Request expected to be a valid SIP request with INVITE method.
// Transport expected to be a non-nil server transport.
// Options are optional and can be nil, in which case default options will be used.
// Transaction key will be filled from the request automatically if not specified in the options.
func NewInviteServerTransaction(
	ctx context.Context,
	req *InboundRequestEnvelope,
	tp ServerTransport,
	opts *ServerTransactionOptions,
) (*InviteServerTransaction, error) {
	if err := req.Validate(); err != nil {
		return nil, errors.NewInvalidArgumentErrorWrap(err)
	}

	if !req.Method().Equal(RequestMethodInvite) {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrMethodNotAllowed)
	}

	tx := new(InviteServerTransaction)

	srvTx, err := newServerTransact(TransactionTypeServerInvite, tx, req, tp, opts)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	tx.serverTransact = srvTx

	ctx = ContextWithTransaction(ctx, tx)

	if err := tx.initFSM(TransactionStateProceeding); err != nil {
		return nil, errors.Wrap(err)
	}

	if err := tx.actProceeding(ctx); err != nil {
		_ = tx.Terminate(ctx)
		return nil, errors.Wrap(err)
	}

	return tx, nil
}

const (
	txEvtRecvAck  = "recv_ack"
	txEvtTimer1xx = "timer_1xx"
	txEvtTimerG   = "timer_g"
	txEvtTimerH   = "timer_h"
	txEvtTimerI   = "timer_i"
	txEvtTimerL   = "timer_l"
)

func (tx *InviteServerTransaction) initFSM(start TransactionState) error {
	if err := tx.serverTransact.initFSM(start); err != nil {
		return errors.Wrap(err)
	}

	tx.fsm.SetTriggerParameters(txEvtRecvAck, reflect.TypeFor[*InboundRequestEnvelope]())

	tx.fsm.Configure(TransactionStateProceeding).
		InternalTransition(txEvtRecvReq, tx.actResendRes).
		InternalTransition(txEvtSend1xx, tx.actSendRes).
		InternalTransition(txEvtTimer1xx, tx.actSend100).
		InternalTransition(txEvtTranspErr, tx.actTranspErr).
		Permit(txEvtSend2xx, TransactionStateAccepted).
		Permit(txEvtSend300699, TransactionStateCompleted).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateAccepted).
		OnEntry(tx.actAccepted).
		OnEntryFrom(txEvtSend2xx, tx.actSendRes).
		InternalTransition(txEvtRecvReq, tx.actNoop).
		InternalTransition(txEvtRecvAck, tx.actPassAck).
		InternalTransition(txEvtSend2xx, tx.actSendRes).
		InternalTransition(txEvtTranspErr, tx.actTranspErr).
		Permit(txEvtTimerL, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateCompleted).
		OnEntry(tx.actCompleted).
		OnEntryFrom(txEvtSend300699, tx.actSendRes).
		InternalTransition(txEvtRecvReq, tx.actResendRes).
		InternalTransition(txEvtTimerG, tx.actResendRes).
		InternalTransition(txEvtTranspErr, tx.actTranspErr).
		Permit(txEvtRecvAck, TransactionStateConfirmed).
		Permit(txEvtTimerH, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateConfirmed).
		OnEntry(tx.actConfirmed).
		InternalTransition(txEvtRecvReq, tx.actNoop).
		InternalTransition(txEvtRecvAck, tx.actNoop).
		Permit(txEvtTimerI, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateTerminated).
		OnEntry(tx.actTerminated).
		OnEntryFrom(txEvtTimerH, tx.actTimedOut).
		InternalTransition(txEvtTerminate, tx.actNoop)

	return nil
}

func (tx *InviteServerTransaction) actSend100(ctx context.Context, _ ...any) error {
	res, err := tx.req.NewResponse(ResponseStatusTrying, nil)
	if err != nil {
		// Request is always valid, so this should never happen.
		panic(errors.ErrorfWrap("create auto %q response: %w", ResponseStatusTrying, err))
	}

	tx.log.LogAttrs(ctx, slog.LevelDebug, "send response",
		slog.Any("transaction", tx),
		slog.Any("response", res),
	)

	tx.sendRes(ctx, res, nil) //nolint:errcheck

	return nil
}

func (tx *InviteServerTransaction) actSendRes(ctx context.Context, args ...any) error {
	if tmr := tx.tmr1xx.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "1xx timer stopped", slog.Any("transaction", tx)) //nolint:sloglint
	}
	return errors.Wrap(tx.serverTransact.actSendRes(ctx, args...))
}

func (tx *InviteServerTransaction) actPassAck(ctx context.Context, args ...any) error {
	ack := args[0].(*InboundRequestEnvelope) //nolint:forcetypeassert

	tx.log.LogAttrs(ctx, slog.LevelDebug, "pass ACK",
		slog.Any("transaction", tx),
		slog.Any("ack", ack),
	)

	tx.pendingAcks.Push(pendingAck{ctx, ack})

	if tx.onAck.Len() > 0 {
		tx.deliverPendingAcks()
	}

	return nil
}

func (tx *InviteServerTransaction) deliverPendingAcks() {
	acks := tx.pendingAcks.Drain()
	if len(acks) == 0 {
		return
	}

	for fn := range tx.onAck.All() {
		for _, e := range acks {
			fn(e.ctx, e.ack)
		}
	}
}

//nolint:unparam
func (tx *InviteServerTransaction) actProceeding(ctx context.Context, args ...any) error {
	tx.serverTransact.actProceeding(ctx, args...) //nolint:errcheck

	tmr := timeutil.AfterFunc(tx.timings.Time100(), tx.timer1xxHdlr(ctx))
	tx.tmr1xx.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "1xx timer started", //nolint:sloglint
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *InviteServerTransaction) timer1xxHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "1xx timer expired", slog.Any("transaction", tx)) //nolint:sloglint

		tx.tmr1xx.Store(nil)

		if tx.State() != TransactionStateProceeding {
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimer1xx); err != nil {
			panic(errors.ErrorfWrap("fire %q in state %q: %w", txEvtTimer1xx, tx.State(), err))
		}
	}
}

func (tx *InviteServerTransaction) actAccepted(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction accepted", slog.Any("transaction", tx))

	tmr := timeutil.AfterFunc(tx.timings.TimeL(), tx.timerLHdlr(ctx))
	tx.tmrL.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer L started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *InviteServerTransaction) timerLHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer L expired", slog.Any("transaction", tx))

		tx.tmrL.Store(nil)

		if tx.State() != TransactionStateAccepted {
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerL); err != nil {
			panic(errors.ErrorfWrap("fire %q in state %q: %w", txEvtTimerL, tx.State(), err))
		}
	}
}

func (tx *InviteServerTransaction) actCompleted(ctx context.Context, args ...any) error {
	tx.serverTransact.actCompleted(ctx, args...) //nolint:errcheck

	if !tx.tp.Reliable() {
		tmr := timeutil.AfterFunc(tx.timings.TimeG(), tx.timerGHdlr(ctx))
		tx.tmrG.Store(tmr)

		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer G started",
			slog.Any("transaction", tx),
			slog.Time("expires_at", time.Now().Add(tmr.Left())),
		)
	}

	tmr := timeutil.AfterFunc(tx.timings.TimeH(), tx.timerHHdlr(ctx))
	tx.tmrH.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer H started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *InviteServerTransaction) timerGHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer G expired", slog.Any("transaction", tx))

		if tx.State() != TransactionStateCompleted {
			tx.tmrG.Store(nil)
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerG); err != nil {
			panic(errors.ErrorfWrap("fire %q in state %q: %w", txEvtTimerG, tx.State(), err))
		}

		if tmr := tx.tmrG.Load(); tmr != nil {
			tmr.Reset(min(2*tmr.Duration(), tx.timings.T2()))

			tx.log.LogAttrs(ctx, slog.LevelDebug, "timer G reset",
				slog.Any("transaction", tx),
				slog.Time("expires_at", time.Now().Add(tmr.Left())),
			)
		}
	}
}

func (tx *InviteServerTransaction) timerHHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer H expired", slog.Any("transaction", tx))

		tx.tmrH.Store(nil)

		if tx.State() != TransactionStateCompleted {
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerH); err != nil {
			panic(errors.ErrorfWrap("fire %q in state %q: %w", txEvtTimerH, tx.State(), err))
		}
	}
}

func (tx *InviteServerTransaction) actConfirmed(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction confirmed", slog.Any("transaction", tx))

	if tmr := tx.tmrH.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer H stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrG.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer G stopped", slog.Any("transaction", tx))
	}

	var timeI time.Duration
	if !tx.tp.Reliable() {
		timeI = tx.timings.TimeI()
	}

	tmr := timeutil.AfterFunc(timeI, tx.timerIHdlr(ctx))
	tx.tmrI.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer I started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *InviteServerTransaction) timerIHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer I expired", slog.Any("transaction", tx))

		tx.tmrI.Store(nil)

		if tx.State() != TransactionStateConfirmed {
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerI); err != nil {
			panic(errors.ErrorfWrap("fire %q in state %q: %w", txEvtTimerI, tx.State(), err))
		}
	}
}

func (tx *InviteServerTransaction) actTerminated(ctx context.Context, args ...any) error {
	tx.serverTransact.actTerminated(ctx, args...) //nolint:errcheck

	// timer G can be active after transition to here by timer H
	if tmr := tx.tmrG.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer G stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrH.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer H stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrI.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer I stopped", slog.Any("transaction", tx))
	}

	if tmr := tx.tmrL.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer L stopped", slog.Any("transaction", tx))
	}

	return nil
}

func (tx *InviteServerTransaction) adjustKeys(txKey, _ *ServerTransactionKey, reqMtd RequestMethod) {
	// set tx key To tag to last response To tag for RFC 2543 matching of ACK on initial INVITE
	if !IsRFC3261Branch(txKey.Branch) && reqMtd.Equal(RequestMethodAck) && txKey.ToTag == "" {
		if res := tx.LastResponse(); res != nil {
			res.AccessMessage(func(r *Response) {
				to, _ := r.Headers.To()
				txKey.ToTag, _ = to.Tag()
			})
		}
	}
}

func (tx *InviteServerTransaction) recvReq(ctx context.Context, req *InboundRequestEnvelope) error {
	if req.Method().Equal(RequestMethodAck) {
		return errors.Wrap(tx.fsm.FireCtx(ctx, txEvtRecvAck, req))
	}
	return errors.Wrap(tx.serverTransact.recvReq(ctx, req))
}

// OnAck binds the callback to be called when the transaction receives an 2xx ACK.
//
// 2xx ACK can be matched to the INVITE transaction only by RFC 2543 matching rules,
// so this callback here only for backward compatibility with old clients.
// 2xx ACK from RFC 3261 always goes outside of the INVITE transaction.
//
// The callback can be unbound by calling the returned cancel function.
// Multiple callbacks are allowed, they will be called in the order they were registered.
// Context passed to the callback will be the context passed to the [InviteServerTransaction.RecvRequest] method.
func (tx *InviteServerTransaction) OnAck(fn InboundRequestHandler) (unbind func()) {
	defer tx.deliverPendingAcks()
	return tx.onAck.Add(fn)
}

func (tx *InviteServerTransaction) takeSnapshot() *ServerTransactionSnapshot {
	return &ServerTransactionSnapshot{
		Time:         time.Now(),
		Type:         tx.typ,
		State:        tx.State(),
		Key:          tx.key,
		Request:      tx.req,
		LastResponse: tx.LastResponse(),
		SendOptions:  cloneSendResOpts(tx.sendOpts.Load()),
		Timings:      tx.timings,
		Timer1xx:     tx.tmr1xx.Load().Snapshot(),
		TimerG:       tx.tmrG.Load().Snapshot(),
		TimerH:       tx.tmrH.Load().Snapshot(),
		TimerI:       tx.tmrI.Load().Snapshot(),
		TimerL:       tx.tmrL.Load().Snapshot(),
	}
}

// RestoreInviteServerTransaction restores an invite server transaction from a snapshot.
//
// Context does not affect the transaction lifecycle, it can be used to
// pass additional information to the transaction.
// The snapshot contains the serialized state of the transaction.
// Transport is required to send responses.
// Options are optional and can be nil. The key field from options is ignored
// and the key from the snapshot will be used instead.
//
// After restoration, the transaction FSM will be in the state specified in the snapshot.
// Timers will be restored and their callbacks will be reconnected to the FSM.
// If a timer has already expired according to the snapshot, it will not be restarted.
func RestoreInviteServerTransaction(
	ctx context.Context,
	snap *ServerTransactionSnapshot,
	tp ServerTransport,
	opts *ServerTransactionOptions,
) (*InviteServerTransaction, error) {
	if !snap.IsValid() || snap.Type != TransactionTypeServerInvite {
		return nil, errors.NewInvalidArgumentErrorWrap("invalid snapshot")
	}

	var restoreOpts ServerTransactionOptions
	if opts != nil {
		restoreOpts = *opts
	}

	restoreOpts.Key = snap.Key
	restoreOpts.Timings = snap.Timings

	tx := new(InviteServerTransaction)

	srvTx, err := newServerTransact(TransactionTypeServerInvite, tx, snap.Request, tp, &restoreOpts)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	tx.serverTransact = srvTx

	ctx = ContextWithTransaction(ctx, tx)

	if snap.LastResponse != nil {
		tx.lastRes.Store(snap.LastResponse)
	}

	if snap.SendOptions != nil {
		tx.sendOpts.Store(cloneSendResOpts(snap.SendOptions))
	}

	if err := tx.initFSM(snap.State); err != nil {
		return nil, errors.Wrap(err)
	}

	tx.restoreTimers(ctx, snap)

	return tx, nil
}

// restoreTimers restores transaction timers from the snapshot and reconnects their callbacks.
func (tx *InviteServerTransaction) restoreTimers(ctx context.Context, snap *ServerTransactionSnapshot) {
	if tmr := snap.Timer1xx; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timer1xxHdlr(ctx))
		tx.tmr1xx.Store(restored)
	}

	if tmr := snap.TimerG; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerGHdlr(ctx))
		tx.tmrG.Store(restored)
	}

	if tmr := snap.TimerH; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerHHdlr(ctx))
		tx.tmrH.Store(restored)
	}

	if tmr := snap.TimerI; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerIHdlr(ctx))
		tx.tmrI.Store(restored)
	}

	if tmr := snap.TimerL; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerLHdlr(ctx))
		tx.tmrL.Store(restored)
	}
}

// NonInviteServerTransaction represents a non-invite server transaction.
type NonInviteServerTransaction struct {
	*serverTransact

	tmrJ atomic.Pointer[timeutil.SerializableTimer]
}

// NewNonInviteServerTransaction creates a new non-invite server transaction and starts its state machine.
//
// Context does not affect the transaction lifecycle, it can be used to
// pass additional information to the transaction.
// Request expected to be a valid SIP request with any method except INVITE or ACK.
// Transport expected to be a non-nil server transport.
// Options are optional and can be nil, in which case default options will be used.
// Transaction key will be filled from the request automatically if not specified in the options.
func NewNonInviteServerTransaction(
	ctx context.Context,
	req *InboundRequestEnvelope,
	tp ServerTransport,
	opts *ServerTransactionOptions,
) (*NonInviteServerTransaction, error) {
	if err := req.Validate(); err != nil {
		return nil, errors.NewInvalidArgumentErrorWrap(err)
	}

	if mtd := req.Method(); mtd.Equal(RequestMethodInvite) || mtd.Equal(RequestMethodAck) {
		return nil, errors.NewInvalidArgumentErrorWrap(ErrMethodNotAllowed)
	}

	tx := new(NonInviteServerTransaction)

	srvTx, err := newServerTransact(TransactionTypeServerNonInvite, tx, req, tp, opts)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	tx.serverTransact = srvTx

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

const txEvtTimerJ = "timer_J"

func (tx *NonInviteServerTransaction) initFSM(start TransactionState) error {
	if err := tx.serverTransact.initFSM(start); err != nil {
		return errors.Wrap(err)
	}

	tx.fsm.Configure(TransactionStateTrying).
		InternalTransition(txEvtRecvReq, tx.actNoop).
		Permit(txEvtSend1xx, TransactionStateProceeding).
		Permit(txEvtSend2xx, TransactionStateCompleted).
		Permit(txEvtSend300699, TransactionStateCompleted).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateProceeding).
		OnEntry(tx.actProceeding).
		OnEntryFrom(txEvtSend1xx, tx.actSendRes).
		InternalTransition(txEvtRecvReq, tx.actResendRes).
		InternalTransition(txEvtSend1xx, tx.actSendRes).
		Permit(txEvtSend2xx, TransactionStateCompleted).
		Permit(txEvtSend300699, TransactionStateCompleted).
		Permit(txEvtTranspErr, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateCompleted).
		OnEntry(tx.actCompleted).
		OnEntryFrom(txEvtSend2xx, tx.actSendRes).
		OnEntryFrom(txEvtSend300699, tx.actSendRes).
		InternalTransition(txEvtRecvReq, tx.actResendRes).
		InternalTransition(txEvtSend2xx, tx.actNoop).
		InternalTransition(txEvtSend300699, tx.actNoop).
		Permit(txEvtTimerJ, TransactionStateTerminated).
		Permit(txEvtTranspErr, TransactionStateTerminated).
		Permit(txEvtTerminate, TransactionStateTerminated)

	tx.fsm.Configure(TransactionStateTerminated).
		OnEntry(tx.actTerminated).
		OnEntryFrom(txEvtTranspErr, tx.actTranspErr).
		InternalTransition(txEvtTerminate, tx.actNoop)

	return nil
}

//nolint:unparam
func (tx *NonInviteServerTransaction) actTrying(ctx context.Context, _ ...any) error {
	tx.log.LogAttrs(ctx, slog.LevelDebug, "transaction trying", slog.Any("transaction", tx))

	return nil
}

func (tx *NonInviteServerTransaction) actCompleted(ctx context.Context, args ...any) error {
	tx.serverTransact.actCompleted(ctx, args...) //nolint:errcheck

	var timeJ time.Duration
	if !tx.tp.Reliable() {
		timeJ = tx.timings.TimeJ()
	}

	tmr := timeutil.AfterFunc(timeJ, tx.timerJHdlr(ctx))
	tx.tmrJ.Store(tmr)

	tx.log.LogAttrs(ctx, slog.LevelDebug, "timer J started",
		slog.Any("transaction", tx),
		slog.Time("expires_at", time.Now().Add(tmr.Left())),
	)

	return nil
}

func (tx *NonInviteServerTransaction) timerJHdlr(ctx context.Context) func() {
	return func() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer J expired", slog.Any("transaction", tx))

		tx.tmrJ.Store(nil)

		if tx.State() != TransactionStateCompleted {
			return
		}

		if err := tx.fsm.FireCtx(ctx, txEvtTimerJ); err != nil {
			panic(errors.ErrorfWrap("fire %q in state %q: %w", txEvtTimerJ, tx.State(), err))
		}
	}
}

func (tx *NonInviteServerTransaction) actTerminated(ctx context.Context, args ...any) error {
	tx.serverTransact.actTerminated(ctx, args...) //nolint:errcheck

	if tmr := tx.tmrJ.Swap(nil); tmr != nil && tmr.Stop() {
		tx.log.LogAttrs(ctx, slog.LevelDebug, "timer J stopped", slog.Any("transaction", tx))
	}

	return nil
}

func (tx *NonInviteServerTransaction) takeSnapshot() *ServerTransactionSnapshot {
	return &ServerTransactionSnapshot{
		Time:         time.Now(),
		Type:         tx.typ,
		State:        tx.State(),
		Key:          tx.key,
		Request:      tx.req,
		LastResponse: tx.LastResponse(),
		SendOptions:  cloneSendResOpts(tx.sendOpts.Load()),
		Timings:      tx.timings,
		TimerJ:       tx.tmrJ.Load().Snapshot(),
	}
}

// RestoreNonInviteServerTransaction restores a non-invite server transaction from a snapshot.
//
// Context does not affect the transaction lifecycle, it can be used to
// pass additional information to the transaction.
// The snapshot contains the serialized state of the transaction.
// Transport is required to send responses.
// Options are optional and can be nil. The key field from options is ignored
// and the key from the snapshot will be used instead.
//
// After restoration, the transaction FSM will be in the state specified in the snapshot.
// Timer J will be restored and its callback reconnected to the FSM.
// If the timer has already expired according to the snapshot, it will not be restarted.
func RestoreNonInviteServerTransaction(
	ctx context.Context,
	snap *ServerTransactionSnapshot,
	tp ServerTransport,
	opts *ServerTransactionOptions,
) (*NonInviteServerTransaction, error) {
	if !snap.IsValid() || snap.Type != TransactionTypeServerNonInvite {
		return nil, errors.NewInvalidArgumentErrorWrap("invalid snapshot")
	}

	var restoreOpts ServerTransactionOptions
	if opts != nil {
		restoreOpts = *opts
	}

	restoreOpts.Key = snap.Key
	restoreOpts.Timings = snap.Timings

	tx := new(NonInviteServerTransaction)

	srvTx, err := newServerTransact(TransactionTypeServerNonInvite, tx, snap.Request, tp, &restoreOpts)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	tx.serverTransact = srvTx

	ctx = ContextWithTransaction(ctx, tx)

	if snap.LastResponse != nil {
		tx.lastRes.Store(snap.LastResponse)
	}

	if snap.SendOptions != nil {
		tx.sendOpts.Store(cloneSendResOpts(snap.SendOptions))
	}

	if err := tx.initFSM(snap.State); err != nil {
		return nil, errors.Wrap(err)
	}

	tx.restoreTimers(ctx, snap)

	return tx, nil
}

func (tx *NonInviteServerTransaction) restoreTimers(ctx context.Context, snap *ServerTransactionSnapshot) {
	if tmr := snap.TimerJ; tmr != nil {
		restored := timeutil.RestoreTimer(tmr)
		restored.SetCallback(tx.timerJHdlr(ctx))
		tx.tmrJ.Store(restored)
	}
}
