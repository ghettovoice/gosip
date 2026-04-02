package syncutil

import (
	"iter"
	"maps"
	"sync"

	"github.com/google/go-cmp/cmp"

	"github.com/ghettovoice/gosip/internal/errors"
)

// RWMap is a thread-safe map protected by a [sync.RWMutex].
// For high-concurrency scenarios, consider using [ShardMap] instead.
type RWMap[K comparable, V any] struct {
	mu   sync.RWMutex
	data map[K]V
}

func (m *RWMap[K, V]) Load(key K) (V, bool) {
	if m == nil {
		var zero V
		return zero, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	v, ok := m.data[key]

	return v, ok
}

func (m *RWMap[K, V]) Store(key K, val V) *RWMap[K, V] {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.data == nil {
		m.data = make(map[K]V)
	}

	m.data[key] = val

	return m
}

func (m *RWMap[K, V]) LoadOrStore(key K, val V) (actual V, found bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if v, ok := m.data[key]; ok {
		return v, true
	}

	if m.data == nil {
		m.data = make(map[K]V)
	}

	m.data[key] = val

	return val, false
}

func (m *RWMap[K, V]) LoadOrStoreFunc(key K, newVal func() (V, error)) (actual V, found bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if v, ok := m.data[key]; ok {
		return v, true, nil
	}

	if m.data == nil {
		m.data = make(map[K]V)
	}

	actual, err = newVal()
	if err != nil {
		return actual, false, errors.Wrap(err)
	}

	m.data[key] = actual

	return actual, false, nil
}

func (m *RWMap[K, V]) Delete(key K) *RWMap[K, V] {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.data != nil {
		delete(m.data, key)
	}

	return m
}

func (m *RWMap[K, V]) LoadAndDelete(key K) (actual V, found bool) {
	if m == nil {
		var zero V
		return zero, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.data[key]
	if ok {
		delete(m.data, key)
	}

	return v, ok
}

func (m *RWMap[K, V]) CompareAndDelete(key K, old V) (deleted bool) {
	if m == nil {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.data == nil {
		return false
	}

	v, ok := m.data[key]
	if !ok || !cmp.Equal(v, old) {
		return false
	}

	delete(m.data, key)

	return true
}

func (m *RWMap[K, V]) CompareAndDeleteFunc(key K, check func(actual V) bool) (deleted bool) {
	if m == nil {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.data == nil {
		return false
	}

	v, ok := m.data[key]
	if !ok || !check(v) {
		return false
	}

	delete(m.data, key)

	return true
}

func (m *RWMap[K, V]) Swap(key K, val V) (prev V, found bool) {
	if m == nil {
		var zero V
		return zero, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.data == nil {
		m.data = make(map[K]V)
	}

	prev, found = m.data[key]
	m.data[key] = val

	return prev, found
}

func (m *RWMap[K, V]) CompareAndSwap(key K, oldVal, newVal V) (swapped bool) {
	if m == nil {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.data == nil {
		return false
	}

	v, ok := m.data[key]
	if !ok || !cmp.Equal(v, oldVal) {
		return false
	}

	m.data[key] = newVal

	return true
}

func (m *RWMap[K, V]) Has(key K) bool {
	if m == nil {
		return false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.data[key]

	return ok
}

func (m *RWMap[K, V]) Clear() *RWMap[K, V] {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.data != nil {
		clear(m.data)
	}

	return m
}

func (m *RWMap[K, V]) Len() int {
	if m == nil {
		return 0
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.data == nil {
		return 0
	}

	return len(m.data)
}

func (m *RWMap[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		if m == nil {
			return
		}

		m.mu.RLock()

		var data map[K]V
		if m.data != nil {
			data = maps.Clone(m.data)
		}

		m.mu.RUnlock()

		for k, v := range data {
			if !yield(k, v) {
				return
			}
		}
	}
}

func (m *RWMap[K, V]) Clone() *RWMap[K, V] {
	if m == nil {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	return &RWMap[K, V]{
		data: maps.Clone(m.data),
	}
}

// CopyTo copies all data from m to dst.
func (m *RWMap[K, V]) CopyTo(dst *RWMap[K, V]) {
	if m == nil || dst == nil {
		return
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	dst.mu.Lock()
	defer dst.mu.Unlock()

	if m.data != nil {
		dst.data = maps.Clone(m.data)
	} else {
		dst.data = nil
	}
}
