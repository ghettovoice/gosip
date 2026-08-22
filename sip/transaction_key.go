package sip

import (
	"context"
	"encoding/hex"
	"fmt"
	"iter"
	"log/slog"
	"strconv"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip/header"
)

// ClientTransactionKey is the key of a client transaction.
// It is used for matching responses to the request that created the transaction.
type ClientTransactionKey struct {
	// Branch parameter of the topmost Via header field.
	Branch string `json:"branch"`
	// Method of the request that created the transaction.
	Method string `json:"method"`
}

// ClientTransactionKeyFromMessage creates a client transaction key from the given message.
func ClientTransactionKeyFromMessage(msg Message) (ClientTransactionKey, error) {
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
		return ClientTransactionKey{}, errors.Wrap(newInvalidMsgErr("invalid Via branch"))
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

type ClientTransactionStore interface {
	Load(ctx context.Context, key ClientTransactionKey) (ClientTransaction, error)
	MatchMessage(ctx context.Context, msg Message) (ClientTransaction, error)
	Store(ctx context.Context, tx ClientTransaction) error
	Delete(ctx context.Context, tx ClientTransaction) error
	LoadAll(ctx context.Context) (iter.Seq[ClientTransaction], error)
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
	key, err := ClientTransactionKeyFromMessage(msg)
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
	if actual, loaded := s.main.LoadOrStore(tx.Key(), tx); loaded && actual != tx {
		return errors.Wrap(ErrDuplicateTransaction)
	}
	return nil
}

func (s *MemoryClientTransactionStore) Delete(_ context.Context, tx ClientTransaction) error {
	s.main.Delete(tx.Key())
	return nil
}

func (s *MemoryClientTransactionStore) LoadAll(_ context.Context) (iter.Seq[ClientTransaction], error) {
	return util.SeqValues(s.main.All()), nil
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

// ServerTransactionKeyFromMessage builds server transaction key from the given message.
func ServerTransactionKeyFromMessage(msg Message) (ServerTransactionKey, error) {
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
	if cseq.Method.Equal(RequestMethodAck) {
		k.Method = string(RequestMethodInvite)
	} else {
		k.Method = string(cseq.Method.ToUpper())
	}

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
		return ServerTransactionKey{}, errors.Wrap(newInvalidMsgErr("missing From tag"))
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

type ServerTransactionStore interface {
	Load(ctx context.Context, key ServerTransactionKey) (ServerTransaction, error)
	MatchMessage(ctx context.Context, msg Message) (ServerTransaction, error)
	LookupMerged(ctx context.Context, key ServerTransactionKey) (ServerTransaction, error)
	Store(ctx context.Context, tx ServerTransaction) error
	Delete(ctx context.Context, tx ServerTransaction) error
	LoadAll(ctx context.Context) (iter.Seq[ServerTransaction], error)
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
	key, err := ServerTransactionKeyFromMessage(msg)
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
	if actual, loaded := s.main.LoadOrStore(key, tx); loaded {
		if actual != tx {
			return errors.Wrap(ErrDuplicateTransaction)
		}
		return nil
	}
	s.merged.Store(ServerTransactionKey{
		FromTag: key.FromTag,
		CallID:  key.CallID,
		SeqNum:  key.SeqNum,
		Method:  key.Method,
	}, tx)
	return nil
}

func (s *MemoryServerTransactionStore) Delete(_ context.Context, tx ServerTransaction) error {
	key := tx.Key()
	s.main.Delete(key)
	s.merged.Delete(ServerTransactionKey{
		FromTag: key.FromTag,
		CallID:  key.CallID,
		SeqNum:  key.SeqNum,
		Method:  key.Method,
	})
	return nil
}

func (s *MemoryServerTransactionStore) LoadAll(_ context.Context) (iter.Seq[ServerTransaction], error) {
	return util.SeqValues(s.main.All()), nil
}
