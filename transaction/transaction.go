// transaction package implements SIP Transaction Layer
package transaction

import (
	"strings"
	"time"

	"github.com/ghettovoice/gosip/transport"
)

const (
	T1      = 500 * time.Millisecond
	T2      = 4 * time.Second
	T4      = 5 * time.Second
	Timer_A = T1
	Timer_B = 64 * T1
	Timer_D = 32 * time.Second
	Timer_E = T1
	Timer_F = 64 * T1
	Timer_G = T1
	Timer_H = 64 * T1
	Timer_I = T4
	Timer_J = 64 * T1
	Timer_K = T4
)

// IncomingMessage is an incoming message with related Tx
type IncomingMessage struct {
	*transport.IncomingMessage
	Tx Tx
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

type TxError interface {
	error
	InitialError() error
	Key() TxKey
	Terminated() bool
	Timeout() bool
	Transport() bool
}

type TxTerminatedError struct {
	Err   error
	TxKey TxKey
	Tx    string
}

func (err *TxTerminatedError) InitialError() error { return err.Err }
func (err *TxTerminatedError) Terminated() bool    { return true }
func (err *TxTerminatedError) Timeout() bool       { return false }
func (err *TxTerminatedError) Transport() bool     { return false }
func (err *TxTerminatedError) Key() TxKey          { return err.TxKey }
func (err *TxTerminatedError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "TxTerminatedError"
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

type TxTimeoutError struct {
	Err   error
	TxKey TxKey
	Tx    string
}

func (err *TxTimeoutError) InitialError() error { return err.Err }
func (err *TxTimeoutError) Terminated() bool    { return false }
func (err *TxTimeoutError) Timeout() bool       { return true }
func (err *TxTimeoutError) Transport() bool     { return false }
func (err *TxTimeoutError) Key() TxKey          { return err.TxKey }
func (err *TxTimeoutError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "TxTimeoutError"
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

type TxTransportError struct {
	Err   error
	TxKey TxKey
	Tx    string
}

func (err *TxTransportError) InitialError() error { return err.Err }
func (err *TxTransportError) Terminated() bool    { return false }
func (err *TxTransportError) Timeout() bool       { return false }
func (err *TxTransportError) Transport() bool     { return true }
func (err *TxTransportError) Key() TxKey          { return err.TxKey }
func (err *TxTransportError) Error() string {
	if err == nil {
		return "<nil>"
	}

	s := "TxTransportError"
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
