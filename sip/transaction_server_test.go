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
	"github.com/ghettovoice/gosip/sip/header"
)

type sendResCall struct {
	ctx  context.Context
	res  *sip.ResponseEnvelope
	opts sip.SendResponseOptions
}

type stubSrvTransp struct {
	reliable bool

	sendResCalls chan sendResCall

	mu          sync.Mutex
	sendResHook func(sendResCall, int) error
	sendResCnt  int
}

func newStubSrvTransp(reliable bool) *stubSrvTransp {
	return &stubSrvTransp{
		reliable:     reliable,
		sendResCalls: make(chan sendResCall, 64),
	}
}

func (tp *stubSrvTransp) Metadata() sip.TransportMetadata {
	if tp == nil {
		return sip.TransportMetadata{}
	}

	var flags sip.TransportFlags

	flags.SetReliable(tp.reliable)

	return sip.TransportMetadata{Flags: flags}
}

func (tp *stubSrvTransp) SendResponse(
	ctx context.Context,
	res *sip.ResponseEnvelope,
	opts ...sip.SendResponseOptions,
) error {
	call := sendResCall{
		ctx:  ctx,
		res:  res,
		opts: util.LastSliceElem(opts, sip.SendResponseOptions{}),
	}

	tp.mu.Lock()
	idx := tp.sendResCnt
	tp.sendResCnt++
	hook := tp.sendResHook
	tp.mu.Unlock()

	if hook != nil {
		if err := hook(call, idx); err != nil {
			return err
		}
	}

	tp.sendResCalls <- call

	return nil
}

func (tp *stubSrvTransp) setSendResHook(hook func(sendResCall, int) error) {
	tp.mu.Lock()
	tp.sendResHook = hook
	tp.mu.Unlock()
}

func (tp *stubSrvTransp) waitSendRes(tb testing.TB) sendResCall {
	tb.Helper()
	return testutil.WaitResult(tb, tp.sendResCalls, "response send call")
}

func (tp *stubSrvTransp) ensureNoSendRes(tb testing.TB) {
	tb.Helper()

	tmr := time.NewTimer(1 * time.Second)
	defer tmr.Stop()

	select {
	case call := <-tp.sendResCalls:
		tb.Fatalf("unexpected response send call with status %v", call.res.Status())
	case <-tmr.C:
	}
}

func startSrvTx(tb testing.TB, tx sip.ServerTransaction) {
	tb.Helper()

	if err := tx.Start(tb.Context()); err != nil {
		tb.Fatalf("tx.Start() error = %v, want nil", err)
	}
}

func (tp *stubSrvTransp) sendResChan() <-chan sendResCall {
	return tp.sendResCalls
}

func (tp *stubSrvTransp) drainSendRess() {
	for {
		select {
		case <-tp.sendResCalls:
		default:
			return
		}
	}
}

func TestRestoreServerTransaction_Dispatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		typ     sip.TransactionType
		want    sip.ServerTransaction
		wantErr bool
	}{
		{name: "invite", typ: sip.TransactionTypeServerInvite, want: (*sip.InviteServerTransaction)(nil)},
		{name: "non-invite", typ: sip.TransactionTypeServerNonInvite, want: (*sip.NonInviteServerTransaction)(nil)},
		{name: "nil snapshot", wantErr: true},
		{name: "client invite", typ: sip.TransactionTypeClientInvite, wantErr: true},
		{name: "client non-invite", typ: sip.TransactionTypeClientNonInvite, wantErr: true},
		{name: "unknown type", typ: sip.TransactionType(255), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				local := netip.MustParseAddrPort("11.11.11.11:5070")
				remote := netip.MustParseAddrPort("55.55.55.55:5060")
				var snap *sip.ServerTransactionSnapshot
				switch tt.typ {
				case sip.TransactionTypeServerInvite:
					req := newInInviteReq(t, "UDP", sip.MagicCookie+".dispatch-inv", local, remote)
					tx, err := sip.NewInviteServerTransaction(req, newStubSrvTransp(false))
					if err != nil {
						t.Fatalf("sip.NewInviteServerTransaction() error = %v, want nil", err)
					}
					registerTxCleanup(t, tx)
					startSrvTx(t, tx)
					snap = mustSrvTxSnapshot(t, tx)
				case sip.TransactionTypeServerNonInvite:
					req := newInNonInviteReq(t, "UDP", sip.MagicCookie+".dispatch-ni", local, remote)
					tx, err := sip.NewNonInviteServerTransaction(req, newStubSrvTransp(false))
					if err != nil {
						t.Fatalf("sip.NewNonInviteServerTransaction() error = %v, want nil", err)
					}
					registerTxCleanup(t, tx)
					startSrvTx(t, tx)
					snap = mustSrvTxSnapshot(t, tx)
				default:
					if tt.typ != 0 {
						snap = &sip.ServerTransactionSnapshot{Type: tt.typ}
					}
				}

				got, err := sip.RestoreServerTransaction(snap, newStubSrvTransp(false))
				if tt.wantErr {
					if !errors.Is(err, sip.ErrInvalidTransactionSnapshot) {
						t.Fatalf("sip.RestoreServerTransaction() error = %v, want %v", err, sip.ErrInvalidTransactionSnapshot)
					}
					if got != nil {
						t.Fatalf("sip.RestoreServerTransaction() = %T, want nil", got)
					}
					return
				}
				if err != nil {
					t.Fatalf("sip.RestoreServerTransaction() error = %v, want nil", err)
				}
				registerTxCleanup(t, got)
				if reflect.TypeOf(got) != reflect.TypeOf(tt.want) {
					t.Fatalf("sip.RestoreServerTransaction() = %T, want %T", got, tt.want)
				}
			})
		})
	}
}

