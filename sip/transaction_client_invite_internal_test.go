package sip

import (
	"testing"

	"github.com/ghettovoice/gosip/internal/errors"
)

func TestInviteClientTransaction_Start_RejectsTerminatedStateBeforeMarker(t *testing.T) {
	t.Parallel()

	tx, err := NewInviteClientTransaction(newInternalInviteReq(MagicCookie+".term-no-marker"), stubClnTransp{})
	if err != nil {
		t.Fatalf("NewInviteClientTransaction() error = %v, want nil", err)
	}

	tx.state.Store(TransactionStateTerminated)
	if tx.isTerminated() {
		t.Fatal("tx.isTerminated() = true, want false (terminal marker gap)")
	}

	if err := tx.Start(t.Context()); !errors.Is(err, ErrTransactionActionNotAllowed) {
		t.Fatalf("tx.Start() error = %v, want %v", err, ErrTransactionActionNotAllowed)
	}
}
