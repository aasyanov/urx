# poolx

Bounded worker pools, generic object pools, and batch processors with panic
recovery and lifecycle management.

## Philosophy

**Three pools, one package.** `poolx` provides `WorkerPool` (fixed goroutine
pool for task dispatch), `ObjectPool` (generic `sync.Pool` wrapper), and
`Batch` (buffered batch processor with periodic flush). Each component recovers
panics via `panix` and reports structured errors via `errx`. They do not retry,
circuit-break, or rate-limit.

## Quick start

```go
// Worker pool
wp := poolx.NewWorkerPool(poolx.WithWorkers(8), poolx.WithQueueSize(128))
defer wp.Close()

wp.Submit(ctx, func(ctx context.Context) error { return doWork(ctx) })

// Object pool
pool := poolx.NewObjectPool(func() *bytes.Buffer { return new(bytes.Buffer) })
buf := pool.Get()
defer pool.Put(buf)

// Batch processor
b := poolx.NewBatch(func(items []Event) error { return db.Insert(ctx, items) },
    poolx.WithBatchSize(200),
    poolx.WithFlushInterval(time.Second),
)
defer b.Close()

b.Add(evt)
```

## API

### WorkerPool

| Function / Method | Description |
|---|---|
| `NewWorkerPool(opts ...WorkerOption) *WorkerPool` | Create and start a pool (4 workers, 64-slot queue) |
| `wp.Submit(ctx, fn) error` | Enqueue task (blocks if queue full) |
| `wp.TrySubmit(ctx, fn) error` | Non-blocking submit; returns `CodeQueueFull` if full |
| `wp.Stats() WorkerStats` | Point-in-time counters |
| `wp.ResetStats()` | Zero counters |
| `wp.Close()` | Shut down pool (idempotent) |
| `wp.IsClosed() bool` | Whether pool is closed |

### ObjectPool

| Function / Method | Description |
|---|---|
| `NewObjectPool[T](factory) *ObjectPool[T]` | Create a generic object pool |
| `op.Get() T` | Acquire object from pool |
| `op.Put(v T)` | Return object to pool |
| `op.Stats() ObjectStats` | Get/Put counters |
| `op.ResetStats()` | Zero counters |

### Batch

| Function / Method | Description |
|---|---|
| `NewBatch[T](flush, opts...) *Batch[T]` | Create a batch processor (100 items, 1 s interval) |
| `b.Add(item) error` | Append item; auto-flushes when batch is full |
| `b.Flush() error` | Force flush of current buffer |
| `b.Stats() BatchStats` | Point-in-time counters |
| `b.ResetStats()` | Zero counters |
| `b.Close() error` | Flush remaining + stop ticker (idempotent) |
| `b.IsClosed() bool` | Whether batch is closed |

### Options

| Option | Default | Description |
|---|---|---|
| `WithWorkers(n)` | `4` | Number of worker goroutines |
| `WithQueueSize(n)` | `64` | Task queue capacity |
| `WithBatchSize(n)` | `100` | Items before auto-flush |
| `WithFlushInterval(d)` | `1s` | Periodic flush interval |

## Behavior details

- **Worker lifecycle**: `NewWorkerPool` starts `n` goroutines. Each pulls tasks
  from a buffered channel. `Close` closes the channel and waits for workers to
  drain remaining tasks.

- **Submit vs TrySubmit**: `Submit` blocks until a queue slot is available but
  respects context cancellation — if `ctx.Done()` fires while waiting for a slot,
  `Submit` returns `CodeCancelled` without enqueuing the task. `TrySubmit` returns
  immediately with `CodeQueueFull` if the queue is full.

- **Batch flush**: `Add` appends to an internal buffer. When the buffer reaches
  `BatchSize`, or when the `FlushInterval` ticker fires, the batch is flushed
  by calling the user-provided `flush` function. `Close` flushes any remaining
  items.

- **Panic recovery**: `WorkerPool` wraps each task with `panix.Safe`. `Batch`
  wraps each flush call with `panix.Safe`. Panics produce structured
  `*errx.Error` values and are counted in stats.

