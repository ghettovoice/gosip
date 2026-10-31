package syncutil

import (
	"iter"
	"maps"
	"sync"
	"sync/atomic"

	"github.com/ghettovoice/gosip/internal/errors"
)

// RWMap is a thread-safe map protected by a [sync.RWMutex].
// For high-concurrency scenarios, consider using [ShardMap] instead.
type RWMap[K comparable, V any] struct {
	mu   sync.RWMutex
	data map[K]V
	len  atomic.Uint64
}

func (m *RWMap[K, V]) init() {
	if m.data == nil {
		m.data = make(map[K]V)
	}
}

func (m *RWMap[K, V]) updLen() {
	m.len.Store(uint64(len(m.data)))
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

	m.init()

	m.data[key] = val
	m.updLen()
	return m
}

// StoreMany collects items before atomically storing them in the map.
// If iteration panics, StoreMany itself leaves the map unchanged.
func (m *RWMap[K, V]) StoreMany(items iter.Seq2[K, V]) *RWMap[K, V] {
	data := maps.Collect(items)

	m.mu.Lock()
	defer m.mu.Unlock()

	m.init()

	maps.Copy(m.data, data)
	m.updLen()
	return m
}

func (m *RWMap[K, V]) LoadOrStore(key K, val V) (actual V, found bool) {
	if v, ok := m.Load(key); ok {
		return v, ok
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if v, ok := m.data[key]; ok {
		return v, ok
	}

	m.init()

	m.data[key] = val
	m.updLen()
	return val, false
}

// LoadOrStoreFunc returns the existing value or stores the value returned by newVal.
// newVal is called only for a missing key, under the map's exclusive lock.
// It must not call methods that acquire the same map's lock.
func (m *RWMap[K, V]) LoadOrStoreFunc(key K, newVal func() (V, error)) (actual V, found bool, err error) {
	if v, ok := m.Load(key); ok {
		return v, ok, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if v, ok := m.data[key]; ok {
		return v, ok, nil
	}

	actual, err = newVal()
	if err != nil {
		return actual, false, errors.Wrap(err)
	}

	m.init()

	m.data[key] = actual
	m.updLen()
	return actual, false, nil
}

func (m *RWMap[K, V]) Delete(key K, keys ...K) *RWMap[K, V] {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.data == nil {
		return m
	}
	defer m.updLen()

	delete(m.data, key)
	for _, k := range keys {
		delete(m.data, k)
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
		m.updLen()
	}
	return v, ok
}

// CompareAndDelete deletes key if check accepts its current value.
// check is called once if key exists, under the map's exclusive lock.
// It must not call methods that acquire the same map's lock.
func (m *RWMap[K, V]) CompareAndDelete(key K, check func(actual V) bool) (deleted bool) {
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
	m.updLen()
	return true
}

func (m *RWMap[K, V]) Swap(key K, val V) (prev V, found bool) {
	if m == nil {
		var zero V
		return zero, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.init()

	prev, found = m.data[key]
	m.data[key] = val
	if !found {
		m.updLen()
	}
	return prev, found
}

// CompareAndSwap replaces key's value with newVal if check accepts its current value.
// check is called once if key exists, under the map's exclusive lock.
// It must not call methods that acquire the same map's lock.
func (m *RWMap[K, V]) CompareAndSwap(key K, newVal V, check func(actual V) bool) (swapped bool) {
	if m == nil {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	v, ok := m.data[key]
	if !ok || !check(v) {
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

func (m *RWMap[K, V]) HasAll(keys ...K) bool {
	if m == nil {
		return false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, key := range keys {
		if _, ok := m.data[key]; !ok {
			return false
		}
	}

	return true
}

func (m *RWMap[K, V]) HasAny(keys ...K) bool {
	if m == nil {
		return false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, key := range keys {
		if _, ok := m.data[key]; ok {
			return true
		}
	}

	return false
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
	m.updLen()
	return m
}

func (m *RWMap[K, V]) Len() int {
	if m == nil {
		return 0
	}
	return int(m.len.Load())
}

func (m *RWMap[K, V]) snapshot() map[K]V {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return maps.Clone(m.data)
}

func (m *RWMap[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		if m == nil {
			return
		}

		data := m.snapshot()

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

	m2 := &RWMap[K, V]{data: m.snapshot()}
	m2.updLen()
	return m2
}

// CopyTo copies all data from m to dst.
// The source snapshot is taken before locking dst; copying to itself is a no-op.
func (m *RWMap[K, V]) CopyTo(dst *RWMap[K, V]) *RWMap[K, V] {
	if m == nil || dst == nil {
		return m
	}

	dst.CopyFrom(m)
	return m
}

// CopyFrom replaces m's contents with a snapshot of src.
// The source snapshot is taken before locking m; copying from itself is a no-op.
func (m *RWMap[K, V]) CopyFrom(src *RWMap[K, V]) *RWMap[K, V] {
	if m == nil || src == nil {
		return nil
	}
	if m == src {
		return m
	}

	data := src.snapshot()

	m.mu.Lock()
	defer m.mu.Unlock()

	m.data = data
	m.updLen()
	return m
}
