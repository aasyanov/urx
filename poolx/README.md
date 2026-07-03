# poolx — Worker Pools, Object Pools, and Batch Processors for Go

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/poolx)
[License: MIT](https://opensource.org/licenses/MIT)

Three pooling primitives — a bounded worker pool, a generic object pool, and a context-aware batch processor — each with panic recovery, observability counters, and idempotent shutdown. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **These are three independent primitives, not a unified framework.** `WorkerPool` bounds *concurrency* (goroutines + queue), `ObjectPool` bounds *allocation* (reuse via `sync.Pool`), and `Batch` bounds *I/O frequency* (amortize writes). They share conventions — `Stats()`, idempotent `Close`, panic-safe user code — but compose through plain Go, not through each other. Pick the one that matches your bottleneck.

## The Problem

Three recurring production needs sit just above the standard library, each with the same set of easy-to-get-wrong details:

1. **Unbounded goroutines.** `go handle(req)` per request works until a traffic spike spawns 100k goroutines, exhausts memory, and the scheduler thrashes. You need a fixed worker count with a bounded queue that applies backpressure — and a panicking handler must not kill a worker or the process.
2. **Allocation churn.** Hot paths that allocate a `bytes.Buffer`, a scratch slice, or a JSON encoder per call generate constant GC pressure. `sync.Pool` solves it but is untyped (`any` casts everywhere) and easy to misuse (forgetting to reset pooled state leaks data between callers).
3. **Per-item I/O.** Writing rows to a database or events to a broker one at a time is dominated by round-trip latency. Buffering into batches and flushing on size-or-time is standard — but the flush must respect cancellation, recover from panics, and surface errors that happen on the background timer.

Hand-rolled versions of these repeat the same bugs: send-on-closed-channel panics during shutdown, lost tasks, leaked goroutines, dropped flush errors, and panics that escape and crash the service. `poolx` provides hardened implementations of all three.

## Architectural Position

```text
✅ WorkerPool — bounded concurrency: fixed goroutines + bounded queue + backpressure
✅ ObjectPool — typed allocation reuse over sync.Pool, with optional reset hook
✅ Batch      — size/interval buffering with a context-aware, panic-safe flush

❌ NOT a job scheduler (no cron, no priorities, no retries — compose with retryx)
❌ NOT a distributed queue (in-process only; no persistence, no delivery guarantees)
❌ NOT a DI container or lifecycle graph (each primitive is standalone)
❌ NOT a rate limiter (use ratex/quotax for request-rate control)
```

Each primitive runs user code through `[panix](../panix)` so a panicking task or flush is converted to an error instead of crashing the process.

## Architecture

```text
 WorkerPool                           ObjectPool[T]              Batch[T]
┌─────────────────────────┐          ┌──────────────┐           ┌─────────────────────────┐
│ Submit/TrySubmit/       │          │ Get ─► sync. │           │ Add ─► buf []T          │
│ SubmitWait              │          │       Pool   │           │   │ len ≥ size?         │
│   │                     │          │ Put ─► reset?│           │   ▼                     │
│   ▼ tasks chan(queue)   │          │      ─► Pool │           │ Flush(ctx) ──► flush()  │
│ ┌────┬────┬────┐        │          └──────────────┘           │   ▲                     │
│ │ w0 │ w1 │ wN │ ◄──────┤  panix.SafeVoid                     │   │ ticker(interval)    │
│ └────┴────┴────┘        │  per task                           │ done ◄── Close ─► Flush │
│ done ◄── Close ─► drain │                                     └─────────────────────────┘
└─────────────────────────┘
```

## How It Works

### WorkerPool

`NewWorkerPool` starts `workers` goroutines, each selecting on a shared buffered `tasks` channel and a `done` channel. `Submit` wraps the user function in a closure that runs it through `panix.SafeVoid` and records the outcome, then sends the closure to the queue:

```text
Submit(ctx, fn)
    ├── closed?                  → ErrClosed
    ├── select:
    │     tasks <- task          → submitted++, nil
    │     <-done  (pool closing) → ErrClosed
    │     <-ctx.Done()           → ErrCancelled(cause)
    └── worker runs task:
          panix.SafeVoid(fn)
            ├── nil    → completed++
            ├── panic  → panics++, failed++
            └── error  → failed++
```

`TrySubmit` uses a non-blocking `select` with a `default` that returns `ErrQueueFull`. `SubmitWait` adds a buffered result channel, enqueues the task, and blocks on the result (or `ctx.Done()`), returning the task's own error or `*panix.PanicError`.

**Shutdown is the hard part.** `Close` signals `done` and waits on a `WaitGroup`. Workers, on observing `done`, drain any already-queued tasks to completion before exiting — so work accepted before `Close` is never silently dropped. The `tasks` channel is **never closed**, which structurally eliminates the classic "send on closed channel" panic that plagues naive worker pools; blocked submitters wake up via the `done` branch instead.

### ObjectPool

A thin, type-safe generic wrapper over `sync.Pool`. `Get` returns a pooled `T` (or calls the factory on a miss, incrementing `Creates`). `Put` optionally runs a reset hook — `WithReset(func(*bytes.Buffer) { b.Reset() })` — before returning the object, so the next caller never sees stale state. Counters (`Gets`, `Puts`, `Creates`) are atomic; the hit ratio is `(Gets-Creates)/Gets`.

### Batch

`Add` appends under a mutex and triggers a `Flush` when the buffer reaches `WithBatchSize`. A background ticker flushes every `WithFlushInterval` regardless of fill level. `Flush(ctx)` swaps out the buffer under the lock (so the user flush runs lock-free), then calls the user function through `panix.SafeVoid`. The flush function is **context-aware** — `func(ctx context.Context, items []T) error` — so a slow database write observes shutdown. Errors from the timer-driven flush, otherwise invisible, are delivered to an optional `WithErrorHandler`. `Close` stops the ticker, performs a final flush with a background context (so shutdown is not aborted by the cancelled lifecycle context), and cancels the lifecycle context.

## Normative Contracts


| Contract                          | Guarantee                                                                                    |
| --------------------------------- | -------------------------------------------------------------------------------------------- |
| Tasks never crash a worker        | Every task runs under `panix.SafeVoid`; a panic becomes a counter increment / returned error |
| No send-on-closed-channel         | The task channel is never closed; shutdown is signaled via a separate `done` channel         |
| `Close` drains accepted work      | Tasks enqueued before `Close` run to completion before workers exit                          |
| `Close` is idempotent             | `WorkerPool.Close` / `Batch.Close` use `sync.Once`; repeat calls are no-ops                  |
| `SubmitWait` never blocks forever | It returns on the result, or on `ctx` cancellation                                           |
| `Panics` ⊆ `Failed`               | A panicking task increments both counters; a regular error increments only `Failed`          |
| `Put` reset runs before pooling   | With `WithReset`, the object is cleaned before reuse                                         |
| Batch flush is context-aware      | The flush function receives a context cancelled on `Close`                                   |
| Batch final flush always runs     | `Close` flushes remaining items with a background context                                    |


## Quick Start

```go
package main

import (
	"context"
	"log"

	"github.com/aasyanov/urx/poolx"
)

func main() {
	wp := poolx.NewWorkerPool(poolx.WithWorkers(8), poolx.WithQueueSize(256))
	defer wp.Close()

	for _, job := range jobs {
		job := job
		if err := wp.Submit(context.Background(), func(ctx context.Context) error {
			return process(ctx, job)
		}); err != nil {
			log.Printf("submit rejected: %v", err)
		}
	}
	// Close drains all queued jobs before returning.
}
```

## Usage Scenarios

### Bounded request processing with backpressure

```go
wp := poolx.NewWorkerPool(poolx.WithWorkers(runtime.NumCPU()), poolx.WithQueueSize(1000))
defer wp.Close()

func handler(w http.ResponseWriter, r *http.Request) {
	// TrySubmit sheds load instead of blocking when saturated.
	err := wp.TrySubmit(r.Context(), func(ctx context.Context) error {
		return ingest(ctx, r.Body)
	})
	if errors.Is(err, poolx.ErrQueueFull) {
		http.Error(w, "overloaded", http.StatusServiceUnavailable)
		return
	}
}
```

### Synchronous fan-out with results

```go
wp := poolx.NewWorkerPool(poolx.WithWorkers(16))
defer wp.Close()

var wg sync.WaitGroup
results := make([]error, len(urls))
for i, url := range urls {
	i, url := i, url
	wg.Add(1)
	go func() {
		defer wg.Done()
		results[i] = wp.SubmitWait(ctx, func(ctx context.Context) error {
			return fetch(ctx, url)
		})
	}()
}
wg.Wait()
```

### Zero-allocation buffer reuse

```go
bufPool := poolx.NewObjectPool(
	func() *bytes.Buffer { return new(bytes.Buffer) },
	poolx.WithReset(func(b *bytes.Buffer) { b.Reset() }),
)

func render(data any) string {
	buf := bufPool.Get()
	defer bufPool.Put(buf) // reset hook clears it for the next caller
	json.NewEncoder(buf).Encode(data)
	return buf.String()
}
```

### Batched database inserts with error handling

```go
batch := poolx.NewBatch(func(ctx context.Context, rows []Row) error {
	return db.InsertContext(ctx, rows)
},
	poolx.WithBatchSize(500),
	poolx.WithFlushInterval(2*time.Second),
	poolx.WithErrorHandler(func(err error) {
		metrics.Inc("batch.flush.errors")
		log.Printf("batch flush failed: %v", err)
	}),
)
defer batch.Close() // flushes the tail

for row := range stream {
	if err := batch.Add(row); err != nil {
		log.Printf("size-flush failed: %v", err)
	}
}
```

## API


| Symbol                  | Signature                                                                               | Description                                              |
| ----------------------- | --------------------------------------------------------------------------------------- | -------------------------------------------------------- |
| **WorkerPool**          |                                                                                         |                                                          |
| `NewWorkerPool`         | `func NewWorkerPool(opts ...WorkerOption) *WorkerPool`                                  | Start a pool (default 4 workers, 64-slot queue)          |
| `WorkerPool.Submit`     | `func (wp *WorkerPool) Submit(ctx, fn func(ctx) error) error`                           | Enqueue, blocking until a slot opens / ctx done / closed |
| `WorkerPool.TrySubmit`  | `func (wp *WorkerPool) TrySubmit(ctx, fn func(ctx) error) error`                        | Enqueue without blocking; `ErrQueueFull` if saturated    |
| `WorkerPool.SubmitWait` | `func (wp *WorkerPool) SubmitWait(ctx, fn func(ctx) error) error`                       | Enqueue and block for the task's own result              |
| `WorkerPool.Stats`      | `func (wp *WorkerPool) Stats() WorkerStats`                                             | Snapshot counters                                        |
| `WorkerPool.ResetStats` | `func (wp *WorkerPool) ResetStats()`                                                    | Zero the counters                                        |
| `WorkerPool.Close`      | `func (wp *WorkerPool) Close()`                                                         | Drain queued tasks, stop workers (idempotent)            |
| `WorkerPool.IsClosed`   | `func (wp *WorkerPool) IsClosed() bool`                                                 | Report closed state                                      |
| `WithWorkers`           | `func WithWorkers(n int) WorkerOption`                                                  | Worker goroutine count (default 4)                       |
| `WithQueueSize`         | `func WithQueueSize(n int) WorkerOption`                                                | Queue capacity (default 64)                              |
| **ObjectPool**          |                                                                                         |                                                          |
| `NewObjectPool[T]`      | `func NewObjectPool[T any](factory func() T, opts ...ObjectOption[T]) *ObjectPool[T]`   | Create a typed pool (panics if factory is nil)           |
| `ObjectPool.Get`        | `func (op *ObjectPool[T]) Get() T`                                                      | Acquire (or create) an object                            |
| `ObjectPool.Put`        | `func (op *ObjectPool[T]) Put(v T)`                                                     | Return an object (runs reset hook if set)                |
| `ObjectPool.Stats`      | `func (op *ObjectPool[T]) Stats() ObjectStats`                                          | Snapshot counters                                        |
| `ObjectPool.ResetStats` | `func (op *ObjectPool[T]) ResetStats()`                                                 | Zero the counters                                        |
| `WithReset[T]`          | `func WithReset[T any](fn func(T)) ObjectOption[T]`                                     | Reset hook run on `Put`                                  |
| **Batch**               |                                                                                         |                                                          |
| `NewBatch[T]`           | `func NewBatch[T any](flush func(ctx, items []T) error, opts ...BatchOption) *Batch[T]` | Create and start a batch (panics if flush is nil)        |
| `Batch.Add`             | `func (b *Batch[T]) Add(item T) error`                                                  | Buffer an item; size-flush when full                     |
| `Batch.Flush`           | `func (b *Batch[T]) Flush(ctx context.Context) error`                                   | Flush the current buffer now                             |
| `Batch.Stats`           | `func (b *Batch[T]) Stats() BatchStats`                                                 | Snapshot counters                                        |
| `Batch.ResetStats`      | `func (b *Batch[T]) ResetStats()`                                                       | Zero the counters                                        |
| `Batch.Close`           | `func (b *Batch[T]) Close() error`                                                      | Stop ticker, final flush (idempotent)                    |
| `Batch.IsClosed`        | `func (b *Batch[T]) IsClosed() bool`                                                    | Report closed state                                      |
| `WithBatchSize`         | `func WithBatchSize(n int) BatchOption`                                                 | Flush threshold (default 100)                            |
| `WithFlushInterval`     | `func WithFlushInterval(d time.Duration) BatchOption`                                   | Periodic flush interval (default 1s)                     |
| `WithErrorHandler`      | `func WithErrorHandler(fn func(error)) BatchOption`                                     | Callback for flush errors (incl. ticker)                 |


## Configuration


| Option                 | Default | Description                                                      |
| ---------------------- | ------- | ---------------------------------------------------------------- |
| `WithWorkers(n)`       | `4`     | Worker goroutines. Non-positive ignored.                         |
| `WithQueueSize(n)`     | `64`    | Task queue capacity. Non-positive ignored.                       |
| `WithReset(fn)`        | nil     | Object reset hook run on `Put`.                                  |
| `WithBatchSize(n)`     | `100`   | Items buffered before an automatic flush. Non-positive ignored.  |
| `WithFlushInterval(d)` | `1s`    | Periodic flush interval. Non-positive ignored.                   |
| `WithErrorHandler(fn)` | nil     | Receives errors from any flush, including the background ticker. |


## Errors


| Error            | Condition                                                                                  |
| ---------------- | ------------------------------------------------------------------------------------------ |
| `ErrClosed`      | Submit/Add on a closed pool or batch                                                       |
| `ErrQueueFull`   | `TrySubmit` when the queue is at capacity                                                  |
| `ErrCancelled`   | `Submit`/`SubmitWait` when ctx is cancelled before enqueue (joined with the context cause) |
| `ErrFlushFailed` | A flush function returned an error or panicked (joined with the cause)                     |


All are sentinel errors created with `errors.New`; compare with `errors.Is`. A panicking task surfaces as `*panix.PanicError` (via `SubmitWait`) with `Op == "poolx.WorkerPool"`; a panicking flush is joined under `ErrFlushFailed`.

## Pitfalls

> [!WARNING]
> `**Submit` and `TrySubmit` do not return the task's error.** They report only enqueue failures (`ErrClosed`, `ErrQueueFull`, `ErrCancelled`). The task's own error/panic is recorded in `Stats()`. Use `SubmitWait` when you need the result.

> [!WARNING]
> **Tasks accepted concurrently with `Close` may be dropped.** `Close` drains tasks already in the queue, but a `Submit` racing with `Close` may return `nil` yet have its task discarded. Sequence shutdown after you stop submitting.

> [!WARNING]
> `**ObjectPool` makes no retention guarantee.** `sync.Pool` may evict pooled objects on any GC cycle. Never store state in the pool that must survive; it is a reuse cache, not a registry.

> [!WARNING]
> **Without `WithReset`, pooled objects keep their state.** A `bytes.Buffer` put back with data still in it will be handed to the next `Get` with that data. Always reset mutable objects — either via `WithReset` or before `Put`.

> [!WARNING]
> **Batch flushes can run concurrently.** A size-triggered `Add` flush and the periodic ticker flush both call your flush function; they never share the same slice, but your flush target (DB, socket) must tolerate concurrent calls.

## Safety and Concurrency

**Thread safety.** All three types are safe for concurrent use. `WorkerPool` uses an atomic `closed` flag, a `sync.Once`-guarded `Close`, and atomic counters; the task channel is never closed, so concurrent submit/close is panic-free. `ObjectPool` delegates synchronization to `sync.Pool` and uses atomic counters. `Batch` guards its buffer with a `sync.Mutex` and runs the user flush outside the lock.

**Goroutine model.** `WorkerPool` runs exactly `workers` goroutines for its lifetime; `Close` joins them via a `WaitGroup`. `Batch` runs one background ticker goroutine, stopped on `Close`. `ObjectPool` spawns no goroutines.

**Race detector.** The suite hammers each type from dozens of goroutines (`SubmitWait`, `Get`/`Put`, `Add`) under `-race`, plus shutdown-race and cancel-during-execution tests.

## Benchmarks

> CPU: Intel i7-10510U · OS: Windows 10 · Go 1.26 · `-benchmem -count=3`


| Benchmark                     | ns/op | B/op | allocs/op |
| ----------------------------- | ----- | ---- | --------- |
| `ObjectPool_GetPut`           | 38    | 0    | 0         |
| `ObjectPool_GetPut_WithReset` | 36    | 0    | 0         |
| `ObjectPool_GetPut_Parallel`  | 93    | 0    | 0         |
| `Batch_Add`                   | 27    | 0    | 0         |
| `Batch_Add_Parallel`          | 80    | 0    | 0         |
| `WorkerPool_SubmitWait`       | 2,600 | 176  | 3         |
| `WorkerPool_Submit_Parallel`  | 2,800 | 176  | 3         |


### Analysis

- `**ObjectPool_GetPut`: 38 ns, 0 allocs.** The happy path is a `sync.Pool.Get`/`Put` round trip plus two atomic increments. Zero allocations because the buffer is reused; the reset hook (`WithReset`) costs nothing measurable (`bytes.Buffer.Reset` is a length zeroing). This is the entire point of the type — turning per-call allocations into amortized reuse.
- `**ObjectPool_GetPut_Parallel`: 93 ns, 0 allocs.** ~2.4× the sequential cost under 8-way parallelism. `sync.Pool` shards per-P, so contention is on the atomic counters, not the pool itself. Still allocation-free.
- `**Batch_Add`: 27 ns, 0 allocs.** A mutex lock, a slice append, and a length check. The reported ~7 B/op is amortized backing-array growth during the benchmark's append sequence; in steady state with a pre-sized buffer there are no allocations. This is the hottest operation and it stays allocation-free per call.
- `**WorkerPool_SubmitWait`: 2,600 ns, 3 allocs (176 B).** Dominated by cross-goroutine handoff: enqueue, worker wakeup, and the result channel round trip. The 3 allocations are the task closure, the result channel, and the `panix.SafeVoid` deferred frame. This is the cost of *bounded concurrency with a result* — appropriate for I/O-bound work (network, disk) where a few microseconds of coordination is dwarfed by the task itself, but not for nanosecond-scale CPU work that should run inline.
- **Parallel worker scaling: 2,800 ns.** Nearly flat versus sequential — the single shared queue channel is the coordination point, and 8 workers keep it saturated without the per-submit cost degrading. Throughput scales with worker count until the queue channel's mutex becomes the bottleneck.
- **Allocation floor.** `ObjectPool` and `Batch` hot paths are 0 allocs by design. `WorkerPool`'s 3 allocs/op is the architectural minimum for a per-task closure + result channel + recovery frame; eliminating them would require giving up either the result (`SubmitWait`) or the panic safety net.

## Quality


| Metric                | Value                                           |
| --------------------- | ----------------------------------------------- |
| Test functions        | 43                                              |
| Table-driven subtests | 2                                               |
| Benchmarks            | 7                                               |
| Fuzz targets          | 0 (no untrusted byte input)                     |
| Examples              | 3                                               |
| Coverage              | 95.7%                                           |
| Race detector         | All pass                                        |
| External deps         | 0 (urx/panix internally; testify in tests only) |


## File Structure

```text
poolx/
├── poolx.go            # Package doc
├── worker.go           # WorkerPool, Submit/TrySubmit/SubmitWait, WorkerOption, isPanic
├── object.go           # ObjectPool[T], WithReset, ObjectOption
├── batch.go            # Batch[T], context-aware flush, ticker, BatchOption
├── types.go            # WorkerStats, ObjectStats, BatchStats
├── errors.go           # ErrClosed, ErrQueueFull, ErrCancelled, ErrFlushFailed + wrappers
├── worker_test.go      # WorkerPool tests — submit, panic, close-race, concurrency
├── object_test.go      # ObjectPool tests — reuse, reset, concurrency
├── batch_test.go       # Batch tests — size/interval flush, ctx, error handler
├── bench_test.go       # 7 benchmarks: sequential + parallel
├── example_test.go     # 3 runnable GoDoc examples
├── footprint_test.go   # Stats/config struct size guards
└── README.md           # This file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.