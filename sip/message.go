package sip

import (
	"encoding/json"
	"io"
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

	"braces.dev/errtrace"
	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/header"
	"github.com/ghettovoice/gosip/internal/errorutil"
	"github.com/ghettovoice/gosip/internal/grammar"
	"github.com/ghettovoice/gosip/internal/syncutil"
	"github.com/ghettovoice/gosip/internal/types"
	"github.com/ghettovoice/gosip/uri"
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
func ParseMessage[T ~string | ~[]byte](s T) (Message, error) {
	node, err := grammar.ParseMessage(s)
	if err != nil {
		return nil, errtrace.Wrap(err)
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
	panic(grammar.ErrUnexpectNode)
}

func buildFromRequestNode(node *abnf.Node) *Request {
	var body []byte
	if n, ok := node.GetNode("message-body"); ok {
		body = n.Value
	}
	return &Request{
		Method:  RequestMethod(grammar.MustGetNode(node, "Method").String()),
		URI:     uri.FromABNF(grammar.MustGetNode(node, "Request-URI").Children[0]),
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
		hdrs.Append(header.FromABNF(node.Children[0].Children[0]))
	}
	return hdrs
}

func parseMsgStart[T ~string | ~[]byte](src T) (Message, error) {
	node, err := grammar.ParseMessageStart(src)
	if err != nil {
		return nil, errtrace.Wrap(err)
	}
	if n, ok := node.GetNode("Request-Line"); ok {
		return buildFromRequestNode(n), nil
	}
	if n, ok := node.GetNode("Status-Line"); ok {
		return buildFromResponseNode(n), nil
	}
	panic(grammar.ErrUnexpectNode)
}

// GetMessageHeaders returns the headers of the given message.
// It panics if the message is not a [Request] or [Response].
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
// It panics if the message is not a [Request] or [Response].
func SetMessageHeaders(msg Message, hdrs Headers) {
	switch m := msg.(type) {
	case *Request:
		m.Headers = hdrs
	case *Response:
		m.Headers = hdrs
	case interface{ AccessMessage(u func(*Request)) }:
		m.AccessMessage(func(r *Request) { r.Headers = hdrs })
	case interface{ AccessMessage(u func(*Response)) }:
		m.AccessMessage(func(r *Response) { r.Headers = hdrs })
	default:
		panic(newUnexpectMsgTypeErr(msg))
	}
}

// GetMessageBody returns the body of the given message.
// It panics if the message is not a [Request] or [Response].
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
// It panics if the message is not a [Request] or [Response].
func SetMessageBody(msg Message, body []byte) {
	switch m := msg.(type) {
	case *Request:
		m.Body = body
	case *Response:
		m.Body = body
	case interface{ AccessMessage(u func(*Request)) }:
		m.AccessMessage(func(r *Request) { r.Body = body })
	case interface{ AccessMessage(u func(*Response)) }:
		m.AccessMessage(func(r *Response) { r.Body = body })
	default:
		panic(newUnexpectMsgTypeErr(msg))
	}
}

func newMissHdrErr(name HeaderName) error {
	if name == "" {
		return errMissHdrs //errtrace:skip
	}
	return errorutil.Errorf("missing mandatory header %q", name) //errtrace:skip
}

func NewInvalidMessageError(args ...any) error {
	return errorutil.NewWrapperError(ErrInvalidMessage, args...) //errtrace:skip
}

func newUnexpectMsgTypeErr(msg Message) error {
	return errorutil.Errorf("unexpected message type %T", msg) //errtrace:skip
}

// MessageMetadata is a thread-safe key-value store for arbitrary data.
// It wraps [syncutil.RWMap] with JSON serialization support.
// Values in the metadata expected to be serializable to JSON.
type MessageMetadata struct {
	syncutil.RWMap[string, any]
}

func (m *MessageMetadata) MarshalJSON() ([]byte, error) {
	if m == nil {
		return jsonNull, nil
	}
	return errtrace.Wrap2(json.Marshal(maps.Collect(m.All())))
}

func (m *MessageMetadata) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return errtrace.Wrap(err)
	}
	for k, v := range raw {
		m.Set(k, v)
	}
	return nil
}

