package syncutil

import (
	"iter"
	"sync"
	"sync/atomic"
)

type ordListItem[T any] struct {
	id    uint64
	value T
}

type ordListState[T any] struct {
	items []ordListItem[T]
}

// OrderedList is a thread-safe ordered collection backed by immutable snapshots.
// Readers do not block writers, and each iteration observes one consistent snapshot.
type OrderedList[T any] struct {
	nextID atomic.Uint64
	state  atomic.Pointer[ordListState[T]]
}

// Add appends value and returns an idempotent function that removes this entry.
func (s *OrderedList[T]) Add(value T) (remove func()) {
	if s == nil {
		return func() {}
	}

	id := s.nextID.Add(1)
	s.update(func(items []ordListItem[T]) []ordListItem[T] {
		next := make([]ordListItem[T], len(items)+1)
		copy(next, items)
		next[len(items)] = ordListItem[T]{id: id, value: value}
		return next
	})

	return sync.OnceFunc(func() { s.remove(id) })
}

// Len returns the number of entries in the current snapshot.
func (s *OrderedList[T]) Len() int {
	if s == nil {
		return 0
	}

	state := s.state.Load()
	if state == nil {
		return 0
	}
	return len(state.items)
}

// At returns the value at index in the current snapshot.
// The second result is false when index is out of range.
func (s *OrderedList[T]) At(index int) (T, bool) {
	if s == nil {
		var zero T
		return zero, false
	}

	state := s.state.Load()
	if state == nil || index < 0 || index >= len(state.items) {
		var zero T
		return zero, false
	}
	return state.items[index].value, true
}

// All returns an iterator over a consistent snapshot in registration order.
func (s *OrderedList[T]) All() iter.Seq[T] {
	return func(yield func(T) bool) {
		if s == nil {
			return
		}

		state := s.state.Load()
		if state == nil {
			return
		}

		for _, item := range state.items {
			if !yield(item.value) {
				return
			}
		}
	}
}

func (s *OrderedList[T]) remove(id uint64) {
	for {
		state := s.state.Load()
		if state == nil {
			return
		}

		index := -1
		for i, item := range state.items {
			if item.id == id {
				index = i
				break
			}
		}
		if index < 0 {
			return
		}

		next := make([]ordListItem[T], len(state.items)-1)
		copy(next, state.items[:index])
		copy(next[index:], state.items[index+1:])
		if s.state.CompareAndSwap(state, &ordListState[T]{items: next}) {
			return
		}
	}
}

func (s *OrderedList[T]) update(fn func([]ordListItem[T]) []ordListItem[T]) {
	for {
		state := s.state.Load()
		var items []ordListItem[T]
		if state != nil {
			items = state.items
		}

		next := &ordListState[T]{items: fn(items)}
		if s.state.CompareAndSwap(state, next) {
			return
		}
	}
}
