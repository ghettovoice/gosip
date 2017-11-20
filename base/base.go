package base

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

type MaybeString interface {
	String() string
}

type String struct {
	Str string
}

func (str String) String() string {
	return str.Str
}
