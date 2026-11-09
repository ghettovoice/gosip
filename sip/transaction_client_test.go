package sip_test

import (
	"context"
	"net/netip"
	"reflect"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
	"github.com/ghettovoice/gosip/internal/testutil"
	"github.com/ghettovoice/gosip/internal/util"
	"github.com/ghettovoice/gosip/sip"
)

type sendReqCall struct {
	ctx  context.Context
	req  *sip.RequestEnvelope
	opts sip.SendRequestOptions
}

type stubClnTransp struct {
	reliable bool

	sendReqCalls chan sendReqCall

	mu          sync.Mutex
	sendReqHook func(sendReqCall, int) error
	sendReqCnt  int
}

func newStubClnTransp(reliable bool) *stubClnTransp {
	return &stubClnTransp{
		reliable:     reliable,
		sendReqCalls: make(chan sendReqCall, 64),
	}
}

func (tp *stubClnTransp) Metadata() sip.TransportMetadata {
	if tp == nil {
		return sip.TransportMetadata{}
	}

	var flags sip.TransportFlags

	flags.SetReliable(tp.reliable)

	return sip.TransportMetadata{Flags: flags}
}

func (tp *stubClnTransp) SendRequest(
	ctx context.Context,
	req *sip.RequestEnvelope,
	opts ...sip.SendRequestOptions,
) error {
	var reqCopy *sip.RequestEnvelope
	if req != nil {
		reqCopy = req.Clone().(*sip.RequestEnvelope)
	}

	call := sendReqCall{
		ctx:  ctx,
		req:  reqCopy,
		opts: util.LastSliceElem(opts, sip.SendRequestOptions{}),
	}

	tp.mu.Lock()
	idx := tp.sendReqCnt
	tp.sendReqCnt++
	hook := tp.sendReqHook
	tp.mu.Unlock()

	if hook != nil {
		if err := hook(call, idx); err != nil {
			return errors.Wrap(err)
		}
	}

	tp.sendReqCalls <- call

	return nil
}

func (tp *stubClnTransp) waitSendReq(tb testing.TB) sendReqCall {
	tb.Helper()
	return testutil.WaitResult(tb, tp.sendReqCalls, "request send call")
}

func (tp *stubClnTransp) ensureNoSendReq(tb testing.TB) {
	tb.Helper()

	tmr := time.NewTimer(1 * time.Second)
	defer tmr.Stop()

	select {
	case call := <-tp.sendReqCalls:
		var mtd sip.RequestMethod
		if call.req != nil {
			mtd = call.req.Method()
		}

		tb.Fatalf("unexpected request send call with method %v", mtd)
	case <-tmr.C:
	}
}

func (tp *stubClnTransp) drainSendReqs() {
	for {
		select {
		case <-tp.sendReqCalls:
		default:
			return
		}
	}
}

func startClnTx(tb testing.TB, tx sip.ClientTransaction) {
	tb.Helper()

	if err := tx.Start(tb.Context()); err != nil {
		tb.Fatalf("tx.Start() error = %v, want nil", err)
	}
}

func assertResStatus(tb testing.TB, resCh <-chan *sip.ResponseEnvelope, want sip.ResponseStatus) {
	tb.Helper()

	select {
	case res := <-resCh:
		if res.Status() != want {
			tb.Fatalf("res.Status = %v, want %v", res.Status(), want)
		}
	case <-time.After(100 * time.Millisecond):
		tb.Fatalf("response wait timeout, want %v", want)
	}
}

func TestClientTransaction_Start_TerminateInCallbackSuppressesSend(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("11.11.11.11:5070")
	raddr := netip.MustParseAddrPort("55.55.55.55:5060")
	tests := []struct {
		name string
		new  func(t *testing.T) (*stubClnTransp, startableTransact)
	}{
		{
			"invite",
			func(t *testing.T) (*stubClnTransp, startableTransact) {
				t.Helper()

				tp := newStubClnTransp(false)
				tx, err := sip.NewInviteClientTransaction(
					newOutInviteReq(t, "UDP", sip.MagicCookie+".lc-term-inv", laddr, raddr), tp,
				)
				if err != nil {
					t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
				}
				return tp, tx
			},
		},
		{
			"non-invite",
			func(t *testing.T) (*stubClnTransp, startableTransact) {
				t.Helper()

				tp := newStubClnTransp(false)
				tx, err := sip.NewNonInviteClientTransaction(
					newOutNonInviteReq(t, "UDP", sip.MagicCookie+".lc-term-non", laddr, raddr), tp,
				)
				if err != nil {
					t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
				}
				return tp, tx
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				tp, tx := tt.new(t)
				tx.BindStartHandler(sip.TransactionStartHandlerFunc(
					func(hdlrCtx context.Context) {
						if err := tx.Terminate(hdlrCtx, errors.ErrorWrap("from callback")); err != nil {
							t.Errorf("tx.Terminate() in start callback error = %v", err)
						}
					},
				))

				if err := tx.Start(ctx); !errors.Is(err, sip.ErrTransactionActionNotAllowed) {
					t.Fatalf("tx.Start() error = %v, want %v", err, sip.ErrTransactionActionNotAllowed)
				}
				if !tx.Started() {
					t.Fatal("tx.Started() = false, want true")
				}
				if got := tx.State(); got != sip.TransactionStateTerminated {
					t.Fatalf("tx.State() = %v, want %v", got, sip.TransactionStateTerminated)
				}
				tp.ensureNoSendReq(t)

				var snap *sip.ClientTransactionSnapshot
				switch tx := tx.(type) {
				case *sip.InviteClientTransaction:
					snap = mustClnTxSnapshot(t, tx)
				case *sip.NonInviteClientTransaction:
					snap = mustClnTxSnapshot(t, tx)
				default:
					t.Fatalf("unexpected tx type %T", tx)
				}
				if snap.TimerA != nil || snap.TimerB != nil || snap.TimerE != nil || snap.TimerF != nil {
					t.Fatalf("timers armed after terminate-in-start callback: %+v", snap)
				}
			})
		})
	}
}

