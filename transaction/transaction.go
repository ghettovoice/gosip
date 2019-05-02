// transaction package implements SIP Transaction Layer
package transaction

import (
	"fmt"
	"strings"
	"time"

	"github.com/ghettovoice/gosip/sip"
)

const (
	T1        = 500 * time.Millisecond
	T2        = 4 * time.Second
	T4        = 5 * time.Second
	Timer_A   = T1
	Timer_B   = 64 * T1
	Timer_D   = 32 * time.Second
	Timer_E   = T1
	Timer_F   = 64 * T1
	Timer_G   = T1
	Timer_H   = 64 * T1
	Timer_I   = T4
	Timer_J   = 64 * T1
	Timer_K   = T4
	Timer_1xx = 200 * time.Millisecond
)

// TxMessage is an message with related Tx
type TxMessage interface {
	sip.Message
	Tx() Tx
	SetTx(tx Tx)
	Origin() sip.Message
	IsRequest() bool
	IsResponse() bool
}

type txMessage struct {
	sip.Message
	tx Tx
}

func (msg *txMessage) Short() string {
	return fmt.Sprintf("Tx%s [%s]", msg.Origin().Short(), msg.Tx())
}

func (msg *txMessage) Tx() Tx {
	return msg.tx
}

func (msg *txMessage) SetTx(tx Tx) {
	msg.tx = tx
}

func (msg *txMessage) IsRequest() bool {
	_, ok := msg.Origin().(sip.Request)
	return ok
}
func (msg *txMessage) IsResponse() bool {
	_, ok := msg.Origin().(sip.Response)
	return ok
}

func (msg *txMessage) Origin() sip.Message {
	return msg.Message
}

type TxError interface {
	error
	UnwrapError() error
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
		parts = append(parts, "key "+err.TxKey.String())
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
		parts = append(parts, "key "+err.TxKey.String())
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
		parts = append(parts, "key "+err.TxKey.String())
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

// MakeServerTxKey creates server commonTx key for matching retransmitting requests - RFC 3261 17.2.3.
func MakeServerTxKey(msg sip.Message) (TxKey, error) {
	var sep = "__"

	firstViaHop, ok := msg.ViaHop()
	if !ok {
		return "", fmt.Errorf("'Via' header not found or empty in %s", msg.Short())
	}

	cseq, ok := msg.CSeq()
	if !ok {
		return "", fmt.Errorf("'CSeq' header not found in %s", msg.Short())
	}
	method := cseq.MethodName
	if method == sip.ACK || method == sip.CANCEL {
		method = sip.INVITE
	}

	var isRFC3261 bool
	branch, ok := firstViaHop.Params.Get("branch")
	if ok && branch.String() != "" &&
		strings.HasPrefix(branch.String(), sip.RFC3261BranchMagicCookie) &&
		strings.TrimPrefix(branch.String(), sip.RFC3261BranchMagicCookie) != "" {

		isRFC3261 = true
	} else {
		isRFC3261 = false
	}

	// RFC 3261 compliant
	if isRFC3261 {
		var port sip.Port

		if firstViaHop.Port == nil {
			port = sip.DefaultPort(firstViaHop.Transport)
		} else {
			port = *firstViaHop.Port
		}

		return TxKey(strings.Join([]string{
			branch.String(),  // branch
			firstViaHop.Host, // sent-by Host
			fmt.Sprint(port), // sent-by Port
			string(method),   // request Method
		}, sep)), nil
	}
	// RFC 2543 compliant
	from, ok := msg.From()
	if !ok {
		return "", fmt.Errorf("'From' header not found in %s", msg.Short())
	}
	fromTag, ok := from.Params.Get("tag")
	if !ok {
		return "", fmt.Errorf("'tag' param not found in 'From' header of %s", msg.Short())
	}
	callId, ok := msg.CallID()
	if !ok {
		return "", fmt.Errorf("'Call-ID' header not found in %s", msg.Short())
	}

	return TxKey(strings.Join([]string{
		// TODO: how to match core.Response in Send method to server tx? currently disabled
		// msg.Recipient().String(), // request-uri
		fromTag.String(),       // from tag
		callId.String(),        // Call-ID
		string(method),         // cseq method
		fmt.Sprint(cseq.SeqNo), // cseq num
		firstViaHop.String(),   // top Via
	}, sep)), nil
}

// MakeClientTxKey creates client commonTx key for matching responses - RFC 3261 17.1.3.
func MakeClientTxKey(msg sip.Message) (TxKey, error) {
	var sep = "__"

	cseq, ok := msg.CSeq()
	if !ok {
		return "", fmt.Errorf("'CSeq' header not found in %s", msg.Short())
	}
	method := cseq.MethodName
	if method == sip.ACK {
		method = sip.INVITE
	}

	firstViaHop, ok := msg.ViaHop()
	if !ok {
		return "", fmt.Errorf("'Via' header not found or empty in %s", msg.Short())
	}

	branch, ok := firstViaHop.Params.Get("branch")
	if !ok || len(branch.String()) == 0 ||
		!strings.HasPrefix(branch.String(), sip.RFC3261BranchMagicCookie) ||
		len(strings.TrimPrefix(branch.String(), sip.RFC3261BranchMagicCookie)) == 0 {
		return "", fmt.Errorf("'branch' not found or empty in 'Via' header of %s", msg.Short())
	}

	return TxKey(strings.Join([]string{
		branch.String(),
		string(method),
	}, sep)), nil
}
