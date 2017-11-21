package core

import "fmt"

// Port number
type Port uint16

func (port *Port) String() string {
	return fmt.Sprintf("%d", *port)
}

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

type MessageTunnel interface {
	SetOutput(output chan Message)
	Output() <-chan Message
	SetErrors(errs chan error)
	Errors() <-chan error
}

type DataTunnel interface {
	SetOutput(output chan []byte)
	Output() <-chan []byte
	SetErrors(errs chan error)
	Errors() <-chan error
}
