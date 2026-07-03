// Package syncx provides generic concurrency primitives for industrial Go
// services: a typed lazy initializer, a panic-safe error group, and a
// type-safe concurrent map.
//
// [Lazy] is a generic, thread-safe lazy initializer — like [sync.Once] but
// with a typed return value and error handling. [Group] is an error group
// with [github.com/aasyanov/urx/panix] panic recovery and optional
// concurrency limiting. [Map] is a generic, type-safe wrapper around
// [sync.Map] with an O(1) length.
//
// # Quick Start
//
//	lazy, err := syncx.NewLazy(func() (*DB, error) { return openDB() })
//	db, err := lazy.Get()
//
//	g, ctx := syncx.NewGroup(parentCtx, syncx.WithLimit(10))
//	g.Go(func(ctx context.Context) error { return doWork(ctx) })
//	err = g.Wait()
//
//	m := syncx.NewMap[string, int]()
//	m.Store("answer", 42)
//	v, ok := m.Load("answer")
//
// # Dependencies
//
// syncx depends only on the Go standard library and the urx panix package
// (used to convert task panics into structured errors).
package syncx
