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

type TransactionError interface {
	error
	InitialError() error
	Key() TransactionKey
	Terminated() bool
	Timeout() bool
	Transport() bool
}

type TransactionTerminatedError struct {
	Err   error
	TxKey TransactionKey
	Tx    string
}

func (err *TransactionTerminatedError) InitialError() error { return err.Err }
func (err *TransactionTerminatedError) Terminated() bool    { return true }
func (err *TransactionTerminatedError) Timeout() bool       { return false }
func (err *TransactionTerminatedError) Transport() bool     { return false }
func (err *TransactionTerminatedError) Key() TransactionKey { return err.TxKey }
func (err *TransactionTerminatedError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "TransactionTerminatedError"
	parts := make([]string, 0)
	if err.TxKey != "" {
		parts = append(parts, "key "+err.TxKey)
	}
	if err.Tx != "" {
		parts = append(parts, "tx "+err.Tx)
	}
	if len(parts) > 0 {
		s += " (" + strings.Join(parts, ", ") + ")"
	}
	s += ": " + err.Err.Error()

	return s
}

type TransactionTimeoutError struct {
	Err   error
	TxKey TransactionKey
	Tx    string
}

func (err *TransactionTimeoutError) InitialError() error { return err.Err }
func (err *TransactionTimeoutError) Terminated() bool    { return false }
func (err *TransactionTimeoutError) Timeout() bool       { return true }
func (err *TransactionTimeoutError) Transport() bool     { return false }
func (err *TransactionTimeoutError) Key() TransactionKey { return err.TxKey }
func (err *TransactionTimeoutError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "TransactionTimeoutError"
	parts := make([]string, 0)
	if err.TxKey != "" {
		parts = append(parts, "key "+err.TxKey)
	}
	if err.Tx != "" {
		parts = append(parts, "tx "+err.Tx)
	}
	if len(parts) > 0 {
		s += " (" + strings.Join(parts, ", ") + ")"
	}
	s += ": " + err.Err.Error()

	return s
}

type TransactionTransportError struct {
	Err   error
	TxKey TransactionKey
	Tx    string
}

func (err *TransactionTransportError) InitialError() error { return err.Err }
func (err *TransactionTransportError) Terminated() bool    { return false }
func (err *TransactionTransportError) Timeout() bool       { return false }
func (err *TransactionTransportError) Transport() bool     { return true }
func (err *TransactionTransportError) Key() TransactionKey { return err.TxKey }
func (err *TransactionTransportError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "TransactionTransportError"
	parts := make([]string, 0)
	if err.TxKey != "" {
		parts = append(parts, "key "+err.TxKey)
	}
	if err.Tx != "" {
		parts = append(parts, "tx "+err.Tx)
	}
	if len(parts) > 0 {
		s += " (" + strings.Join(parts, ", ") + ")"
	}
	s += ": " + err.Err.Error()

	return s
}
