package types_test

import (
	"reflect"
	"testing"

	"github.com/ghettovoice/gosip/internal/types"
)

func TestDeque_AppendPopFirst(t *testing.T) {
	t.Parallel()

	var d types.Deque[int]

	d.Append(1)
	d.Append(2)
	d.Append(3)

	if got := d.Len(); got != 3 {
		t.Fatalf("dq.Len() = %d, want 3", got)
	}

	for want := 1; want <= 3; want++ {
		item, ok := d.PopFirst()
		if !ok {
			t.Fatalf("dq.PopFirst() returned ok=false, want true for value %d", want)
		}
		if item != want {
			t.Fatalf("dq.PopFirst() = %d, want %d", item, want)
		}
	}

	if !d.IsEmpty() {
		t.Fatalf("dq.IsEmpty() = false, want true")
	}
}

func TestDeque_PrependPopLast(t *testing.T) {
	t.Parallel()

	var d types.Deque[int]

	d.Append(2)
	d.Append(3)
	d.Prepend(1)

	item, ok := d.PopLast()
	if !ok {
		t.Fatalf("dq.PopLast() returned ok=false, want true")
	}
	if item != 3 {
		t.Fatalf("dq.PopLast() = %d, want 3", item)
	}

	item, ok = d.PopFirst()
	if !ok || item != 1 {
		t.Fatalf("dq.PopFirst() = (%d, %v), want (1, true)", item, ok)
	}

	item, ok = d.PopFirst()
	if !ok || item != 2 {
		t.Fatalf("dq.PopFirst() = (%d, %v), want (2, true)", item, ok)
	}

	if _, ok = d.PopFirst(); ok {
		t.Fatalf("dq.PopFirst() on empty deque returned ok=true, want false")
	}
}

func TestDeque_Drain(t *testing.T) {
	t.Parallel()

	var d types.Deque[int]

	if out := d.Drain(); out != nil {
		t.Fatalf("dq.Drain() on empty deque = %v, want nil", out)
	}

	d.Append(10)
	d.Append(20)

	out := d.Drain()
	if !reflect.DeepEqual(out, []int{10, 20}) {
		t.Fatalf("dq.Drain() = %v, want [10 20]", out)
	}

	if !d.IsEmpty() {
		t.Fatalf("dq.IsEmpty() after Drain() = false, want true")
	}

	// mutate returned slice to ensure deque does not retain references
	out[0] = 99

	d.Append(30)
	item, ok := d.PopFirst()
	if !ok || item != 30 {
		t.Fatalf("dq.PopFirst() after dq.Drain() = (%d, %v), want (30, true)", item, ok)
	}
}