// Clone returns a deep copy of the MessageMetadata.
func (m *MessageMetadata) Clone() *MessageMetadata {
	if m == nil {
		return nil
	}
	clone := &MessageMetadata{}
	m.CopyTo(&clone.RWMap)
	return clone
}

type messageEnvelope[T Message] struct {
	msg     atomic.Value // T
	msgTime time.Time
	tp      atomic.Value // TransportProto
	locAddr,
	rmtAddr atomic.Value // netip.AddrPort
	data *MessageMetadata
}

func (m *messageEnvelope[T]) message() T {
	return m.msg.Load().(T) //nolint:forcetypeassert
}

func (m *messageEnvelope[T]) Message() T {
	if m == nil {
		var zero T
		return zero
	}
	return m.message().Clone().(T) //nolint:forcetypeassert
}

func (m *messageEnvelope[T]) Headers() Headers {
	if m == nil {
		return nil
	}
	return GetMessageHeaders(m.message()).Clone()
}

func (m *messageEnvelope[T]) Body() []byte {
	if m == nil {
		return nil
	}
	return slices.Clone(GetMessageBody(m.message()))
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
	laddr, ok := m.locAddr.Load().(netip.AddrPort)
	if !ok {
		return zeroAddrPort
	}
	return laddr
}

func (m *messageEnvelope[T]) LocalAddr() netip.AddrPort {
	if m == nil {
		return zeroAddrPort
	}
	return m.localAddr()
}

func (m *messageEnvelope[T]) remoteAddr() netip.AddrPort {
	raddr, ok := m.rmtAddr.Load().(netip.AddrPort)
	if !ok {
		return zeroAddrPort
	}
	return raddr
}

func (m *messageEnvelope[T]) RemoteAddr() netip.AddrPort {
	if m == nil {
		return zeroAddrPort
	}
	return m.remoteAddr()
}

func (m *messageEnvelope[T]) MessageTime() time.Time {
	if m == nil {
		return zeroTime
	}
	return m.msgTime
}

func (m *messageEnvelope[T]) Metadata() *MessageMetadata {
	if m == nil {
		return nil
	}
	return m.data
}

func (m *messageEnvelope[T]) RenderTo(w io.Writer, opts *RenderOptions) (int, error) {
	if m == nil {
		return 0, nil
	}
	return errtrace.Wrap2(m.message().RenderTo(w, opts))
}

func (m *messageEnvelope[T]) Render(opts *RenderOptions) string {
	if m == nil {
		return ""
	}
	return m.message().Render(opts)
}

func (m *messageEnvelope[T]) Clone() Message {
	if m == nil {
		return nil
	}

	m2 := &messageEnvelope[T]{
		msgTime: time.Now(),
		data:    m.data.Clone(),
	}
	m2.msg.Store(m.message().Clone())
	m2.tp.Store(m.transport())
	m2.locAddr.Store(m.localAddr())
	m2.rmtAddr.Store(m.remoteAddr())
	return m2
}

func (m *messageEnvelope[T]) Equal(v any) bool {
	if m == nil {
		return v == nil
	}
	if other, ok := v.(*messageEnvelope[T]); ok {
		return m.message().Equal(other.message())
	}
	return false
}

func (m *messageEnvelope[T]) IsValid() bool {
	if m == nil {
		return false
	}
	return m.message().IsValid()
}

func (m *messageEnvelope[T]) Validate() error {
	if m == nil {
		return errtrace.Wrap(NewInvalidArgumentError("invalid message"))
	}
	return errtrace.Wrap(m.message().Validate())
}

type messageEnvelopeData[T Message] struct {
	Message     T                `json:"message"`
	Transport   TransportProto   `json:"transport"`
	LocalAddr   netip.AddrPort   `json:"local_addr"`
	RemoteAddr  netip.AddrPort   `json:"remote_addr"`
	MessageTime time.Time        `json:"message_time"`
	Metadata    *MessageMetadata `json:"metadata"`
}

