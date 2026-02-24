package syncx

import (
	"sync"
	"sync/atomic"
)

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
	done uint32 // 0 = not initialized, 1 = initialized; atomic
}

// NewLazy creates a [Lazy] that will call init on the first [Lazy.Get].
func NewLazy[T any](init func() (T, error)) *Lazy[T] {
	return &Lazy[T]{init: init}
}

// Get returns the cached value, running the init function on the first call.
// If the init function returns an error, it is wrapped as [CodeInitFailed].
// Thread-safe; concurrent callers block until init completes.
// Safe to call concurrently with [Lazy.Reset].
func (l *Lazy[T]) Get() (T, error) {
	if atomic.LoadUint32(&l.done) == 1 {
		l.mu.Lock()
		v, e := l.val, l.err
		l.mu.Unlock()
		return v, e
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.done == 0 {
		l.val, l.err = l.init()
		if l.err != nil {
			l.err = errInitFailed(l.err)
		}
		atomic.StoreUint32(&l.done, 1)
	}
	return l.val, l.err
}

// Reset allows the init function to run again on the next [Lazy.Get].
// Any cached value is discarded.
func (l *Lazy[T]) Reset() {
	l.mu.Lock()
	atomic.StoreUint32(&l.done, 0)
	var zero T
	l.val = zero
	l.err = nil
	l.mu.Unlock()
}
