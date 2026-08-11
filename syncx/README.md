# syncx — Typed Lazy Init, Panic-Safe Error Group, Generic Concurrent Map

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/syncx)
[License: MIT](../LICENSE)

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
✅ Lazy[T]  — run-once typed init with error handling, panic recovery, and reset
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
└──────────────┬─────────────────────────┬───────────────────┘
               │                         │
┌──────────────▼─────────────────────────▼───────────────────┐
│  panix.Safe / SafeVoid  (Lazy init + Group tasks)          │
│  (panic → PanicError)                                      │
└─────────────────────────────┬──────────────────────────────┘
                              │
               ┌──────────────▼────────────────────┐
               │  sync · sync/atomic · context     │
               └───────────────────────────────────┘
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
 val T               sem chan struct{}   mu sync.Mutex (len/Clear)
 done bool           wg / once           Store/Load/Delete
   │                 started/succeeded/  Swap/LoadAndDelete
 Get / Done /        failed/panicked     LoadOrStore/Range
 Reset               (atomic.Int64)      Len/Clear
   │                   │
 panix.Safe          Go / TryGo / Wait / Stats
 (panic recovery)        │
                       panix.SafeVoid (panic recovery)
```



## How It Works



### Lazy: double-checked run-once with retry-on-failure

```text
Get()
  │ lock mu
  ├── done? ──► return cached val, nil
  └── panix.Safe("syncx.Lazy", init)
        ├── panic    ─► return *panix.PanicError (NOT cached)
        ├── err != nil ─► return zero, ErrInitFailed(err)   (NOT cached)
        └── ok       ─► val = v; done = true; return val, nil
```

`Lazy` holds the result behind a `sync.Mutex`. The first `Get` runs `init` under `panix.Safe`; concurrent callers block until it finishes and then observe the cached value. A successful value is latched (`done = true`) and never recomputed until `Reset`. **Failures and panics are deliberately not latched**: `done` stays false so the next `Get` retries — the right behavior when init dials a flaky dependency or hits transient third-party panics. `Reset` clears `done` and the cached value so init runs again.

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
Store/Delete/Swap/LoadOrStore/LoadAndDelete/CompareAndSwap/CompareAndDelete:
  lock mu → sync.Map op → adjust len when a new key appears or one is removed
  (CompareAndSwap updates existing entries only — no len change)
Clear:
  lock mu → sync.Map.Clear → len = 0
Load / Range / Len:
  no mu — reads use sync.Map and atomic len snapshot
```

`Map` wraps `sync.Map` and adds compile-time `K`/`V` typing (no `any` casts in caller code). Length is maintained as an `atomic.Int64`, updated only while `mu` is held during mutating operations so `Len` stays consistent with live entries even when `Clear` runs concurrently with `Store` or `Delete`. Reads (`Load`, `Range`, `Len`) stay lock-free aside from the atomic counter load.

## Normative Contracts


