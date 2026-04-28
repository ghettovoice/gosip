package sip

import (
	"bytes"
	"encoding/json"
	"io"
	"iter"
	"log/slog"
	"maps"
	"net/netip"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/uri"
)

// Message errors.
const (
	ErrInvalidMessage        errors.Error = "invalid message"
	ErrEntityTooLarge        errors.Error = "entity too large"
	ErrMessageTooLarge       errors.Error = "message too large"
	ErrMethodNotAllowed      errors.Error = "method not allowed"
	ErrMessageNotMatched     errors.Error = "message not matched"
	ErrUnhandledMessage      errors.Error = "unhandled message"
	ErrNoDestAddressResolved errors.Error = "no destination address resolved"
)

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

	node, err := grammar.ParseMessage(s)
	if err != nil {
		return nil, errors.Wrap(err)
	}

	return buildFromMessageNode(node), nil
}

func buildFromMessageNode(node *abnf.Node) Message {
	if n, ok := node.GetNode("Request"); ok {
		return buildFromRequestNode(n)
	}

	if n, ok := node.GetNode("Response"); ok {
		return buildFromResponseNode(n)
	}

	panic(errors.Wrap(grammar.ErrUnexpectNode))
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
	code, _ := strconv.ParseUint(grammar.MustGetNode(node, "Status-Code").String(), 10, 16)

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

	panic(errors.Wrap(grammar.ErrUnexpectNode))
}

// GetMessageHeaders returns the headers of the given message.
// Message expected to be a [Request], [Response] or implement interface { Headers() Headers },
// otherwise it returns nil.
func GetMessageHeaders(msg Message) Headers {
	switch m := msg.(type) {
	case *Request:
		if m == nil {
			return nil
		}
		return m.Headers
	case *Response:
		if m == nil {
			return nil
		}
		return m.Headers
	case interface{ Headers() Headers }:
		if m == nil {
			return nil
		}
		return m.Headers()
	default:
		return nil
	}
}

// SetMessageHeaders sets the headers of the given message.
// Message expected to be a [Request], [Response],
// implement interface { AccessMessage(func(*Request)) } or interface { AccessMessage(func(*Response)) },
// otherwise it returns an error.
func SetMessageHeaders(msg Message, hdrs Headers) error {
	switch m := msg.(type) {
	case *Request:
		m.Headers = hdrs
		return nil
	case *Response:
		m.Headers = hdrs
		return nil
	case interface{ AccessMessage(u func(*Request)) }:
		m.AccessMessage(func(r *Request) { r.Headers = hdrs })
		return nil
	case interface{ AccessMessage(u func(*Response)) }:
		m.AccessMessage(func(r *Response) { r.Headers = hdrs })
		return nil
	default:
		return newUnexpectMsgTypeErrWrap(msg)
	}
}

// GetMessageBody returns the body of the given message.
// Message expected to be a [Request], [Response] or implement interface { Body() []byte },
// otherwise it returns nil.
func GetMessageBody(msg Message) []byte {
	switch m := msg.(type) {
	case *Request:
		if m == nil {
			return nil
		}
		return m.Body
	case *Response:
		if m == nil {
			return nil
		}
		return m.Body
	case interface{ Body() []byte }:
		if m == nil {
			return nil
		}
		return m.Body()
	default:
		return nil
	}
}

