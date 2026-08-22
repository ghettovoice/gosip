package sip

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/netutil"
	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

const (
	DefaultMaxForwards header.MaxForwards = 70
)

// Message errors.
const (
	ErrInvalidMessage    Error = "invalid message"
	ErrEntityTooLarge    Error = "entity too large"
	ErrMessageTooLarge   Error = "message too large"
	ErrMethodNotAllowed  Error = "method not allowed"
	ErrMessageNotMatched Error = "message not matched"
	ErrUnhandledMessage  Error = "unhandled message"
)

// IsMessageError reports whether err belongs to the message error class.
func IsMessageError(err error) bool {
	return errors.Is(err, ErrClassMessage)
}

// Message represents a SIP message.
type Message interface {
	types.Renderer
	types.Cloneable[Message]
	types.Validatable
	types.ValidFlag
	types.Equalable
}

// ParseMessage parses a SIP message from a byte sequence.
func ParseMessage[T ~string | ~[]byte](s T) (msg Message, err error) {
	node, err := grammar.ParseMessage(s)
	if err != nil {
		return nil, errors.Wrap(err)
	}
	return errors.Wrap2(MessageFromABNF(node))
}

func MessageFromABNF(node *abnf.Node) (msg Message, err error) {
	defer func() {
		if rv := recover(); rv != nil {
			msg = nil
			if e, ok := rv.(error); ok {
				err = errors.Wrap(e)
			} else {
				err = errors.ErrorfWrap("%v", rv)
			}
		}
	}()

	return buildFromMessageNode(node), nil
}

func buildFromMessageNode(node *abnf.Node) Message {
	if n, ok := node.GetNode("Request"); ok {
		return buildFromRequestNode(n)
	}

	if n, ok := node.GetNode("Response"); ok {
		return buildFromResponseNode(n)
	}

	panic(errors.PrefixWrap(grammar.ErrUnexpectedNode, `node %q doesn't have "Request" or "Response" node`, node.Key))
}

func buildFromRequestNode(node *abnf.Node) *Request {
	var body []byte
	if n, ok := node.GetNode("message-body"); ok {
		body = n.Value
	}
	return &Request{
		Method:  RequestMethod(grammar.MustGetNode(node, "Method").String()),
		URI:     util.Must2(uri.FromABNF(grammar.MustGetNode(node, "Request-URI").Children[0])),
		Proto:   buildFromSIPVersionNode(grammar.MustGetNode(node, "SIP-Version")),
		Headers: buildFromMessageHeaderNodes(node.GetNodes("message-header")),
		Body:    body,
	}
}

func buildFromResponseNode(node *abnf.Node) *Response {
	code, err := strconv.ParseUint(grammar.MustGetNode(node, "Status-Code").String(), 10, 16)
	if err != nil {
		panic(errors.ErrorfWrap("invalid response status: %w", err))
	}

	var body []byte
	if n, ok := node.GetNode("message-body"); ok {
		body = n.Value
	}

	return &Response{
		Status:  ResponseStatus(code),
		Reason:  ResponseReason(grammar.MustGetNode(node, "Reason-Phrase").String()),
		Proto:   buildFromSIPVersionNode(grammar.MustGetNode(node, "SIP-Version")),
		Headers: buildFromMessageHeaderNodes(node.GetNodes("message-header")),
		Body:    body,
	}
}

func buildFromSIPVersionNode(node *abnf.Node) ProtoInfo {
	var version strings.Builder
	for _, n := range node.Children[2:] {
		version.WriteString(n.String())
	}
	return ProtoInfo{Name: node.Children[0].String(), Version: version.String()}
}

func buildFromMessageHeaderNodes(nodes abnf.Nodes) Headers {
	if len(nodes) == 0 {
		return nil
	}

	hdrs := make(Headers)
	for _, node := range nodes {
		hdrs.Append(util.Must2(header.FromABNF(node.Children[0].Children[0])))
	}
	return hdrs
}

func parseMsgStart[T ~string | ~[]byte](src T) (Message, error) {
	node, err := grammar.ParseMessageStart(src)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	if n, ok := node.GetNode("Request-Line"); ok {
		return buildFromRequestNode(n), nil
	}

	if n, ok := node.GetNode("Status-Line"); ok {
		return buildFromResponseNode(n), nil
	}

	panic(errors.PrefixWrap(grammar.ErrUnexpectedNode, `node %q doesn't have "Request-Line" or "Status-Line" node`, node.Key))
}

