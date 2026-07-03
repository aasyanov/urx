# poolx — Worker Pools, Object Pools, and Batch Processors for Go

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/poolx)
[License: MIT](../LICENSE)

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

### Position in the urx Stack

```text
┌──────────────────────────────────────────────────────────┐
│  service code: handlers, I/O pipelines, background work  │
└────────────────────────┬─────────────────────────────────┘
                         │
┌────────────────────────▼─────────────────────────────────┐
│  poolx   WorkerPool · ObjectPool[T] · Batch[T]           │
└──────────────┬───────────────────────┬───────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼───────────────────┐
│  panix.Safe / SafeVoid │   │  sync.Pool · channels · time │
│  (panic → error/stats) │   │  (bounded queues, tickers)   │
└────────────────────────┘   └──────────────────────────────┘
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

`Add` appends under a mutex (checking `closed` under the same lock) and triggers a `Flush` when the buffer reaches `WithBatchSize`. A background ticker flushes every `WithFlushInterval` regardless of fill level. `Flush(ctx)` swaps out the buffer under the lock (so the user flush runs lock-free), then calls the user function through `panix.SafeVoid`. The flush function is **context-aware** — `func(ctx context.Context, items []T) error` — so a slow database write observes shutdown. Errors from the timer-driven flush, otherwise invisible, are delivered to an optional `WithErrorHandler`. `Close` sets `closed` under the buffer mutex, drains the remaining slice, stops the ticker, and performs a final flush with a background context (so shutdown is not aborted by the cancelled lifecycle context).

## Normative Contracts


| Contract                          | Guarantee                                                                                    |
| --------------------------------- | -------------------------------------------------------------------------------------------- |
| Tasks never crash a worker        | Every task runs under `panix.SafeVoid`; a panic becomes a counter increment / returned error |
| No send-on-closed-channel         | The task channel is never closed; shutdown is signaled via a separate `done` channel         |
| `Close` drains accepted work      | Tasks enqueued before `Close` run to completion before workers exit                          |
| `Close` is idempotent             | `WorkerPool.Close` / `Batch.Close` use `sync.Once`; repeat calls are no-ops                  |
| `SubmitWait` delivers queued results | After enqueue, `Close` drains every accepted task; `SubmitWait` waits for the result unless ctx is cancelled first |
| Cancelled ctx rejected at admission | `Submit`, `TrySubmit`, and `SubmitWait` call `ctx.Err()` before enqueue, matching urx bulkhead semantics |
| Failed batch flush restores items | A flush error re-queues the batch slice so a later `Flush`/`Add` can retry |
| `Panics` ⊆ `Failed`               | A panicking task increments both counters; a regular error increments only `Failed`          |
| `Put` reset runs before pooling   | With `WithReset`, the object is cleaned before reuse                                         |
| Batch flush is context-aware      | The flush function receives a context cancelled on `Close`                                   |
| Batch final flush always runs     | `Close` flushes remaining items with a background context                                    |
| Batch shutdown is lossless        | `Add` checks `closed` under the buffer mutex; `Close` sets `closed` under the same lock before the final drain |
| `Batch.Flush` rejected after close | Manual `Flush` after `Close` returns [ErrClosed]; only the internal final flush runs during shutdown |


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
	defer func() { _ = wp.Close() }()

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
defer func() { _ = wp.Close() }()

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
defer func() { _ = wp.Close() }()

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
bufPool, err := poolx.NewObjectPool(
	func() *bytes.Buffer { return new(bytes.Buffer) },
	poolx.WithReset(func(b *bytes.Buffer) { b.Reset() }),
)
if err != nil {
	return "", err
}

func render(data any) string {
	buf := bufPool.Get()
	defer bufPool.Put(buf) // reset hook clears it for the next caller
	json.NewEncoder(buf).Encode(data)
	return buf.String()
}
```

### Batched database inserts with error handling

