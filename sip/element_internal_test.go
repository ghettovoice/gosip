package sip

import (
	"context"
	"runtime"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
)

type countingClnTxHdlr struct {
	calls atomic.Int32
}

func (h *countingClnTxHdlr) HandleClientTransaction(context.Context, ClientTransaction) {
	h.calls.Add(1)
}

type countingOutReqIntcptr struct {
	calls atomic.Int32
}

func (i *countingOutReqIntcptr) InterceptOutboundRequest(
	ctx context.Context,
	next RequestSender,
	req *RequestEnvelope,
	opts ...SendRequestOptions,
) error {
	i.calls.Add(1)
	return errors.Wrap(next.SendRequest(ctx, req, opts...))
}

// TestElementInternal_ManagersInitializedOnce proves lazy initialization
// creates the private transport and transaction managers exactly once:
// public queries before and after Close reuse the same instances, and Close
// leaves them in their closed state instead of recreating them.
func TestElementInternal_ManagersInitializedOnce(t *testing.T) {
	t.Parallel()

	elm := &Element{}
	if elm.Logger() == nil {
		t.Fatal("elm.Logger() = nil, want initialized element")
	}

	tpm := elm.tpm
	txm := elm.txm
	if tpm == nil {
		t.Fatal("elm.tpm = nil after init, want initialized transport manager")
	}
	if txm == nil {
		t.Fatal("elm.txm = nil after init, want initialized transaction manager")
	}

	// Public queries reuse the same private managers.
	_ = slices.Collect(elm.AllTransports())
	_ = slices.Collect(elm.AllTransportMetadata())
	_ = slices.Collect(elm.AllClientTransactions())
	_ = slices.Collect(elm.AllServerTransactions())
	if _, ok := elm.TransportMetadataByProto(UDPMetadata().Proto); ok {
		t.Error("elm.TransportMetadataByProto(UDP) = true, want no transports")
	}

	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}

	// Post-close queries still observe the same instances.
	_ = slices.Collect(elm.AllTransports())
	_ = slices.Collect(elm.AllClientTransactions())
	if elm.tpm != tpm {
		t.Error("elm.tpm changed after Close, want stable private manager")
	}
	if elm.txm != txm {
		t.Error("elm.txm changed after Close, want stable private manager")
	}

	// The retained managers are closed, not recreated. TrackTransport checks
	// the nil argument before the lifecycle, so the closed state is verified
	// through the private lifecycle state instead.
	if err := txm.checkRunning(); !errors.Is(err, ErrTransactionManagerClosed) {
		t.Errorf("txm.checkRunning() error = %v, want %v", err, ErrTransactionManagerClosed)
	}
	tpm.lcMu.Lock()
	tpmState := tpm.state
	tpm.lcMu.Unlock()
	if tpmState < tpmStateClosing {
		t.Errorf("tpm.state = %d after Close, want >= %d (closing or closed)", tpmState, tpmStateClosing)
	}
}

// TestElementInternal_TransactionTimeoutForwarding proves the element forwards
// the configured transaction timeouts to its private transaction manager
// unchanged; the manager applies its own defaults internally and the public
// fields are not rewritten.
func TestElementInternal_TransactionTimeoutForwarding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stale time.Duration
		start time.Duration
	}{
		{"zero", 0, 0},
		{"positive", 3 * time.Minute, 2 * time.Second},
		{"negative", -time.Second, -time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			elm := &Element{
				StaleTransactionTimeout: tt.stale,
				TransactionStartTimeout: tt.start,
			}
			t.Cleanup(func() { _ = elm.Close() })

			elm.ensureInit()
			if got := elm.txm.TransactionStartTimeout; got != tt.start {
				t.Errorf("txm.TransactionStartTimeout = %v, want raw configured %v", got, tt.start)
			}
			if got := elm.txm.StaleTransactionTimeout; got != tt.stale {
				t.Errorf("txm.StaleTransactionTimeout = %v, want raw configured %v", got, tt.stale)
			}
		})
	}
}

