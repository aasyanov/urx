// Package poolx provides bounded worker pools, generic object pools, and
// batch processors with panic recovery and lifecycle management.
//
// All three primitives run user code under [github.com/aasyanov/urx/panix]
// recovery, so a panicking task or flush cannot crash the pool. Each exposes
// an observability snapshot via a Stats method and an idempotent Close.
//
// # Worker Pool
//
// [WorkerPool] manages a fixed set of goroutines draining a bounded queue:
//
//	wp := poolx.NewWorkerPool(poolx.WithWorkers(8), poolx.WithQueueSize(128))
//	defer wp.Close()
//	wp.Submit(ctx, func(ctx context.Context) error { return doWork(ctx) })
//	err := wp.SubmitWait(ctx, func(ctx context.Context) error { return doWork(ctx) })
//
// # Object Pool
//
// [ObjectPool] is a generic, type-safe pool backed by [sync.Pool], with an
// optional reset hook:
//
//	pool, err := poolx.NewObjectPool(
//	    func() *bytes.Buffer { return new(bytes.Buffer) },
//	    poolx.WithReset(func(b *bytes.Buffer) { b.Reset() }),
//	)
//	if err != nil { return err }
//	buf := pool.Get()
//	defer pool.Put(buf)
//
// # Batch
//
// [Batch] buffers items and flushes them through a context-aware function
// when the buffer fills or the interval elapses:
//
//	b, err := poolx.NewBatch(func(ctx context.Context, items []Event) error {
//	    return db.Insert(ctx, items)
//	}, poolx.WithBatchSize(500))
//	if err != nil { return err }
//	defer b.Close()
//	b.Add(evt)
//
// # Zero Dependencies
//
// poolx depends only on the Go standard library and the urx panix package.
package poolx
