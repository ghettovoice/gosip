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

// ServerTransaction represents a SIP server transaction.
type ServerTransaction interface {
	Transaction
	// MatchRequest checks whether the request matches the server transaction.
	MatchRequest(req *InboundRequest) error
	// RecvRequest receives a request from the transport layer.
	RecvRequest(ctx context.Context, req *InboundRequest) error
	// Respond sends a response to the remote address with specified options.
	Respond(ctx context.Context, sts ResponseStatus, opts *RespondOptions) error
}

type TransactionRequestHandler = func(ctx context.Context, tx ServerTransaction, req *InboundRequest)

type ServerTransactionStore = TransactionStore[ServerTransactionKey, ServerTransaction]

func NewMemoryServerTransactionStore() ServerTransactionStore {
	return NewMemoryTransactionStore[ServerTransactionKey, ServerTransaction]()
}

type ServerTransactionFactory interface {
	NewServerTransaction(
		ctx context.Context,
		req *InboundRequest,
		tp ServerTransport,
		opts *ServerTransactionOptions,
	) (ServerTransaction, error)
}

type StdServerTransactionFactory struct{}

var defSrvTxFactory = &StdServerTransactionFactory{}

func DefaultServerTransactionFactory() *StdServerTransactionFactory { return defSrvTxFactory }

func (*StdServerTransactionFactory) NewServerTransaction(
	ctx context.Context,
	req *InboundRequest,
	tp ServerTransport,
	opts *ServerTransactionOptions,
) (ServerTransaction, error) {
	if req.Method().Equal(RequestMethodInvite) {
		return errtrace.Wrap2(NewInviteServerTransaction(req, tp, opts))
	}
	return errtrace.Wrap2(NewNonInviteServerTransaction(req, tp, opts))
}

const srvTransactCtxKey types.ContextKey = "server_transaction"

func ServerTransactionFromContext(ctx context.Context) (ServerTransaction, bool) {
	tx, ok := ctx.Value(srvTransactCtxKey).(ServerTransaction)
	return tx, ok
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
	// Log is the logger that will be used with the transaction.
	// If nil, the [log.Default] will be used.
	Log *slog.Logger
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
	if o == nil || o.Log == nil {
		return log.Default()
	}
	return o.Log
}

type serverTransact struct {
	*baseTransact
	key      ServerTransactionKey
	tp       ServerTransport
	timings  TimingConfig
	req      *InboundRequest
	lastRes  atomic.Pointer[OutboundResponse]
	sendOpts atomic.Pointer[SendResponseOptions]
}

func newServerTransact(
	typ TransactionType,
	impl serverTransactImpl,
	req *InboundRequest,
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
		if err := key.FillFromMessage(req); err != nil {
			return nil, errtrace.Wrap(NewInvalidArgumentError(err))
		}
	}

	tx := &serverTransact{
		key:     key,
		tp:      tp,
		timings: opts.timings(),
		req:     req,
	}
	ctx := context.WithValue(context.Background(), srvTransactCtxKey, impl)
	tx.baseTransact = newBaseTransact(ctx, typ, impl, opts.log())
	return tx, nil
}

type serverTransactImpl interface {
	transactImpl
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
		return zeroSrvTxKey
	}
	return tx.key
}

// Request returns the initial request that started this transaction.
func (tx *serverTransact) Request() *InboundRequest {
	if tx == nil {
		return nil
	}
	return tx.req
}

// LastResponse returns the last response sent by the transaction.
func (tx *serverTransact) LastResponse() *OutboundResponse {
	if tx == nil {
		return nil
	}
	return tx.lastRes.Load()
}

// MatchRequest checks whether the request matches the server transaction.
// It implements the matching rules defined in RFC 3261 section 17.2.3.
func (tx *serverTransact) MatchRequest(req *InboundRequest) error {
	var reqKey ServerTransactionKey
	if err := reqKey.FillFromMessage(req); err != nil {
		return errtrace.Wrap(NewInvalidArgumentError(err))
	}

	txKey := tx.key
	if v, ok := tx.impl.(interface {
		adjustKeys(txKey, reqKey *ServerTransactionKey, req *InboundRequest)
	}); ok {
		v.adjustKeys(&txKey, &reqKey, req)
	}

	if !txKey.Equal(reqKey) {
		return errtrace.Wrap(ErrTransactionNotMatched)
	}
	return nil
}

// RecvRequest is called on each inbound request received by the transport layer.
func (tx *serverTransact) RecvRequest(ctx context.Context, req *InboundRequest) error {
	if err := tx.MatchRequest(req); err != nil {
		return errtrace.Wrap(err)
	}

	if v, ok := tx.impl.(interface {
		recvReq(ctx context.Context, req *InboundRequest) error
	}); ok {
		return errtrace.Wrap(v.recvReq(ctx, req))
	}
	return errtrace.Wrap(tx.recvReq(ctx, req))
}

