package syncutil

import (
	"fmt"
	"hash"
	"hash/fnv"
	"iter"
	"maps"
	"runtime"
	"sync"
	"sync/atomic"
)

// ShardMap is a thread-safe map that uses sharding to reduce lock contention.
type ShardMap[K comparable, V any] struct {
	shards []*shard[K, V]
	len    atomic.Uint64
	once   sync.Once
}

// shard is a single thread-safe map with its own mutex.
type shard[K comparable, V any] struct {
	sync.RWMutex
	items map[K]V
}

func defShardsNum() uint {
	return uint(min(max(runtime.GOMAXPROCS(0), 8), 32))
}

// NewShardMap creates a new [ShardMap].
// If no number of shards is specified, the default number of shards is used,
// which is bounded between 8 and 32 based on the current GOMAXPROCS value.
func NewShardMap[K comparable, V any](shardsNum uint) *ShardMap[K, V] {
	if shardsNum == 0 {
		shardsNum = defShardsNum()
	}

	m := &ShardMap[K, V]{}
	m.setup(shardsNum)
	return m
}

// setup initializes the shard slice. It is safe to call multiple times, but
// only the first call actually allocates the shards.
func (m *ShardMap[K, V]) setup(shardsNum uint) {
	if len(m.shards) != 0 {
		return
	}

	m.shards = make([]*shard[K, V], shardsNum)
	for i := range m.shards {
		m.shards[i] = &shard[K, V]{
			items: make(map[K]V),
		}
	}
}

// init lazily initializes a zero-value map with the default number of shards.
func (m *ShardMap[K, V]) init() {
	m.setup(defShardsNum())
}

func (m *ShardMap[K, V]) incLen() { m.len.Add(1) }

func (m *ShardMap[K, V]) decLen() {
	for {
		cur := m.len.Load()
		if cur == 0 {
			return
		}
		if m.len.CompareAndSwap(cur, cur-1) {
			return
		}
	}
}

var shardHasherPool = sync.Pool{
	New: func() any { return fnv.New32a() },
}

func (m *ShardMap[K, V]) getShard(key K) *shard[K, V] {
	if m == nil || len(m.shards) == 0 {
		return nil
	}

	h := shardHasherPool.Get().(hash.Hash32) //nolint:forcetypeassert
	defer func() {
		h.Reset()
		shardHasherPool.Put(h)
	}()

	fmt.Fprint(h, key)
	return m.shards[h.Sum32()%uint32(len(m.shards))]
}

// Store adds or updates a key-value pair.
func (m *ShardMap[K, V]) Store(key K, value V) *ShardMap[K, V] {
	if m == nil {
		return nil
	}

	m.once.Do(m.init)

	shard := m.getShard(key)
	shard.Lock()
	defer shard.Unlock()

	if _, ok := shard.items[key]; !ok {
		m.incLen()
	}
	shard.items[key] = value
	return m
}

// Load retrieves a value by key.
func (m *ShardMap[K, V]) Load(key K) (V, bool) {
	if m == nil {
		var zero V
		return zero, false
	}

	m.once.Do(m.init)

	shard := m.getShard(key)
	if shard == nil {
		var zero V
		return zero, false
	}

	shard.RLock()
	defer shard.RUnlock()

	val, ok := shard.items[key]
	return val, ok
}

// Delete removes a key-value pair by key.
func (m *ShardMap[K, V]) Delete(key K) (V, bool) {
	if m == nil {
		var zero V
		return zero, false
	}

	m.once.Do(m.init)

	shard := m.getShard(key)
	if shard == nil {
		var zero V
		return zero, false
	}

	shard.Lock()
	defer shard.Unlock()

	val, ok := shard.items[key]
	if ok {
		delete(shard.items, key)
		m.decLen()
	}
	return val, ok
}

