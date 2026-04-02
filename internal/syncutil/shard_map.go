package syncutil

import (
	"fmt"
	"hash"
	"hash/fnv"
	"iter"
	"maps"
	"sync"

	"github.com/google/go-cmp/cmp"
)

// ShardMap is a thread-safe map that uses sharding to reduce lock contention.
type ShardMap[K comparable, V any] struct {
	shards    []*shard[K, V]
	shardsNum uint32
}

// shard is a single thread-safe map with its own mutex.
type shard[K comparable, V any] struct {
	sync.RWMutex
	items map[K]V
}

type ShardsNum uint

// defShardsNum is the default number of shards to use.
const defShardsNum ShardsNum = 32

// NewShardMap creates a new [ShardMap].
// If no number of shards is specified, the default number of shards (32) is used.
// The number of shards can be specified using the [ShardsNum] option and must be greater than 0.
func NewShardMap[K comparable, V any](opts ...any) *ShardMap[K, V] {
	var shardsNum ShardsNum
	for _, o := range opts {
		if v, ok := o.(ShardsNum); ok {
			shardsNum = v
		}
	}

	if shardsNum == 0 {
		shardsNum = defShardsNum
	}

	shards := make([]*shard[K, V], shardsNum)
	for i := range shards {
		shards[i] = &shard[K, V]{
			items: make(map[K]V),
		}
	}

	return &ShardMap[K, V]{
		shards:    shards,
		shardsNum: uint32(shardsNum),
	}
}

var shardHasherPool = sync.Pool{
	New: func() any { return fnv.New32a() },
}

func (m *ShardMap[K, V]) getShard(key K) *shard[K, V] {
	if m == nil {
		return nil
	}

	h := shardHasherPool.Get().(hash.Hash32) //nolint:forcetypeassert
	defer func() {
		h.Reset()
		shardHasherPool.Put(h)
	}()

	fmt.Fprint(h, key)
	sum := h.Sum32()

	return m.shards[sum%m.shardsNum]
}

// Store adds or updates a key-value pair.
func (m *ShardMap[K, V]) Store(key K, value V) *ShardMap[K, V] {
	if m == nil {
		return nil
	}

	shard := m.getShard(key)
	if shard == nil {
		return m
	}

	shard.Lock()
	defer shard.Unlock()

	shard.items[key] = value

	return m
}

// Load retrieves a value by key.
func (m *ShardMap[K, V]) Load(key K) (V, bool) {
	if m == nil {
		var zero V
		return zero, false
	}

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
	}

	return val, ok
}

// LoadOrStore retrieves a value by key, or stores it if not present.
func (m *ShardMap[K, V]) LoadOrStore(key K, value V) (actual V, loaded bool) {
	if m == nil {
		return value, false
	}

	shard := m.getShard(key)
	if shard == nil {
		return value, false
	}

	shard.Lock()
	defer shard.Unlock()

	if v, ok := shard.items[key]; ok {
		return v, true
	}

	shard.items[key] = value

	return value, false
}

// LoadAndDelete retrieves a value by key and deletes it.
func (m *ShardMap[K, V]) LoadAndDelete(key K) (actual V, loaded bool) {
	if m == nil {
		var zero V
		return zero, false
	}

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
	}

	return v, ok
}

// CompareAndDelete deletes a key-value pair if the current value equals old.
func (m *ShardMap[K, V]) CompareAndDelete(key K, old V) (deleted bool) {
	if m == nil {
		return false
	}

	shard := m.getShard(key)
	if shard == nil {
		return false
	}

	shard.Lock()
	defer shard.Unlock()

	v, ok := shard.items[key]
	if !ok || !cmp.Equal(v, old) {
		return false
	}

	delete(shard.items, key)

	return true
}

// Swap swaps the value for a key and returns the previous value.
func (m *ShardMap[K, V]) Swap(key K, newVal V) (oldVal V, loaded bool) {
	if m == nil {
		var zero V
		return zero, false
	}

	shard := m.getShard(key)
	if shard == nil {
		var zero V
		return zero, false
	}

	shard.Lock()
	defer shard.Unlock()

	prev, ok := shard.items[key]
	shard.items[key] = newVal

	return prev, ok
}

// CompareAndSwap swaps the value for a key if the current value equals old.
func (m *ShardMap[K, V]) CompareAndSwap(key K, oldVal, newVal V) (swapped bool) {
	if m == nil {
		return false
	}

	shard := m.getShard(key)
	if shard == nil {
		return false
	}

	shard.Lock()
	defer shard.Unlock()

	v, ok := shard.items[key]
	if !ok || !cmp.Equal(v, oldVal) {
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

	size := 0
	for _, shard := range m.shards {
		shard.RLock()
		size += len(shard.items)
		shard.RUnlock()
	}

	return size
}

// Clear removes all items from the map.
func (m *ShardMap[K, V]) Clear() *ShardMap[K, V] {
	if m == nil {
		return nil
	}

	for _, shard := range m.shards {
		shard.Lock()
		clear(shard.items)
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

	newShards := make([]*shard[K, V], m.shardsNum)
	for i, currentShard := range m.shards {
		currentShard.RLock()
		clonedItems := maps.Clone(currentShard.items)
		currentShard.RUnlock()

		newShards[i] = &shard[K, V]{
			items: clonedItems,
		}
	}

	return &ShardMap[K, V]{
		shards:    newShards,
		shardsNum: m.shardsNum,
	}
}
