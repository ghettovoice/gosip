package msg

import (
	"strings"

	"bytes"

	"fmt"

	"github.com/ghettovoice/gosip/log"
)

// A representation of a SIP method.
// This is syntactic sugar around the string type, so make sure to use
// the Equals method rather than built-in equality, or you'll fall foul of case differences.
// If you're defining your own Method, uppercase is preferred but not compulsory.
type Method string

// StatusCode - response status code: 1xx - 6xx
type StatusCode uint16

// Determine if the given method equals some other given method.
// This is syntactic sugar for case insensitive equality checking.
func (method *Method) Equals(other *Method) bool {
	if method != nil && other != nil {
		return strings.EqualFold(string(*method), string(*other))
	} else {
		return method == other
	}
}

// It's nicer to avoid using raw strings to represent methods, so the following standard
// method names are defined here as constants for convenience.
const (
	INVITE    Method = "INVITE"
	ACK       Method = "ACK"
	CANCEL    Method = "CANCEL"
	BYE       Method = "BYE"
	REGISTER  Method = "REGISTER"
	OPTIONS   Method = "OPTIONS"
	SUBSCRIBE Method = "SUBSCRIBE"
	NOTIFY    Method = "NOTIFY"
	REFER     Method = "REFER"
)

// Message introduces common SIP message RFC 3261 - 7.
type Message interface {
	log.WithLogger
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
	// RemoveHeader removes header from message.
	RemoveHeader(name string)

	// Body returns message body.
	Body() string
	// SetBody sets message body.
	SetBody(body string)

	/* Helper getters for common headers */
	// CallId returns 'Call-Id' header.
	CallId() (*CallId, bool)
	// Via returns the top 'Via' header field.
	Via() (*ViaHeader, bool)
	// From returns 'From' header field.
	From() (*FromHeader, bool)
	// To returns 'To' header field.
	To() (*ToHeader, bool)
	// CSeq returns 'CSeq' header field.
	CSeq() (*CSeq, bool)
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
// if there is no header has h's name, add h to the tail of all headers
// if there are some headers have h's name, add h to front of the sublist
func (hs *headers) PrependHeader(header Header) {
	name := strings.ToLower(header.Name())
	if hdrs, ok := hs.headers[name]; ok {
		newHdrs := make([]Header, 1, len(hdrs)+1)
		newHdrs[0] = header
		hs.headers[name] = append(newHdrs, hdrs...)
	} else {
		hs.headers[name] = []Header{header}
		hs.headerOrder = append(hs.headerOrder, name)
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

func (hs *headers) CallId() (*CallId, bool) {
	hdrs := hs.GetHeaders("Call-Id")
	if len(hdrs) == 0 {
		return nil, false
	}
	callId, ok := hdrs[0].(*CallId)
	if !ok {
		return nil, false
	}
	return callId, true
}

func (hs *headers) Via() (*ViaHeader, bool) {
	hdrs := hs.GetHeaders("Via")
	if len(hdrs) == 0 {
		return nil, false
	}
	via, ok := hdrs[0].(*ViaHeader)
	if !ok {
		return nil, false
	}

	return via, true
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

// basic message implementation
type message struct {
	// message headers
	*headers
	sipVersion string
	body       string
	log        log.Logger
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
func (msg *message) SetBody(body string) {
	msg.body = body
	hdrs := msg.GetHeaders("Content-Length")
	if len(hdrs) == 0 {
		length := ContentLength(len(body))
		msg.AppendHeader(length)
	} else {
		hdrs[0] = ContentLength(len(body))
	}
}

func (msg *message) Log() log.Logger {
	return msg.log.WithFields(msg.logFields())
}

func (msg *message) SetLog(logger log.Logger) {
	msg.log = logger
}

func (msg *message) logFields() map[string]interface{} {
	fields := make(map[string]interface{})
	fields["msg-ptr"] = fmt.Sprintf("%p", msg)
	// add Call-Id
	if callId, ok := msg.CallId(); ok {
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