- **Object pool**: thin wrapper around `sync.Pool` with atomic Get/Put counters.
  Note: for pointer types, `Put(nil)` is allowed by `sync.Pool` and will cause
  `Get()` to return `nil`. Callers should guard against this or use the factory
  to guarantee non-nil values.

## Error diagnostics

All errors are `*errx.Error` with `Domain = "POOL"`.

### Codes

| Code | When |
|---|---|
| `CLOSED` | Submit/Add on a closed pool or batch |
| `QUEUE_FULL` | TrySubmit when queue is at capacity |
| `CANCELLED` | Context cancelled while waiting for a queue slot |
| `FLUSH_FAILED` | Batch flush function returned an error |

### Example

```text
POOL.CLOSED: worker pool is closed
POOL.QUEUE_FULL: worker pool queue is full
POOL.CANCELLED: context cancelled while waiting for queue slot | cause: context canceled
POOL.FLUSH_FAILED: batch flush failed | cause: <underlying error>
```

## Thread safety

- `WorkerPool`: `Submit` / `TrySubmit` use a buffered channel — concurrent safe; `Close` uses `atomic.Bool.CompareAndSwap` (idempotent); `IsClosed` reads `atomic.Bool`
- `Batch`: `Add` / `Flush` acquire `sync.Mutex`; `Close` uses `atomic.Bool.CompareAndSwap` (idempotent); `IsClosed` reads `atomic.Bool`
- `ObjectPool`: backed by `sync.Pool` (concurrent safe); counters are atomic
- `Stats` / `ResetStats` use atomic counters — lock-free

## Tests

**22 tests, 96.8% statement coverage.**

```text
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/poolx  coverage: 96.8% of statements
```

Coverage includes:
- WorkerPool: submit, queue full, closed pool, panic recovery, concurrent submit
- ObjectPool: get/put, stats, reset
- Batch: add, auto-flush, manual flush, close flush, closed batch, flush error
- Lifecycle: Close idempotent, IsClosed

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz.
Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkWorkerPool_Submit   ~1038 ns/op      64 B/op     2 allocs/op
BenchmarkObjectPool_GetPut    ~132 ns/op      24 B/op     1 allocs/op
BenchmarkBatch_Add             ~41 ns/op       8 B/op     0 allocs/op
```

### Analysis

**WorkerPool Submit:** ~1.0 us, 2 allocs. Channel send + goroutine wakeup + `panix.Safe` closure. Under 1.5 us for a full dispatch cycle.

**ObjectPool Get/Put:** ~132 ns, 1 alloc. The single allocation is the factory call on a `sync.Pool` miss. On cache hit, the cost drops to ~20 ns.

**Batch Add:** ~41 ns, 0 allocs. Mutex lock + slice append. No allocation because the slice is pre-allocated to `BatchSize`. Extremely fast buffering.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| WorkerPool Submit | < 1.5 us, 2 allocs | Negligible vs task work |
| ObjectPool Get/Put | < 150 ns, 1 alloc | sync.Pool performance |
| Batch Add | < 50 ns, 0 allocs | Hot-path friendly |

## What poolx does NOT do

| Concern | Owner |
|---------|-------|
| Retry on task failure | `retryx` |
| Rate limiting | `ratex` |
| Circuit breaking | `circuitx` |
| Task prioritization | caller |
| Persistent queue | message broker |
| Logging | caller / `slog` |

## File structure

```text
pkg/poolx/
    poolx.go       -- package doc
    worker.go      -- WorkerPool, NewWorkerPool(), Submit(), TrySubmit()
    batch.go       -- Batch, NewBatch(), Add(), Flush()
    object.go      -- ObjectPool, NewObjectPool(), Get(), Put()
    errors.go      -- DomainPool, Code constants, error constructors
    helpers.go     -- asErrx helper
    poolx_test.go  -- 22 tests, 96.8% coverage
    bench_test.go  -- 3 benchmarks
    README.md
```
