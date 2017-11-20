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
