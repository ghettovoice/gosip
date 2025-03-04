package types

import (
	"container/list"
	"sync"
)

type CallbackManager[T any] struct {
	mu     sync.RWMutex
	cbs    map[int]*list.Element
	order  *list.List
	nextID int
}

type callback[T any] struct {
	id int
	cb T
}

func (m *CallbackManager[T]) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.cbs)
}

func (m *CallbackManager[T]) Add(cb T) (remove func()) {
	m.mu.Lock()
	id := m.nextID
	m.nextID++

	if m.cbs == nil {
		m.cbs = make(map[int]*list.Element)
	}
	if m.order == nil {
		m.order = list.New()
	}
	el := m.order.PushBack(&callback[T]{id, cb})
	m.cbs[id] = el
	m.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			if el, ok := m.cbs[id]; ok {
				m.order.Remove(el)
				delete(m.cbs, id)
			}
			m.mu.Unlock()
		})
	}
}

func (m *CallbackManager[T]) Range(fn func(cb T)) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for el := m.order.Front(); el != nil; el = el.Next() {
		entry := el.Value.(*callback[T]) //nolint:forcetypeassert
		fn(entry.cb)
	}
}
