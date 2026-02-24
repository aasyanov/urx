// Package poolx provides bounded worker pools, generic object pools, and
// batch processors with panic recovery and lifecycle management.
//
// [WorkerPool] manages a fixed set of goroutines that process submitted tasks:
//
//	wp := poolx.NewWorkerPool(poolx.WithWorkers(8), poolx.WithQueueSize(128))
//	defer wp.Close()
//	wp.Submit(ctx, func(ctx context.Context) error { return doWork(ctx) })
//
// [ObjectPool] is a generic, type-safe pool backed by [sync.Pool]:
//
//	pool := poolx.NewObjectPool(func() *bytes.Buffer { return new(bytes.Buffer) })
//	buf := pool.Get()
//	defer pool.Put(buf)
//
// [Batch] buffers items and flushes them in configurable batches:
//
//	b := poolx.NewBatch(func(items []Event) error { return db.Insert(ctx, items) })
//	defer b.Close()
//	b.Add(evt)
package poolx
