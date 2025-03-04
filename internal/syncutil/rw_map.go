package syncutil

import (
	"iter"
	"maps"
	"sync"
)

// RWMap is a thread-safe map protected by a [sync.RWMutex].
// For high-concurrency scenarios, consider using [ShardMap] instead.
type RWMap[K comparable, V any] struct {
	mu   sync.RWMutex
	data map[K]V
}

func (m *RWMap[K, V]) Get(key K) (V, bool) {
	if m == nil {
		var zero V
		return zero, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	v, ok := m.data[key]
	return v, ok
}

func (m *RWMap[K, V]) Set(key K, val V) *RWMap[K, V] {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = make(map[K]V)
	}
	m.data[key] = val
	return m
}

func (m *RWMap[K, V]) GetOrSet(key K, val V) (V, bool) {
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

func (m *RWMap[K, V]) Del(key K) *RWMap[K, V] {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return m
}

func (m *RWMap[K, V]) GetAndDel(key K) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if ok {
		delete(m.data, key)
	}
	return v, ok
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
	m.mu.Lock()
	defer m.mu.Unlock()
	clear(m.data)
	return m
}

func (m *RWMap[K, V]) Len() int {
	if m == nil {
		return 0
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.data)
}

func (m *RWMap[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		if m == nil {
			return
		}

		m.mu.RLock()
		data := maps.Clone(m.data)
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
	dst.data = maps.Clone(m.data)
}