// GetMessageHeaders returns the headers of the given message.
// Message expected to be a [Request], [Response] or implement interface { Headers() Headers }.
// If msg does not match any supported type, it returns nil and false.
func GetMessageHeaders(msg Message) (Headers, bool) {
	switch m := msg.(type) {
	case *Request:
		if m == nil {
			return nil, true
		}
		return m.Headers, true
	case *Response:
		if m == nil {
			return nil, true
		}
		return m.Headers, true
	case interface{ Headers() Headers }:
		if m == nil {
			return nil, true
		}
		return m.Headers(), true
	default:
		return nil, false
	}
}

// SetMessageHeaders sets the headers of the given message.
// Message expected to be a [Request], [Response],
// implement interface { WithMessage(func(*Request)) } or interface { WithMessage(func(*Response)) },
// otherwise it returns an error.
func SetMessageHeaders(msg Message, hdrs Headers) error {
	switch m := msg.(type) {
	case *Request:
		m.Headers = hdrs
		return nil
	case *Response:
		m.Headers = hdrs
		return nil
	case interface{ WithMessage(u func(*Request)) }:
		m.WithMessage(func(r *Request) { r.Headers = hdrs })
		return nil
	case interface{ WithMessage(u func(*Response)) }:
		m.WithMessage(func(r *Response) { r.Headers = hdrs })
		return nil
	default:
		return errors.Wrap(newUnexpectMsgTypeErr(msg))
	}
}

// GetMessageBody returns the body of the given message.
// Message expected to be a [Request], [Response] or implement interface { Body() []byte }.
// If msg does not match any supported type, it returns nil and false.
func GetMessageBody(msg Message) ([]byte, bool) {
	switch m := msg.(type) {
	case *Request:
		if m == nil {
			return nil, true
		}
		return m.Body, true
	case *Response:
		if m == nil {
			return nil, true
		}
		return m.Body, true
	case interface{ Body() []byte }:
		if m == nil {
			return nil, true
		}
		return m.Body(), true
	default:
		return nil, false
	}
}

// SetMessageBody sets the body of the given message.
// Message expected to be a [Request], [Response],
// implement interface { WithMessage(func(*Request)) } or interface { WithMessage(func(*Response)) },
// otherwise it returns an error.
func SetMessageBody(msg Message, body []byte) error {
	switch m := msg.(type) {
	case *Request:
		m.Body = body
		return nil
	case *Response:
		m.Body = body
		return nil
	case interface{ WithMessage(u func(*Request)) }:
		m.WithMessage(func(r *Request) { r.Body = body })
		return nil
	case interface{ WithMessage(u func(*Response)) }:
		m.WithMessage(func(r *Response) { r.Body = body })
		return nil
	default:
		return errors.Wrap(newUnexpectMsgTypeErr(msg))
	}
}

func newMissHdrErr(name HeaderName) error {
	if name == "" {
		return errors.Error("missing mandatory headers")
	}
	return errors.Errorf("missing mandatory header %q", name)
}

func newInvalidMsgErr(args ...any) error {
	return errors.Prefix(ErrInvalidMessage, args...)
}

func newUnexpectMsgTypeErr(msg Message) error {
	return errors.Errorf("unexpected message type %T", msg)
}

func EnsureMessageContentLength(msg Message) {
	hdrs, ok := GetMessageHeaders(msg)
	if !ok {
		return
	}

	if _, ok := hdrs.ContentLength(); ok {
		return
	}

	body, ok := GetMessageBody(msg)
	if !ok || len(body) == 0 {
		return
	}

	hdrs.Set(header.ContentLength(len(body)))
}

// MessageMetadata is a thread-safe key-value store for arbitrary data.
// It wraps [syncutil.RWMap] with JSON serialization support.
// Values in the metadata expected to be serializable to JSON.
type MessageMetadata struct {
	data syncutil.RWMap[string, any]
}

func (d *MessageMetadata) Get(key string) (any, bool) {
	if d == nil {
		return nil, false
	}
	return d.data.Load(key)
}

func (d *MessageMetadata) Set(key string, val any) *MessageMetadata {
	d.data.Store(key, val)
	return d
}

func (d *MessageMetadata) SetAll(vals iter.Seq2[string, any]) *MessageMetadata {
	d.data.BulkStore(vals)
	return d
}

