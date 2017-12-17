package txs

import (
	"fmt"

	"github.com/discoviking/fsm"
	"github.com/ghettovoice/gosip/core"
)

type ClientTransaction interface {
	Transaction
}

type clientTransaction struct {
	transaction
	lastResp core.Response
}

func (tx *clientTransaction) String() string {
	return fmt.Sprintf("Server%s", tx.transaction.String())
}

func (tx *clientTransaction) Receive(msg core.Message) error {
	res, ok := msg.(core.Response)
	if !ok {
		return &UnexpectedMessageError{
			fmt.Errorf("%s recevied unexpected %s", tx, msg.Short()),
			msg.String(),
		}
	}

	tx.lastResp = res

	var input fsm.Input
	switch {
	case res.IsProvisional():
		input = client_input_1xx
	case res.IsSuccess():
		input = client_input_2xx
	default:
		input = client_input_300_plus
	}

	return tx.fsm.Spin(input)
}

func (tx clientTransaction) ack() {
	ack := core.NewRequest(
		core.ACK,
		tx.Origin().Recipient(),
		tx.Origin().SipVersion(),
		[]core.Header{},
		"",
	)
	ack.SetLog(tx.Log())

	// Copy headers from original request.
	// TODO: Safety
	core.CopyHeaders("From", tx.origin, ack)
	core.CopyHeaders("Call-Id", tx.origin, ack)
	core.CopyHeaders("Route", tx.origin, ack)
	cseq, ok := tx.Origin().CSeq()
	if !ok {
		tx.Log().Errorf("failed to send ACK request on client transaction %p: %s", tx)
		return
	}
	cseq = cseq.Clone().(*core.CSeq)
	cseq.MethodName = core.ACK
	ack.AppendHeader(cseq)
	via, ok := tx.origin.Via()
	if !ok {
		tx.Log().Errorf("failed to send ACK request on client transaction %p: %s", tx)
		return
	}
	via = via.Clone().(core.ViaHeader)
	ack.AppendHeader(via)
	// Copy headers from response.
	core.CopyHeaders("To", tx.lastResp, ack)

	// Send the ACK.
	err := tx.transport.Send(tx.dest, ack)
	if err != nil {
		tx.Log().Warnf("failed to send ACK request on client transaction %p: %s", tx, err)
		tx.lastErr = err
		tx.fsm.Spin(client_input_transport_err)
	}
}
