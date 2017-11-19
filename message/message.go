package message

import (
	"fmt"
	"strings"

	"github.com/ghettovoice/gosip/log"
)

// A representation of a SIP method.
// This is syntactic sugar around the string type, so make sure to use
// the Equals method rather than built-in equality, or you'll fall foul of case differences.
// If you're defining your own Method, uppercase is preferred but not compulsory.
type RequestMethod string

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
	Short()
	// SipVersion returns SIP protocol version.
	SipVersion() string
	// SetSipVersion sets SIP protocol version.
	SetSipVersion(version string)

	// Headers returns all message headers.
	Headers()
	// GetHeaders returns slice of headers of the given type.
	GetHeaders(name string) []Header
	// AppendHeader appends header to message.
	AppendHeader(header Header)
	// PrependHeader prepends header to message.
	PrependHeader(header Header)
	// RemoveHeader removes header from message.
	RemoveHeader(name string) error

	// Body returns message body.
	Body() string
	// SetBody sets message body.
	SetBody(body string)
}

// headers is a struct with methods to work with SIP headers.
type headers struct {
}