func (d *MessageMetadata) Delete(key string, keys ...string) *MessageMetadata {
	d.data.Delete(key, keys...)
	return d
}

func (d *MessageMetadata) Has(key string) bool {
	if d == nil {
		return false
	}
	return d.data.Has(key)
}

func (d *MessageMetadata) HasAll(keys ...string) bool {
	if d == nil {
		return false
	}
	return d.data.HasAll(keys...)
}

func (d *MessageMetadata) HasAny(keys ...string) bool {
	if d == nil {
		return false
	}
	return d.data.HasAny(keys...)
}

func (d *MessageMetadata) Len() int {
	if d == nil {
		return 0
	}
	return d.data.Len()
}

func (d *MessageMetadata) Clear() *MessageMetadata {
	if d == nil {
		return d
	}

	d.data.Clear()
	return d
}

func (d *MessageMetadata) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		if d == nil {
			return
		}

		for k, v := range d.data.All() {
			if !yield(k, v) {
				break
			}
		}
	}
}

func (d *MessageMetadata) MarshalJSON() ([]byte, error) {
	if d == nil {
		return jsonNull, nil
	}
	return errors.Wrap2(json.Marshal(maps.Collect(d.All())))
}

func (d *MessageMetadata) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return errors.Wrap(err)
	}

	d.Clear().SetAll(maps.All(raw))
	return nil
}

// Clone returns a deep copy of the MessageMetadata.
func (d *MessageMetadata) Clone() *MessageMetadata {
	if d == nil {
		return nil
	}

	clone := &MessageMetadata{}
	clone.data.CopyFrom(&d.data)
	return clone
}

type MessageEnvelope[T Message] struct {
	msgMu     sync.RWMutex
	msg       T
	msgTime   time.Time
	rendCache *bytes.Buffer
	rendOpts  RenderOptions

	tp           atomic.Value // TransportMetadata
	laddr, raddr atomic.Value // netip.AddrPort

	meta *MessageMetadata
}

func NewMessageEnvelope[T Message](msg T) *MessageEnvelope[T] {
	util.InitIfNil(&msg)
	return &MessageEnvelope[T]{
		msg:     msg,
		msgTime: time.Now(),
		meta:    new(MessageMetadata),
	}
}

func (m *MessageEnvelope[T]) Message() T {
	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	return m.cloneMsg()
}

func (m *MessageEnvelope[T]) cloneMsg() T {
	return m.msg.Clone().(T) //nolint:forcetypeassert
}

func (m *MessageEnvelope[T]) WithMessage(fn func(T)) *MessageEnvelope[T] {
	m.msgMu.Lock()
	defer m.msgMu.Unlock()

	fn(m.msg)
	m.resetRendCache()
	return m
}

func (m *MessageEnvelope[T]) Headers() Headers {
	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	hdrs, ok := GetMessageHeaders(m.msg)
	if !ok {
		return nil
	}
	return hdrs.Clone()
}

func (m *MessageEnvelope[T]) Body() []byte {
	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	body, ok := GetMessageBody(m.msg)
	if !ok {
		return nil
	}
	return slices.Clone(body)
}

func (m *MessageEnvelope[T]) MessageTime() time.Time { return m.msgTime }

func (m *MessageEnvelope[T]) transp() TransportMetadata {
	if tp, ok := m.tp.Load().(TransportMetadata); ok {
		return tp
	}
	return TransportMetadata{}
}

func (m *MessageEnvelope[T]) Transport() TransportMetadata { return m.transp() }

func (m *MessageEnvelope[T]) SetTransport(tp TransportMetadata) *MessageEnvelope[T] {
	m.tp.Store(tp.Canonic())
	return m
}

func (m *MessageEnvelope[T]) locAddr() netip.AddrPort {
	if laddr, ok := m.laddr.Load().(netip.AddrPort); ok {
		return laddr
	}
	return netip.AddrPort{}
}

func (m *MessageEnvelope[T]) LocalAddr() netip.AddrPort { return m.locAddr() }

func (m *MessageEnvelope[T]) SetLocalAddr(addr netip.AddrPort) *MessageEnvelope[T] {
	m.laddr.Store(netutil.UnmapAddrPort(addr))
	return m
}

func (m *MessageEnvelope[T]) rmtAddr() netip.AddrPort {
	if raddr, ok := m.raddr.Load().(netip.AddrPort); ok {
		return raddr
	}
	return netip.AddrPort{}
}

