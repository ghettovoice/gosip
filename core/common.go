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
}

type ContentLengthError struct {
	Msg Message
	Err error
}

func (err *ContentLengthError) Malformed() bool { return true }
func (err *ContentLengthError) Error() string {
	if err == nil {
		return "<nil>"
	}

	var msg string
	if err.Msg == nil {
		msg = "<nil>"
	} else {
		msg = err.Msg.Short()
	}

	return fmt.Sprintf("ContentLengthError with message '%s': %s", msg, err.Err)
}

type BrokenMessageError struct {
	Msg Message
	Err error
}

func (err *BrokenMessageError) Malformed() bool { return false }
func (err *BrokenMessageError) Error() string {
	if err == nil {
		return "<nil>"
	}

	var msg string
	if err.Msg == nil {
		msg = "<nil>"
	} else {
		msg = err.Msg.Short()
	}

	return fmt.Sprintf("BrokenMessageError with message '%s': %s", msg, err.Err)
}
