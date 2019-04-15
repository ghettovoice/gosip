package transaction

import (
	"fmt"
	"sync"
	"time"

	"github.com/discoviking/fsm"
	"github.com/ghettovoice/gosip/log"
	"github.com/ghettovoice/gosip/sip"
	"github.com/ghettovoice/gosip/timing"
	"github.com/ghettovoice/gosip/transport"
)

type ClientTx interface {
	Tx
	Responses() <-chan sip.Response
}

type clientTx struct {
	commonTx
	responses    chan sip.Response
	timer_a_time time.Duration // Current duration of timer A.
	timer_a      timing.Timer
	timer_b      timing.Timer
	timer_d_time time.Duration // Current duration of timer D.
	timer_d      timing.Timer
	reliable     bool
	mu           *sync.RWMutex
}

func NewClientTx(origin sip.Request, tpl transport.Layer) (ClientTx, error) {
	tx := new(clientTx)
	tx.logger = log.NewSafeLocalLogger()
	tx.origin = origin
	tx.tpl = tpl
	// buffer chan - about ~10 retransmit responses
	tx.responses = make(chan sip.Response, 10)
	tx.errs = make(chan error, 10)
	tx.done = make(chan bool)
	tx.mu = new(sync.RWMutex)

	return tx, nil
}

func (tx *clientTx) Init() error {
	tx.initFSM()

	if err := tx.tpl.Send(tx.Origin()); err != nil {
		tx.lastErr = err
		tx.fsm.Spin(client_input_transport_err)
		return err
	}

	if key, err := MakeClientTxKey(tx.Origin()); err != nil {
		tx.lastErr = err
		tx.fsm.Spin(client_input_transport_err)
		return err
	} else {
		tx.key = key
		if viaHop, ok := tx.Origin().ViaHop(); ok {
			tx.reliable = tx.tpl.IsReliable(viaHop.Transport)
		}
	}

	if tx.reliable {
		tx.timer_d_time = 0
	} else {
		// RFC 3261 - 17.1.1.2.
		// If an unreliable transport is being used, the client transaction MUST start timer A with a value of T1.
		// If a reliable transport is being used, the client transaction SHOULD NOT
		// start timer A (Timer A controls request retransmissions).
		// Timer A - retransmission
		tx.Log().Debugf("%s, timer_a set to %v", tx, Timer_A)
		tx.timer_a_time = Timer_A
		tx.timer_a = timing.AfterFunc(tx.timer_a_time, func() {
			tx.Log().Debugf("%s, timer_a fired", tx)
			tx.fsm.Spin(client_input_timer_a)
		})
		// Timer D is set to 32 seconds for unreliable transports
		tx.timer_d_time = Timer_D
	}
	// Timer B - timeout
	tx.Log().Debugf("%s, timer_b set to %v", tx, Timer_B)
	tx.timer_b = timing.AfterFunc(Timer_B, func() {
		tx.Log().Debugf("%s, timer_b fired", tx)
		tx.fsm.Spin(client_input_timer_b)
	})

	return tx.lastErr
}

func (tx *clientTx) String() string {
	return fmt.Sprintf("Client%s", tx.commonTx.String())
}

