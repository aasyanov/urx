package syncx

import (
	"sync"

	"github.com/aasyanov/urx/panix"
)

// opLazy labels panics recovered while running a lazy init function.
const opLazy = "syncx.Lazy"

// Lazy is a generic, thread-safe lazy initializer. The init function runs at
// most once per successful initialization (until [Lazy.Reset] is called);
// subsequent [Lazy.Get] calls return the cached value. It is the typed,
// error-aware analogue of [sync.Once].
//
// Init runs under [github.com/aasyanov/urx/panix] recovery: a panicking init
// is converted into a [*panix.PanicError] instead of crashing the caller.
//
// All methods are safe for concurrent use. In particular, [Lazy.Get] and
// [Lazy.Reset] may be called from different goroutines without external
// synchronization.
//
// Create with [NewLazy].
type Lazy[T any] struct {
	mu   sync.Mutex
	init func() (T, error)
	val  T
	done bool
}

// NewLazy creates a [Lazy] that calls init on the first [Lazy.Get].
//
// Returns [ErrNilInit] if init is nil.
func NewLazy[T any](init func() (T, error)) (*Lazy[T], error) {
	if init == nil {
		return nil, ErrNilInit
	}
	return &Lazy[T]{init: init}, nil
}

// Get returns the cached value, running the init function on the first call.
// If init returns an error, it is wrapped as [ErrInitFailed]; neither the value
// nor the error is cached, and init runs again on the next Get. If init
// panics, the recovered [*panix.PanicError] is returned and init is not
// latched.
//
// Concurrent callers block until init completes. Safe to call concurrently
// with [Lazy.Reset].
func (l *Lazy[T]) Get() (T, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.done {
		return l.val, nil
	}

	val, err := panix.Safe(opLazy, func() (T, error) {
		return l.init()
	})
	if err != nil {
		var zero T
		if isPanic(err) {
			return zero, err
		}
		// Do not latch failures: a transient init error should be retryable
		// on the next Get rather than cached forever.
		return zero, errInitFailed(err)
	}

	l.val = val
	l.done = true
	return l.val, nil
}

// Done reports whether the value has been successfully initialized and not
// since reset.
func (l *Lazy[T]) Done() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.done
}

// Reset discards any cached value so the init function runs again on the next
// [Lazy.Get]. It is idempotent.
func (l *Lazy[T]) Reset() {
	l.mu.Lock()
	var zero T
	l.val = zero
	l.done = false
	l.mu.Unlock()
}