```go
batch, err := poolx.NewBatch(func(ctx context.Context, rows []Row) error {
	return db.InsertContext(ctx, rows)
},
	poolx.WithBatchSize(500),
	poolx.WithFlushInterval(2*time.Second),
	poolx.WithErrorHandler(func(err error) {
		metrics.Inc("batch.flush.errors")
		log.Printf("batch flush failed: %v", err)
	}),
)
if err != nil {
	return err
}
defer func() { _ = batch.Close() }()

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
| `WorkerPool.Close`      | `func (wp *WorkerPool) Close() error`                                                   | Drain queued tasks, stop workers (idempotent)            |
| `WorkerPool.IsClosed`   | `func (wp *WorkerPool) IsClosed() bool`                                                 | Report closed state                                      |
| `WithWorkers`           | `func WithWorkers(n int) WorkerOption`                                                  | Worker goroutine count (default [DefaultWorkers])        |
| `WithQueueSize`         | `func WithQueueSize(n int) WorkerOption`                                                | Queue capacity (default [DefaultQueueSize])              |
| `WithWorkerOp`          | `func WithWorkerOp(op string) WorkerOption`                                             | Custom panix op label for worker tasks                   |
| **ObjectPool**          |                                                                                         |                                                          |
| `NewObjectPool[T]`      | `func NewObjectPool[T any](factory func() T, opts ...ObjectOption[T]) (*ObjectPool[T], error)` | Create a typed pool; [ErrNilFactory] when factory is nil |
| `ObjectPool.Get`        | `func (op *ObjectPool[T]) Get() T`                                                      | Acquire (or create) an object                            |
| `ObjectPool.Put`        | `func (op *ObjectPool[T]) Put(v T)`                                                     | Return an object (runs reset hook if set)                |
| `ObjectPool.Stats`      | `func (op *ObjectPool[T]) Stats() ObjectStats`                                          | Snapshot counters                                        |
| `ObjectPool.ResetStats` | `func (op *ObjectPool[T]) ResetStats()`                                                 | Zero the counters                                        |
| `WithReset[T]`          | `func WithReset[T any](fn func(T)) ObjectOption[T]`                                     | Reset hook run on `Put`                                  |
| **Batch**               |                                                                                         |                                                          |
| `NewBatch[T]`           | `func NewBatch[T any](flush func(ctx, items []T) error, opts ...BatchOption) (*Batch[T], error)` | Create and start a batch; [ErrNilFlush] when flush is nil |
| `Batch.Add`             | `func (b *Batch[T]) Add(item T) error`                                                  | Buffer an item; size-flush when full                     |
| `Batch.Flush`           | `func (b *Batch[T]) Flush(ctx context.Context) error`                                   | Flush the current buffer now; [ErrClosed] after [Batch.Close] |
| `Batch.Stats`           | `func (b *Batch[T]) Stats() BatchStats`                                                 | Snapshot counters                                        |
| `Batch.ResetStats`      | `func (b *Batch[T]) ResetStats()`                                                       | Zero the counters                                        |
| `Batch.Close`           | `func (b *Batch[T]) Close() error`                                                      | Stop ticker, final flush (idempotent)                    |
| `Batch.IsClosed`        | `func (b *Batch[T]) IsClosed() bool`                                                    | Report closed state                                      |
| `WithBatchSize`         | `func WithBatchSize(n int) BatchOption`                                                 | Flush threshold (default 100)                            |
| `WithFlushInterval`     | `func WithFlushInterval(d time.Duration) BatchOption`                                   | Periodic flush interval (default 1s)                     |
| `WithBatchOp`           | `func WithBatchOp(op string) BatchOption`                                               | Custom panix op label for flush callbacks                |
| `WithErrorHandler`      | `func WithErrorHandler(fn func(error)) BatchOption`                                     | Callback for flush errors (incl. ticker)                 |


## Configuration


| Option                 | Default                              | Description                                                      |
| ---------------------- | ------------------------------------ | ---------------------------------------------------------------- |
| `WithWorkers(n)`       | `DefaultWorkers` (4)                 | Worker goroutines. Non-positive ignored.                         |
| `WithQueueSize(n)`     | `DefaultQueueSize` (64)              | Task queue capacity. Non-positive ignored.                       |
| `WithWorkerOp(op)`     | `poolx.WorkerPool`                   | Panic-report operation name for worker tasks.                    |
| `WithReset(fn)`        | nil                                  | Object reset hook run on `Put`.                                  |
| `WithBatchSize(n)`     | `DefaultBatchSize` (100)             | Items buffered before an automatic flush. Non-positive ignored.  |
| `WithFlushInterval(d)` | `DefaultFlushInterval` (1s)          | Periodic flush interval. Non-positive ignored.                   |
| `WithBatchOp(op)`      | `poolx.Batch.Flush`                  | Panic-report operation name for flush callbacks.                 |
| `WithErrorHandler(fn)` | nil                                  | Receives errors from any flush, including the background ticker. |


## Errors


| Error            | Condition                                                                                  |
| ---------------- | ------------------------------------------------------------------------------------------ |
| `ErrClosed`      | Submit/Add/Flush on a closed pool or batch                                                 |
| `ErrQueueFull`   | `TrySubmit` when the queue is at capacity                                                  |
| `ErrCancelled`   | `Submit`/`TrySubmit`/`SubmitWait` when ctx is cancelled or times out during wait           |
| `ErrNilFunc`     | `Submit`/`TrySubmit`/`SubmitWait` when the task function is nil                          |
| `ErrNilFactory`  | `NewObjectPool` when factory is nil                                                        |
| `ErrNilFlush`    | `NewBatch` when flush is nil                                                               |
| `ErrFlushFailed` | Flush returned an error or panicked; items are restored to the buffer for retry            |


All are sentinel errors created with `errors.New`; compare with `errors.Is`. A panicking task surfaces as `*panix.PanicError` (via `SubmitWait`) with `Op == "poolx.WorkerPool"`; a panicking flush is joined under `ErrFlushFailed`.

## Pitfalls

> [!WARNING]
> **Submit** and `TrySubmit` do not return the task's error.** They report only enqueue failures (`ErrClosed`, `ErrQueueFull`, `ErrCancelled`, `ErrNilFunc`). The task's own error/panic is recorded in `Stats()`. Use `SubmitWait` when you need the result.

> [!WARNING]
> **`SubmitWait` waits for the task result after enqueue.** `Close` drains every accepted task before returning, so a queued `SubmitWait` receives its result unless the caller's ctx is cancelled first. Cancelled contexts are rejected before enqueue via `ctx.Err()`.

> [!WARNING]
> **Failed batch flushes restore buffered items.** `ErrFlushFailed` means the flush callback failed but items remain in the buffer for a later retry. Only successful flushes increment `Items`/`Flushed`.

> [!WARNING]
> **Sequence shutdown after you stop submitting.** Call `Close` only after producers stop. `WorkerPool.Close` drains every queued task before returning. `Batch.Close` rejects further `Add` calls and performs one final flush.

> [!WARNING]
> **`Batch.Flush` after `Close` returns [ErrClosed].** The final flush runs only inside `Close`. If the final flush fails, items remain in the buffer for a manual retry only before you discard the instance — do not call `Flush` after `Close`.

> [!WARNING]
> **ObjectPool** makes no retention guarantee.** `sync.Pool` may evict pooled objects on any GC cycle. Never store state in the pool that must survive; it is a reuse cache, not a registry.

> [!WARNING]
> **Without `WithReset`, pooled objects keep their state.** A `bytes.Buffer` put back with data still in it will be handed to the next `Get` with that data. Always reset mutable objects — either via `WithReset` or before `Put`.

> [!WARNING]
> **Batch flushes can run concurrently.** A size-triggered `Add` flush and the periodic ticker flush both call your flush function; they never share the same slice, but your flush target (DB, socket) must tolerate concurrent calls.

## Safety and Concurrency

**Thread safety.** All three types are safe for concurrent use. `WorkerPool` uses an atomic `closed` flag, a `sync.Once`-guarded `Close`, and atomic counters; the task channel is never closed, so concurrent submit/close is panic-free. `ObjectPool` delegates synchronization to `sync.Pool` and uses atomic counters. `Batch` guards its buffer with a `sync.Mutex` — including the `closed` check in `Add` and the final drain in `Close` — and runs the user flush outside the lock.

**Goroutine model.** `WorkerPool` runs exactly `workers` goroutines for its lifetime; `Close` joins them via a `WaitGroup`. `Batch` runs one background ticker goroutine, stopped on `Close`. `ObjectPool` spawns no goroutines.

**Race detector.** The suite hammers each type from dozens of goroutines (`SubmitWait`, `Get`/`Put`, `Add`) under `-race`, plus shutdown-race and cancel-during-execution tests.

## Benchmarks

Three environments, two hardware classes, two operating systems. All values are **medians** of `-count=3` runs. `B/op` and `allocs/op` are deterministic — they depend on code, not hardware.

### Environments

| | Laptop | CI Server (Linux) | CI Server (Windows) |
|---|---|---|---|
| CPU | Intel Core i7-10510U, 4C/8T | AMD EPYC 7763, 4 vCPU | AMD EPYC 9V74, 4 vCPU |
| TDP | 15W (mobile, throttles) | 280W (server, stable) | server, stable |
| OS | Windows 10 | Ubuntu | Windows Server 2022 |
| Go | 1.26 | 1.26 | 1.26 |
| GOMAXPROCS | 8 | 4 | 4 |
| Source | `quality.result` | `benchmark-ubuntu-latest.txt` | `benchmark-windows-latest.txt` |

| Benchmark | What it measures | Laptop | Linux | Windows | B/op | allocs/op |
|---|---|---|---|---|---|---|
| `ObjectPool_GetPut` | `sync.Pool` get/put round trip | 23.2 ns | **16.3 ns** | 16.8 ns | 0 | 0 |
| `ObjectPool_GetPut_WithReset` | Get/put with `Reset` callback | 24.1 ns | **16.9 ns** | 18.1 ns | 0 | 0 |
| `ObjectPool_GetPut_Parallel` | Get/put, 8/4 goroutines | 50.5 ns | **47.8 ns** | 51.8 ns | 0 | 0 |
| `ObjectPool_GetPut_WithReset_Parallel` | Get/put + reset, parallel | 52.4 ns | **47.8 ns** | 43.2 ns | 0 | 0 |
| `Batch_Add` | Mutex + slice append | 15.4 ns | **7.2 ns** | 8.0 ns | 8 | 0 |
| `Batch_Add_Parallel` | `Add` under contention | 53.6 ns | **26.4 ns** | 27.2 ns | 8 | 0 |
| `WorkerPool_Submit` | Fire-and-forget enqueue | 599.8 ns | **284.3 ns** | 540.3 ns | 48 | 1 |
| `WorkerPool_SubmitWait` | Enqueue + result round trip | 1.29 µs | **753.2 ns** | 2.27 µs | 176 | 3 |
| `WorkerPool_Submit_Parallel` | `SubmitWait`, parallel | 1.69 µs | **1.23 µs** | 1.66 µs | 176 | 3 |

### Analysis

**Pure in-memory work — no I/O.** Every benchmark is a CPU + synchronization operation: `sync.Pool` bookkeeping, mutex acquire/release, channel handoff, or slice append. Cross-platform gaps reflect scheduler and mutex implementation, not filesystem or network behavior.

**Laptop vs CI server — 1.4–2.1× on ObjectPool and Batch.** `ObjectPool_GetPut`: 23.2 ns (laptop) vs 16.3 ns (Linux) = 1.4×. `Batch_Add`: 15.4 ns vs 7.2 ns = **2.1×**. The i7-10510U's 15W envelope throttles under sustained load; EPYC's stable clocks and wider pipeline dominate on short critical sections. `sync.Pool` and atomic counters scale predictably — parallel overhead is ~2–3× sequential on both platforms.

**WorkerPool is scheduler-bound, not CPU-bound.** `SubmitWait` on Linux CI: 753 ns — a full cross-goroutine round trip (enqueue, worker wakeup, result channel) in under a microsecond. The same benchmark on Windows CI: 2.27 µs (**3× slower**) and on the laptop: 1.29 µs. This spread is goroutine scheduling and channel wakeup latency, not poolx logic. `Submit` (fire-and-forget, no result channel) shows the same pattern: 284 ns (Linux) vs 540 ns (Windows). Use `Submit` when the caller does not need the result; reserve `SubmitWait` for fan-in where the latency cost is acceptable.

**Parallel worker scaling is flat by design.** `WorkerPool_Submit_Parallel` (1.23 µs Linux) is within noise of sequential `SubmitWait` (753 ns) scaled by contention — the shared task channel is the coordination point, and 8 workers on the laptop saturate it without per-op degradation beyond the inherent channel cost.

**ObjectPool and Batch: 0 allocs/op on all platforms.** The happy path is a `sync.Pool.Get`/`Put` round trip plus two atomic increments (ObjectPool), or a mutex lock + slice append (Batch). The reported B/op on Batch (8 B) is amortized backing-array growth during the benchmark sequence; with a pre-sized buffer, steady-state `Add` is allocation-free.

**Allocation floor.** `ObjectPool` and `Batch` hot paths are 0 allocs/op by design. `WorkerPool.Submit` costs 1 alloc/op (closure only); `SubmitWait` costs 3 (closure + result channel + `panix.SafeVoid` recovery frame).

## Quality


| Metric                | Value                                           |
| --------------------- | ----------------------------------------------- |
| Test functions        | 70                                              |
| Table-driven subtests | 3                                               |
| Benchmarks            | 9                                               |
| Fuzz targets          | 5                                               |
| Examples              | 4                                               |
| Coverage              | 97.7%                                           |
| Race detector         | All pass                                        |
| External deps         | 0 (urx/panix internally; testify in tests only) |


## File Structure

```text
poolx/
├── poolx.go            # Package doc
├── options.go          # WorkerOption, BatchOption, ObjectOption, defaults, WithXxx
├── worker.go           # WorkerPool, Submit/TrySubmit/SubmitWait
├── object.go           # ObjectPool[T]
├── batch.go            # Batch[T], context-aware flush, ticker
├── lifecycle.go        # closeSignal context for batch shutdown (no stored ctx)
├── types.go            # WorkerStats, ObjectStats, BatchStats, isPanic
├── errors.go           # Sentinel errors + internal wrappers
├── helpers_test.go     # Shared test fixtures (closePool, newTestBatch, collectingFlush)
├── worker_test.go      # WorkerPool tests — submit, panic, close-race, concurrency
├── object_test.go      # ObjectPool tests — reuse, reset, options, concurrency
├── batch_test.go       # Batch tests — size/interval flush, requeue, ctx, shutdown race
├── lifecycle_test.go   # closeSignal context interface compliance
├── bench_test.go       # 9 benchmarks: sequential + parallel
├── fuzz_test.go        # FuzzWorkerPool*, FuzzBatchAdd*, FuzzObjectPoolGetPut
├── example_test.go     # 4 runnable GoDoc examples
├── footprint_test.go   # Primary type + stats/config struct size guards
└── README.md           # This file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.