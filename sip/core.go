package sip

import (
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

// Port number
type Port uint16

func (port *Port) Clone() *Port {
	if port == nil {
		return nil
	}
	newPort := *port
	return &newPort
}

// String wrapper
type MaybeString interface {
	String() string
}

type String struct {
	Str string
}

func (str String) String() string {
	return str.Str
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
		util.RandStr(16),
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