// LoadOrStore retrieves a value by key, or stores it if not present.
func (m *ShardMap[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	if m == nil {
		return value, false
	}

	m.once.Do(m.init)

	shard := m.getShard(key)
	shard.Lock()
	defer shard.Unlock()

	if v, ok := shard.items[key]; ok {
		return v, true
	}

	shard.items[key] = value
	m.incLen()
	return value, false
}

// LoadAndDelete retrieves a value by key and deletes it.
func (m *ShardMap[K, V]) LoadAndDelete(key K) (actual V, loaded bool) {
	if m == nil {
		var zero V
		return zero, false
	}

	m.once.Do(m.init)

	shard := m.getShard(key)
	if shard == nil {
		var zero V
		return zero, false
	}

	shard.Lock()
	defer shard.Unlock()

	v, ok := shard.items[key]
	if ok {
		delete(shard.items, key)
		m.decLen()
	}
	return v, ok
}

// CompareAndDelete deletes a key-value pair if check accepts its current value.
// check is called once if key exists, under the shard's exclusive lock.
// It must not call methods that acquire the same map's locks.
func (m *ShardMap[K, V]) CompareAndDelete(key K, check func(actual V) bool) (deleted bool) {
	if m == nil {
		return false
	}

	m.once.Do(m.init)

	shard := m.getShard(key)
	if shard == nil {
		return false
	}

	shard.Lock()
	defer shard.Unlock()

	v, ok := shard.items[key]
	if !ok || !check(v) {
		return false
	}

	delete(shard.items, key)
	m.decLen()
	return true
}

// Swap swaps the value for a key and returns the previous value.
func (m *ShardMap[K, V]) Swap(key K, newVal V) (oldVal V, loaded bool) {
	if m == nil {
		var zero V
		return zero, false
	}

	m.once.Do(m.init)

	shard := m.getShard(key)
	shard.Lock()
	defer shard.Unlock()

	prev, ok := shard.items[key]
	shard.items[key] = newVal
	if !ok {
		m.incLen()
	}
	return prev, ok
}

// CompareAndSwap swaps the value for a key if check accepts its current value.
// check is called once if key exists, under the shard's exclusive lock.
// It must not call methods that acquire the same map's locks.
func (m *ShardMap[K, V]) CompareAndSwap(key K, newVal V, check func(actual V) bool) (swapped bool) {
	if m == nil {
		return false
	}

	m.once.Do(m.init)

	shard := m.getShard(key)
	if shard == nil {
		return false
	}

	shard.Lock()
	defer shard.Unlock()

	v, ok := shard.items[key]
	if !ok || !check(v) {
		return false
	}

	shard.items[key] = newVal
	return true
}

// Has checks if a key exists.
func (m *ShardMap[K, V]) Has(key K) bool {
	if m == nil {
		return false
	}

	m.once.Do(m.init)

	shard := m.getShard(key)
	if shard == nil {
		return false
	}

	shard.RLock()
	defer shard.RUnlock()

	_, ok := shard.items[key]
	return ok
}

// Len returns the total number of items in the map.
func (m *ShardMap[K, V]) Len() int {
	if m == nil {
		return 0
	}
	return int(m.len.Load())
}

// Clear removes all items from the map.
// It acquires all shard locks simultaneously, so it provides snapshot
// semantics: any Store/Delete/LoadOrStore that starts after Clear returns
// will observe an empty map, and no concurrent Store can leak into the map.
func (m *ShardMap[K, V]) Clear() *ShardMap[K, V] {
	if m == nil {
		return nil
	}

	m.once.Do(m.init)

	// Lock all shards in ascending order. No other method in this file
	// acquires more than one shard lock at a time, so this cannot deadlock.
	for _, shard := range m.shards {
		shard.Lock()
	}

	for _, shard := range m.shards {
		clear(shard.items)
	}
	m.len.Store(0)

	for _, shard := range m.shards {
		shard.Unlock()
	}

	return m
}

// All returns an iterator over all items in the map.
func (m *ShardMap[K, V]) All() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
		if m == nil {
			return
		}

		m.once.Do(m.init)

		for _, shard := range m.shards {
			shard.RLock()
			items := maps.Clone(shard.items)
			shard.RUnlock()

			for k, v := range items {
				if !yield(k, v) {
					return
				}
			}
		}
	}
}

// Clone creates a deep copy of the ShardMap.
func (m *ShardMap[K, V]) Clone() *ShardMap[K, V] {
	if m == nil {
		return nil
	}

	m.once.Do(m.init)

	newShards := make([]*shard[K, V], len(m.shards))
	total := 0
	for i, currentShard := range m.shards {
		currentShard.RLock()
		clonedItems := maps.Clone(currentShard.items)
		currentShard.RUnlock()

		total += len(clonedItems)
		newShards[i] = &shard[K, V]{
			items: clonedItems,
		}
	}

	clone := &ShardMap[K, V]{
		shards: newShards,
	}
	clone.len.Store(uint64(total))
	return clone
}
