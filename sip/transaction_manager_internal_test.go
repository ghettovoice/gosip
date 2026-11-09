package sip

import (
	"context"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ghettovoice/gosip/internal/errors"
)

func TestTransactionManager_RegisterClientTransaction_RejectsStartedBeforeReady(t *testing.T) {
	t.Parallel()

	tx, err := NewInviteClientTransaction(newInternalInviteReq(MagicCookie+".started-no-ready"), stubClnTransp{})
	if err != nil {
		t.Fatalf("NewInviteClientTransaction() error = %v, want nil", err)
	}

	if !tx.admitStart(t.Context()) {
		t.Fatal("tx.admitStart() = false, want true")
	}
	if tx.isActive() {
		t.Fatal("tx.isActive() = true, want false (ready must stay closed)")
	}

	txm := &TransactionManager{}
	t.Cleanup(func() { _ = txm.Close() })

	if err := txm.RegisterClientTransaction(t.Context(), tx); !errors.Is(err, ErrTransactionActionNotAllowed) {
		t.Fatalf("txm.RegisterClientTransaction(ctx) error = %v, want %v", err, ErrTransactionActionNotAllowed)
	}
}

func newStaleFixtureTx(t *testing.T) *InviteClientTransaction {
	t.Helper()

	tx, err := NewInviteClientTransaction(newInternalInviteReq(MagicCookie+".wd-rec"), stubClnTransp{})
	if err != nil {
		t.Fatalf("NewInviteClientTransaction() error = %v, want nil", err)
	}
	tx.lcMu.Lock()
	tx.state.Store(TransactionStateProceeding)
	tx.started = true
	tx.lcMu.Unlock()
	return tx
}

func TestTransaction_Registration_StaleWatcherRearmIdentity(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		tx := newStaleFixtureTx(t)

		var calls atomic.Int32
		onTimeout := func(context.Context, Transaction) { calls.Add(1) }
		rec := &txRegistration[Transaction]{tx: tx, admitted: true}

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

		rec.expireStaleWatcher(ctx, first, onTimeout)
		if got := calls.Load(); got != 0 {
			t.Fatalf("onTimeout calls = %d after stale expireStaleWatcher, want 0", got)
		}
		rec.mu.Lock()
		unchanged := rec.staleWatcher == second
		rec.mu.Unlock()
		if !unchanged {
			t.Fatal("rec.staleWatcher cleared by stale expireStaleWatcher, want unchanged")
		}

		time.Sleep(2 * time.Second)
		if got := calls.Load(); got != 1 {
			t.Fatalf("onTimeout calls = %d after new deadline, want 1", got)
		}
	})
}

func TestTransaction_Registration_ExpireStaleWatcherAfterClose(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx := t.Context()
		tx := newStaleFixtureTx(t)

		var calls atomic.Int32
		onTimeout := func(context.Context, Transaction) { calls.Add(1) }
		rec := &txRegistration[Transaction]{tx: tx, admitted: true}

		rec.watchStale(ctx, time.Second, onTimeout)
		rec.mu.Lock()
		watcher := rec.staleWatcher
		rec.mu.Unlock()
		if watcher == nil {
			t.Fatal("rec.staleWatcher = nil after watchStale, want armed watchdog")
		}

		rec.close()
		rec.expireStaleWatcher(ctx, watcher, onTimeout)
		if got := calls.Load(); got != 0 {
			t.Fatalf("onTimeout calls = %d after closed record, want 0", got)
		}

		time.Sleep(2 * time.Second)
		if got := calls.Load(); got != 0 {
			t.Fatalf("onTimeout calls = %d after closed record deadline, want 0", got)
		}
	})
}