func (tx *serverTransact) recvReq(ctx context.Context, req *InboundRequest) error {
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
	if err := res.msg.Validate(); err != nil {
		return errtrace.Wrap(err)
	}

	switch {
	case res.msg.Status.IsProvisional():
		return errtrace.Wrap(tx.fsm.FireCtx(ctx, txEvtSend1xx, res, opts.sendOpts()))
	case res.msg.Status.IsSuccessful():
		return errtrace.Wrap(tx.fsm.FireCtx(ctx, txEvtSend2xx, res, opts.sendOpts()))
	default:
		return errtrace.Wrap(tx.fsm.FireCtx(ctx, txEvtSend300699, res, opts.sendOpts()))
	}
}

func (tx *serverTransact) sendRes(ctx context.Context, res *OutboundResponse, opts *SendResponseOptions) error {
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

	tx.fsm.SetTriggerParameters(txEvtRecvReq, reflect.TypeOf((*InboundRequest)(nil)))
	tx.fsm.SetTriggerParameters(txEvtSend1xx,
		reflect.TypeOf((*OutboundResponse)(nil)),
		reflect.TypeOf((*SendResponseOptions)(nil)),
	)
	tx.fsm.SetTriggerParameters(txEvtSend2xx,
		reflect.TypeOf((*OutboundResponse)(nil)),
		reflect.TypeOf((*SendResponseOptions)(nil)),
	)
	tx.fsm.SetTriggerParameters(txEvtSend300699,
		reflect.TypeOf((*OutboundResponse)(nil)),
		reflect.TypeOf((*SendResponseOptions)(nil)),
	)

	return nil
}

func (tx *serverTransact) actSendRes(ctx context.Context, args ...any) error {
	res := args[0].(*OutboundResponse)     //nolint:forcetypeassert
	opts := args[1].(*SendResponseOptions) //nolint:forcetypeassert
	defer func() {
		tx.lastRes.Store(res)
		tx.sendOpts.Store(cloneSendResOpts(opts))
	}()

	tx.log.LogAttrs(ctx, slog.LevelDebug, "send response", slog.Any("transaction", tx.impl), slog.Any("response", res))

	tx.sendRes(ctx, res, opts) //nolint:errcheck
	return nil
}

func (tx *serverTransact) actResendRes(ctx context.Context, _ ...any) error {
	res := tx.LastResponse()
	if res == nil {
		return nil
	}
	opts := tx.sendOpts.Load()

	tx.log.LogAttrs(ctx, slog.LevelDebug, "re-send response", slog.Any("transaction", tx.impl), slog.Any("response", res))

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
	Request *InboundRequest `json:"request"`
	// LastResponse is the last response sent by the transaction.
	LastResponse *OutboundResponse `json:"last_response,omitempty"`
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
	// CSeqNum is the CSeq number of the request that created the transaction.
	// RFC 2543 transactions.
	CSeqNum uint `json:"cseq_num,omitempty"`
	// Topmost Via header field of the request that created the transaction.
	// RFC 2543 transactions.
	Via string `json:"via,omitempty"`
}

var zeroSrvTxKey ServerTransactionKey

// FillFromMessage populates the key fields from the given message.
func (k *ServerTransactionKey) FillFromMessage(msg Message) error {
	if msg == nil {
		return errtrace.Wrap(NewInvalidArgumentError("invalid message"))
	}
	if err := msg.Validate(); err != nil {
		return errtrace.Wrap(NewInvalidArgumentError(err))
	}

	hdrs := GetMessageHeaders(msg)
	via, _ := hdrs.FirstVia()
	cseq, _ := hdrs.CSeq()

	if branch, _ := via.Branch(); IsRFC3261Branch(branch) {
		k.Branch = branch
		k.SentBy = util.LCase(via.Addr.String())
		k.Method = util.UCase(string(cseq.Method))

		if util.EqFold(k.Method, RequestMethodAck) {
			k.Method = string(RequestMethodInvite)
		}

		return nil
	}

	// RFC 2543 can match only requests
	var (
		ruri URI
		rmtd RequestMethod
	)
	switch m := msg.(type) {
	case *Request:
		ruri = m.URI
		rmtd = m.Method
	case interface {
		Method() RequestMethod
		URI() URI
	}:
		ruri = m.URI()
		rmtd = m.Method()
	default:
		return errtrace.Wrap(NewInvalidArgumentError("unexpected message type %T", msg))
	}

	return errtrace.Wrap(k.fillFromRequestRFC2543(rmtd, ruri, hdrs))
}