// TestElementInternal_CallbackLeaseBlocksClose proves the callback drain is
// independent of the admitted-operation gate: an outstanding callback lease
// keeps Close blocked after callback admission was already sealed, and Close
// completes once the lease is released.
func TestElementInternal_CallbackLeaseBlocksClose(t *testing.T) {
	t.Parallel()

	elm := &Element{}
	elm.ensureInit()

	release, ok := elm.admitCallback()
	if !ok {
		t.Fatal("elm.admitCallback() rejected on a running element")
	}

	released := false
	closeDone := make(chan error, 1)
	closeRead := false
	t.Cleanup(func() {
		if !released {
			release()
		}
		if closeRead {
			return
		}
		select {
		case err := <-closeDone:
			if err != nil {
				t.Errorf("elm.Close() error = %v, want nil", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("elm.Close() did not finish after releasing the callback lease")
		}
	})

	go func() { closeDone <- elm.Close() }()

	// Wait until Close seals callback admission; the outstanding lease keeps
	// the callback group non-zero, so Close must still be blocked on the
	// callback drain.
	tmr := time.NewTimer(5 * time.Second)
	defer tmr.Stop()
	for {
		elm.lcMu.Lock()
		sealed := elm.cbSealed
		elm.lcMu.Unlock()
		if sealed {
			break
		}
		select {
		case <-tmr.C:
			t.Fatal("timed out waiting for Close to seal callback admission")
		default:
			runtime.Gosched()
		}
	}

	select {
	case err := <-closeDone:
		closeRead = true
		t.Fatalf("elm.Close() = %v completed while a callback lease is outstanding, want blocked", err)
	default:
	}

	released = true
	release()

	select {
	case err := <-closeDone:
		closeRead = true
		if err != nil {
			t.Fatalf("elm.Close() error = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for elm.Close()")
	}

	if _, ok := elm.admitCallback(); ok {
		t.Error("elm.admitCallback() admitted after close, want sealed")
	}
	if _, ok := elm.admitOp(); ok {
		t.Error("elm.admitOp() admitted after close, want rejected")
	}
}

// TestElementInternal_CallbackAdmissionSealed covers the callback drain lease:
// callbacks are admitted while the element is running and rejected once Close
// sealed admission before closing middleware.
func TestElementInternal_CallbackAdmissionSealed(t *testing.T) {
	t.Parallel()

	elm := &Element{}

	release, ok := elm.admitCallback()
	if !ok {
		t.Fatal("elm.admitCallback() rejected on a running element")
	}
	release()

	opRelease, ok := elm.admitOp()
	if !ok {
		t.Fatal("elm.admitOp() rejected on a running element")
	}
	opRelease()

	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}

	if _, ok := elm.admitCallback(); ok {
		t.Error("elm.admitCallback() admitted after close, want sealed")
	}
	if _, ok := elm.admitOp(); ok {
		t.Error("elm.admitOp() admitted after close, want rejected")
	}
}

// TestElementInternal_AdaptersAfterSeal covers saved Element-wired callbacks
// invoked after callback admission was sealed: transaction notifications
// become no-ops and interceptors report the element as closed instead of
// reaching closed middleware.
func TestElementInternal_AdaptersAfterSeal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	elm := &Element{}

	clnHdlr := &countingClnTxHdlr{}
	wrappedCln := elmClnTxHandler{elm: elm, hdlr: clnHdlr}

	outReq := &countingOutReqIntcptr{}
	wrappedOutReq := elmOutReqInterceptor{elm: elm, intcptr: outReq}

	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}

	wrappedCln.HandleClientTransaction(ctx, nil)
	if got := clnHdlr.calls.Load(); got != 0 {
		t.Errorf("middleware handler calls after seal = %d, want 0", got)
	}

	if err := wrappedOutReq.InterceptOutboundRequest(ctx, nil, nil); !errors.Is(err, ErrElementClosed) {
		t.Errorf("interceptor after seal error = %v, want %v", err, ErrElementClosed)
	}
	if got := outReq.calls.Load(); got != 0 {
		t.Errorf("middleware interceptor calls after seal = %d, want 0", got)
	}
}

// TestElementInternal_LifecycleStates covers the managed-runtime lifecycle
// state enum: a fresh element is Ready, a successful Start moves it to
// Running, and Close ends it Closed.
func TestElementInternal_LifecycleStates(t *testing.T) {
	t.Parallel()

	var elm Element

	if got := elm.state.Load(); got != elmStateReady {
		t.Fatalf("fresh element state = %d, want %d (ready)", got, elmStateReady)
	}

	if err := elm.Start(t.Context()); err != nil {
		t.Fatalf("elm.Start() error = %v, want nil", err)
	}
	if got := elm.state.Load(); got != elmStateRunning {
		t.Fatalf("state after Start = %d, want %d (running)", got, elmStateRunning)
	}

	if err := elm.Close(); err != nil {
		t.Fatalf("elm.Close() error = %v, want nil", err)
	}
	if got := elm.state.Load(); got != elmStateClosed {
		t.Fatalf("state after Close = %d, want %d (closed)", got, elmStateClosed)
	}
}
