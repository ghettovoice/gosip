package sip

import (
	"encoding/json"
	"log/slog"
	"maps"
	"net/netip"
	"slices"
	"strconv"
	"sync"
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

// Message common errors.
const (
	ErrInvalidMessage   Error = "invalid message"
	ErrMessageTooLarge  Error = "message too large"
	ErrMethodNotAllowed Error = "request method not allowed"
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
	var version string
	for _, n := range node.Children[2:] {
		version += n.String()
	}
	return ProtoInfo{Name: node.Children[0].String(), Version: version}
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
		return m.Headers
	case *Response:
		return m.Headers
	case interface{ Headers() Headers }:
		return m.Headers()
	default:
		panic(newUnexpectMsgTypeErr(msg))
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
	case interface{ UpdateMessage(u func(*Request)) }:
		m.UpdateMessage(func(r *Request) { r.Headers = hdrs })
	case interface{ UpdateMessage(u func(*Response)) }:
		m.UpdateMessage(func(r *Response) { r.Headers = hdrs })
	default:
		panic(newUnexpectMsgTypeErr(msg))
	}
}

// GetMessageBody returns the body of the given message.
// It panics if the message is not a [Request] or [Response].
func GetMessageBody(msg Message) []byte {
	switch m := msg.(type) {
	case *Request:
		return m.Body
	case *Response:
		return m.Body
	case interface{ Body() []byte }:
		return m.Body()
	default:
		panic(newUnexpectMsgTypeErr(msg))
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
	case interface{ UpdateMessage(u func(*Request)) }:
		m.UpdateMessage(func(r *Request) { r.Body = body })
	case interface{ UpdateMessage(u func(*Response)) }:
		m.UpdateMessage(func(r *Response) { r.Body = body })
	default:
		panic(newUnexpectMsgTypeErr(msg))
	}
}

const errMissHdrs Error = "missing mandatory headers"

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

type message[T Message] struct {
	msg     T
	msgTime time.Time
	locAddr,
	rmtAddr netip.AddrPort
	data *MessageMetadata
}

func (m *message[T]) Message() T {
	if m == nil {
		var zero T
		return zero
	}
	return m.msg.Clone().(T) //nolint:forcetypeassert
}

func (m *message[T]) Headers() Headers {
	if m == nil {
		return nil
	}
	return GetMessageHeaders(m.msg).Clone()
}

func (m *message[T]) Body() []byte {
	if m == nil {
		return nil
	}
	return slices.Clone(GetMessageBody(m.msg))
}

func (m *message[T]) Transport() TransportProto {
	if m == nil {
		return ""
	}
	via, ok := GetMessageHeaders(m.msg).FirstVia()
	if !ok {
		return ""
	}
	return via.Transport
}

func (m *message[T]) LocalAddr() netip.AddrPort {
	if m == nil {
		return zeroAddrPort
	}
	return m.locAddr
}

func (m *message[T]) RemoteAddr() netip.AddrPort {
	if m == nil {
		return zeroAddrPort
	}
	return m.rmtAddr
}

func (m *message[T]) MessageTime() time.Time {
	if m == nil {
		return zeroTime
	}
	return m.msgTime
}

func (m *message[T]) Metadata() *MessageMetadata {
	if m == nil {
		return nil
	}
	return m.data
}

type messageSnapshot[T Message] struct {
	Message     T                `json:"message"`
	Transport   TransportProto   `json:"transport"`
	LocalAddr   netip.AddrPort   `json:"local_addr"`
	RemoteAddr  netip.AddrPort   `json:"remote_addr"`
	MessageTime time.Time        `json:"message_time"`
	Metadata    *MessageMetadata `json:"metadata"`
}

func (m *message[T]) MarshalJSON() ([]byte, error) {
	if m == nil {
		return jsonNull, nil
	}
	return errtrace.Wrap2(json.Marshal(messageSnapshot[T]{
		Message:     m.msg,
		LocalAddr:   m.locAddr,
		RemoteAddr:  m.rmtAddr,
		MessageTime: m.msgTime,
		Metadata:    m.data,
	}))
}

func (m *message[T]) UnmarshalJSON(data []byte) error {
	var snap messageSnapshot[T]
	if err := json.Unmarshal(data, &snap); err != nil {
		return errtrace.Wrap(err)
	}
	m.msg = snap.Message
	m.locAddr = snap.LocalAddr
	m.rmtAddr = snap.RemoteAddr
	m.msgTime = snap.MessageTime
	m.data = snap.Metadata
	return nil
}

func (m *message[T]) LogValue() slog.Value {
	if m == nil {
		return slog.Value{}
	}
	return slog.GroupValue(
		slog.Any("message", m.msg.Clone()),
		slog.Any("local_addr", m.locAddr),
		slog.Any("remote_addr", m.rmtAddr),
		slog.Any("message_time", m.msgTime),
	)
}

type inboundMessage[T Message] = message[T]

type outboundMessage[T Message] struct {
	message[T]
	mu sync.RWMutex
}

func (m *outboundMessage[T]) Message() T {
	if m == nil {
		var zero T
		return zero
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.message.Message()
}

func (m *outboundMessage[T]) SetMessage(msg T) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msg = msg
}

func (m *outboundMessage[T]) UpdateMessage(update func(T)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	update(m.msg)
}

func (m *outboundMessage[T]) Headers() Headers {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.message.Headers()
}

func (m *outboundMessage[T]) Body() []byte {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.message.Body()
}

func (m *outboundMessage[T]) Transport() TransportProto {
	if m == nil {
		return ""
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.message.Transport()
}

func (m *outboundMessage[T]) LocalAddr() netip.AddrPort {
	if m == nil {
		return zeroAddrPort
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.locAddr
}

func (m *outboundMessage[T]) SetLocalAddr(addr netip.AddrPort) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.locAddr = addr
}

func (m *outboundMessage[T]) RemoteAddr() netip.AddrPort {
	if m == nil {
		return zeroAddrPort
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.rmtAddr
}

func (m *outboundMessage[T]) SetRemoteAddr(addr netip.AddrPort) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rmtAddr = addr
}

func (m *outboundMessage[T]) MessageTime() time.Time {
	if m == nil {
		return zeroTime
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.msgTime
}

func (m *outboundMessage[T]) Metadata() *MessageMetadata {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.message.Metadata()
}

func (m *outboundMessage[T]) MarshalJSON() ([]byte, error) {
	if m == nil {
		return jsonNull, nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return errtrace.Wrap2(m.message.MarshalJSON())
}

func (m *outboundMessage[T]) UnmarshalJSON(data []byte) error {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	return errtrace.Wrap(m.message.UnmarshalJSON(data))
}

func (m *outboundMessage[T]) LogValue() slog.Value {
	if m == nil {
		return slog.Value{}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.message.LogValue()
}
