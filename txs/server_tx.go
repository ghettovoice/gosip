package txs

import (
	"fmt"

	"github.com/discoviking/fsm"
	"github.com/ghettovoice/gosip/core"
)

type ServerTransaction interface {
	Transaction
	Respond(res core.Response) error
}

type serverTransaction struct {
	transaction
}

func (tx *serverTransaction) String() string {
	return fmt.Sprintf("Server%s", tx.transaction.String())
}

func (tx *serverTransaction) Receive(msg core.Message) error {
	req, ok := msg.(core.Request)
	if !ok {
		return &UnexpectedMessageError{
			fmt.Errorf("%s recevied unexpected %s", tx, msg.Short()),
			msg.String(),
		}
	}

	var input fsm.Input = fsm.NO_INPUT
	switch {
	case req.Method() == tx.Origin().Method():
		input = server_input_request
	case req.IsAck(): // ACK for non-2xx response
		input = server_input_ack
		tx.ack <- req
	default:
		return &UnexpectedMessageError{
			fmt.Errorf("invalid message %s correlated to server transaction %p", req.Short(), tx),
			msg.String(),
		}
	}

	return tx.fsm.Spin(input)
}

func (tx *serverTransaction) Respond(res core.Response) error {
	tx.lastResp = res

	var input fsm.Input
	switch {
	case res.IsProvisional():
		input = server_input_user_1xx
	case res.IsSuccess():
		input = server_input_user_2xx
	default:
		input = server_input_user_300_plus
	}

	return tx.fsm.Spin(input)
}

func (tx *serverTransaction) Trying(hdrs ...core.Header) error {
	trying := core.NewResponse(
		tx.Origin().SipVersion(),
		100,
		"Trying",
		[]core.Header{},
		"",
	)
	trying.SetLog(tx.Log())

	core.CopyHeaders("Via", tx.Origin(), trying)
	core.CopyHeaders("From", tx.origin, trying)
	core.CopyHeaders("To", tx.origin, trying)
	core.CopyHeaders("Call-Id", tx.origin, trying)
	core.CopyHeaders("CSeq", tx.origin, trying)
	// RFC 3261 - 8.2.6.1
	// Any Timestamp header field present in the request MUST be copied into this 100 (Trying) response.
	// TODO delay?
	core.CopyHeaders("Timestamp", tx.origin, trying)
	// additional custom headers
	for _, h := range hdrs {
		trying.AppendHeader(h)
	}

	// change FSM to send provisional response
	tx.lastResp = trying
	return tx.fsm.Spin(server_input_user_1xx)
}

func (tx *serverTransaction) Ok(hdrs ...core.Header) error {
	ok := core.NewResponse(
		tx.Origin().SipVersion(),
		200,
		"OK",
		[]core.Header{},
		"",
	)
	ok.SetLog(tx.Log())

	core.CopyHeaders("Via", tx.Origin(), ok)
	core.CopyHeaders("From", tx.origin, ok)
	core.CopyHeaders("To", tx.origin, ok)
	core.CopyHeaders("Call-Id", tx.origin, ok)
	core.CopyHeaders("CSeq", tx.origin, ok)
	// additional custom headers
	for _, h := range hdrs {
		ok.AppendHeader(h)
	}

	// change FSM to send provisional response
	tx.lastResp = ok
	return tx.fsm.Spin(server_input_user_2xx)
}
