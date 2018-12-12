package sip

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/ghettovoice/gosip/log"
)

// A representation of a SIP method.
// This is syntactic sugar around the string type, so make sure to use
// the Equals method rather than built-in equality, or you'll fall foul of case differences.
// If you're defining your own Method, uppercase is preferred but not compulsory.
type RequestMethod string

// StatusCode - response status code: 1xx - 6xx
type StatusCode uint16

// Determine if the given method equals some other given method.
// This is syntactic sugar for case insensitive equality checking.
func (method *RequestMethod) Equals(other *RequestMethod) bool {
	if method != nil && other != nil {
		return strings.EqualFold(string(*method), string(*other))
	} else {
		return method == other
	}
}

// It's nicer to avoid using raw strings to represent methods, so the following standard
// method names are defined here as constants for convenience.
const (
	INVITE    RequestMethod = "INVITE"
	ACK       RequestMethod = "ACK"
	CANCEL    RequestMethod = "CANCEL"
	BYE       RequestMethod = "BYE"
	REGISTER  RequestMethod = "REGISTER"
	OPTIONS   RequestMethod = "OPTIONS"
	SUBSCRIBE RequestMethod = "SUBSCRIBE"
	NOTIFY    RequestMethod = "NOTIFY"
	REFER     RequestMethod = "REFER"
)

// Message introduces common SIP message RFC 3261 - 7.
type Message interface {
	log.LocalLogger
	Clone() Message
	// Start line returns message start line.
	StartLine() string
	// String returns string representation of SIP message in RFC 3261 form.
	String() string
	// Short returns short string info about message.
	Short() string
	// SipVersion returns SIP protocol version.
	SipVersion() string
	// SetSipVersion sets SIP protocol version.
	SetSipVersion(version string)

	// Headers returns all message headers.
	Headers() []Header
	// GetHeaders returns slice of headers of the given type.
	GetHeaders(name string) []Header
	// AppendHeader appends header to message.
	AppendHeader(header Header)
	// PrependHeader prepends header to message.
	PrependHeader(header Header)
	PrependHeaderAfter(header Header, afterName string)
	// RemoveHeader removes header from message.
	RemoveHeader(name string)

	// Body returns message body.
	Body() string
	// SetBody sets message body.
	SetBody(body string, setContentLength bool)

	/* Helper getters for common headers */
	// CallID returns 'Call-ID' header.
	CallID() (*CallID, bool)
	// Via returns the top 'Via' header field.
	Via() (ViaHeader, bool)
	// ViaHop returns the first segment of the top 'Via' header.
	ViaHop() (*ViaHop, bool)
	// From returns 'From' header field.
	From() (*FromHeader, bool)
	// To returns 'To' header field.
	To() (*ToHeader, bool)
	// CSeq returns 'CSeq' header field.
	CSeq() (*CSeq, bool)
	ContentLength() (*ContentLength, bool)

	Transport() string
	Source() string
	SetSource(src string)
	Destination() string
	SetDestination(dest string)
}

// headers is a struct with methods to work with SIP headers.
type headers struct {
	// The logical SIP headers attached to this message.
	headers map[string][]Header
	// The order the headers should be displayed in.
	headerOrder []string
}

func newHeaders(hdrs []Header) *headers {
	hs := new(headers)
	hs.headers = make(map[string][]Header)
	hs.headerOrder = make([]string, 0)
	for _, header := range hdrs {
		hs.AppendHeader(header)
	}
	return hs
}

func (hs headers) String() string {
	buffer := bytes.Buffer{}
	// Construct each header in turn and add it to the message.
	for typeIdx, name := range hs.headerOrder {
		headers := hs.headers[name]
		for idx, header := range headers {
			buffer.WriteString(header.String())
			if typeIdx < len(hs.headerOrder) || idx < len(headers) {
				buffer.WriteString("\r\n")
			}
		}
	}
	return buffer.String()
}

// Add the given header.
func (hs *headers) AppendHeader(header Header) {
	name := strings.ToLower(header.Name())
	if _, ok := hs.headers[name]; ok {
		hs.headers[name] = append(hs.headers[name], header)
	} else {
		hs.headers[name] = []Header{header}
		hs.headerOrder = append(hs.headerOrder, name)
	}
}

// AddFrontHeader adds header to the front of header list
// if there is no header has h's name, add h to the font of all headers
// if there are some headers have h's name, add h to front of the sublist
func (hs *headers) PrependHeader(header Header) {
	name := strings.ToLower(header.Name())
	if hdrs, ok := hs.headers[name]; ok {
		hs.headers[name] = append([]Header{header}, hdrs...)
	} else {
		hs.headers[name] = []Header{header}
		newOrder := make([]string, 1, len(hs.headerOrder)+1)
		newOrder[0] = name
		hs.headerOrder = append(newOrder, hs.headerOrder...)
	}
}

