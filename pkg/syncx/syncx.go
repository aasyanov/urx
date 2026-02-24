// Package syncx provides generic concurrency primitives for industrial Go
// services.
//
// [Lazy] is a generic, thread-safe lazy initializer (like [sync.Once] but
// with a typed return value and error handling). [Group] is an error-group
// with [panix.Safe] panic recovery and optional concurrency limiting.
// [Map] is a generic, type-safe concurrent map.
//
//	lazy := syncx.NewLazy(func() (DB, error) { return openDB() })
//	db, err := lazy.Get()
//
//	g, ctx := syncx.NewGroup(parentCtx, syncx.WithLimit(10))
//	g.Go(func(ctx context.Context) error { return doWork(ctx) })
//	err = g.Wait()
//
//	m := syncx.NewMap[string, int]()
//	m.Store("key", 42)
//	v, ok := m.Load("key")
package syncx
