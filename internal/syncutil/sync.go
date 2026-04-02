// Package syncutil provides thread-safe data structures and synchronization utilities.
// It includes [RWMap] for simple concurrent map access, [ShardMap] for high-concurrency
// scenarios with reduced lock contention, and [MutexPool] for managing per-key mutexes.
package syncutil