func (k *ServerTransactionKey) fillFromRequestRFC2543(rmtd RequestMethod, ruri URI, hdrs Headers) error {
	via, _ := hdrs.FirstVia()
	k.Via = util.LCase(via.String())
	k.URI = util.LCase(ruri.Render(nil))

	from, _ := hdrs.From()
	k.FromTag, _ = from.Tag()
	if k.FromTag == "" {
		return errtrace.Wrap(NewInvalidArgumentError("missing From tag"))
	}

	to, _ := hdrs.To()
	k.ToTag, _ = to.Tag()
	if k.ToTag == "" && !rmtd.Equal(RequestMethodInvite) {
		return errtrace.Wrap(NewInvalidArgumentError("missing To tag"))
	}

	callID, _ := hdrs.CallID()
	k.CallID = string(callID)

	cseq, _ := hdrs.CSeq()
	k.Method = util.UCase(string(cseq.Method))
	k.CSeqNum = cseq.SeqNum

	if util.EqFold(k.Method, RequestMethodAck) {
		k.Method = string(RequestMethodInvite)
		k.ToTag = ""
	}

	return nil
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
		k.CSeqNum == other.CSeqNum &&
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
		(util.EqFold(k.Method, RequestMethodInvite) || k.ToTag != "") &&
		k.CallID != "" &&
		k.CSeqNum > 0 &&
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
		k.CSeqNum == 0 &&
		k.Via == ""
}

// LogValue returns a slog.Value for the key.
func (k ServerTransactionKey) LogValue() slog.Value {
	if IsRFC3261Branch(k.Branch) {
		return slog.GroupValue(
			slog.Any("branch", k.Branch),
			slog.Any("sent-by", k.SentBy),
			slog.Any("method", k.Method),
		)
	}
	return slog.GroupValue(
		slog.Any("method", k.Method),
		slog.Any("uri", k.URI),
		slog.Any("from-tag", k.FromTag),
		slog.Any("to-tag", k.ToTag),
		slog.Any("call-id", k.CallID),
		slog.Any("cseq-num", k.CSeqNum),
		slog.Any("via", k.Via),
	)
}

const (
	srvTxKeyHashRFC3261 byte = 1
	srvTxKeyHashRFC2543 byte = 2
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
		return k.marshalRFC3261(), nil
	}
	return k.marshalRFC2543(), nil
}

func (k ServerTransactionKey) marshalRFC3261() []byte {
	sentBy := util.LCase(k.SentBy)
	method := util.UCase(k.Method)

	size := 1 +
		util.SizePrefixedString(k.Branch) +
		util.SizePrefixedString(sentBy) +
		util.SizePrefixedString(method)

	buf := make([]byte, 0, size)
	buf = append(buf, srvTxKeyHashRFC3261)
	buf = util.AppendPrefixedString(buf, k.Branch)
	buf = util.AppendPrefixedString(buf, sentBy)
	buf = util.AppendPrefixedString(buf, method)
	return buf
}

func (k ServerTransactionKey) marshalRFC2543() []byte {
	method := util.UCase(k.Method)
	uri := util.LCase(k.URI)
	via := util.LCase(k.Via)

	size := 1 +
		util.SizePrefixedString(method) +
		util.SizePrefixedString(uri) +
		util.SizePrefixedString(k.FromTag) +
		util.SizePrefixedString(k.ToTag) +
		util.SizePrefixedString(k.CallID) +
		util.SizeUVarInt(uint64(k.CSeqNum)) +
		util.SizePrefixedString(via)

	buf := make([]byte, 0, size)
	buf = append(buf, srvTxKeyHashRFC2543)
	buf = util.AppendPrefixedString(buf, method)
	buf = util.AppendPrefixedString(buf, uri)
	buf = util.AppendPrefixedString(buf, k.FromTag)
	buf = util.AppendPrefixedString(buf, k.ToTag)
	buf = util.AppendPrefixedString(buf, k.CallID)
	buf = util.AppendUVarInt(buf, uint64(k.CSeqNum))
	buf = util.AppendPrefixedString(buf, via)
	return buf
}

// UnmarshalBinary populates the key fields from a binary representation produced by [ServerTransactionKey.MarshalBinary].
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
	case srvTxKeyHashRFC3261:
		if key.Branch, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return errtrace.Wrap(err)
		}
		if key.SentBy, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return errtrace.Wrap(err)
		}
		if key.Method, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return errtrace.Wrap(err)
		}
	case srvTxKeyHashRFC2543:
		if key.Method, rest, err = util.ConsumePrefixedString(rest); err != nil {
			return errtrace.Wrap(err)
		}
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
		var cseq uint64
		if cseq, rest, err = util.ConsumeUVarInt(rest); err != nil {
			return errtrace.Wrap(err)
		}
		key.CSeqNum = uint(cseq)
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

func GetServerTransactionKey(tx ServerTransaction) (ServerTransactionKey, bool) {
	if v, ok := tx.(interface{ Key() ServerTransactionKey }); ok {
		return v.Key(), true
	}
	return zeroSrvTxKey, false
}