func TestServerTransactionKey_RoundTripBinary(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  sip.ServerTransactionKey
	}{
		{
			name: "rfc3261",
			key: sip.ServerTransactionKey{
				Branch: "z9hG4bK-123",
				SentBy: "Example.com:5060",
				Method: "INVITE",
			},
		},
		{
			name: "rfc2543",
			key: sip.ServerTransactionKey{
				Method:  "INVITE",
				URI:     "sip:user@example.com",
				FromTag: "from",
				ToTag:   "to",
				CallID:  "call",
				SeqNum:  42,
				Via:     "SIP/2.0/UDP example.com:5060",
			},
		},
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

			// t.Logf("hash: %x", data)

			var restored sip.ServerTransactionKey
			if err := restored.UnmarshalBinary(data); err != nil {
				t.Fatalf("new.UnmarshalBinary(data) error = %v, want nil", err)
			}
			if !original.Equal(&restored) {
				t.Fatalf("round-trip mismatch: got %+v, want %+v", restored, original)
			}
		})
	}
}

func TestServerTransactionKey_UnmarshalBinary_Invalid(t *testing.T) {
	t.Parallel()

	var key sip.ServerTransactionKey
	if err := key.UnmarshalBinary([]byte{0x03}); err == nil {
		t.Fatalf("key.UnmarshalBinary([]byte{0x03}) = nil, want error")
	}
}

func TestMakeServerTransactionKey_Method(t *testing.T) {
	t.Parallel()

	laddr := netip.MustParseAddrPort("192.168.1.100:5060")
	raddr := netip.MustParseAddrPort("0.0.0.0:5060")

	tests := []struct {
		name   string
		branch string
		method sip.RequestMethod
	}{
		{"rfc3261 invite", sip.MagicCookie + ".key-invite", sip.RequestMethodInvite},
		{"rfc3261 ack", sip.MagicCookie + ".key-ack", sip.RequestMethodAck},
		{"rfc3261 cancel", sip.MagicCookie + ".key-cancel", sip.RequestMethodCancel},
		{"rfc2543 invite", "legacy.key-invite", sip.RequestMethodInvite},
		{"rfc2543 ack", "legacy.key-ack", sip.RequestMethodAck},
		{"rfc2543 cancel", "legacy.key-cancel", sip.RequestMethodCancel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := newInviteReq(t, "UDP", tt.branch, raddr)
			req.Method = tt.method
			if cseq, ok := req.Headers.CSeq(); ok {
				req.Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: tt.method})
			}

			key, err := sip.MakeServerTransactionKey(req)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			if key.Method != string(tt.method) {
				t.Fatalf("key.Method = %q, want %q", key.Method, tt.method)
			}

			env := newInInviteReq(t, "UDP", tt.branch, laddr, raddr)
			env.WithMessage(func(r *sip.Request) {
				r.Method = tt.method
				if cseq, ok := r.Headers.CSeq(); ok {
					r.Headers.Set(&header.CSeq{SeqNum: cseq.SeqNum, Method: tt.method})
				}
			})

			envKey, err := sip.MakeServerTransactionKey(env)
			if err != nil {
				t.Fatalf("sip.MakeServerTransactionKey() error = %v, want nil", err)
			}
			if envKey.Method != string(tt.method) {
				t.Fatalf("envelope key.Method = %q, want %q", envKey.Method, tt.method)
			}
			if !key.Equal(envKey) {
				t.Fatalf("envelope key = %+v, want equal to %+v", envKey, key)
			}

			if got := key.Canonic().Method; got != string(tt.method) {
				t.Fatalf("key.Canonic().Method = %q, want %q", got, tt.method)
			}

			if tt.method != sip.RequestMethodAck {
				return
			}

			inviteKey := key
			inviteKey.Method = string(sip.RequestMethodInvite)
			if key.Equal(inviteKey) {
				t.Fatalf("ack key must not equal invite key %+v", inviteKey)
			}

			data, err := key.MarshalBinary()
			if err != nil {
				t.Fatalf("key.MarshalBinary() error = %v, want nil", err)
			}
			inviteData, err := inviteKey.MarshalBinary()
			if err != nil {
				t.Fatalf("inviteKey.MarshalBinary() error = %v, want nil", err)
			}
			if string(data) == string(inviteData) {
				t.Fatalf("ack key MarshalBinary = %x, want different from invite key", data)
			}
		})
	}
}
