package sip

import (
	"fmt"
	"io"
	"iter"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/abnfutils"
	"github.com/ghettovoice/gosip/internal/constraints"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
	"github.com/ghettovoice/gosip/sip/uri"
)

// Message represents a SIP message.
type Message interface {
	// Render renders the SIP message to a string.
	Render() string
	// RenderTo renders the SIP message to a writer.
	RenderTo(w io.Writer) error
	// Clone returns a clone of the message.
	Clone() Message
}

// ParseMessage parses a SIP message from a byte sequence.
func ParseMessage[T constraints.Byteseq](s T, hdrPrs map[string]HeaderParser) (Message, error) {
	node, err := grammar.ParseMessage(s)
	if err != nil {
		return nil, err
	}
	return buildFromMessageNode(node, hdrPrs), nil
}

func buildFromMessageNode(node *abnf.Node, hdrPrs map[string]HeaderParser) Message {
	if n := node.GetNode("Request"); n != nil {
		return buildFromRequestNode(n, hdrPrs)
	}
	if n := node.GetNode("Response"); n != nil {
		return buildFromResponseNode(n, hdrPrs)
	}
	panic("invalid message node")
}

func buildFromRequestNode(node *abnf.Node, hdrPrs map[string]HeaderParser) *Request {
	var body []byte
	if n := node.GetNode("message-body"); n != nil {
		body = n.Value
	}
	return &Request{
		Method:  RequestMethod(abnfutils.MustGetNode(node, "Method").String()),
		URI:     uri.FromABNF(abnfutils.MustGetNode(node, "Request-URI").Children[0]),
		Proto:   buildFromSIPVerNode(abnfutils.MustGetNode(node, "SIP-Version")),
		Headers: buildFromMessageHeaderNodes(node.GetNodes("message-header"), hdrPrs),
		Body:    body,
	}
}

func buildFromResponseNode(node *abnf.Node, hdrPrs map[string]HeaderParser) *Response {
	code, _ := strconv.ParseUint(abnfutils.MustGetNode(node, "Status-Code").String(), 10, 16)
	var body []byte
	if n := node.GetNode("message-body"); n != nil {
		body = n.Value
	}
	return &Response{
		Status:  ResponseStatus(code),
		Reason:  abnfutils.MustGetNode(node, "Reason-Phrase").String(),
		Proto:   buildFromSIPVerNode(abnfutils.MustGetNode(node, "SIP-Version")),
		Headers: buildFromMessageHeaderNodes(node.GetNodes("message-header"), hdrPrs),
		Body:    body,
	}
}

func buildFromSIPVerNode(node *abnf.Node) ProtoInfo {
	var version string
	for _, n := range node.Children[2:] {
		version += n.String()
	}
	return ProtoInfo{Name: node.Children[0].String(), Version: version}
}

func buildFromMessageHeaderNodes(nodes abnf.Nodes, hdrPrs map[string]HeaderParser) Headers {
	if len(nodes) == 0 {
		return nil
	}

	hdrs := make(Headers)
	for _, node := range nodes {
		hdrs.Append(header.FromABNF(node.Children[0].Children[0], hdrPrs))
	}
	return hdrs
}

func parseMessageStart[T constraints.Byteseq](src T) (Message, error) {
	node, err := grammar.ParseMessageStart(src)
	if err != nil {
		return nil, err
	}
	if n := node.GetNode("Request-Line"); n != nil {
		return buildFromRequestNode(n, nil), nil
	}
	if n := node.GetNode("Status-Line"); n != nil {
		return buildFromResponseNode(n, nil), nil
	}
	panic("invalid message start node")
}

// Headers maps string header name to a list of headers.
// The keys in the map are canonical header names.
type Headers map[header.Name][]Header

func (hdrs Headers) Get(n HeaderName) []Header {
	return hdrs[n.ToCanonic()]
}

func (hdrs Headers) Set(hdr Header) Headers {
	hdrs[hdr.CanonicName()] = []Header{hdr}
	return hdrs
}

func (hdrs Headers) Append(hdr Header) Headers {
	n := hdr.CanonicName()
	hdrs[n] = append(hdrs[n], hdr)
	return hdrs
}

