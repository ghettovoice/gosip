package txs

import (
	"fmt"

	"github.com/discoviking/fsm"
	"github.com/ghettovoice/gosip/core"
	"github.com/ghettovoice/gosip/timing"
	"github.com/ghettovoice/gossip/base"
)

type ServerTransaction interface {
	Transaction
	Respond(res core.Response) error
}

type serverTransaction struct {
	transaction
	timer_g timing.Timer
	timer_h timing.Timer
	timer_i timing.Timer
}

func NewServerTransaction(txl Layer, origin core.Request, dest string) ServerTransaction {
	tx := new(serverTransaction)
	tx.txl = txl
	tx.origin = origin
	tx.dest = dest
	tx.initFSM()

	return tx
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

// FSM States
const (
	server_state_trying = iota
	server_state_proceeding
	server_state_completed
	server_state_confirmed
	server_state_terminated
)

// FSM Inputs
const (
	server_input_request fsm.Input = iota
	server_input_ack
	server_input_user_1xx
	server_input_user_2xx
	server_input_user_300_plus
	server_input_timer_g
	server_input_timer_h
	server_input_timer_i
	server_input_transport_err
	server_input_delete
)

// Choose the right FSM init function depending on request method.
func (tx *serverTransaction) initFSM() {
	if tx.Origin().IsInvite() {
		tx.initInviteFSM()
	} else {
		tx.initNonInviteFSM()
	}
}

func (tx *serverTransaction) initInviteFSM() {
	// Define States
	tx.Log().Debugf("%s initialises INVITE FSM", tx)

	// Proceeding
	server_state_def_proceeding := fsm.State{
		Index: server_state_proceeding,
		Outcomes: map[fsm.Input]fsm.Outcome{
			server_input_request:       {server_state_proceeding, tx.act_respond},
			server_input_user_1xx:      {server_state_proceeding, tx.act_respond},
			server_input_user_2xx:      {server_state_terminated, tx.act_respond_delete},
			server_input_user_300_plus: {server_state_completed, tx.act_respond},
			server_input_transport_err: {server_state_terminated, tx.act_trans_err},
		},
	}

	// Completed
	server_state_def_completed := fsm.State{
		Index: server_state_completed,
		Outcomes: map[fsm.Input]fsm.Outcome{
			server_input_request:       {server_state_completed, tx.act_respond},
			server_input_ack:           {server_state_confirmed, fsm.NO_ACTION},
			server_input_user_1xx:      {server_state_completed, fsm.NO_ACTION},
			server_input_user_2xx:      {server_state_completed, fsm.NO_ACTION},
			server_input_user_300_plus: {server_state_completed, fsm.NO_ACTION},
			server_input_timer_g:       {server_state_completed, tx.act_respond},
			server_input_timer_h:       {server_state_terminated, tx.act_timeout},
			server_input_transport_err: {server_state_terminated, tx.act_trans_err},
		},
	}

	// Confirmed
	server_state_def_confirmed := fsm.State{
		Index: server_state_confirmed,
		Outcomes: map[fsm.Input]fsm.Outcome{
			server_input_request:       {server_state_confirmed, fsm.NO_ACTION},
			server_input_user_1xx:      {server_state_confirmed, fsm.NO_ACTION},
			server_input_user_2xx:      {server_state_confirmed, fsm.NO_ACTION},
			server_input_user_300_plus: {server_state_confirmed, fsm.NO_ACTION},
			server_input_timer_i:       {server_state_terminated, tx.act_delete},
		},
	}

	// Terminated
	server_state_def_terminated := fsm.State{
		Index: server_state_terminated,
		Outcomes: map[fsm.Input]fsm.Outcome{
			server_input_request:       {server_state_terminated, fsm.NO_ACTION},
			server_input_ack:           {server_state_terminated, fsm.NO_ACTION},
			server_input_user_1xx:      {server_state_terminated, fsm.NO_ACTION},
			server_input_user_2xx:      {server_state_terminated, fsm.NO_ACTION},
			server_input_user_300_plus: {server_state_terminated, fsm.NO_ACTION},
			server_input_delete:        {server_state_terminated, tx.act_delete},
		},
	}

	// Define FSM
	fsm_, err := fsm.Define(
		server_state_def_proceeding,
		server_state_def_completed,
		server_state_def_confirmed,
		server_state_def_terminated,
	)
	if err != nil {
		tx.Log().Errorf("%s failed to define FSM: will be dropped, error: %s", tx, err.Error())
		return
	}

	tx.fsm = fsm_
}

func (tx *serverTransaction) initNonInviteFSM() {
	// Define States
	tx.Log().Debugf("%s initialises non-INVITE FSM", tx)

	// Trying
	server_state_def_trying := fsm.State{
		Index: server_state_trying,
		Outcomes: map[fsm.Input]fsm.Outcome{
			server_input_request:       {server_state_trying, fsm.NO_ACTION},
			server_input_user_1xx:      {server_state_proceeding, tx.act_respond},
			server_input_user_2xx:      {server_state_completed, tx.act_respond},
			server_input_user_300_plus: {server_state_completed, tx.act_respond},
		},
	}

	// Proceeding
	server_state_def_proceeding := fsm.State{
		Index: server_state_proceeding,
		Outcomes: map[fsm.Input]fsm.Outcome{
			server_input_request:       {server_state_proceeding, tx.act_respond},
			server_input_user_1xx:      {server_state_proceeding, tx.act_respond},
			server_input_user_2xx:      {server_state_completed, tx.act_final},
			server_input_user_300_plus: {server_state_completed, tx.act_final},
			server_input_transport_err: {server_state_terminated, tx.act_trans_err},
		},
	}

	// Completed
	server_state_def_completed := fsm.State{
		Index: server_state_completed,
		Outcomes: map[fsm.Input]fsm.Outcome{
			server_input_request:       {server_state_completed, tx.act_respond},
			server_input_user_1xx:      {server_state_completed, fsm.NO_ACTION},
			server_input_user_2xx:      {server_state_completed, fsm.NO_ACTION},
			server_input_user_300_plus: {server_state_completed, fsm.NO_ACTION},
			server_input_timer_h:       {server_state_terminated, tx.act_timeout},
			server_input_transport_err: {server_state_terminated, tx.act_trans_err},
		},
	}

	// Terminated
	server_state_def_terminated := fsm.State{
		Index: server_state_terminated,
		Outcomes: map[fsm.Input]fsm.Outcome{
			server_input_request:       {server_state_terminated, fsm.NO_ACTION},
			server_input_user_1xx:      {server_state_terminated, fsm.NO_ACTION},
			server_input_user_2xx:      {server_state_terminated, fsm.NO_ACTION},
			server_input_user_300_plus: {server_state_terminated, fsm.NO_ACTION},
			server_input_timer_h:       {server_state_terminated, fsm.NO_ACTION},
			server_input_delete:        {server_state_terminated, tx.act_delete},
		},
	}

	// Define FSM
	fsm_, err := fsm.Define(
		server_state_def_trying,
		server_state_def_proceeding,
		server_state_def_completed,
		server_state_def_terminated,
	)
	if err != nil {
		tx.Log().Errorf("%s failed to define FSM: will be dropped, error: %s", tx, err.Error())
		return
	}

	tx.fsm = fsm_
}

// Define actions.
// Send response
func (tx *serverTransaction) act_respond() fsm.Input {
	err := tx.tpl.Send(tx.Destination(), tx.lastResp)
	if err != nil {
		return server_input_transport_err
	}

	return fsm.NO_INPUT
}

// Send final response
func (tx *serverTransaction) act_final() fsm.Input {
	err := tx.tpl.Send(tx.Destination(), tx.lastResp)
	if err != nil {
		return server_input_transport_err
	}

	// Start timer J (we just reuse timer h)
	tx.timer_h = timing.AfterFunc(64*T1, func() {
		tx.fsm.Spin(server_input_timer_h)
	})

	return fsm.NO_INPUT
}

// Inform user of transport error
func (tx *serverTransaction) act_trans_err() fsm.Input {
	tx.tu_err <- errors.New("failed to send response")
	return server_input_delete
}

// Inform user of timeout error
func (tx *ServerTransaction) act_timeout() fsm.Input {
	tx.tu_err <- errors.New("transaction timed out")
	return server_input_delete
}

// Just delete the transaction.
func (tx *ServerTransaction) act_delete() fsm.Input {
	tx.Delete()
	return fsm.NO_INPUT
}

// Send response and delete the transaction.
func (tx *ServerTransaction) act_respond_delete() fsm.Input {
	tx.Delete()

	err := tx.transport.Send(tx.dest, tx.lastResp)
	if err != nil {
		return server_input_transport_err
	}
	return fsm.NO_INPUT
}
