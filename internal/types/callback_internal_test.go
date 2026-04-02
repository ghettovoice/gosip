package types

import (
	"testing"
)

func TestCallbackManager_CompactsStorage(t *testing.T) {
	t.Parallel()

	var cm CallbackManager[int]

	const n = 32

	removers := make([]func(), 0, n)
	for i := range n {
		removers = append(removers, cm.Add(i))
	}

	for _, rm := range removers {
		rm()
	}

	if got, want := cm.Len(), 0; got != want {
		t.Fatalf("cm.Len() = %d, want %d", got, want)
	}

	if got, want := len(cm.items), 0; got != want {
		t.Fatalf("len(cm.items) = %d, want %d", got, want)
	}

	if got, want := len(cm.positions), 0; got != want {
		t.Fatalf("len(cm.positions) = %d, want %d", got, want)
	}
}