func (hdrs Headers) Prepend(hdr Header) Headers {
	n := hdr.CanonicName()
	hdrs[n] = append([]Header{hdr}, hdrs[n]...)
	return hdrs
}

func (hdrs Headers) Del(n HeaderName) Headers {
	delete(hdrs, n.ToCanonic())
	return hdrs
}

func (hdrs Headers) Has(n HeaderName) bool {
	_, ok := hdrs[n.ToCanonic()]
	return ok
}

func (hdrs Headers) Clear() Headers {
	clear(hdrs)
	return hdrs
}

func (hdrs Headers) Clone() Headers {
	var hdrs2 Headers
	for n, hs := range hdrs {
		if hdrs2 == nil {
			hdrs2 = make(Headers, len(hdrs))
		}
		hdrs2[n] = make([]Header, len(hs))
		for i := range hs {
			hdrs2[n][i] = utils.Clone[Header](hs[i])
		}
	}
	return hdrs2
}

func (hdrs Headers) ViaHops() iter.Seq2[int, *header.ViaHop] {
	return func(yield func(int, *header.ViaHop) bool) {
		var i int
		for _, hdr := range hdrs.Get("Via") {
			if via, ok := hdr.(header.Via); ok {
				for j := range via {
					if !yield(i, &via[j]) {
						return
					}
					i++
				}
			}
		}
	}
}

func (hdrs Headers) From() *header.From {
	for _, hdr := range hdrs.Get("From") {
		if from, ok := hdr.(*header.From); ok {
			return from
		}
	}
	return nil
}

func (hdrs Headers) To() *header.To {
	for _, hdr := range hdrs.Get("To") {
		if to, ok := hdr.(*header.To); ok {
			return to
		}
	}
	return nil
}

func (hdrs Headers) CallID() header.CallID {
	for _, hdr := range hdrs.Get("Call-ID") {
		if callID, ok := hdr.(header.CallID); ok {
			return callID
		}
	}
	return ""
}

func (hdrs Headers) CSeq() *header.CSeq {
	for _, hdr := range hdrs.Get("CSeq") {
		if cseq, ok := hdr.(*header.CSeq); ok {
			return cseq
		}
	}
	return nil
}

func (hdrs Headers) MaxForwards() header.MaxForwards {
	for _, hdr := range hdrs.Get("Max-Forwards") {
		if maxFwd, ok := hdr.(header.MaxForwards); ok {
			return maxFwd
		}
	}
	return 0
}

func (hdrs Headers) Contacts() iter.Seq2[int, *header.EntityAddr] {
	return func(yield func(int, *header.EntityAddr) bool) {
		var i int
		for _, hdr := range hdrs.Get("Contact") {
			if cnt, ok := hdr.(header.Contact); ok {
				for j := range cnt {
					if !yield(i, &cnt[j]) {
						return
					}
					i++
				}
			}
		}
	}
}

func (hdrs Headers) CopyFrom(other Headers, name HeaderName, names ...HeaderName) Headers {
	copyMessageHeader(hdrs, other, name)
	for _, n := range names {
		copyMessageHeader(hdrs, other, n)
	}
	return hdrs
}

func copyMessageHeader(dst, src Headers, name HeaderName) {
	for _, hdr := range src.Get(name) {
		dst.Append(utils.Clone[Header](hdr))
	}
}

func validateHeaders(hdrs Headers) bool {
	if len(hdrs) == 0 {
		return false
	}
	for _, hs := range hdrs {
		for i := range hs {
			if !utils.IsValid(hs[i]) {
				return false
			}
		}
	}
	return true
}

func compareHeaders(hdrs, other Headers) bool {
	if len(hdrs) != len(other) {
		return false
	}
	for k, hs1 := range hdrs {
		if !other.Has(k) {
			return false
		}
		hs2 := other.Get(k)
		if len(hs1) != len(hs2) {
			return false
		}
		for i := range hs1 {
			if !utils.IsEqual(hs1[i], hs2[i]) {
				return false
			}
		}
	}
	return true
}

