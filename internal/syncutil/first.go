package syncutil

import "sync"

// FirstOf waits for the first of several events and cancels all registered
// sources so that only the first event is delivered through the result channel.
//
// The typical usage is:
//
//	first := NewFirstOf[error]()
//
//	errHandler := ErrorHandlerFunc(func(_ context.Context, err error) {
//	    first.Resolve(err)
//	})
//	first.AddCancel(tx.BindErrorHandler(errHandler))
//
//	stateHandler := TransactionStateHandlerFunc(func(_ context.Context, _, _ TransactionState) {
//	    first.Resolve(nil)
//	})
//	first.AddCancel(tx.BindStateHandler(stateHandler))
//
//	sendErr := <-first.Chan()
type FirstOf[T any] struct {
	mu       sync.Mutex
	resolved bool
	ch       chan T
	cancels  []func()
	cleaned  int
	once     sync.Once
}

// NewFirstOf creates a new FirstOf that delivers the first value resolved by any
// of its sources.
func NewFirstOf[T any]() *FirstOf[T] {
	return &FirstOf[T]{ch: make(chan T, 1)}
}

// Resolve records the first result and starts cancelling registered sources.
// It is safe to call from every source; only the first call has any effect.
func (f *FirstOf[T]) Resolve(value T) *FirstOf[T] {
	f.once.Do(func() {
		f.ch <- value

		close(f.ch)

		f.mu.Lock()
		f.resolved = true
		f.mu.Unlock()

		f.cleanup()
	})

	return f
}

// AddCancel registers a cancel function for one source. The caller should add
// one cancel for every source it binds. Registered cancels are invoked once the
// FirstOf has been resolved.
func (f *FirstOf[T]) AddCancel(cancel func()) *FirstOf[T] {
	f.mu.Lock()
	f.cancels = append(f.cancels, cancel)
	f.mu.Unlock()

	f.cleanup()

	return f
}

// Chan returns the channel on which the first resolved value is delivered.
// The channel is closed after the first value is sent.
func (f *FirstOf[T]) Chan() <-chan T {
	return f.ch
}

func (f *FirstOf[T]) cleanup() {
	f.mu.Lock()
	if !f.resolved {
		f.mu.Unlock()
		return
	}

	start := f.cleaned
	cancels := f.cancels
	f.cleaned = len(cancels)
	f.mu.Unlock()

	for _, cancel := range cancels[start:] {
		if cancel != nil {
			cancel()
		}
	}
}
