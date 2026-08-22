package syncutil_test

import (
	"iter"
	"slices"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/syncutil"
)

func collectOrderedList[T any](seq iter.Seq[T]) []T {
	return slices.Collect(seq)
}

func TestOrderedList_OrderAndRemove(t *testing.T) {
	t.Parallel()

	var values syncutil.OrderedList[int]
	removeFirst := values.Add(1)
	removeSecond := values.Add(2)
	removeThird := values.Add(3)

	if diff := cmp.Diff([]int{1, 2, 3}, collectOrderedList(values.All())); diff != "" {
		t.Errorf("OrderedList.All() mismatch (-want +got):\n%s", diff)
	}
	if got, want := values.Len(), 3; got != want {
		t.Errorf("OrderedList.Len() = %d, want %d", got, want)
	}

	removeSecond()
	removeSecond()

	if diff := cmp.Diff([]int{1, 3}, collectOrderedList(values.All())); diff != "" {
		t.Errorf("OrderedList.All() after remove mismatch (-want +got):\n%s", diff)
	}

	removeFirst()
	removeThird()
	if got, want := values.Len(), 0; got != want {
		t.Errorf("OrderedList.Len() after remove all = %d, want %d", got, want)
	}
}

func TestOrderedList_At(t *testing.T) {
	t.Parallel()

	var values syncutil.OrderedList[int]
	values.Add(10)
	removeSecond := values.Add(20)
	values.Add(30)
	removeSecond()

	tests := []struct {
		name   string
		index  int
		want   int
		wantOK bool
	}{
		{name: "negative index", index: -1},
		{name: "first value", index: 0, want: 10, wantOK: true},
		{name: "second value", index: 1, want: 30, wantOK: true},
		{name: "past end", index: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := values.At(tt.index)
			if ok != tt.wantOK {
				t.Errorf("OrderedList.At(%d) ok = %t, want %t", tt.index, ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("OrderedList.At(%d) = %d, want %d", tt.index, got, tt.want)
			}
		})
	}
}

func TestOrderedList_All_UsesSnapshot(t *testing.T) {
	t.Parallel()

	var values syncutil.OrderedList[int]
	values.Add(1)
	removeSecond := values.Add(2)

	var got []int
	for value := range values.All() {
		got = append(got, value)
		if value == 1 {
			values.Add(3)
			removeSecond()
		}
	}

	if diff := cmp.Diff([]int{1, 2}, got); diff != "" {
		t.Errorf("OrderedList.All() mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]int{1, 3}, collectOrderedList(values.All())); diff != "" {
		t.Errorf("OrderedList.All() after concurrent update mismatch (-want +got):\n%s", diff)
	}
}

func TestOrderedList_ConcurrentAddAndRemove(t *testing.T) {
	t.Parallel()

	const n = 128
	var (
		values  syncutil.OrderedList[int]
		removes = make([]func(), n)
		wg      sync.WaitGroup
	)

	wg.Add(n)
	for i := range n {
		go func(index int) {
			defer wg.Done()
			removes[index] = values.Add(index)
		}(i)
	}
	wg.Wait()

	if got, want := values.Len(), n; got != want {
		t.Fatalf("OrderedList.Len() after concurrent add = %d, want %d", got, want)
	}

	got := collectOrderedList(values.All())
	slices.Sort(got)
	want := make([]int, n)
	for i := range want {
		want[i] = i
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("OrderedList.All() after concurrent add mismatch (-want +got):\n%s", diff)
	}

	wg.Add(n)
	for _, remove := range removes {
		go func(remove func()) {
			defer wg.Done()
			remove()
		}(remove)
	}
	wg.Wait()

	if got, want := values.Len(), 0; got != want {
		t.Errorf("OrderedList.Len() after concurrent remove = %d, want %d", got, want)
	}
}