var headersOrder = []HeaderName{
	"Route",
	"Record-Route",
	"Via",
	"From",
	"To",
	"Call-ID",
	"CSeq",
	"Contact",
	"Max-Forwards",
	"Authorization",
	"Proxy-Authorization",
	"WWW-Authenticate",
	"Proxy-Authenticate",
	"Expires",
	"Allow",
	"Accept",
	"Accept-Encoding",
	"Accept-Language",
	"Require",
	"Proxy-Require",
	"Supported",
	"Unsupported",
	"Timestamp",
	"Date",
	"Subject",
	"Min-SE",
	"Session-Expires",
	"Refer-To",
	"In-Reply-To",
	"User-Agent",
	"Server",
	"Content-Type",
	"Content-Length",
}

func renderHeaders(w io.Writer, hdrs Headers) error {
	if len(hdrs) == 0 {
		return nil
	}

	var elems [][]Header
	for _, hs := range hdrs {
		if len(hs) > 0 {
			elems = append(elems, hs)
		}
	}
	slices.SortStableFunc(elems, func(hs1, hs2 []Header) int {
		n1, n2 := hs1[0].CanonicName(), hs2[0].CanonicName()
		i1, i2 := slices.Index(headersOrder, n1), slices.Index(headersOrder, n2)
		if i1 == -1 && i2 == -1 {
			return strings.Compare(string(n1), string(n2))
		}
		if i1 == -1 {
			return 1
		}
		if i2 == -1 {
			return -1
		}
		return i1 - i2
	})
	for _, hs := range elems {
		for i := range hs {
			if err := hs[i].RenderTo(w); err != nil {
				return err
			}
			if _, err := fmt.Fprint(w, "\r\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

type MessageMetadata map[string]any

const (
	// TransportField is the metadata field name of the message transport protocol.
	TransportField = "transport"
	// RemoteAddrField is the metadata field name of the message remote address.
	RemoteAddrField = "remote_addr"
	// LocalAddrField is the metadata field name of the message local address.
	LocalAddrField = "local_addr"
	// RequestTstampField is the metadata field name of the timestamp when the request was sent/received.
	RequestTstampField = "request_tstamp"
	// ResponseTstampField is the metadata field name of the timestamp when the response was sent/received.
	ResponseTstampField = "response_tstamp"
)

func (md MessageMetadata) Transport() TransportProto {
	v, _ := md[TransportField].(TransportProto)
	return v
}

func (md MessageMetadata) RemoteAddr() netip.AddrPort {
	v, _ := md[RemoteAddrField].(netip.AddrPort)
	return v
}

func (md MessageMetadata) LocalAddr() netip.AddrPort {
	v, _ := md[LocalAddrField].(netip.AddrPort)
	return v
}

func (md MessageMetadata) RequestTstamp() time.Time {
	v, _ := md[RequestTstampField].(time.Time)
	return v
}

func (md MessageMetadata) ResponseTstamp() time.Time {
	v, _ := md[ResponseTstampField].(time.Time)
	return v
}

func unexpectMsgTypeError(msg any) error {
	return fmt.Errorf("unexpected message type %T", msg)
}

func GetMessageHeaders(msg Message) Headers {
	switch m := msg.(type) {
	case *Request:
		return m.Headers
	case *Response:
		return m.Headers
	default:
		panic(unexpectMsgTypeError(msg))
	}
}

func SetMessageHeaders(msg Message, hdrs Headers) {
	switch m := msg.(type) {
	case *Request:
		m.Headers = hdrs
	case *Response:
		m.Headers = hdrs
	default:
		panic(unexpectMsgTypeError(msg))
	}
}

func GetMessageBody(msg Message) []byte {
	switch m := msg.(type) {
	case *Request:
		return m.Body
	case *Response:
		return m.Body
	default:
		panic(unexpectMsgTypeError(msg))
	}
}

func SetMessageBody(msg Message, body []byte) {
	switch m := msg.(type) {
	case *Request:
		m.Body = body
	case *Response:
		m.Body = body
	default:
		panic(unexpectMsgTypeError(msg))
	}
}

func GetMessageMetadata(msg Message) MessageMetadata {
	switch m := msg.(type) {
	case *Request:
		return m.Metadata
	case *Response:
		return m.Metadata
	default:
		panic(unexpectMsgTypeError(msg))
	}
}

func SetMessageMetadata(msg Message, md MessageMetadata) {
	switch m := msg.(type) {
	case *Request:
		m.Metadata = md
	case *Response:
		m.Metadata = md
	default:
		panic(unexpectMsgTypeError(msg))
	}
}