func (hs *headers) PrependHeaderAfter(header Header, afterName string) {
	headerName := strings.ToLower(header.Name())
	afterName = strings.ToLower(afterName)
	if _, ok := hs.headers[afterName]; ok {
		afterIdx := -1
		headerIdx := -1
		for i, name := range hs.headerOrder {
			if name == afterName {
				afterIdx = i
			}
			if name == headerName {
				headerIdx = i
			}
		}

		if headerIdx == -1 {
			hs.headers[headerName] = []Header{header}
			newOrder := make([]string, 0)
			newOrder = append(newOrder, hs.headerOrder[:afterIdx+1]...)
			newOrder = append(newOrder, headerName)
			newOrder = append(newOrder, hs.headerOrder[afterIdx+1:]...)
			hs.headerOrder = newOrder
		} else {
			hs.headers[headerName] = append([]Header{header}, hs.headers[headerName]...)
			newOrder := make([]string, 0)
			if afterIdx < headerIdx {
				newOrder = append(newOrder, hs.headerOrder[:afterIdx+1]...)
				newOrder = append(newOrder, headerName)
				newOrder = append(newOrder, hs.headerOrder[afterIdx+1:headerIdx]...)
				newOrder = append(newOrder, hs.headerOrder[headerIdx+1:]...)
			} else {
				newOrder = append(newOrder, hs.headerOrder[:headerIdx]...)
				newOrder = append(newOrder, hs.headerOrder[headerIdx+1:afterIdx+1]...)
				newOrder = append(newOrder, headerName)
				newOrder = append(newOrder, hs.headerOrder[afterIdx+1:]...)
			}
			hs.headerOrder = newOrder
		}
	} else {
		hs.PrependHeader(header)
	}
}

// Gets some headers.
func (hs *headers) Headers() []Header {
	hdrs := make([]Header, 0)
	for _, key := range hs.headerOrder {
		hdrs = append(hdrs, hs.headers[key]...)
	}

	return hdrs
}

func (hs *headers) GetHeaders(name string) []Header {
	name = strings.ToLower(name)
	if hs.headers == nil {
		hs.headers = map[string][]Header{}
		hs.headerOrder = []string{}
	}
	if headers, ok := hs.headers[name]; ok {
		return headers
	}

	return []Header{}
}

func (hs *headers) RemoveHeader(name string) {
	name = strings.ToLower(name)
	delete(hs.headers, name)
	// update order slice
	for idx, entry := range hs.headerOrder {
		if entry == name {
			hs.headerOrder = append(hs.headerOrder[:idx], hs.headerOrder[idx+1:]...)
			break
		}
	}
}

// CloneHeaders returns all cloned headers in slice.
func (hs *headers) CloneHeaders() []Header {
	hdrs := make([]Header, 0)
	for _, header := range hs.Headers() {
		hdrs = append(hdrs, header.Clone())
	}

	return hdrs
}

func (hs *headers) CallID() (*CallID, bool) {
	hdrs := hs.GetHeaders("Call-ID")
	if len(hdrs) == 0 {
		return nil, false
	}
	callId, ok := hdrs[0].(*CallID)
	if !ok {
		return nil, false
	}
	return callId, true
}

func (hs *headers) Via() (ViaHeader, bool) {
	hdrs := hs.GetHeaders("Via")
	if len(hdrs) == 0 {
		return nil, false
	}
	via, ok := (hdrs[0]).(ViaHeader)
	if !ok {
		return nil, false
	}

	return via, true
}

func (hs *headers) ViaHop() (*ViaHop, bool) {
	via, ok := hs.Via()
	if !ok {
		return nil, false
	}
	hops := []*ViaHop(via)
	if len(hops) == 0 {
		return nil, false
	}

	return hops[0], true
}

func (hs *headers) From() (*FromHeader, bool) {
	hdrs := hs.GetHeaders("From")
	if len(hdrs) == 0 {
		return nil, false
	}
	from, ok := hdrs[0].(*FromHeader)
	if !ok {
		return nil, false
	}
	return from, true
}

func (hs *headers) To() (*ToHeader, bool) {
	hdrs := hs.GetHeaders("To")
	if len(hdrs) == 0 {
		return nil, false
	}
	to, ok := hdrs[0].(*ToHeader)
	if !ok {
		return nil, false
	}
	return to, true
}

