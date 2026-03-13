package syncx

import "sync"

// Lazy is a generic, thread-safe lazy initializer. The init function runs
// at most once (until [Lazy.Reset] is called). Subsequent calls to [Lazy.Get]
// return the cached value and error.
//
// All methods are safe for concurrent use. In particular, [Lazy.Get] and
// [Lazy.Reset] may be called from different goroutines without external
// synchronization.
type Lazy[T any] struct {
	init func() (T, error)
	mu   sync.Mutex
	val  T
	err  error
	done bool
}

// NewLazy creates a [Lazy] that will call init on the first [Lazy.Get].
// Panics if init is nil.
func NewLazy[T any](init func() (T, error)) *Lazy[T] {
	if init == nil {
		panic("syncx: nil init function")
	}
	return &Lazy[T]{init: init}
}

// Get returns the cached value, running the init function on the first call.
// If the init function returns an error, it is wrapped as [CodeInitFailed].
// Thread-safe; concurrent callers block until init completes.
// Safe to call concurrently with [Lazy.Reset].
func (l *Lazy[T]) Get() (T, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.done {
		l.val, l.err = l.init()
		if l.err != nil {
			l.err = errInitFailed(l.err)
		}
		l.done = true
	}
	return l.val, l.err
}

// Reset allows the init function to run again on the next [Lazy.Get].
// Any cached value is discarded.
func (l *Lazy[T]) Reset() {
	l.mu.Lock()
	l.done = false
	var zero T
	l.val = zero
	l.err = nil
	l.mu.Unlock()
}