func (m *messageEnvelope[T]) MarshalJSON() ([]byte, error) {
	if m == nil {
		return jsonNull, nil
	}

	return errtrace.Wrap2(json.Marshal(messageEnvelopeData[T]{
		Message:     m.message(),
		Transport:   m.transport(),
		LocalAddr:   m.localAddr(),
		RemoteAddr:  m.remoteAddr(),
		MessageTime: m.msgTime,
		Metadata:    m.data,
	}))
}

func (m *messageEnvelope[T]) UnmarshalJSON(data []byte) error {
	var msgData messageEnvelopeData[T]
	if err := json.Unmarshal(data, &msgData); err != nil {
		return errtrace.Wrap(err)
	}

	msgVal := reflect.ValueOf(msgData.Message)
	if k := msgVal.Kind(); !msgVal.IsValid() || (k == reflect.Pointer || k == reflect.Interface) && msgVal.IsNil() {
		return errtrace.Wrap(NewInvalidArgumentError("invalid message"))
	}
	if msgData.Transport == "" {
		return errtrace.Wrap(NewInvalidArgumentError("invalid transport"))
	}
	if !msgData.LocalAddr.IsValid() {
		return errtrace.Wrap(NewInvalidArgumentError("invalid local address"))
	}
	if !msgData.RemoteAddr.IsValid() {
		return errtrace.Wrap(NewInvalidArgumentError("invalid remote address"))
	}

	m.msg.Store(msgData.Message)
	m.tp.Store(msgData.Transport)
	m.locAddr.Store(msgData.LocalAddr)
	m.rmtAddr.Store(msgData.RemoteAddr)
	if msgData.MessageTime.IsZero() {
		msgData.MessageTime = time.Now()
	}
	m.msgTime = msgData.MessageTime
	if msgData.Metadata == nil {
		msgData.Metadata = new(MessageMetadata)
	}
	m.data = msgData.Metadata
	return nil
}

func (m *messageEnvelope[T]) LogValue() slog.Value {
	if m == nil {
		return zeroSlogValue
	}
	return slog.GroupValue(
		slog.Any("message", m.message().Clone()),
		slog.Any("transport", m.transport()),
		slog.Any("local_addr", m.localAddr()),
		slog.Any("remote_addr", m.remoteAddr()),
		slog.Any("message_time", m.msgTime),
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

func (m *outboundMessageEnvelope[T]) AccessMessage(update func(T)) {
	m.msgMu.Lock()
	defer m.msgMu.Unlock()
	update(m.message())
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
		return zeroAddrPort
	}
	return m.localAddr()
}

func (m *outboundMessageEnvelope[T]) SetLocalAddr(addr netip.AddrPort) {
	m.locAddr.Store(addr)
}

func (m *outboundMessageEnvelope[T]) RemoteAddr() netip.AddrPort {
	if m == nil {
		return zeroAddrPort
	}
	return m.remoteAddr()
}

func (m *outboundMessageEnvelope[T]) SetRemoteAddr(addr netip.AddrPort) {
	m.rmtAddr.Store(addr)
}

func (m *outboundMessageEnvelope[T]) MessageTime() time.Time {
	if m == nil {
		return zeroTime
	}
	return m.msgTime
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
	return errtrace.Wrap2(m.messageEnvelope.RenderTo(w, opts))
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
		return errtrace.Wrap(NewInvalidArgumentError("invalid message"))
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()
	return errtrace.Wrap(m.messageEnvelope.Validate())
}

func (m *outboundMessageEnvelope[T]) MarshalJSON() ([]byte, error) {
	if m == nil {
		return jsonNull, nil
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()
	return errtrace.Wrap2(m.messageEnvelope.MarshalJSON())
}

func (m *outboundMessageEnvelope[T]) UnmarshalJSON(data []byte) error {
	if m.messageEnvelope == nil {
		m.messageEnvelope = new(messageEnvelope[T])
	}
	return errtrace.Wrap(m.messageEnvelope.UnmarshalJSON(data))
}

func (m *outboundMessageEnvelope[T]) LogValue() slog.Value {
	if m == nil {
		return zeroSlogValue
	}

	m.msgMu.RLock()
	defer m.msgMu.RUnlock()
	return m.messageEnvelope.LogValue()
}