func (hs *headers) CSeq() (*CSeq, bool) {
	hdrs := hs.GetHeaders("CSeq")
	if len(hdrs) == 0 {
		return nil, false
	}
	cseq, ok := hdrs[0].(*CSeq)
	if !ok {
		return nil, false
	}
	return cseq, true
}

func (hs *headers) ContentLength() (*ContentLength, bool) {
	hdrs := hs.GetHeaders("Content-Length")
	if len(hdrs) == 0 {
		return nil, false
	}
	contentLength, ok := hdrs[0].(*ContentLength)
	if !ok {
		return nil, false
	}
	return contentLength, true
}

// basic message implementation
type message struct {
	// message headers
	*headers
	sipVersion string
	body       string
	logger     log.LocalLogger
	startLine  func() string
	src        string
	dest       string
}

func (msg *message) StartLine() string {
	return msg.startLine()
}

func (msg *message) Short() string {
	if msg == nil {
		return "<nil>"
	}

	s := "Message " + msg.StartLine()
	parts := make([]string, 0)
	if cseq, ok := msg.CSeq(); ok {
		parts = append(parts, fmt.Sprintf("%s", cseq))
	}
	if callId, ok := msg.CallID(); ok {
		parts = append(parts, fmt.Sprintf("%s", callId))
	}
	if from, ok := msg.From(); ok {
		parts = append(parts, fmt.Sprintf("%s", from))
	}
	if to, ok := msg.To(); ok {
		parts = append(parts, fmt.Sprintf("%s", to))
	}
	parts = append(parts, fmt.Sprintf("ptr %p", msg))
	if len(parts) > 0 {
		s += " (" + strings.Join(parts, ", ") + ")"
	}

	return s
}

func (msg *message) String() string {
	var buffer bytes.Buffer

	// write message start line
	buffer.WriteString(msg.StartLine() + "\r\n")
	// Write the headers.
	buffer.WriteString(msg.headers.String())
	// message body
	buffer.WriteString("\r\n" + msg.Body())

	return buffer.String()
}

func (msg *message) SipVersion() string {
	return msg.sipVersion
}

func (msg *message) SetSipVersion(version string) {
	msg.sipVersion = version
}

func (msg *message) Body() string {
	return msg.body
}

// SetBody sets message body, calculates it length and add 'Content-Length' header.
func (msg *message) SetBody(body string, setContentLength bool) {
	msg.body = body
	if setContentLength {
		hdrs := msg.GetHeaders("Content-Length")
		if len(hdrs) == 0 {
			length := ContentLength(len(body))
			msg.AppendHeader(length)
		} else {
			hdrs[0] = ContentLength(len(body))
		}
	}
}

func (msg *message) Log() log.Logger {
	return msg.logger.Log().WithFields(msg.logFields())
}

func (msg *message) SetLog(logger log.Logger) {
	msg.logger.SetLog(logger)
}

func (msg *message) logFields() map[string]interface{} {
	fields := make(map[string]interface{})
	fields["msg"] = fmt.Sprintf("%p", msg)
	// add Call-ID
	if callId, ok := msg.CallID(); ok {
		fields[strings.ToLower(callId.Name())] = string(*callId)
	}
	// add Via
	if via, ok := msg.Via(); ok {
		fields[strings.ToLower(via.Name())] = via
	}
	// add cseq
	if cseq, ok := msg.CSeq(); ok {
		fields[strings.ToLower(cseq.Name())] = cseq
	}
	// add From
	if from, ok := msg.From(); ok {
		fields[strings.ToLower(from.Name())] = from
	}
	// add To
	if to, ok := msg.To(); ok {
		fields[strings.ToLower(to.Name())] = to
	}

	return fields
}

func (msg *message) Transport() string {
	if viaHop, ok := msg.ViaHop(); ok {
		return viaHop.Transport
	} else {
		return DefaultProtocol
	}
}

func (msg *message) Source() string {
	return msg.src
}
func (msg *message) SetSource(src string) {
	msg.src = src
}
func (msg *message) Destination() string {
	return msg.dest
}
func (msg *message) SetDestination(dest string) {
	msg.dest = dest
}

// Copy all headers of one type from one message to another.
// Appending to any headers that were already there.
func CopyHeaders(name string, from, to Message) {
	name = strings.ToLower(name)
	for _, h := range from.GetHeaders(name) {
		to.AppendHeader(h.Clone())
	}
}

func PrependCopyHeaders(name string, from, to Message) {
	name = strings.ToLower(name)
	for _, h := range from.GetHeaders(name) {
		to.PrependHeader(h.Clone())
	}
}
