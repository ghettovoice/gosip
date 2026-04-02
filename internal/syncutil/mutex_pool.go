package syncutil

import "sync"

// MutexPool is a thread-safe map of mutexes.
type MutexPool[K comparable] struct {
	pool sync.Map
}

// Lock acquires a mutex for the given key.
// Returns a function that releases the mutex.
func (mp *MutexPool[K]) Lock(key K) (unlock func()) {
	v, _ := mp.pool.LoadOrStore(key, &sync.Mutex{})
	m := v.(*sync.Mutex) //nolint:forcetypeassert
	m.Lock()
	return func() { m.Unlock() }
}

// TryLock acquires a mutex for the given key if it is not already locked.
// Returns true if the mutex was acquired, false otherwise.
func (mp *MutexPool[K]) TryLock(key K) (unlock func(), ok bool) {
	v, _ := mp.pool.LoadOrStore(key, &sync.Mutex{})

	m := v.(*sync.Mutex) //nolint:forcetypeassert
	if !m.TryLock() {
		return nil, false
	}

	return func() { m.Unlock() }, true
}

// Delete removes the mutex for the given key.
func (mp *MutexPool[K]) Delete(key K) {
	mp.pool.Delete(key)
}
