package poolx

import (
	"sync"
	"sync/atomic"
)

// ObjectOption configures an [ObjectPool] created with [NewObjectPool].
type ObjectOption[T any] func(*objectConfig[T])

type objectConfig[T any] struct {
	reset func(T)
}

// WithReset registers a hook invoked on every [ObjectPool.Put] before the
// object is returned to the pool. Use it to clear an object's state (e.g.
// `buf.Reset()`) so the next [ObjectPool.Get] sees a clean instance.
// Default: nil (objects are pooled as-is).
func WithReset[T any](fn func(T)) ObjectOption[T] {
	return func(c *objectConfig[T]) { c.reset = fn }
}

// ObjectPool is a generic, type-safe object pool backed by [sync.Pool]. It
// amortizes allocation of reusable objects (buffers, encoders, scratch
// slices) across goroutines. It is safe for concurrent use.
//
// Create with [NewObjectPool]. Objects are not guaranteed to be retained:
// [sync.Pool] may drop pooled objects at any GC cycle.
type ObjectPool[T any] struct {
	pool  sync.Pool
	reset func(T)

	gets    atomic.Uint64
	puts    atomic.Uint64
	creates atomic.Uint64
}

// NewObjectPool creates an [ObjectPool] that calls factory to construct a new
// instance whenever the pool is empty. Panics if factory is nil.
func NewObjectPool[T any](factory func() T, opts ...ObjectOption[T]) *ObjectPool[T] {
	if factory == nil {
		panic("poolx: NewObjectPool factory function is nil")
	}
	var cfg objectConfig[T]
	for _, opt := range opts {
		opt(&cfg)
	}

	op := &ObjectPool[T]{reset: cfg.reset}
	op.pool.New = func() any {
		op.creates.Add(1)
		return factory()
	}
	return op
}

// Get acquires an object from the pool, constructing a new one via the factory
// when the pool is empty.
func (op *ObjectPool[T]) Get() T {
	op.gets.Add(1)
	return op.pool.Get().(T)
}

// Put returns an object to the pool for reuse. If a reset hook was configured
// via [WithReset], it runs before the object is pooled.
func (op *ObjectPool[T]) Put(v T) {
	op.puts.Add(1)
	if op.reset != nil {
		op.reset(v)
	}
	op.pool.Put(v)
}

// Stats returns a point-in-time snapshot of the pool counters.
func (op *ObjectPool[T]) Stats() ObjectStats {
	return ObjectStats{
		Gets:    op.gets.Load(),
		Puts:    op.puts.Load(),
		Creates: op.creates.Load(),
	}
}

// ResetStats zeroes the gets, puts, and creates counters.
func (op *ObjectPool[T]) ResetStats() {
	op.gets.Store(0)
	op.puts.Store(0)
	op.creates.Store(0)
}
