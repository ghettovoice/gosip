package txs

import (
	"fmt"
	"strings"
	"time"

	"github.com/ghettovoice/gosip/transp"
)

const (
	T1      = 500 * time.Millisecond
	T2      = 4 * time.Second
	T4      = 5 * time.Second
	Timer_A = T1
	Timer_B = 64 * T1
	Timer_D = 32 * time.Second
	Timer_H = 64 * T1
)

type IncomingMessage struct {
	*transp.IncomingMessage
	Tx Transaction
}

func (msg *IncomingMessage) String() string {
	if msg == nil {
		return "IncomingMessage <nil>"
	}
	s := "IncomingMessage " + msg.Msg.Short()
	parts := make([]string, 0)
	if msg.Tx != nil {
		parts = append(parts, "tx "+msg.Tx.String())
	}
	if msg.Network != "" {
		parts = append(parts, "net "+msg.Network)
	}
	if msg.LAddr != "" {
		parts = append(parts, "laddr "+msg.LAddr)
	}
	if msg.RAddr != "" {
		parts = append(parts, "raddr "+msg.RAddr)
	}
	if len(parts) > 0 {
		s += " (" + strings.Join(parts, ", ") + ")"
	}

	return s
}

type Error interface {
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