func (tx *clientTx) Receive(msg sip.Message) error {
	res, ok := msg.(sip.Response)
	if !ok {
		return &sip.UnexpectedMessageError{
			fmt.Errorf("%s recevied unexpected %s", tx, msg.Short()),
			msg.String(),
		}
	}
	tx.mu.Lock()
	tx.lastResp = res
	tx.mu.Unlock()
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

func (tx *clientTx) Responses() <-chan sip.Response {
	return tx.responses
}

func (tx *clientTx) Terminate() {
	select {
	case <-tx.done:
		return
	default:
	}
	tx.delete()
}

func (tx clientTx) ack() {
	ack := sip.NewRequest(
		sip.ACK,
		tx.Origin().Recipient(),
		tx.Origin().SipVersion(),
		[]sip.Header{},
		"",
	)
	ack.SetLog(tx.Log())

	// Copy headers from original request.
	// TODO: Safety
	sip.CopyHeaders("From", tx.Origin(), ack)
	sip.CopyHeaders("Call-ID", tx.Origin(), ack)
	sip.CopyHeaders("Route", tx.Origin(), ack)
	sip.CopyHeaders("To", tx.lastResp, ack)
	cseq, ok := tx.Origin().CSeq()
	if !ok {
		tx.Log().Errorf("failed to send ACK request on client transaction %p: %s", tx)
		return
	}
	cseq = cseq.Clone().(*sip.CSeq)
	cseq.MethodName = sip.ACK
	ack.AppendHeader(cseq)
	via, ok := tx.Origin().Via()
	if !ok {
		tx.Log().Errorf("failed to send ACK request on client transaction %p: %s", tx)
		return
	}
	via = via.Clone().(sip.ViaHeader)
	ack.AppendHeader(via)

	// Send the ACK.
	err := tx.tpl.Send(ack)
	if err != nil {
		tx.Log().Warnf("failed to send ACK request on client transaction %p: %s", tx, err)
		tx.lastErr = err
		tx.fsm.Spin(client_input_transport_err)
	}
}

// FSM States
const (
	client_state_calling = iota
	client_state_proceeding
	client_state_completed
	client_state_terminated
)

// FSM Inputs
const (
	client_input_1xx fsm.Input = iota
	client_input_2xx
	client_input_300_plus
	client_input_timer_a
	client_input_timer_b
	client_input_timer_d
	client_input_transport_err
	client_input_delete
)

// Initialises the correct kind of FSM based on request method.
func (tx *clientTx) initFSM() {
	if tx.Origin().IsInvite() {
		tx.initInviteFSM()
	} else {
		tx.initNonInviteFSM()
	}
}

func (tx *clientTx) initInviteFSM() {
	tx.Log().Debugf("initialising INVITE client transaction %p FSM", tx)
	// Define States
	// Calling
	client_state_def_calling := fsm.State{
		Index: client_state_calling,
		Outcomes: map[fsm.Input]fsm.Outcome{
			client_input_1xx:           {client_state_proceeding, tx.act_passup},
			client_input_2xx:           {client_state_terminated, tx.act_passup_delete},
			client_input_300_plus:      {client_state_completed, tx.act_invite_final},
			client_input_timer_a:       {client_state_calling, tx.act_invite_resend},
			client_input_timer_b:       {client_state_terminated, tx.act_timeout},
			client_input_transport_err: {client_state_terminated, tx.act_trans_err},
		},
	}

	// Proceeding
	client_state_def_proceeding := fsm.State{
		Index: client_state_proceeding,
		Outcomes: map[fsm.Input]fsm.Outcome{
			client_input_1xx:      {client_state_proceeding, tx.act_passup},
			client_input_2xx:      {client_state_terminated, tx.act_passup_delete},
			client_input_300_plus: {client_state_completed, tx.act_invite_final},
			client_input_timer_a:  {client_state_proceeding, fsm.NO_ACTION},
			client_input_timer_b:  {client_state_proceeding, fsm.NO_ACTION},
		},
	}

	// Completed
	client_state_def_completed := fsm.State{
		Index: client_state_completed,
		Outcomes: map[fsm.Input]fsm.Outcome{
			client_input_1xx:           {client_state_completed, fsm.NO_ACTION},
			client_input_2xx:           {client_state_completed, fsm.NO_ACTION},
			client_input_300_plus:      {client_state_completed, tx.act_ack},
			client_input_transport_err: {client_state_terminated, tx.act_trans_err},
			client_input_timer_a:       {client_state_completed, fsm.NO_ACTION},
			client_input_timer_b:       {client_state_completed, fsm.NO_ACTION},
			client_input_timer_d:       {client_state_terminated, tx.act_delete},
		},
	}

	// Terminated
	client_state_def_terminated := fsm.State{
		Index: client_state_terminated,
		Outcomes: map[fsm.Input]fsm.Outcome{
			client_input_1xx:      {client_state_terminated, fsm.NO_ACTION},
			client_input_2xx:      {client_state_terminated, fsm.NO_ACTION},
			client_input_300_plus: {client_state_terminated, fsm.NO_ACTION},
			client_input_timer_a:  {client_state_terminated, fsm.NO_ACTION},
			client_input_timer_b:  {client_state_terminated, fsm.NO_ACTION},
			client_input_timer_d:  {client_state_terminated, fsm.NO_ACTION},
			client_input_delete:   {client_state_terminated, tx.act_delete},
		},
	}

	fsm_, err := fsm.Define(
		client_state_def_calling,
		client_state_def_proceeding,
		client_state_def_completed,
		client_state_def_terminated,
	)

	if err != nil {
		tx.Log().Errorf("failure to define INVITE client transaction %p fsm: %s", tx, err.Error())
	}

	tx.fsm = fsm_
}

func (tx *clientTx) initNonInviteFSM() {
	tx.Log().Debugf("initialising non-INVITE client transaction %p FSM", tx)
	// Define States
	// "Trying"
	client_state_def_calling := fsm.State{
		Index: client_state_calling,
		Outcomes: map[fsm.Input]fsm.Outcome{
			client_input_1xx:           {client_state_proceeding, tx.act_passup},
			client_input_2xx:           {client_state_completed, tx.act_non_invite_final},
			client_input_300_plus:      {client_state_completed, tx.act_non_invite_final},
			client_input_timer_a:       {client_state_calling, tx.act_non_invite_resend},
			client_input_timer_b:       {client_state_terminated, tx.act_timeout},
			client_input_transport_err: {client_state_terminated, tx.act_trans_err},
		},
	}

	// Proceeding
	client_state_def_proceeding := fsm.State{
		Index: client_state_proceeding,
		Outcomes: map[fsm.Input]fsm.Outcome{
			client_input_1xx:           {client_state_proceeding, tx.act_passup},
			client_input_2xx:           {client_state_completed, tx.act_non_invite_final},
			client_input_300_plus:      {client_state_completed, tx.act_non_invite_final},
			client_input_timer_a:       {client_state_proceeding, tx.act_non_invite_resend},
			client_input_timer_b:       {client_state_terminated, tx.act_timeout},
			client_input_transport_err: {client_state_terminated, tx.act_trans_err},
		},
	}

	// Completed
	client_state_def_completed := fsm.State{
		Index: client_state_completed,
		Outcomes: map[fsm.Input]fsm.Outcome{
			client_input_1xx:      {client_state_completed, fsm.NO_ACTION},
			client_input_2xx:      {client_state_completed, fsm.NO_ACTION},
			client_input_300_plus: {client_state_completed, fsm.NO_ACTION},
			client_input_timer_d:  {client_state_terminated, tx.act_delete},
			client_input_timer_a:  {client_state_completed, fsm.NO_ACTION},
			client_input_timer_b:  {client_state_completed, fsm.NO_ACTION},
		},
	}

	// Terminated
	client_state_def_terminated := fsm.State{
		Index: client_state_terminated,
		Outcomes: map[fsm.Input]fsm.Outcome{
			client_input_1xx:      {client_state_terminated, fsm.NO_ACTION},
			client_input_2xx:      {client_state_terminated, fsm.NO_ACTION},
			client_input_300_plus: {client_state_terminated, fsm.NO_ACTION},
			client_input_timer_a:  {client_state_terminated, fsm.NO_ACTION},
			client_input_timer_b:  {client_state_terminated, fsm.NO_ACTION},
			client_input_timer_d:  {client_state_terminated, fsm.NO_ACTION},
			client_input_delete:   {client_state_terminated, tx.act_delete},
		},
	}

	fsm_, err := fsm.Define(
		client_state_def_calling,
		client_state_def_proceeding,
		client_state_def_completed,
		client_state_def_terminated,
	)

	if err != nil {
		tx.Log().Errorf("failure to define INVITE client transaction %p fsm: %s", tx, err.Error())
	}

	tx.fsm = fsm_
}

func (tx *clientTx) resend() {
	tx.Log().Infof("%s resend %v", tx, tx.Origin().Short())
	tx.lastErr = tx.tpl.Send(tx.Origin())
	if tx.lastErr != nil {
		tx.fsm.Spin(client_input_transport_err)
	}
}

func (tx *clientTx) passUp() {
	tx.mu.RLock()
	lastResp := tx.lastResp
	tx.mu.RUnlock()

	if lastResp != nil {
		tx.mu.RLock()
		select {
		case <-tx.done:
		case tx.responses <- lastResp:
		}
		tx.mu.RUnlock()
	}
}

func (tx *clientTx) transportErr() {
	// todo bloody patch
	defer func() { recover() }()

	err := &TxTransportError{
		fmt.Errorf("%s failed to send %s: %s", tx, tx.lastResp.Short(), tx.lastErr),
		tx.Key(),
		tx.String(),
	}
	tx.mu.RLock()
	select {
	case <-tx.done:
	case tx.errs <- err:
	}
	tx.mu.RUnlock()
}

func (tx *clientTx) timeoutErr() {
	// todo bloody patch
	defer func() { recover() }()

	err := &TxTimeoutError{
		fmt.Errorf("%s timed out", tx),
		tx.Key(),
		tx.String(),
	}
	tx.mu.RLock()
	select {
	case <-tx.done:
	case tx.errs <- err:
	}
	tx.mu.RUnlock()
}

func (tx *clientTx) delete() {
	select {
	case <-tx.done:
		return
	default:
	}
	// todo bloody patch
	defer func() { recover() }()

	close(tx.done)

	tx.mu.Lock()
	if tx.timer_a != nil {
		tx.timer_a.Stop()
	}
	if tx.timer_b != nil {
		tx.timer_b.Stop()
	}
	if tx.timer_d != nil {
		tx.timer_d.Stop()
	}
	tx.mu.Unlock()

	time.Sleep(time.Microsecond)

	tx.mu.Lock()
	close(tx.responses)
	close(tx.errs)
	tx.mu.Unlock()
}

// Define actions
func (tx *clientTx) act_invite_resend() fsm.Input {
	tx.Log().Debugf("%s, act_invite_resend", tx)
	tx.mu.Lock()
	tx.timer_a_time *= 2
	tx.timer_a.Reset(tx.timer_a_time)
	tx.mu.Unlock()
	tx.resend()
	return fsm.NO_INPUT
}

func (tx *clientTx) act_non_invite_resend() fsm.Input {
	tx.Log().Debugf("%s, act_non_invite_resend", tx)
	tx.mu.Lock()
	tx.timer_a_time *= 2
	// For non-INVITE, cap timer A at T2 seconds.
	if tx.timer_a_time > T2 {
		tx.timer_a_time = T2
	}
	tx.timer_a.Reset(tx.timer_a_time)
	tx.mu.Unlock()
	tx.resend()
	return fsm.NO_INPUT
}

func (tx *clientTx) act_passup() fsm.Input {
	tx.Log().Debugf("%s, act_passup", tx)
	tx.passUp()
	return fsm.NO_INPUT
}

func (tx *clientTx) act_invite_final() fsm.Input {
	tx.Log().Debugf("%s, act_invite_final", tx)
	tx.passUp()
	tx.ack()
	tx.mu.Lock()
	if tx.timer_d != nil {
		tx.timer_d.Stop()
	}
	tx.timer_d = timing.AfterFunc(tx.timer_d_time, func() {
		tx.fsm.Spin(client_input_timer_d)
	})
	tx.mu.Unlock()
	return fsm.NO_INPUT
}

func (tx *clientTx) act_non_invite_final() fsm.Input {
	tx.Log().Debugf("%s, act_non_invite_final", tx)
	tx.passUp()
	tx.mu.Lock()
	if tx.timer_d != nil {
		tx.timer_d.Stop()
	}
	tx.timer_d = timing.AfterFunc(tx.timer_d_time, func() {
		tx.fsm.Spin(client_input_timer_d)
	})
	tx.mu.Unlock()
	return fsm.NO_INPUT
}

func (tx *clientTx) act_ack() fsm.Input {
	tx.Log().Debugf("%s, act_ack", tx)
	tx.ack()
	return fsm.NO_INPUT
}

func (tx *clientTx) act_trans_err() fsm.Input {
	tx.Log().Debugf("%s, act_trans_err", tx)
	tx.transportErr()
	return client_input_delete
}

func (tx *clientTx) act_timeout() fsm.Input {
	tx.Log().Debugf("%s, act_timeout", tx)
	tx.timeoutErr()
	return client_input_delete
}

func (tx *clientTx) act_passup_delete() fsm.Input {
	tx.Log().Debugf("%s, act_passup_delete", tx)
	tx.passUp()
	return client_input_delete
}

func (tx *clientTx) act_delete() fsm.Input {
	tx.Log().Debugf("%s, act_delete", tx)
	tx.delete()
	return fsm.NO_INPUT
}