func (m *MessageEnvelope[T]) RemoteAddr() netip.AddrPort { return m.rmtAddr() }

func (m *MessageEnvelope[T]) SetRemoteAddr(addr netip.AddrPort) *MessageEnvelope[T] {
	m.raddr.Store(netutil.UnmapAddrPort(addr))
	return m
}

func (m *MessageEnvelope[T]) Metadata() *MessageMetadata { return m.meta }

func (m *MessageEnvelope[T]) RenderTo(w io.Writer, opts ...RenderOptions) (int, error) {
	if m == nil {
		return 0, nil
	}

	m.msgMu.Lock()
	defer m.msgMu.Unlock()

	return errors.Wrap2(m.renderTo(w, opts...))
}

func (m *MessageEnvelope[T]) renderTo(w io.Writer, opts ...RenderOptions) (int, error) {
	if m.rendCache == nil || m.rendCache.Len() == 0 || !types.IsEqual(m.rendOpts, opts) {
		m.resetRendCache()
		m.rendOpts = util.LastSliceElemOr(opts, RenderOptions{})
		if n, err := m.msg.RenderTo(m.rendCache, m.rendOpts); err != nil {
			return n, errors.Wrap(err)
		}
	}

	return errors.Wrap2(w.Write(m.rendCache.Bytes()))
}

func (m *MessageEnvelope[T]) Render(opts ...RenderOptions) string {
	if m == nil {
		return ""
	}

	m.msgMu.Lock()
	defer m.msgMu.Unlock()

	_, _ = m.renderTo(io.Discard, opts...)
	return m.rendCache.String()
}

func (m *MessageEnvelope[T]) resetRendCache() {
	if m.rendCache == nil {
		m.rendCache = bytes.NewBuffer(make([]byte, 0, 1024))
	} else {
		m.rendCache.Reset()
	}
	m.rendOpts = RenderOptions{}
}

func (m *MessageEnvelope[T]) cloneRendCache() *bytes.Buffer {
	if m.rendCache == nil {
		return bytes.NewBuffer(make([]byte, 0, 1024))
	}
	return bytes.NewBuffer(slices.Clone(m.rendCache.Bytes()))
}

