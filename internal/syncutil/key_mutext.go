package syncutil

import "sync"

// KeyMutex is a thread-safe mutex that uses a map to store mutexes for each key.
type KeyMutex[K comparable] struct {
	muxs sync.Map
}

// Lock acquires a mutex for the given key.
// Returns a function that releases the mutex.
func (km *KeyMutex[K]) Lock(key K) (unlock func()) {
	v, _ := km.muxs.LoadOrStore(key, &sync.Mutex{})
	m := v.(*sync.Mutex) //nolint:forcetypeassert
	m.Lock()
	return func() { m.Unlock() }
}

// TryLock acquires a mutex for the given key if it is not already locked.
// Returns true if the mutex was acquired, false otherwise.
func (km *KeyMutex[K]) TryLock(key K) (unlock func(), ok bool) {
	v, _ := km.muxs.LoadOrStore(key, &sync.Mutex{})
	m := v.(*sync.Mutex) //nolint:forcetypeassert
	if !m.TryLock() {
		return nil, false
	}
	return func() { m.Unlock() }, true
}

// Del removes the mutex for the given key.
func (km *KeyMutex[K]) Del(key K) {
	km.muxs.Delete(key)
}
