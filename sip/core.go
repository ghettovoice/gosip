package sip

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/ghettovoice/gosip/util"
)

const (
	DefaultHost     = "127.0.0.1"
	DefaultProtocol = "TCP"

	DefaultUdpPort Port = 5060
	DefaultTcpPort Port = 5060
	DefaultTlsPort Port = 5061
)

// TODO should be refactored, currently here the pit

type Address struct {
	DisplayName MaybeString
	Uri         *SipUri
	Params      Params
}

func NewAddressFromFromHeader(from *FromHeader) *Address {
	return &Address{
		DisplayName: from.DisplayName,
		Uri:         from.Address.Clone().(*SipUri),
		Params:      from.Params.Clone(),
	}
}

func NewAddressFromToHeader(to *ToHeader) *Address {
	return &Address{
		DisplayName: to.DisplayName,
		Uri:         to.Address.Clone().(*SipUri),
		Params:      to.Params.Clone(),
	}
}

func NewAddressFromContactHeader(cnt *ContactHeader) *Address {
	return &Address{
		DisplayName: cnt.DisplayName,
		Uri:         cnt.Address.Clone().(*SipUri),
		Params:      cnt.Params.Clone(),
	}
}

func (addr *Address) String() string {
	var buffer bytes.Buffer

	if displayName, ok := addr.DisplayName.(String); ok && displayName.String() != "" {
		buffer.WriteString(fmt.Sprintf("\"%s\" ", displayName))
	}

	buffer.WriteString(fmt.Sprintf("<%s>", addr.Uri))
	if addr.Params.Length() > 0 {
		buffer.WriteString(";")
		buffer.WriteString(addr.Params.ToString(';'))
	}

	return buffer.String()
}

func (addr *Address) Clone() *Address {
	return &Address{
		DisplayName: addr.DisplayName,
		Uri:         addr.Uri.Clone().(*SipUri),
		Params:      addr.Params.Clone(),
	}
}

func (addr *Address) Equals(other interface{}) bool {
	if v, ok := other.(*Address); ok {
		return addr.DisplayName.Equals(v.DisplayName) &&
			addr.Uri.Equals(v.Uri) &&
			addr.Params.Equals(v.Params)
	}

	return false
}

func (addr *Address) AsToHeader() *ToHeader {
	return &ToHeader{
		DisplayName: addr.DisplayName,
		Address:     addr.Uri.Clone().(*SipUri),
		Params:      addr.Params.Clone(),
	}
}

func (addr *Address) AsFromHeader() *FromHeader {
	return &FromHeader{
		DisplayName: addr.DisplayName,
		Address:     addr.Uri.Clone().(*SipUri),
		Params:      addr.Params.Clone(),
	}
}

func (addr *Address) AsContactHeader() *ContactHeader {
	return &ContactHeader{
		DisplayName: addr.DisplayName,
		Address:     addr.Uri.Clone().(*SipUri),
		Params:      addr.Params.Clone(),
	}
}

// Port number
type Port uint16

func (port *Port) Clone() *Port {
	if port == nil {
		return nil
	}
	newPort := *port
	return &newPort
}

func (port *Port) String() string {
	if port == nil {
		return ""
	}
	return fmt.Sprintf("%d", *port)
}

func (port *Port) Equals(other interface{}) bool {
	if p, ok := other.(*Port); ok {
		return util.Uint16PtrEq((*uint16)(port), (*uint16)(p))
	}

	return false
}

// String wrapper
type MaybeString interface {
	String() string
	Equals(other interface{}) bool
}

type String struct {
	Str string
}

func (str String) String() string {
	return str.Str
}

func (str String) Equals(other interface{}) bool {
	if v, ok := other.(String); ok {
		return str.Str == v.Str
	}

	return false
}

type CancelError interface {
	Canceled() bool
}

type ExpireError interface {
	Expired() bool
}

type MessageError interface {
	error
	// Malformed indicates that message is syntactically valid but has invalid headers, or
	// without required headers.
	Malformed() bool
	// Broken or incomplete message, or not a SIP message
	Broken() bool
}

// Broken or incomplete messages, or not a SIP message.
type BrokenMessageError struct {
	Err error
	Msg string
}

func (err *BrokenMessageError) Malformed() bool { return false }
func (err *BrokenMessageError) Broken() bool    { return true }
func (err *BrokenMessageError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "BrokenMessageError: " + err.Err.Error()
	if err.Msg != "" {
		s += fmt.Sprintf("\nMessage dump:\n%s", err.Msg)
	}

	return s
}

// syntactically valid but logically invalid message
type MalformedMessageError struct {
	Err error
	Msg string
}

func (err *MalformedMessageError) Malformed() bool { return true }
func (err *MalformedMessageError) Broken() bool    { return false }
func (err *MalformedMessageError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "MalformedMessageError: " + err.Err.Error()
	if err.Msg != "" {
		s += fmt.Sprintf("\nMessage dump:\n%s", err.Msg)
	}

	return s
}

type UnsupportedMessageError struct {
	Err error
	Msg string
}

func (err *UnsupportedMessageError) Malformed() bool { return true }
func (err *UnsupportedMessageError) Broken() bool    { return false }
func (err *UnsupportedMessageError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "UnsupportedMessageError: " + err.Err.Error()
	if err.Msg != "" {
		s += fmt.Sprintf("\nMessage dump:\n%s", err.Msg)
	}

	return s
}

type UnexpectedMessageError struct {
	Err error
	Msg string
}

func (err *UnexpectedMessageError) Broken() bool    { return false }
func (err *UnexpectedMessageError) Malformed() bool { return false }
func (err *UnexpectedMessageError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "UnexpectedMessageError: " + err.Err.Error()
	if err.Msg != "" {
		s += fmt.Sprintf("\nMessage dump:\n%s", err.Msg)
	}

	return s
}

const RFC3261BranchMagicCookie = "z9hG4bK"

// GenerateBranch returns random unique branch ID.
func GenerateBranch() string {
	return strings.Join([]string{
		RFC3261BranchMagicCookie,
		util.RandString(32),
	}, "")
}

// DefaultPort returns protocol default port by network.
func DefaultPort(protocol string) Port {
	switch strings.ToLower(protocol) {
	case "tls":
		return DefaultTlsPort
	case "tcp":
		return DefaultTcpPort
	case "udp":
		return DefaultUdpPort
	default:
		return DefaultTcpPort
	}
}

func MakeDialogIDFromMessage(msg Message) (string, error) {
	callID, ok := msg.CallID()
	if !ok {
		return "", fmt.Errorf("missing Call-ID header")
	}

	to, ok := msg.To()
	if !ok {
		return "", fmt.Errorf("missing To header")
	}

	toTag, ok := to.Params.Get("tag")
	if !ok {
		return "", fmt.Errorf("missing tag param in To header")
	}

	from, ok := msg.From()
	if !ok {
		return "", fmt.Errorf("missing To header")
	}

	fromTag, ok := from.Params.Get("tag")
	if !ok {
		return "", fmt.Errorf("missing tag param in From header")
	}

	return MakeDialogID(string(*callID), toTag.String(), fromTag.String()), nil
}

func MakeDialogID(callID, innerID, externalID string) string {
	return strings.Join([]string{callID, innerID, externalID}, "__")
}