func (m *MessageEnvelope[T]) String() string {
	if m == nil {
		return sNilTag
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	return fmt.Sprintf("%v", m.msg)
}

func (m *MessageEnvelope[T]) Clone() Message {
	if m == nil {
		return nil
	}

	m.msgMu.Lock()
	clone := &MessageEnvelope[T]{
		msg:       m.cloneMsg(),
		msgTime:   time.Now(),
		meta:      m.meta.Clone(),
		rendCache: m.cloneRendCache(),
		rendOpts:  m.rendOpts,
	}
	m.msgMu.Unlock()

	clone.tp.Store(m.transp())
	clone.laddr.Store(m.locAddr())
	clone.raddr.Store(m.rmtAddr())
	return clone
}

func (m *MessageEnvelope[T]) Equal(val any) bool {
	var other *MessageEnvelope[T]
	switch v := val.(type) {
	case MessageEnvelope[T]:
		other = &v
	case *MessageEnvelope[T]:
		other = v
	default:
		return false
	}

	if m == other {
		return true
	} else if m == nil || other == nil {
		return false
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	other.msgMu.RLock()
	defer other.msgMu.RUnlock()

	return m.msg.Equal(other.msg)
}

func (m *MessageEnvelope[T]) IsValid() bool {
	if m == nil {
		return false
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	return m.msg.IsValid()
}

func (m *MessageEnvelope[T]) Validate() error {
	if m == nil {
		return errors.ErrorWrap("nil message envelope")
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	return errors.Wrap(m.msg.Validate())
}

type messageEnvelopeData[T Message] struct {
	Message     T                 `json:"message"`
	MessageTime time.Time         `json:"message_time"`
	Transport   TransportMetadata `json:"transport,omitzero"`
	LocalAddr   netip.AddrPort    `json:"local_addr,omitzero"`
	RemoteAddr  netip.AddrPort    `json:"remote_addr,omitzero"`
	Metadata    *MessageMetadata  `json:"metadata,omitempty"`
}

func (m *MessageEnvelope[T]) MarshalJSON() ([]byte, error) {
	if m == nil {
		return jsonNull, nil
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	return errors.Wrap2(json.Marshal(messageEnvelopeData[T]{
		Message:     m.msg,
		MessageTime: m.msgTime,
		Transport:   m.transp(),
		LocalAddr:   m.locAddr(),
		RemoteAddr:  m.rmtAddr(),
		Metadata:    m.meta,
	}))
}

func (m *MessageEnvelope[T]) UnmarshalJSON(data []byte) error {
	var dto messageEnvelopeData[T]
	if err := json.Unmarshal(data, &dto); err != nil {
		return errors.Wrap(err)
	}

	m.msgMu.Lock()
	m.msg = dto.Message
	util.InitIfNil(&m.msg)

	m.msgTime = dto.MessageTime
	if m.msgTime.IsZero() {
		m.msgTime = time.Now()
	}

	m.meta = dto.Metadata
	if m.meta == nil {
		m.meta = new(MessageMetadata)
	}

	m.resetRendCache()
	m.msgMu.Unlock()

	m.SetTransport(dto.Transport).
		SetLocalAddr(dto.LocalAddr).
		SetRemoteAddr(dto.RemoteAddr)

	return nil
}

func (m *MessageEnvelope[T]) LogValue() slog.Value {
	if m == nil {
		return slog.Value{}
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	return slog.GroupValue(
		slog.Any("message", m.cloneMsg()),
		slog.Time("message_time", m.msgTime),
		slog.Any("transport", m.transp()),
		slog.Any("local_addr", m.locAddr()),
		slog.Any("remote_addr", m.rmtAddr()),
	)
}

func (m *MessageEnvelope[T]) UpdateFrom(other *MessageEnvelope[T]) *MessageEnvelope[T] {
	m.msgMu.Lock()
	other.msgMu.RLock()
	m.msg = other.cloneMsg()
	m.msgTime = other.msgTime
	m.rendCache = other.cloneRendCache()
	m.rendOpts = other.rendOpts
	m.msgMu.Unlock()
	other.msgMu.RUnlock()

	m.tp.Store(other.transp())
	m.laddr.Store(other.locAddr())
	m.raddr.Store(other.rmtAddr())

	m.meta = other.meta.Clone()

	return m
}

// GenerateTag generates a tag to be used in From/To headers.
// Tag is a random string of specified length.
// If length is not specified, it defaults to 8.
func GenerateTag(length uint) string {
	l := 8
	if length > 0 {
		l = int(length)
	}
	return util.RandStringLC(l)
}

func GenerateStableToTag(msg Message, salt []byte) string {
	if key, err := ServerTransactionKeyFromMessage(msg); err == nil {
		if buf, err := key.MarshalBinary(); err == nil {
			buf = append(buf, salt...)
			sum := sha256.Sum256(buf)
			return hex.EncodeToString(sum[:16])
		}
	}

	var buf []byte
	hdrs, _ := GetMessageHeaders(msg)

	if callID, ok := hdrs.CallID(); ok {
		buf = append(buf, callID...)
	}

	if from, ok := hdrs.From(); ok && from != nil {
		if t, ok := from.Tag(); ok {
			buf = append(buf, t...)
		}
	}

	if via, ok := hdrs.FirstVia(); ok && via != nil {
		buf = append(buf, util.LCase(via.String())...)
	}

	buf = append(buf, salt...)
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:16])
}

// GenerateCallID generates a Call-ID.
// Call-ID is a random string of specified length or 16 if not specified.
// If host is provided, it is appended after "@".
func GenerateCallID(length uint, host string) string {
	l := 16
	if length > 0 {
		l = int(length)
	}
	if len(host) > 0 {
		return util.RandStringLC(l) + "@" + host
	}
	return util.RandStringLC(l)
}

// MagicCookie is a constant string defined in RFC 3261.
// It is used as a prefix for a branch in Via header.
const MagicCookie = "z9hG4bK"

// IsRFC3261Branch checks whether a branch is a valid RFC 3261 branch.
func IsRFC3261Branch(branch string) bool {
	return len(branch) > len(MagicCookie) && branch[:len(MagicCookie)] == MagicCookie
}

// GenerateBranch generates a branch for a Via header.
// Branch is a random string of specified length or 16 if not specified.
func GenerateBranch(length uint) string {
	l := 16
	if length > 0 {
		l = int(length)
	}
	return MagicCookie + "." + util.RandStringLC(l)
}
