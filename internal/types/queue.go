package types

import "sync"

// Queue is a thread-safe queue backed by a slice.
type Queue[T any] struct {
	mu    sync.Mutex
	items []T
}

// Push adds the element to the end of the queue.
func (q *Queue[T]) Push(item T) {
	q.mu.Lock()
	q.items = append(q.items, item)
	q.mu.Unlock()
}

// Pop removes and returns the element from the front of the queue.
// The second return value is false when the queue is empty.
func (q *Queue[T]) Pop() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var zero T
	if len(q.items) == 0 {
		return zero, false
	}

	item := q.items[0]
	q.items[0] = zero
	q.items = q.items[1:]

	return item, true
}

// Len returns the current number of elements in the queue.
func (q *Queue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// IsEmpty reports whether the queue has no elements.
func (q *Queue[T]) IsEmpty() bool {
	return q.Len() == 0
}

// Drain returns all buffered elements in FIFO order and clears the queue.
func (q *Queue[T]) Drain() []T {
	q.mu.Lock()
	if len(q.items) == 0 {
		q.mu.Unlock()
		return nil
	}

	out := make([]T, len(q.items))
	copy(out, q.items)
	clear(q.items)
	q.items = q.items[:0]
	q.mu.Unlock()

	return out
}
