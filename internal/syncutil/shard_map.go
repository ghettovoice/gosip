package syncutil

import (
	"fmt"
	"hash/fnv"
	"iter"
	"maps"
	"sync"
)

// ShardMap is a thread-safe map that uses sharding to reduce lock contention.
type ShardMap[K comparable, V any] struct {
	shards     []*shard[K, V]
	shardCount uint32
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
		shards:     shards,
		shardCount: uint32(shardsNum),
	}
}

func (m *ShardMap[K, V]) getShard(key K) *shard[K, V] {
	hash := fnv.New32a()
	fmt.Fprint(hash, key)
	hashSum := hash.Sum32()
	return m.shards[hashSum%m.shardCount]
}

// Set adds or updates a key-value pair.
func (m *ShardMap[K, V]) Set(key K, value V) {
	shard := m.getShard(key)
	shard.Lock()
	shard.items[key] = value
	shard.Unlock()
}

// Get retrieves a value by key.
func (m *ShardMap[K, V]) Get(key K) (V, bool) {
	shard := m.getShard(key)
	shard.RLock()
	defer shard.RUnlock()
	val, ok := shard.items[key]
	return val, ok
}

// Delete removes a key-value pair by key.
func (m *ShardMap[K, V]) Del(key K) (V, bool) {
	shard := m.getShard(key)
	shard.Lock()
	val, ok := shard.items[key]
	if ok {
		delete(shard.items, key)
	}
	shard.Unlock()
	return val, ok
}

// Has checks if a key exists.
func (m *ShardMap[K, V]) Has(key K) bool {
	shard := m.getShard(key)
	shard.RLock()
	_, ok := shard.items[key]
	shard.RUnlock()
	return ok
}

// Size returns the total number of items in the map.
func (m *ShardMap[K, V]) Size() int {
	size := 0
	for _, shard := range m.shards {
		shard.RLock()
		size += len(shard.items)
		shard.RUnlock()
	}
	return size
}

// Clear removes all items from the map.
func (m *ShardMap[K, V]) Clear() {
	for _, shard := range m.shards {
		shard.Lock()
		clear(shard.items)
		shard.Unlock()
	}
}

// Items returns an iterator over all items in the map.
func (m *ShardMap[K, V]) Items() iter.Seq2[K, V] {
	return func(yield func(K, V) bool) {
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
