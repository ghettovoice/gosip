package types

import (
	"iter"
	"slices"
	"sync"
)

type callbackItem[T any] struct {
	id int
	cb T
}

type CallbackManager[T any] struct {
	mu        sync.RWMutex
	items     []callbackItem[T]
	positions map[int]int
	nextID    int
}

func (cm *CallbackManager[T]) Len() int {
	if cm == nil {
		return 0
	}

	cm.mu.RLock()
	defer cm.mu.RUnlock()

	return len(cm.items)
}

func (cm *CallbackManager[T]) Add(cb T) (remove func()) {
	cm.mu.Lock()
	if cm.items == nil {
		cm.items = make([]callbackItem[T], 0, 1)
		cm.positions = make(map[int]int)
	}

	id := cm.nextID
	cm.nextID++
	cm.positions[id] = len(cm.items)
	cm.items = append(cm.items, callbackItem[T]{
		id: id,
		cb: cb,
	})
	cm.mu.Unlock()

	var once sync.Once

	return func() {
		once.Do(func() {
			cm.mu.Lock()
			defer cm.mu.Unlock()

			idx, ok := cm.positions[id]
			if !ok {
				return
			}

			delete(cm.positions, id)

			cm.items = slices.Delete(cm.items, idx, idx+1)
			for i := idx; i < len(cm.items); i++ {
				cm.positions[cm.items[i].id] = i
			}
		})
	}
}

func (cm *CallbackManager[T]) All() iter.Seq[T] {
	return func(yield func(T) bool) {
		if cm == nil {
			return
		}

		cm.mu.RLock()

		if len(cm.items) == 0 {
			cm.mu.RUnlock()
			return
		}

		// Make a copy to avoid holding lock during callback execution.
		callbacks := make([]T, len(cm.items))
		for i, item := range cm.items {
			callbacks[i] = item.cb
		}

		cm.mu.RUnlock()

		for _, cb := range callbacks {
			if !yield(cb) {
				return
			}
		}
	}
}
