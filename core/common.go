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

// Broken or malformed message
type MalformedMessageError struct {
	Txt string
	Msg Message
}

func (err *MalformedMessageError) Error() string {
	var msg string
	if err.Msg == nil {
		msg = "<nil>"
	} else {
		msg = err.Msg.Short()
	}

	return fmt.Sprintf("malformed message error: %s, message: %s", err.Txt, msg)
}

func (err *MalformedMessageError) String() string {
	return err.Error()
}