| Contract                          | Guarantee                                                                                                             |
| --------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| `Lazy.Get` run-once               | `init` runs at most once per successful initialization; concurrent callers see the same value                         |
| `Lazy` failure semantics          | A failing `init` is **not** cached — the next `Get` retries                                                           |
| `Lazy` panic safety               | A panicking `init` returns `*panix.PanicError`; it is **not** cached — the next `Get` retries                         |
| `Lazy` error wrapping             | A non-nil init error is always wrapped as `ErrInitFailed` (joins the cause)                                           |
| `Group.Wait`                      | Blocks until every launched task completes, then cancels the derived context                                          |
| `Group` first error               | Returns the **first** non-nil error or `*panix.PanicError`; siblings are cancelled                                    |
| `Group` panic safety              | A panicking task never crashes the process; it becomes a `*panix.PanicError`                                          |
| `Group.TryGo`                     | Starts a task only if a concurrency slot is free; returns (started, err)                                              |
| `Group.Go` / `Group.TryGo` nil fn | Return [ErrNilFunc] synchronously; no goroutine is launched                                                           |
| `Map.Len`                         | O(1) snapshot; matches live entries once each mutation completes; may briefly lag [Map.Load] during concurrent writes |
| `Map` typing                      | No runtime type assertions are exposed to the caller                                                                  |




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
		if err := g.Go(func(ctx context.Context) error {
			return process(ctx, i)
		}); err != nil {
			panic(err)
		}
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
	if err := g.Go(func(ctx context.Context) error {
		return fetch(ctx, url) // a panic here becomes a *panix.PanicError
	}); err != nil {
		return err
	}
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
	ok, err := g.TryGo(func(ctx context.Context) error { return run(ctx, job) })
	if err != nil {
		return err
	}
	if !ok {
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


| Symbol       | Signature                                                       | Description                                                           |
| ------------ | --------------------------------------------------------------- | --------------------------------------------------------------------- |
| `NewLazy`    | `func NewLazy[T any](init func() (T, error)) (*Lazy[T], error)` | Create a lazy initializer; returns `ErrNilInit` if init is nil        |
| `Lazy.Get`   | `func (l *Lazy[T]) Get() (T, error)`                            | Return cached value, running init once; errors and panics are retried |
| `Lazy.Done`  | `func (l *Lazy[T]) Done() bool`                                 | Report whether the value is initialized                               |
| `Lazy.Reset` | `func (l *Lazy[T]) Reset()`                                     | Discard the cached value so init runs again                           |




### Group


| Symbol        | Signature                                                                           | Description                                                       |
| ------------- | ----------------------------------------------------------------------------------- | ----------------------------------------------------------------- |
| `NewGroup`    | `func NewGroup(ctx context.Context, opts ...GroupOption) (*Group, context.Context)` | Create a group and its derived context; nil parent → `Background` |
| `Group.Go`    | `func (g *Group) Go(fn func(ctx context.Context) error) error`                      | Launch a panic-safe task; returns `ErrNilFunc` when fn is nil     |
| `Group.TryGo` | `func (g *Group) TryGo(fn func(ctx context.Context) error) (bool, error)`           | Launch when a slot is free; `(false, ErrNilFunc)` when fn is nil  |
| `Group.Wait`  | `func (g *Group) Wait() error`                                                      | Wait for all tasks; return the first error and cancel the context |
| `Group.Stats` | `func (g *Group) Stats() GroupStats`                                                | Snapshot of started/succeeded/failed/panicked counters            |
| `WithLimit`   | `func WithLimit(n int) GroupOption`                                                 | Bound concurrency to n (≤0 means unlimited)                       |
| `GroupOption` | `type GroupOption func(*groupConfig)`                                               | Functional option for `NewGroup`                                  |
| `GroupStats`  | `struct{ Started, Succeeded, Failed, Panicked int64 }`                              | Task counter snapshot                                             |




### Map


| Symbol                 | Signature                                                    | Description                          |
| ---------------------- | ------------------------------------------------------------ | ------------------------------------ |
| `NewMap`               | `func NewMap[K comparable, V any]() *Map[K, V]`              | Create an empty typed concurrent map |
| `Map.Load`             | `func (m *Map[K, V]) Load(key K) (V, bool)`                  | Read a value                         |
| `Map.Store`            | `func (m *Map[K, V]) Store(key K, value V)`                  | Set a value                          |
| `Map.Swap`             | `func (m *Map[K, V]) Swap(key K, value V) (V, bool)`         | Set and return the previous value    |
| `Map.Delete`           | `func (m *Map[K, V]) Delete(key K)`                          | Remove a key                         |
| `Map.LoadAndDelete`    | `func (m *Map[K, V]) LoadAndDelete(key K) (V, bool)`         | Remove and return a value            |
| `Map.LoadOrStore`      | `func (m *Map[K, V]) LoadOrStore(key K, value V) (V, bool)`  | Get-or-set atomically                |
| `Map.CompareAndSwap`   | `func (m *Map[K, V]) CompareAndSwap(key K, old, new V) bool` | Swap when old matches; no insert     |
| `Map.CompareAndDelete` | `func (m *Map[K, V]) CompareAndDelete(key K, old V) bool`    | Delete when old matches              |
| `Map.Range`            | `func (m *Map[K, V]) Range(fn func(key K, value V) bool)`    | Iterate; stop when fn returns false  |
| `Map.Len`              | `func (m *Map[K, V]) Len() int`                              | O(1) entry count                     |
| `Map.Clear`            | `func (m *Map[K, V]) Clear()`                                | Remove all entries                   |




## Configuration


| Option         | Default   | Description                                                    |
| -------------- | --------- | -------------------------------------------------------------- |
| `WithLimit(n)` | unlimited | Maximum concurrent tasks in a `Group`; `n ≤ 0` means unlimited |




## Errors


| Error           | Condition                                                                   |
| --------------- | --------------------------------------------------------------------------- |
| `ErrInitFailed` | Returned (joined with the cause) by `Lazy.Get` when the init function fails |
| `ErrNilInit`    | Returned by `NewLazy` when the init function is nil                         |
| `ErrNilFunc`    | Returned by `Group.Go` and `Group.TryGo` when the task function is nil      |


A panicking `Lazy` init or `Group` task returns a `*panix.PanicError` (use `errors.As`). Neither is cached; `Lazy.Get` retries on the next call.

## Pitfalls

> [!WARNING]
> **Lazy** does not cache failures or panics.** A failing or panicking `init` is retried on the next `Get`. This is intentional (transient I/O should be retryable) but means a permanently-broken init will run on every call. Add your own backoff in the init function if needed.

> [!WARNING]
> **A** `Group` **must not be reused after** `Wait`**.** The derived context is cancelled by `Wait`, so tasks launched afterward run with an already-cancelled context. Create a fresh `Group` per fan-out.

> [!WARNING]
> **Map.Range** is not a snapshot.** Like `sync.Map.Range`, it may observe concurrent insertions or deletions mid-iteration. `Len` taken before `Range` may differ from the number of entries visited.

> [!NOTE]
> `Map.Len` **is an atomic snapshot.** It matches live entries once each mutating operation completes. A concurrent `Load` may observe an entry before `Len` increments because reads do not take the length mutex.

> [!NOTE]
> **Map** mutating operations take a short mutex for length accounting.** Reads stay on the `sync.Map` fast path; writes and `Clear` serialize only for `Len` consistency, not for the underlying map storage.

> [!NOTE]
> **Map** is read-optimized.** It inherits `sync.Map`'s trade-offs: excellent for read-mostly or disjoint-key workloads, but a `map` guarded by a `sync.Mutex` can be faster for write-heavy shared keys.



## Safety and Concurrency

All three types are safe for concurrent use. `Lazy` serializes through a `sync.Mutex`; init runs under `panix.Safe`; `Get` and `Reset` may race freely. `Group` uses a `sync.WaitGroup`, a `sync.Once` for first-error capture, an optional buffered-channel semaphore, and `atomic.Int64` counters; the derived context propagates parent cancellation and sibling failures to all tasks. `Map` delegates storage to `sync.Map`, maintains `Len` with an `atomic.Int64`, and serializes length updates (including `Clear`) through a `sync.Mutex` while leaving reads lock-free. Every test runs under `-race`.

## Benchmarks

Three environments, two hardware classes, two operating systems. All values are **medians**. `B/op` and `allocs/op` are deterministic — they depend on code, not hardware.

### Environments

| | Laptop | CI Server (Linux) | CI Server (Windows) |
|---|---|---|---|
| CPU | Intel Core i7-10510U, 4C/8T | Intel Xeon 6973P-C | AMD EPYC 7763 |
| TDP | 15W (mobile, throttles) | 280W (server, stable) | server, stable |
| OS | Windows 10 | Ubuntu | Windows Server 2022 |
| Go | 1.26.2 | 1.26 | 1.26 |
| GOMAXPROCS | 8 | 4 | 4 |
| Runs | 3 (`-count=3`) | 3 (`-count=3`) | 3 (`-count=3`) |

### Lazy[T]

| Benchmark | What it measures | Laptop | Linux | Windows | B/op | allocs/op |
|---|---|---|---|---|---|---|
| Lazy_Get | First / cached `Get` | 14.6 ns | 13.1 ns | **8.2 ns** | 0 | 0 |
| Lazy_Get_Parallel | `Get`, 8/4 goroutines | 54.2 ns | 54.7 ns | **23.8 ns** | 0 | 0 |

### Map[K,V]

| Benchmark | What it measures | Laptop | Linux | Windows | B/op | allocs/op |
|---|---|---|---|---|---|---|
| Map_Load_Hit | Typed load, key present | 16.0 ns | **8.5 ns** | 15.7 ns | 0 | 0 |
| Map_Load_Miss | Typed load, key absent | 10.9 ns | **6.0 ns** | 12.4 ns | 0 | 0 |
| Map_Load_Parallel | Read-only `sync.Map` path | 5.0 ns | **3.9 ns** | 6.8 ns | 0 | 0 |
| Map_LoadOrStore | First-store fast path | 32.8 ns | **20.1 ns** | 24.1 ns | 0 | 0 |
| Map_Store | Insert + length track | 77.2 ns | **51.2 ns** | 88.3 ns | 48 | 1 |
| Map_Store_Parallel | Concurrent stores | 350 ns | **251.5 ns** | 497.6 ns | 79 | 3 |
| Map_Delete | Remove + length dec | 139 ns | **94.1 ns** | 131.7 ns | 48 | 1 |
| Map_Clear | Drain all entries | 1.97 µs | **1.13 µs** | 2.07 µs | 1408 | 20 |

### Group

| Benchmark | What it measures | Laptop | Linux | Windows | B/op | allocs/op |
|---|---|---|---|---|---|---|
| Group_Go | One goroutine + errgroup | 1.19 µs | **564.6 ns** | 2.31 µs | 240 | 5 |
| Group_Go_Parallel | `Go` from parallel bench | 341 ns | **266.1 ns** | 617.8 ns | 240 | 5 |
| Group_Go_Limited | `Go` with semaphore limit | 3.59 µs | **1.51 µs** | 7.32 µs | 424 | 9 |
| Group_TryGo | Non-blocking `TryGo` | 1.20 µs | **575.8 ns** | 2.58 µs | 240 | 5 |

### Analysis

**Map reads are genuinely 0-alloc on all platforms.** `Map_Load_Parallel` at ~6 ns on CI is the `sync.Map` read-only fast path — no mutex, no interface boxing on cache hits. Serial `Map_Load_Hit` (~14 ns) adds typed key conversion overhead; both are far below earlier single-run laptop numbers that reflected scheduler noise, not steady-state cost.

**Map_Store — 1 alloc / 48 B is the `sync.Map` interface boxing floor.** The length-tracking mutex adds latency versus a raw `sync.Map` but keeps `Len`/`Clear` correct under concurrent mutation. `Map_Store_Parallel` (~360 ns Linux, 460 ns Windows) reflects write contention when every goroutine inserts a distinct key — **Windows ~1.3× slower** under parallel stores, consistent with heavier kernel mutex paths.

**Lazy_Get — 0 allocs, mutex on first init only.** Cached `Get` is ~8 ns on CI (lock + `done` check + return by copy). `Lazy_Get_Parallel` spreads to 17–54 ns because every goroutine hits the same mutex until initialization completes; after warmup, contention drops to the cached path.

**Group_Go — 5 allocs is per-group, not per-task.** Covers `context.WithCancel`, the goroutine, and the `panix.SafeVoid` recover frame. Linux CI (763 ns) is **2.5× faster** than Windows sequential (1.95 µs) — goroutine spawn + scheduler latency on Windows inflates the one-shot `Go` benchmark. `Group_Go_Parallel` (266.1 ns) amortizes that setup across concurrent iterations.

**Group_Go_Limited — 9 allocs, semaphore channel bookkeeping.** 1.5 µs (Linux) vs 7.3 µs (Windows) — the limited variant adds channel send/receive per task; Windows pays a larger penalty on the combined spawn + semaphore path.

**Map_Clear — bulk rebuild cost.** ~1.1 µs on Linux, 21 allocs from draining and re-inserting entries in the benchmark loop; not a per-key hot path.



## Quality


| Metric         | Value                                  |
| -------------- | -------------------------------------- |
| Test functions | 44                                     |
| Benchmarks     | 14                                     |
| Fuzz targets   | 3 (`FuzzMap`, `FuzzLazy`, `FuzzGroup`) |
| Examples       | 6                                      |
| Coverage       | 100.0%                                 |
| Race detector  | All pass (CI matrix)                   |
| External deps  | 0 (panix; testify in dev only)         |




## File Structure

```text
syncx/
├── syncx.go            # package doc
├── lazy.go             # Lazy[T] — typed run-once initializer
├── group.go            # Group — panic-safe error group
├── map.go              # Map[K,V] — typed concurrent map
├── options.go          # GroupOption, WithLimit, defaults
├── types.go            # GroupStats, isPanic helper
├── errors.go           # ErrInitFailed, ErrNilInit, ErrNilFunc
├── errors_test.go      # sentinel and wrapper tests
├── syncx_test.go       # unit + table-driven tests
├── bench_test.go       # benchmarks
├── fuzz_test.go        # FuzzMap, FuzzLazy, FuzzGroup
├── example_test.go     # runnable GoDoc examples
├── footprint_test.go   # struct size guards
└── README.md           # this file
```



## License

MIT — see [LICENSE](../LICENSE) in the repository root.