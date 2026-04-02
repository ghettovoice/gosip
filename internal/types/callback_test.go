package types_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/types"
)

func collectCallbacks[T any](seq func(func(T) bool)) []T {
	var out []T
	seq(func(v T) bool {
		out = append(out, v)
		return true
	})

	return out
}

func TestCallbackManager_OrderAndRemove(t *testing.T) {
	t.Parallel()

	var cm types.CallbackManager[int]

	rm1 := cm.Add(1)
	rm2 := cm.Add(2)
	rm3 := cm.Add(3)

	if got, want := collectCallbacks(cm.All()), []int{1, 2, 3}; !cmp.Equal(got, want) {
		t.Errorf("cm.All() = %v, want %v", got, want)
	}

	if got, want := cm.Len(), 3; got != want {
		t.Errorf("cm.Len() = %d, want %d", got, want)
	}

	rm2()

	if got, want := collectCallbacks(cm.All()), []int{1, 3}; !cmp.Equal(got, want) {
		t.Errorf("cm.All() after remove middle = %v, want %v", got, want)
	}

	if got, want := cm.Len(), 2; got != want {
		t.Errorf("cm.Len() after remove middle = %d, want %d", got, want)
	}

	rm2()

	if got, want := cm.Len(), 2; got != want {
		t.Errorf("cm.Len() after second remove call = %d, want %d", got, want)
	}

	rm1()
	rm3()

	if got := collectCallbacks(cm.All()); len(got) != 0 {
		t.Errorf("len(cm.All()) after remove all = %d, want 0", len(got))
	}

	if got, want := cm.Len(), 0; got != want {
		t.Errorf("cm.Len() after remove all = %d, want %d", got, want)
	}
}