// SetMessageBody sets the body of the given message.
// Message expected to be a [Request], [Response],
// implement interface { AccessMessage(func(*Request)) } or interface { AccessMessage(func(*Response)) },
// otherwise it returns an error.
func SetMessageBody(msg Message, body []byte) error {
	switch m := msg.(type) {
	case *Request:
		m.Body = body
		return nil
	case *Response:
		m.Body = body
		return nil
	case interface{ AccessMessage(u func(*Request)) }:
		m.AccessMessage(func(r *Request) { r.Body = body })
		return nil
	case interface{ AccessMessage(u func(*Response)) }:
		m.AccessMessage(func(r *Response) { r.Body = body })
		return nil
	default:
		return newUnexpectMsgTypeErrWrap(msg)
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

func newUnexpectMsgTypeErrWrap(msg Message) error {
	return errors.GetCaller().Wrap(newUnexpectMsgTypeErr(msg))
}

// MessageMetadata is a thread-safe key-value store for arbitrary data.
// It wraps [syncutil.RWMap] with JSON serialization support.
// Values in the metadata expected to be serializable to JSON.
type MessageMetadata struct {
	data syncutil.RWMap[string, any]
}

func MessageMetadataFromValues(vals iter.Seq2[string, any]) *MessageMetadata {
	var d MessageMetadata
	for k, v := range vals {
		d.Set(k, v)
	}

	return &d
}

func (d *MessageMetadata) Populate(vals iter.Seq2[string, any]) *MessageMetadata {
	for k, v := range vals {
		d.Set(k, v)
	}
	return d
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

func (d *MessageMetadata) Delete(key string) *MessageMetadata {
	d.data.Delete(key)
	return d
}

func (d *MessageMetadata) Has(key string) bool {
	if d == nil {
		return false
	}
	return d.data.Has(key)
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
	if d == nil {
		return errors.NewInvalidArgumentErrorWrap("nil metadata")
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return errors.Wrap(err)
	}

	for k, v := range raw {
		d.Set(k, v)
	}

	return nil
}

// Clone returns a deep copy of the MessageMetadata.
func (d *MessageMetadata) Clone() *MessageMetadata {
	if d == nil {
		return nil
	}

	clone := &MessageMetadata{}
	d.data.CopyTo(&clone.data)

	return clone
}

type messageEnvelope[T Message] struct {
	msg          T
	msgBuf       bytes.Buffer
	msgTime      time.Time
	tp           atomic.Value // TransportProto
	laddr, raddr atomic.Value // netip.AddrPort
	meta         *MessageMetadata
}

func (m *messageEnvelope[T]) Message() T {
	if m == nil {
		var zero T
		return zero
	}

	return m.msg.Clone().(T) //nolint:forcetypeassert
}

func (m *messageEnvelope[T]) Headers() Headers {
	if m == nil {
		return nil
	}
	return GetMessageHeaders(m.msg).Clone()
}

func (m *messageEnvelope[T]) Body() []byte {
	if m == nil {
		return nil
	}
	return slices.Clone(GetMessageBody(m.msg))
}

func (m *messageEnvelope[T]) MessageTime() time.Time {
	if m == nil {
		return time.Time{}
	}
	return m.msgTime
}

func (m *messageEnvelope[T]) transport() TransportProto {
	tp, ok := m.tp.Load().(TransportProto)
	if !ok {
		return ""
	}

	return tp
}

func (m *messageEnvelope[T]) Transport() TransportProto {
	if m == nil {
		return ""
	}
	return m.transport()
}

func (m *messageEnvelope[T]) localAddr() netip.AddrPort {
	laddr, ok := m.laddr.Load().(netip.AddrPort)
	if !ok {
		return netip.AddrPort{}
	}

	return laddr
}

func (m *messageEnvelope[T]) LocalAddr() netip.AddrPort {
	if m == nil {
		return netip.AddrPort{}
	}
	return m.localAddr()
}

func (m *messageEnvelope[T]) remoteAddr() netip.AddrPort {
	raddr, ok := m.raddr.Load().(netip.AddrPort)
	if !ok {
		return netip.AddrPort{}
	}

	return raddr
}

func (m *messageEnvelope[T]) RemoteAddr() netip.AddrPort {
	if m == nil {
		return netip.AddrPort{}
	}
	return m.remoteAddr()
}

func (m *messageEnvelope[T]) Metadata() *MessageMetadata {
	if m == nil {
		return nil
	}
	return m.meta
}

func (m *messageEnvelope[T]) RenderTo(w io.Writer, opts *RenderOptions) (int, error) {
	if m == nil {
		return 0, nil
	}

	if m.msgBuf.Len() == 0 {
		if n, err := m.msg.RenderTo(&m.msgBuf, opts); err != nil {
			return n, errors.Wrap(err)
		}
	}

	return errors.Wrap2(w.Write(m.msgBuf.Bytes()))
}

func (m *messageEnvelope[T]) Render(opts *RenderOptions) string {
	if m == nil {
		return ""
	}

	if m.msgBuf.Len() == 0 {
		m.msg.RenderTo(&m.msgBuf, opts) //nolint:errcheck
	}

	return m.msgBuf.String()
}

func (m *messageEnvelope[T]) Clone() Message {
	if m == nil {
		return nil
	}

	//nolint:forcetypeassert
	m2 := &messageEnvelope[T]{
		msg:     m.msg.Clone().(T),
		msgTime: time.Now(),
		meta:    m.meta.Clone(),
	}
	m2.tp.Store(m.transport())
	m2.laddr.Store(m.localAddr())
	m2.raddr.Store(m.remoteAddr())

	return m2
}

func (m *messageEnvelope[T]) Equal(v any) bool {
	if m == nil {
		return v == nil
	}

	if other, ok := v.(*messageEnvelope[T]); ok {
		return m.msg.Equal(other.msg)
	}

	return false
}

func (m *messageEnvelope[T]) IsValid() bool {
	if m == nil {
		return false
	}
	return m.msg.IsValid()
}

func (m *messageEnvelope[T]) Validate() error {
	if m == nil {
		return errors.NewInvalidArgumentErrorWrap("nil envelope")
	}
	return errors.Wrap(m.msg.Validate())
}

type messageEnvelopeData[T Message] struct {
	Message     T                `json:"message"`
	MessageTime time.Time        `json:"message_time"`
	Transport   TransportProto   `json:"transport"`
	LocalAddr   netip.AddrPort   `json:"local_addr"`
	RemoteAddr  netip.AddrPort   `json:"remote_addr"`
	Metadata    *MessageMetadata `json:"metadata"`
}

func (m *messageEnvelope[T]) MarshalJSON() ([]byte, error) {
	if m == nil {
		return jsonNull, nil
	}

	return errors.Wrap2(json.Marshal(messageEnvelopeData[T]{
		Message:     m.msg,
		MessageTime: m.msgTime,
		Transport:   m.transport(),
		LocalAddr:   m.localAddr(),
		RemoteAddr:  m.remoteAddr(),
		Metadata:    m.meta,
	}))
}

func (m *messageEnvelope[T]) UnmarshalJSON(data []byte) error {
	if m == nil {
		return errors.NewInvalidArgumentErrorWrap("nil envelope")
	}

	var msgData messageEnvelopeData[T]
	if err := json.Unmarshal(data, &msgData); err != nil {
		*m = messageEnvelope[T]{}
		return errors.Wrap(err)
	}

	msgVal := reflect.ValueOf(msgData.Message)
	if k := msgVal.Kind(); !msgVal.IsValid() || (k == reflect.Pointer || k == reflect.Interface) && msgVal.IsNil() {
		*m = messageEnvelope[T]{}
		return errors.NewInvalidArgumentErrorWrap("nil message")
	}

	if msgData.MessageTime.IsZero() {
		msgData.MessageTime = time.Now()
	}

	if msgData.Metadata == nil {
		msgData.Metadata = new(MessageMetadata)
	}

	m.msg = msgData.Message
	m.msgBuf.Reset()
	m.msgTime = msgData.MessageTime
	m.tp.Store(msgData.Transport)
	m.laddr.Store(msgData.LocalAddr)
	m.raddr.Store(msgData.RemoteAddr)
	m.meta = msgData.Metadata

	return nil
}

func (m *messageEnvelope[T]) LogValue() slog.Value {
	if m == nil {
		return slog.Value{}
	}

	return slog.GroupValue(
		slog.Any("message", m.msg.Clone()),
		slog.Any("message_time", m.msgTime),
		slog.Any("transport", m.transport()),
		slog.Any("local_addr", m.localAddr()),
		slog.Any("remote_addr", m.remoteAddr()),
	)
}

type inboundMessageEnvelope[T Message] = messageEnvelope[T]

type outboundMessageEnvelope[T Message] struct {
	*messageEnvelope[T]
	msgMu sync.RWMutex
}

func (m *outboundMessageEnvelope[T]) Message() T {
	if m == nil {
		var zero T
		return zero
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	return m.messageEnvelope.Message()
}

func (m *outboundMessageEnvelope[T]) AccessMessage(fn func(T)) {
	m.msgMu.Lock()
	defer m.msgMu.Unlock()

	fn(m.msg)
	m.msgBuf.Reset()
}

func (m *outboundMessageEnvelope[T]) Headers() Headers {
	if m == nil {
		return nil
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	return m.messageEnvelope.Headers()
}

func (m *outboundMessageEnvelope[T]) Body() []byte {
	if m == nil {
		return nil
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	return m.messageEnvelope.Body()
}

func (m *outboundMessageEnvelope[T]) MessageTime() time.Time {
	if m == nil {
		return time.Time{}
	}
	return m.messageEnvelope.MessageTime()
}

func (m *outboundMessageEnvelope[T]) Transport() TransportProto {
	if m == nil {
		return ""
	}
	return m.messageEnvelope.Transport()
}

func (m *outboundMessageEnvelope[T]) SetTransport(tp TransportProto) {
	m.tp.Store(tp)
}

func (m *outboundMessageEnvelope[T]) LocalAddr() netip.AddrPort {
	if m == nil {
		return netip.AddrPort{}
	}
	return m.localAddr()
}

func (m *outboundMessageEnvelope[T]) SetLocalAddr(addr netip.AddrPort) {
	m.laddr.Store(addr)
}

func (m *outboundMessageEnvelope[T]) RemoteAddr() netip.AddrPort {
	if m == nil {
		return netip.AddrPort{}
	}
	return m.remoteAddr()
}

func (m *outboundMessageEnvelope[T]) SetRemoteAddr(addr netip.AddrPort) {
	m.raddr.Store(addr)
}

func (m *outboundMessageEnvelope[T]) Metadata() *MessageMetadata {
	if m == nil {
		return nil
	}
	return m.messageEnvelope.Metadata()
}

func (m *outboundMessageEnvelope[T]) RenderTo(w io.Writer, opts *RenderOptions) (int, error) {
	if m == nil {
		return 0, nil
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	return errors.Wrap2(m.messageEnvelope.RenderTo(w, opts))
}

func (m *outboundMessageEnvelope[T]) Render(opts *RenderOptions) string {
	if m == nil {
		return ""
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	return m.messageEnvelope.Render(opts)
}

func (m *outboundMessageEnvelope[T]) Clone() Message {
	if m == nil {
		return nil
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	return &outboundMessageEnvelope[T]{
		messageEnvelope: m.messageEnvelope.Clone().(*messageEnvelope[T]), //nolint:forcetypeassert
	}
}

func (m *outboundMessageEnvelope[T]) Equal(v any) bool {
	if m == nil {
		return v == nil
	}

	if other, ok := v.(*outboundMessageEnvelope[T]); ok {
		m.msgMu.RLock()
		defer m.msgMu.RUnlock()
		return m.messageEnvelope.Equal(other.messageEnvelope)
	}

	return false
}

func (m *outboundMessageEnvelope[T]) IsValid() bool {
	if m == nil {
		return false
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	return m.messageEnvelope.IsValid()
}

func (m *outboundMessageEnvelope[T]) Validate() error {
	if m == nil {
		return errors.NewInvalidArgumentErrorWrap("nil envelope")
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	return errors.Wrap(m.messageEnvelope.Validate())
}

func (m *outboundMessageEnvelope[T]) MarshalJSON() ([]byte, error) {
	if m == nil {
		return jsonNull, nil
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	return errors.Wrap2(m.messageEnvelope.MarshalJSON())
}

func (m *outboundMessageEnvelope[T]) UnmarshalJSON(data []byte) error {
	if m == nil {
		return errors.NewInvalidArgumentErrorWrap("nil envelope")
	}

	m.msgMu.Lock()
	defer m.msgMu.Unlock()

	return errors.Wrap(m.unmarshalUnsafe(data))
}

func (m *outboundMessageEnvelope[T]) unmarshalUnsafe(data []byte) error {
	if m.messageEnvelope == nil {
		m.messageEnvelope = new(messageEnvelope[T])
	}

	if err := m.messageEnvelope.UnmarshalJSON(data); err != nil {
		*m = outboundMessageEnvelope[T]{}
		return errors.Wrap(err)
	}

	return nil
}

func (m *outboundMessageEnvelope[T]) LogValue() slog.Value {
	if m == nil {
		return slog.Value{}
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()

	return m.messageEnvelope.LogValue()
}
