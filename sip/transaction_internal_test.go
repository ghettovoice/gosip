package sip

import (
	"context"
	"net/netip"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/sip/header"
)

type stubClientTP struct{}

func (stubClientTP) Metadata() TransportMetadata { return UDPMetadata() }

func (stubClientTP) SendRequest(context.Context, *RequestEnvelope, ...SendRequestOptions) error {
	return nil
}

func newInternalInviteReq(branch string) *RequestEnvelope {
	viaAddr := netip.MustParseAddrPort("192.168.1.100:5060")
	req := &Request{
		Proto:  ProtoVer20(),
		Method: RequestMethodInvite,
		URI:    &URI{User: MakeUserInfo("alice"), Addr: MakeHostAddr("alice.voip.com")},
		Headers: make(Headers).
			Set(header.Via{
				{
					Proto:     ProtoVer20(),
					Transport: "UDP",
					Addr:      MakeHostPortAddr(viaAddr.Addr().String(), viaAddr.Port()),
					Params:    make(Values).Set("branch", branch),
				},
			}).
			Set(&header.From{
				URI:    &URI{User: MakeUserInfo("bob"), Addr: MakeHostAddr("bob.voip.com")},
				Params: make(Values).Set("tag", "from-1234"),
			}).
			Set(&header.To{
				URI: &URI{User: MakeUserInfo("alice"), Addr: MakeHostAddr("alice.voip.com")},
			}).
			Set(header.CallID("call-1234@bob.voip.com")).
			Set(&header.CSeq{SeqNum: 1, Method: RequestMethodInvite}).
			Set(DefaultMaxForwards),
	}
	return NewRequestEnvelope(req).
		SetTransport(UDPMetadata()).
		SetLocalAddr(netip.MustParseAddrPort("0.0.0.0:5060")).
		SetRemoteAddr(netip.MustParseAddrPort("192.168.1.100:5060"))
}

func TestTransaction_RegisterRejectsStartedBeforeReady(t *testing.T) {
	t.Parallel()

	tx, err := NewInviteClientTransaction(
		newInternalInviteReq(MagicCookie+".started-no-ready"), stubClientTP{},
	)
	if err != nil {
		t.Fatalf("NewInviteClientTransaction() error = %v, want nil", err)
	}

	if !tx.admitStart(t.Context()) {
		t.Fatal("tx.admitStart() = false, want true")
	}
	if tx.isActive() {
		t.Fatal("tx.activated() = true, want false (ready must stay closed)")
	}

	txm := &TransactionManager{}
	defer func() { _ = txm.Close(t.Context()) }()

	if err := txm.RegisterClientTransaction(t.Context(), tx); !errors.Is(err, ErrTransactionActionNotAllowed) {
		t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want %v",
			err, ErrTransactionActionNotAllowed)
	}
}

func TestTransaction_StartRejectsTerminatedStateBeforeMarker(t *testing.T) {
	t.Parallel()

	tx, err := NewInviteClientTransaction(
		newInternalInviteReq(MagicCookie+".term-no-marker"), stubClientTP{},
	)
	if err != nil {
		t.Fatalf("NewInviteClientTransaction() error = %v, want nil", err)
	}

	tx.state.Store(TransactionStateTerminated)
	if tx.isTerminated() {
		t.Fatal("tx.terminated() = true, want false (terminal marker gap)")
	}

	if err := tx.Start(t.Context()); !errors.Is(err, ErrTransactionActionNotAllowed) {
		t.Fatalf("tx.Start() error = %v, want %v", err, ErrTransactionActionNotAllowed)
	}
}

func TestTransaction_StateSetterClosesTerminalMarker(t *testing.T) {
	t.Parallel()

	tx, err := NewInviteClientTransaction(
		newInternalInviteReq(MagicCookie+".term-marker"), stubClientTP{},
	)
	if err != nil {
		t.Fatalf("NewInviteClientTransaction() error = %v, want nil", err)
	}

	if err := tx.fsm.FireCtx(t.Context(), txEvtTerminate, errors.ErrorWrap("test")); err != nil {
		t.Fatalf("tx.fsm.FireCtx(terminate) error = %v, want nil", err)
	}

	select {
	case <-tx.terminated:
	case <-time.After(5 * time.Second):
		t.Fatal("termCh not closed after terminated state store")
	}
	if err := tx.Start(t.Context()); !errors.Is(err, ErrTransactionActionNotAllowed) {
		t.Fatalf("tx.Start() error = %v, want %v", err, ErrTransactionActionNotAllowed)
	}
}

type staleStubTransaction struct {
	typ     TransactionType
	state   TransactionState
	started bool
}

func (tx *staleStubTransaction) Type() TransactionType    { return tx.typ }
func (tx *staleStubTransaction) State() TransactionState  { return tx.state }
func (tx *staleStubTransaction) Started() bool            { return tx.started }
func (*staleStubTransaction) MatchMessage(Message) bool   { return true }
func (*staleStubTransaction) LastError() error            { return nil }
func (*staleStubTransaction) Start(context.Context) error { return nil }
func (*staleStubTransaction) BindStateHandler(TransactionStateHandler) func() {
	return func() {}
}

func (*staleStubTransaction) BindStartHandler(TransactionStartHandler) func() {
	return func() {}
}
func (*staleStubTransaction) BindErrorHandler(ErrorHandler) func() { return func() {} }
func (*staleStubTransaction) Terminate(context.Context, error) error {
	return nil
}

