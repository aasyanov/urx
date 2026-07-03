# syncx — Typed Lazy Init, Panic-Safe Error Group, Generic Concurrent Map

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/syncx)
[License: MIT](https://opensource.org/licenses/MIT)

Three generic concurrency primitives — a typed lazy initializer, a panic-safe error group, and a type-safe concurrent map with O(1) length. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **These are three independent primitives that share a package, not a single abstraction.** `Lazy` defers and deduplicates *initialization*, `Group` bounds and supervises *goroutines*, `Map` adds *typing and length* to `sync.Map`. They share conventions — generics, panic safety, `-race`-clean concurrency — but compose through plain Go, not through each other. The common thread is that each fixes a sharp edge in `sync` that every service re-implements (and re-bugs).

## The Problem

The standard library's `sync` package is a toolbox of correct-but-low-level primitives. Three patterns recur in every service and are repeatedly re-implemented with subtle bugs:

1. **Run-once initialization with a value and an error.** `sync.Once` runs a function once but returns nothing — so callers wrap it with a package-level variable, a separate error variable, and manual double-checked locking. The init function often performs I/O (open a DB, dial a service), so it can *fail*, and a transient failure should be retryable rather than cached forever.
2. **Supervised concurrent tasks.** `golang.org/x/sync/errgroup` collects the first error and cancels siblings, but a panicking task crashes the whole process. Production task functions touch user code, third-party libraries, and reflection — any of which can panic. You also frequently want to bound concurrency and observe how many tasks ran, succeeded, or panicked.
3. **A typed concurrent map with a length.** `sync.Map` is the right structure for read-mostly or disjoint-key workloads, but its API is `any`-typed (casts at every call site) and deliberately omits `Len()` — so callers bolt on an `atomic.Int64` and get the counting wrong under concurrent overwrites and deletes.

`syncx` provides hardened, generic implementations of all three, each `-race`-clean and 100% covered.

## Architectural Position

```text
✅ Lazy[T]  — run-once typed init with error handling and reset (sync.Once + value + error)
✅ Group    — panic-safe error group with optional concurrency limit and task stats
✅ Map[K,V] — type-safe sync.Map wrapper with O(1) Len, Swap, LoadAndDelete, Clear

❌ NOT a generic "sync utilities" grab-bag — exactly three focused primitives
❌ NOT a goroutine scheduler or work queue (see poolx.WorkerPool for bounded queues)
❌ NOT a replacement for sync.Mutex / sync.WaitGroup in simple cases
❌ NOT a DI container — Lazy memoizes one value, it is not a dependency graph
```

### Position in the urx Stack

```text
┌────────────────────────────────────────────────────────────┐
│  service code: lazy singletons, fan-out tasks, caches      │
└────────────────────────┬───────────────────────────────────┘
                         │
┌────────────────────────▼───────────────────────────────────┐
│  syncx   Lazy[T] · Group · Map[K,V]                        │
└──────────────┬────────────────────────┬────────────────────┘
               │ (Group only)           │
┌──────────────▼─────────┐   ┌──────────▼────────────────────┐
│  panix.SafeVoid        │   │  sync · sync/atomic · context │
│  (panic → PanicError)  │   │                               │
└────────────────────────┘   └───────────────────────────────┘
```

## Architecture

```text
                         syncx
   ┌───────────────────┬───────────────────┬───────────────────┐
   │                   │                   │                   │
 Lazy[T] (lazy.go)   Group (group.go)    Map[K,V] (map.go)
   │                   │                   │
 mu sync.Mutex       ctx (derived)       sync.Map
 init func           cancel CancelFunc   len atomic.Int64
 val T               sem chan struct{}     │
 done bool           wg / once           Store/Load/Delete
   │                 started/succeeded/  Swap/LoadAndDelete
 Get / Done /        failed/panicked     LoadOrStore/Range
 Reset               (atomic.Int64)      Len/Clear
                       │
                     Go / TryGo / Wait / Stats
                       │
                     panix.SafeVoid (panic recovery)
```

## How It Works

### Lazy: double-checked run-once with retry-on-failure

```text
Get()
  │ lock mu
  ├── done? ──► return cached val, nil
  └── run init()
        ├── err != nil ─► unlock; return zero, ErrInitFailed(err)   (NOT cached)
        └── ok          ─► val = v; done = true; return val, nil
```

`Lazy` holds the result behind a `sync.Mutex`. The first `Get` runs `init`; concurrent callers block until it finishes and then observe the cached value. A successful value is latched (`done = true`) and never recomputed until `Reset`. A **failure is deliberately not latched**: `done` stays false so the next `Get` retries — the right behavior when init dials a flaky dependency. `Reset` clears `done` and the cached value so init runs again.

### Group: panic-safe, optionally-bounded errgroup

```text
Go(fn) / TryGo(fn)
  │ (limit set?) acquire semaphore slot   [Go blocks · TryGo fails fast]
  └── go:
        err = panix.SafeVoid("syncx.Group", fn(derivedCtx))
        ├── nil      ─► succeeded++
        ├── panic    ─► panicked++, failed++   (err is *panix.PanicError)
        └── error    ─► failed++
        once.Do: record first err; cancel(derivedCtx)   (siblings observe ctx.Done)
Wait()
  └── wg.Wait(); cancel(); return first err
```

Every task runs under `panix.SafeVoid`, which converts a panic into a `*panix.PanicError` instead of unwinding the stack and crashing the process. The first non-nil error (or recovered panic) is captured exactly once via `sync.Once`, and the derived context is cancelled so well-behaved siblings can abort early. `WithLimit(n)` installs a buffered-channel semaphore: `Go` blocks for a slot; `TryGo` takes one only if immediately available. `Stats` exposes atomic counters for observability.

### Map: typed sync.Map with a maintained length

```text
Store(k,v):  Swap → if !loaded { len++ }
Delete(k):   LoadAndDelete → if loaded { len-- }
LoadOrStore: → if !loaded { len++ }
Len():       atomic.Int64.Load()   (O(1))
```

`Map` wraps `sync.Map` and adds compile-time `K`/`V` typing (no `any` casts in caller code). Length is maintained as a single `atomic.Int64`, incremented or decremented based on the `**loaded` result of each atomic `sync.Map` operation** — `Swap`, `LoadAndDelete`, and `LoadOrStore` each report definitively whether a prior entry existed, so the counter stays consistent under concurrent mutation without an extra mutex.

## Normative Contracts


| Contract                 | Guarantee                                                                                     |
| ------------------------ | --------------------------------------------------------------------------------------------- |
| `Lazy.Get` run-once      | `init` runs at most once per successful initialization; concurrent callers see the same value |
| `Lazy` failure semantics | A failing `init` is **not** cached — the next `Get` retries                                   |
| `Lazy` error wrapping    | A non-nil init error is always wrapped as `ErrInitFailed` (joins the cause)                   |
| `Group.Wait`             | Blocks until every launched task completes, then cancels the derived context                  |
| `Group` first error      | Returns the **first** non-nil error or `*panix.PanicError`; siblings are cancelled            |
| `Group` panic safety     | A panicking task never crashes the process; it becomes a `*panix.PanicError`                  |
| `Group.TryGo`            | Starts a task only if a concurrency slot is free; returns whether it started                  |
| `Map.Len`                | Equals the number of live entries; O(1); consistent under concurrent mutation                 |
| `Map` typing             | No runtime type assertions are exposed to the caller                                          |


## Quick Start

```go
package main

import (
	"context"
	"fmt"

	"github.com/aasyanov/urx/syncx"
)

func main() {
	// Lazy: open the DB once, on first use.
	lazyDB, err := syncx.NewLazy(func() (string, error) {
		return "db-handle", nil // openDB() in real code
	})
	if err != nil {
		panic(err)
	}
	db, err := lazyDB.Get()
	if err != nil {
		panic(err)
	}

	// Group: fan out work, bounded to 4 concurrent tasks.
	g, ctx := syncx.NewGroup(context.Background(), syncx.WithLimit(4))
	for i := range 10 {
		g.Go(func(ctx context.Context) error {
			return process(ctx, i)
		})
	}
	if err := g.Wait(); err != nil {
		fmt.Println("work failed:", err)
	}

	// Map: typed concurrent cache.
	cache := syncx.NewMap[string, int]()
	cache.Store("db", len(db))
	fmt.Println("entries:", cache.Len())
}

func process(ctx context.Context, id int) error { return nil }
```

## Usage Scenarios

### Lazy singleton with a fallible initializer

```go
var lazyClient, _ = syncx.NewLazy(func() (*Client, error) {
	return dialClient(os.Getenv("ENDPOINT")) // may fail; retried on next Get
})

func Client() (*Client, error) {
	c, err := lazyClient.Get()
	if errors.Is(err, syncx.ErrInitFailed) {
		// transient — Get will retry next call
	}
	return c, err
}
```

### Bounded fan-out with panic isolation

```go
g, ctx := syncx.NewGroup(parentCtx, syncx.WithLimit(8))
for _, url := range urls {
	g.Go(func(ctx context.Context) error {
		return fetch(ctx, url) // a panic here becomes a *panix.PanicError
	})
}
if err := g.Wait(); err != nil {
	var pe *panix.PanicError
	if errors.As(err, &pe) {
		log.Printf("task panicked: %v", pe.Value)
	}
}
log.Printf("stats: %+v", g.Stats())
```

### Best-effort scheduling with TryGo

```go
g, _ := syncx.NewGroup(ctx, syncx.WithLimit(maxWorkers))
for _, job := range jobs {
	if !g.TryGo(func(ctx context.Context) error { return run(ctx, job) }) {
		overflow = append(overflow, job) // queue is full; handle elsewhere
	}
}
_ = g.Wait()
```

### Type-safe concurrent map with length

```go
sessions := syncx.NewMap[string, *Session]()
sessions.Store(id, sess)

if s, ok := sessions.Load(id); ok {
	s.Touch()
}
metrics.Gauge("sessions.active", float64(sessions.Len()))

prev, evicted := sessions.LoadAndDelete(id)
_ = prev
_ = evicted
```

## API

### Lazy


| Symbol       | Signature                                                       | Description                                                    |
| ------------ | --------------------------------------------------------------- | -------------------------------------------------------------- |
| `NewLazy`    | `func NewLazy[T any](init func() (T, error)) (*Lazy[T], error)` | Create a lazy initializer; returns `ErrNilInit` if init is nil |
| `Lazy.Get`   | `func (l *Lazy[T]) Get() (T, error)`                            | Return cached value, running init once; failures are retried   |
| `Lazy.Done`  | `func (l *Lazy[T]) Done() bool`                                 | Report whether the value is initialized                        |
| `Lazy.Reset` | `func (l *Lazy[T]) Reset()`                                     | Discard the cached value so init runs again                    |


### Group


| Symbol        | Signature                                                                           | Description                                                       |
| ------------- | ----------------------------------------------------------------------------------- | ----------------------------------------------------------------- |
| `NewGroup`    | `func NewGroup(ctx context.Context, opts ...GroupOption) (*Group, context.Context)` | Create a group and its derived context                            |
| `Group.Go`    | `func (g *Group) Go(fn func(ctx context.Context) error)`                            | Launch a panic-safe task; blocks for a slot when limited          |
| `Group.TryGo` | `func (g *Group) TryGo(fn func(ctx context.Context) error) bool`                    | Launch a task only if a slot is free; reports whether it started  |
| `Group.Wait`  | `func (g *Group) Wait() error`                                                      | Wait for all tasks; return the first error and cancel the context |
| `Group.Stats` | `func (g *Group) Stats() GroupStats`                                                | Snapshot of started/succeeded/failed/panicked counters            |
| `WithLimit`   | `func WithLimit(n int) GroupOption`                                                 | Bound concurrency to n (≤0 means unlimited)                       |
| `GroupOption` | `type GroupOption func(*groupConfig)`                                               | Functional option for `NewGroup`                                  |
| `GroupStats`  | `struct{ Started, Succeeded, Failed, Panicked int64 }`                              | Task counter snapshot                                             |


### Map


| Symbol              | Signature                                                   | Description                          |
| ------------------- | ----------------------------------------------------------- | ------------------------------------ |
| `NewMap`            | `func NewMap[K comparable, V any]() *Map[K, V]`             | Create an empty typed concurrent map |
| `Map.Load`          | `func (m *Map[K, V]) Load(key K) (V, bool)`                 | Read a value                         |
| `Map.Store`         | `func (m *Map[K, V]) Store(key K, value V)`                 | Set a value                          |
| `Map.Swap`          | `func (m *Map[K, V]) Swap(key K, value V) (V, bool)`        | Set and return the previous value    |
| `Map.Delete`        | `func (m *Map[K, V]) Delete(key K)`                         | Remove a key                         |
| `Map.LoadAndDelete` | `func (m *Map[K, V]) LoadAndDelete(key K) (V, bool)`        | Remove and return a value            |
| `Map.LoadOrStore`   | `func (m *Map[K, V]) LoadOrStore(key K, value V) (V, bool)` | Get-or-set atomically                |
| `Map.Range`         | `func (m *Map[K, V]) Range(fn func(key K, value V) bool)`   | Iterate; stop when fn returns false  |
| `Map.Len`           | `func (m *Map[K, V]) Len() int`                             | O(1) entry count                     |
| `Map.Clear`         | `func (m *Map[K, V]) Clear()`                               | Remove all entries                   |


## Configuration


| Option         | Default   | Description                                                    |
| -------------- | --------- | -------------------------------------------------------------- |
| `WithLimit(n)` | unlimited | Maximum concurrent tasks in a `Group`; `n ≤ 0` means unlimited |


## Errors


| Error           | Condition                                                                   |
| --------------- | --------------------------------------------------------------------------- |
| `ErrInitFailed` | Returned (joined with the cause) by `Lazy.Get` when the init function fails |
| `ErrNilInit`    | Returned by `NewLazy` when the init function is nil                         |


A nil task function passed to `Group.Go` or `Group.TryGo` is ignored (no error, no goroutine).

## Pitfalls

> [!WARNING]
> `**Lazy` does not cache failures.** A failing `init` is retried on the next `Get`. This is intentional (transient I/O should be retryable) but means a permanently-broken init will run on every call. Add your own backoff in the init function if needed.

> [!WARNING]
> **A `Group` must not be reused after `Wait`.** The derived context is cancelled by `Wait`, so tasks launched afterward run with an already-cancelled context. Create a fresh `Group` per fan-out.

> [!WARNING]
> `**Map.Range` is not a snapshot.** Like `sync.Map.Range`, it may observe concurrent insertions or deletions mid-iteration. `Len` taken before `Range` may differ from the number of entries visited.

> [!NOTE]
> `**Map` is read-optimized.** It inherits `sync.Map`'s trade-offs: excellent for read-mostly or disjoint-key workloads, but a `map` guarded by a `sync.Mutex` can be faster for write-heavy shared keys.

## Safety and Concurrency

All three types are safe for concurrent use. `Lazy` serializes through a `sync.Mutex`; `Get` and `Reset` may race freely. `Group` uses a `sync.WaitGroup`, a `sync.Once` for first-error capture, an optional buffered-channel semaphore, and `atomic.Int64` counters; the derived context propagates cancellation to all tasks. `Map` delegates storage to `sync.Map` and maintains its length with a single `atomic.Int64`. Every test runs under `-race`.

## Benchmarks

> CPU: Intel i7-10510U · OS: Windows 10 · Go 1.26 · `-benchmem -count=1`


| Benchmark         | ns/op | B/op | allocs/op |
| ----------------- | ----- | ---- | --------- |
| Lazy_Get          | 15.3  | 0    | 0         |
| Lazy_Get_Parallel | 56.2  | 0    | 0         |
| Map_Load_Hit      | 17.1  | 0    | 0         |
| Map_Load_Miss     | 11.6  | 0    | 0         |
| Map_Store         | 74.1  | 48   | 1         |
| Map_LoadOrStore   | 21.1  | 0    | 0         |
| Map_Load_Parallel | 5.0   | 0    | 0         |
| Group_Go          | 1267  | 240  | 5         |
| Group_Go_Limited  | 4106  | 424  | 9         |


### Analysis

- **Lazy_Get**: 0 allocs — the hot path is a `sync.Mutex` lock plus a `done` check; the value is returned by copy from a struct field, with no heap escape. The parallel variant (56 ns) is dominated by mutex contention since every `Get` takes the same lock; for read-heavy singletons after warm-up an `atomic.Pointer` fast path could shave this, but the simple mutex keeps `Get`/`Reset` trivially correct.
- **Map_Load**: 0 allocs and 5 ns under parallelism — reads hit `sync.Map`'s read-only path with no lock, which is why the parallel benchmark is *faster* than serial (per-CPU cache locality, no shared counter touched on reads).
- **Map_Store**: 1 alloc / 48 B is the architectural floor. `sync.Map.Swap` boxes the value into an `any` interface; for a non-pointer `V` this is an unavoidable heap allocation in the standard library's design. Storing pointer values (`*T`) removes the per-store box. The atomic length counter adds no allocations.
- **Group_Go**: 5 allocs covers `context.WithCancel` (the derived context + cancel closure), the goroutine, and the `panix.SafeVoid` deferred-recover frame. This is per-*group*, not per-request, so it is a warm-path cost. The limited variant adds the semaphore channel and per-task slot bookkeeping (9 allocs for 4 tasks).
- **Allocation floor**: `Lazy` and `Map` reads are genuinely 0-alloc. `Map.Store` and `Group` allocations are dictated by `sync.Map` interface boxing and `context` machinery respectively — neither is reducible without changing semantics.

## Quality


| Metric         | Value                          |
| -------------- | ------------------------------ |
| Test functions | 30                             |
| Benchmarks     | 9                              |
| Fuzz targets   | 1                              |
| Examples       | 4                              |
| Coverage       | 100.0%                         |
| Race detector  | All pass                       |
| External deps  | 0 (panix; testify in dev only) |


## File Structure

```text
syncx/
├── syncx.go            # package doc
├── lazy.go             # Lazy[T] — typed run-once initializer
├── group.go            # Group — panic-safe error group
├── map.go              # Map[K,V] — typed concurrent map
├── options.go          # GroupOption, WithLimit, defaults
├── types.go            # GroupStats, isPanic helper
├── errors.go           # ErrInitFailed, ErrNilInit
├── syncx_test.go       # unit + table-driven tests
├── bench_test.go       # benchmarks
├── fuzz_test.go        # FuzzMap — operation-sequence invariant
├── example_test.go     # runnable GoDoc examples
├── footprint_test.go   # struct size guards
└── README.md           # this file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.