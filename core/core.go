package core

import (
	"fmt"
)

// Port number
type Port uint16

func (port *Port) Clone() *Port {
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

type MessageError interface {
	error
	// Malformed indicates that message is syntactically valid but has invalid headers, or
	// without required headers and etc.
	Malformed() bool
	// Broken or incomplete message, or not a SIP message
	Broken() bool
}

// Broken or incomplete messages, or not a SIP message.
type BrokenMessageError struct {
	Err error
	Msg Message
}

func (err *BrokenMessageError) Malformed() bool { return false }
func (err *BrokenMessageError) Broken() bool    { return true }
func (err *BrokenMessageError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "BrokenMessageError"
	if err.Msg != nil {
		s += fmt.Sprintf(" with message '%s'", err.Msg.Short())
	}
	s += ": " + err.Err.Error()

	return s
}

// syntactically valid but logically invalid message
type MalformedMessageError struct {
	Err error
	Msg Message
}

func (err *MalformedMessageError) Malformed() bool { return true }
func (err *MalformedMessageError) Broken() bool    { return false }
func (err *MalformedMessageError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "MalformedMessageError"
	if err.Msg != nil {
		s += fmt.Sprintf(" with message '%s'", err.Msg.Short())
	}
	s += ": " + err.Err.Error()

	return s
}

type UnsupportedMessageError struct {
	Err error
	Msg Message
}

func (err *UnsupportedMessageError) Malformed() bool { return true }
func (err *UnsupportedMessageError) Broken() bool    { return false }
func (err *UnsupportedMessageError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "UnsupportedMessageError"
	if err.Msg != nil {
		s += fmt.Sprintf(" '%s'", err.Msg.Short())
	}
	s += ": " + err.Err.Error()

	return s
}
