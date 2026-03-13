package poolx

import (
	"sync"
	"sync/atomic"
)

// --- ObjectPool ---

// ObjectPool is a generic, type-safe object pool backed by [sync.Pool].
type ObjectPool[T any] struct {
	pool    sync.Pool
	gets    atomic.Uint64
	puts    atomic.Uint64
	creates atomic.Uint64
}

// ObjectStats holds point-in-time counters for an [ObjectPool].
type ObjectStats struct {
	Gets    uint64 `json:"gets"`
	Puts    uint64 `json:"puts"`
	Creates uint64 `json:"creates"`
}

// NewObjectPool creates an [ObjectPool] that uses factory to create new
// instances when the pool is empty. Panics if factory is nil.
func NewObjectPool[T any](factory func() T) *ObjectPool[T] {
	if factory == nil {
		panic("poolx: nil factory function")
	}
	op := &ObjectPool[T]{}
	op.pool.New = func() any {
		op.creates.Add(1)
		return factory()
	}
	return op
}

// Get acquires an object from the pool (or creates a new one).
func (op *ObjectPool[T]) Get() T {
	op.gets.Add(1)
	return op.pool.Get().(T)
}

// Put returns an object to the pool for reuse.
func (op *ObjectPool[T]) Put(v T) {
	op.puts.Add(1)
	op.pool.Put(v)
}

// Stats returns point-in-time counters.
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
