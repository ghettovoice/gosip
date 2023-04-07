package sip

import (
	"fmt"
	"io"
	"iter"
	"slices"
	"strconv"
	"strings"

	"github.com/ghettovoice/abnf"

	"github.com/ghettovoice/gosip/internal/constraints"
	"github.com/ghettovoice/gosip/internal/utils"
	"github.com/ghettovoice/gosip/sip/header"
	"github.com/ghettovoice/gosip/sip/internal/grammar"
	"github.com/ghettovoice/gosip/sip/uri"
)

// Message represents a SIP message.
type Message interface {
	MessageHeaders() Headers
	SetMessageHeaders(hdrs Headers) Message
	// MessageBody returns the body of the message.
	MessageBody() []byte
	SetMessageBody(body []byte) Message
	MessageMetadata() Metadata
	SetMessageMetadata(data Metadata) Message
	RenderMessage() string
	RenderMessageTo(io.Writer) error
	Clone() Message
}

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
		Method:  utils.MustGetNode(node, "Method").String(),
		URI:     uri.FromABNF(utils.MustGetNode(node, "Request-URI").Children[0]),
		Proto:   buildFromSIPVerNode(utils.MustGetNode(node, "SIP-Version")),
		Headers: buildFromMessageHeaderNodes(node.GetNodes("message-header"), hdrPrs),
		Body:    body,
	}
}

func buildFromResponseNode(node *abnf.Node, hdrPrs map[string]HeaderParser) *Response {
	code, _ := strconv.ParseUint(utils.MustGetNode(node, "Status-Code").String(), 10, 16)
	var body []byte
	if n := node.GetNode("message-body"); n != nil {
		body = n.Value
	}
	return &Response{
		Status:  uint(code),
		Reason:  utils.MustGetNode(node, "Reason-Phrase").String(),
		Proto:   buildFromSIPVerNode(utils.MustGetNode(node, "SIP-Version")),
		Headers: buildFromMessageHeaderNodes(node.GetNodes("message-header"), hdrPrs),
		Body:    body,
	}
}

func buildFromSIPVerNode(node *abnf.Node) Proto {
	var version string
	for _, n := range node.Children[2:] {
		version += n.String()
	}
	return Proto{Name: node.Children[0].String(), Version: version}
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
type Headers map[string][]Header

func (hdrs Headers) Get(name string) []Header {
	return hdrs[CanonicHeaderName(name)]
}

func (hdrs Headers) Set(hdr Header) Headers {
	hdrs[hdr.HeaderName()] = []Header{hdr}
	return hdrs
}

func (hdrs Headers) Append(hdr Header) Headers {
	n := hdr.HeaderName()
	hdrs[n] = append(hdrs[n], hdr)
	return hdrs
}

func (hdrs Headers) Prepend(hdr Header) Headers {
	n := hdr.HeaderName()
	hdrs[n] = append([]Header{hdr}, hdrs[n]...)
	return hdrs
}

func (hdrs Headers) Del(name string) Headers {
	delete(hdrs, CanonicHeaderName(name))
	return hdrs
}

func (hdrs Headers) Has(name string) bool {
	_, ok := hdrs[CanonicHeaderName(name)]
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
					yield(i, &via[j])
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
					yield(i, &cnt[j])
					i++
				}
			}
		}
	}
}

func (hdrs Headers) CopyFrom(other Headers, name string, names ...string) Headers {
	copyMessageHeader(hdrs, other, name)
	for _, n := range names {
		copyMessageHeader(hdrs, other, n)
	}
	return hdrs
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

func compareHeaders(hdrs Headers, other Headers) bool {
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

var headersOrder = []string{
	"Via",
	"From",
	"To",
	"Call-ID",
	"CSeq",
	"Contact",
	"Route",
	"Record-Route",
	"Max-Forwards",
	"Date",
	"Subject",
	"Expires",
	"Min-SE",
	"Session-Expires",
	"In-Reply-To",
	"Refer-To",
	"Allow",
	"Accept",
	"Accept-Encoding",
	"Accept-Language",
	"User-Agent",
	"Server",
	"Timestamp",
	"Supported",
	"Unsupported",
	"Require",
	"Proxy-Require",
	"Authorization",
	"Proxy-Authorization",
	"WWW-Authenticate",
	"Proxy-Authenticate",
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
		n1, n2 := hs1[0].HeaderName(), hs2[0].HeaderName()
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
			if err := hs[i].RenderHeaderTo(w); err != nil {
				return err
			}
			if _, err := fmt.Fprint(w, "\r\n"); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyMessageHeader(dst, src Headers, name string) {
	for _, hdr := range src.Get(name) {
		dst.Append(utils.Clone[Header](hdr))
	}
}