func TestIsStaleTransaction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		typ     TransactionType
		started bool
		state   TransactionState
		want    bool
	}{
		{"client_invite/proceeding", TransactionTypeClientInvite, true, TransactionStateProceeding, true},
		{"client_invite/calling", TransactionTypeClientInvite, true, TransactionStateCalling, false},
		{"client_invite/completed", TransactionTypeClientInvite, true, TransactionStateCompleted, false},
		{"client_invite/unstarted", TransactionTypeClientInvite, false, TransactionStateProceeding, false},
		{"client_non_invite/trying", TransactionTypeClientNonInvite, true, TransactionStateTrying, false},
		{"client_non_invite/proceeding", TransactionTypeClientNonInvite, true, TransactionStateProceeding, false},
		{"server_invite/proceeding", TransactionTypeServerInvite, true, TransactionStateProceeding, true},
		{"server_invite/trying", TransactionTypeServerInvite, true, TransactionStateTrying, false},
		{"server_non_invite/trying", TransactionTypeServerNonInvite, true, TransactionStateTrying, true},
		{"server_non_invite/proceeding", TransactionTypeServerNonInvite, true, TransactionStateProceeding, true},
		{"server_non_invite/completed", TransactionTypeServerNonInvite, true, TransactionStateCompleted, false},
		{"server_non_invite/unstarted", TransactionTypeServerNonInvite, false, TransactionStateProceeding, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tx := &staleStubTransaction{typ: tt.typ, state: tt.state, started: tt.started}
			if got := isStaleTransaction(tx); got != tt.want {
				t.Errorf("isStaleTransaction(%v/%v/%v) = %v, want %v", tt.typ, tt.started, tt.state, got, tt.want)
			}
		})
	}
}

func newStaleFixtureTx(t *testing.T) *InviteClientTransaction {
	t.Helper()
	tx, err := NewInviteClientTransaction(
		newInternalInviteReq(MagicCookie+".wd-rec"), stubClientTP{},
	)
	if err != nil {
		t.Fatalf("NewInviteClientTransaction() error = %v, want nil", err)
	}
	tx.lcMu.Lock()
	tx.state.Store(TransactionStateProceeding)
	tx.started = true
	tx.lcMu.Unlock()
	return tx
}

func TestTransactionRegistration_StaleWatcherRearmIdentity(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		tx := newStaleFixtureTx(t)

		var calls atomic.Int32
		onTimeout := func(context.Context, Transaction) { calls.Add(1) }
		rec := &transactionRegistration[Transaction]{tx: tx, admitted: true}

		rec.watchStale(ctx, time.Second, onTimeout)
		rec.mu.Lock()
		first := rec.staleWatcher
		rec.mu.Unlock()
		if first == nil {
			t.Fatal("rec.staleWatcher = nil after watchStale, want armed watchdog")
		}

		tx.lcMu.Lock()
		tx.state.Store(TransactionStateCompleted)
		tx.lcMu.Unlock()
		rec.watchStale(ctx, time.Second, onTimeout)

		rec.mu.Lock()
		cleared := rec.staleWatcher == nil
		rec.mu.Unlock()
		if !cleared {
			t.Fatal("rec.staleWatcher != nil after leaving stale state, want cleared")
		}

		tx.lcMu.Lock()
		tx.state.Store(TransactionStateProceeding)
		tx.lcMu.Unlock()
		rec.watchStale(ctx, time.Second, onTimeout)

		rec.mu.Lock()
		second := rec.staleWatcher
		rec.mu.Unlock()
		if second == nil || second == first {
			t.Fatalf("rec.staleWatcher after rearm = %v, want fresh watchdog pointer", second)
		}

		rec.expireStaleWatchdog(ctx, first, onTimeout)
		if got := calls.Load(); got != 0 {
			t.Fatalf("onTimeout calls = %d after stale expireStaleWatchdog, want 0", got)
		}
		rec.mu.Lock()
		unchanged := rec.staleWatcher == second
		rec.mu.Unlock()
		if !unchanged {
			t.Fatal("rec.staleWatcher cleared by stale expireStaleWatchdog, want unchanged")
		}

		time.Sleep(2 * time.Second)
		if got := calls.Load(); got != 1 {
			t.Fatalf("onTimeout calls = %d after new deadline, want 1", got)
		}
	})
}

func TestTransactionRegistration_ExpireWatchdogAfterClose(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		tx := newStaleFixtureTx(t)

		var calls atomic.Int32
		onTimeout := func(context.Context, Transaction) { calls.Add(1) }
		rec := &transactionRegistration[Transaction]{tx: tx, admitted: true}

		rec.watchStale(ctx, time.Second, onTimeout)
		rec.mu.Lock()
		watcher := rec.staleWatcher
		rec.mu.Unlock()
		if watcher == nil {
			t.Fatal("rec.staleWatcher = nil after watchStale, want armed watchdog")
		}

		rec.close()
		rec.expireStaleWatchdog(ctx, watcher, onTimeout)
		if got := calls.Load(); got != 0 {
			t.Fatalf("onTimeout calls = %d after closed record, want 0", got)
		}

		time.Sleep(2 * time.Second)
		if got := calls.Load(); got != 0 {
			t.Fatalf("onTimeout calls = %d after closed record deadline, want 0", got)
		}
	})
}
