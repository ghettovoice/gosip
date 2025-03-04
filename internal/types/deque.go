package types

import "sync"

// Deque is a thread-safe double-ended queue backed by a slice.
// It preserves insertion order and allows pushing or popping
// elements from both ends.
type Deque[T any] struct {
	mu   sync.Mutex
	data []T
}

// Append adds the element to the end of the deque.
func (d *Deque[T]) Append(item T) {
	d.mu.Lock()
	d.data = append(d.data, item)
	d.mu.Unlock()
}

// Prepend adds the element to the front of the deque.
func (d *Deque[T]) Prepend(item T) {
	d.mu.Lock()
	d.data = append(d.data, item)
	copy(d.data[1:], d.data[:len(d.data)-1])
	d.data[0] = item
	d.mu.Unlock()
}

// PopFirst removes and returns the element from the front of the deque.
// The second return value is false when the deque is empty.
func (d *Deque[T]) PopFirst() (T, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.data) == 0 {
		var zero T
		return zero, false
	}

	item := d.data[0]
	d.data = d.data[1:]
	return item, true
}

// PopLast removes and returns the element from the end of the deque.
// The second return value is false when the deque is empty.
func (d *Deque[T]) PopLast() (T, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if len(d.data) == 0 {
		var zero T
		return zero, false
	}

	lastIdx := len(d.data) - 1
	item := d.data[lastIdx]
	var zero T
	d.data[lastIdx] = zero
	d.data = d.data[:lastIdx]
	return item, true
}

// Drain returns all buffered elements in FIFO order and clears the deque.
func (d *Deque[T]) Drain() []T {
	d.mu.Lock()
	if len(d.data) == 0 {
		d.mu.Unlock()
		return nil
	}

	out := make([]T, len(d.data))
	copy(out, d.data)
	clear(d.data)
	d.data = d.data[:0]
	d.mu.Unlock()
	return out
}

// Len returns the current number of elements in the deque.
func (d *Deque[T]) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.data)
}

// IsEmpty reports whether the deque has no elements.
func (d *Deque[T]) IsEmpty() bool {
	return d.Len() == 0
}