func TestRestoreClientTransaction_Dispatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		typ     sip.TransactionType
		want    sip.ClientTransaction
		wantErr bool
	}{
		{name: "invite", typ: sip.TransactionTypeClientInvite, want: (*sip.InviteClientTransaction)(nil)},
		{name: "non-invite", typ: sip.TransactionTypeClientNonInvite, want: (*sip.NonInviteClientTransaction)(nil)},
		{name: "nil snapshot", wantErr: true},
		{name: "server invite", typ: sip.TransactionTypeServerInvite, wantErr: true},
		{name: "server non-invite", typ: sip.TransactionTypeServerNonInvite, wantErr: true},
		{name: "unknown type", typ: sip.TransactionType(255), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				local := netip.MustParseAddrPort("11.11.11.11:5070")
				remote := netip.MustParseAddrPort("55.55.55.55:5060")
				var snap *sip.ClientTransactionSnapshot
				switch tt.typ {
				case sip.TransactionTypeClientInvite:
					req := newOutInviteReq(t, "UDP", sip.MagicCookie+".dispatch-inv", local, remote)
					tx, err := sip.NewInviteClientTransaction(req, newStubClnTransp(false))
					if err != nil {
						t.Fatalf("sip.NewInviteClientTransaction() error = %v, want nil", err)
					}
					registerTxCleanup(t, tx)
					startClnTx(t, tx)
					snap = mustClnTxSnapshot(t, tx)
				case sip.TransactionTypeClientNonInvite:
					req := newOutNonInviteReq(t, "UDP", sip.MagicCookie+".dispatch-ni", local, remote)
					tx, err := sip.NewNonInviteClientTransaction(req, newStubClnTransp(false))
					if err != nil {
						t.Fatalf("sip.NewNonInviteClientTransaction() error = %v, want nil", err)
					}
					registerTxCleanup(t, tx)
					startClnTx(t, tx)
					snap = mustClnTxSnapshot(t, tx)
				default:
					if tt.typ != 0 {
						snap = &sip.ClientTransactionSnapshot{Type: tt.typ}
					}
				}

				got, err := sip.RestoreClientTransaction(snap, newStubClnTransp(false))
				if tt.wantErr {
					if !errors.Is(err, sip.ErrInvalidTransactionSnapshot) {
						t.Fatalf("sip.RestoreClientTransaction() error = %v, want %v", err, sip.ErrInvalidTransactionSnapshot)
					}
					if got != nil {
						t.Fatalf("sip.RestoreClientTransaction() = %T, want nil", got)
					}
					return
				}
				if err != nil {
					t.Fatalf("sip.RestoreClientTransaction() error = %v, want nil", err)
				}
				registerTxCleanup(t, got)
				if reflect.TypeOf(got) != reflect.TypeOf(tt.want) {
					t.Fatalf("sip.RestoreClientTransaction() = %T, want %T", got, tt.want)
				}
			})
		})
	}
}

func TestClientTransactionKey_RoundTripBinary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  sip.ClientTransactionKey
	}{
		{"standard", sip.ClientTransactionKey{Branch: "z9hG4bK-123", Method: "INVITE"}},
		{"lowercase method", sip.ClientTransactionKey{Branch: "z9hG4bK-abc", Method: "invite"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			original := tt.key
			data, err := original.MarshalBinary()
			if err != nil {
				t.Fatalf("key.MarshalBinary() error = %v", err)
			}

			if len(data) == 0 {
				t.Fatalf("key.MarshalBinary() = %v, want non-empty", data)
			}

			var restored sip.ClientTransactionKey
			if err := restored.UnmarshalBinary(data); err != nil {
				t.Fatalf("new.UnmarshalBinary(data) error = %v, want nil", err)
			}

			// Compare with canonical form (method uppercased)
			if !original.Canonic().Equal(&restored) {
				t.Fatalf("round-trip mismatch: got %+v, want %+v", restored, original.Canonic())
			}
		})
	}
}

func TestClientTransactionKey_UnmarshalBinary_Invalid(t *testing.T) {
	t.Parallel()

	var key sip.ClientTransactionKey
	if err := key.UnmarshalBinary([]byte{0x03}); err == nil {
		t.Fatalf("key.UnmarshalBinary([]byte{0x03}) = nil, want error")
	}
}
