# syncx

Generic concurrency primitives for industrial Go services.

## Philosophy

**Three primitives, one package.** `syncx` provides `Lazy[T]` (thread-safe
lazy initializer with error handling), `Group` (error-group with `panix.Safe`
recovery and optional concurrency limiting), and `Map[K, V]` (generic
concurrent map). Each is minimal and composable.

## Quick start

```go
// Lazy initializer
lazy := syncx.NewLazy(func() (*sql.DB, error) { return sql.Open("postgres", dsn) })
db, err := lazy.Get()

// Error group with concurrency limit
g, ctx := syncx.NewGroup(parentCtx, syncx.WithLimit(10))
for _, url := range urls {
    url := url
    g.Go(func(ctx context.Context) error { return fetch(ctx, url) })
}
if err := g.Wait(); err != nil {
    log.Error("group failed", "error", err)
}

// Generic concurrent map
m := syncx.NewMap[string, int]()
m.Store("requests", 42)
v, ok := m.Load("requests")
```

## API

### Lazy

| Function / Method | Description |
|---|---|
| `NewLazy[T](init) *Lazy[T]` | Create a lazy initializer |
| `l.Get() (T, error)` | Return cached value; runs init on first call |
| `l.Reset()` | Allow init to run again on next Get |

### Group

| Function / Method | Description |
|---|---|
| `NewGroup(ctx, opts...) (*Group, context.Context)` | Create error-group with derived context |
| `g.Go(fn)` | Launch fn in goroutine with `panix.Safe` recovery |
| `g.Wait() error` | Wait for all goroutines; return first error |

### Map

| Function / Method | Description |
|---|---|
| `NewMap[K, V]() *Map[K, V]` | Create an empty concurrent map |
| `m.Load(key) (V, bool)` | Load value for key |
| `m.Store(key, value)` | Store value for key |
| `m.Delete(key)` | Remove entry |
| `m.LoadOrStore(key, value) (V, bool)` | Load or store atomically |
| `m.Range(fn)` | Iterate over entries |
| `m.Len() int` | Number of entries |

### Options

| Option | Default | Description |
|---|---|---|
| `WithLimit(n)` | unlimited | Max concurrent goroutines in Group |

## Behavior details

- **Lazy initialization**: `Get` uses an atomic fast-path with `sync.Mutex`
  fallback. The first call runs the init function; subsequent calls return the
  cached result via atomic check (no lock on the hot path). If init fails, the
  error is cached and returned on every `Get`. `Reset` clears the cache under
  the mutex, allowing init to run again. `Get` and `Reset` are safe to call
  concurrently from different goroutines.

- **Group panic recovery**: every `Go` call wraps the function with
  `panix.Safe`. If the function panics, the panic is recovered as a structured
  `*errx.Error` and reported as the group error.

- **Concurrency limit**: `WithLimit(n)` creates a semaphore channel. `Go`
  blocks until a slot is available, enforcing at most `n` concurrent goroutines.

- **Map type safety**: `Map[K, V]` wraps `sync.Map` with generics. `Len` is
  maintained via an `atomic.Int64` counter that is incremented on
  `Store`/`LoadOrStore` (new keys only) and decremented on `Delete` — O(1) with
  no locking overhead.

## Error diagnostics

All errors are `*errx.Error` with `Domain = "SYNC"`.

### Codes

| Code | When |
|---|---|
| `INIT_FAILED` | Lazy initializer returned an error |

### Example

```text
SYNC.INIT_FAILED: lazy init failed | cause: connection refused
```

## Thread safety

- `Lazy.Get` uses atomic load + `sync.Mutex` fallback — concurrent calls block until init completes; concurrent `Get`/`Reset` is safe
- `Lazy.Reset` uses `sync.Mutex` — safe for concurrent use
- `Group.Go` / `Group.Wait` use `sync.WaitGroup` + `sync.Once` for first-error capture — fully concurrent
- `Map` wraps `sync.Map` — fully concurrent

## Tests

**17 tests, 100% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/syncx  coverage: 100% of statements
```

Coverage includes:
- Lazy: success, error, concurrent Get, Reset
- Group: single task, multiple tasks, first-error wins, panic recovery, limit
- Map: Store/Load, Delete, LoadOrStore, Range, Len, concurrent access

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz.
Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkLazy_Get           ~3 ns/op       0 B/op     0 allocs/op
BenchmarkGroup_Go        ~1492 ns/op     208 B/op     5 allocs/op
BenchmarkMap_StoreLoad    ~136 ns/op      48 B/op     1 allocs/op
```

### Analysis

**Lazy Get (cached):** ~3 ns, 0 allocs. After initialization, `Get` is a single `sync.Once.Do` fast path (atomic load). Effectively free.

**Group Go:** ~1.5 us, 5 allocs. Goroutine spawn + `panix.Safe` closure + `WaitGroup` add/done. In production, the function body dominates.

**Map Store+Load:** ~136 ns, 1 alloc. `sync.Map` internal allocation for the entry. Comparable to raw `sync.Map`.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| Lazy Get | < 4 ns, 0 allocs | Free after init |
| Group Go | < 1.5 us, 5 allocs | Goroutine spawn cost |
| Map Store+Load | < 150 ns, 1 alloc | sync.Map performance |

## What syncx does NOT do

| Concern | Owner |
|---------|-------|
| Task queuing | `poolx.WorkerPool` |
| Retry on failure | `retryx` |
| Timeout enforcement | `toutx` |
| Rate limiting | `ratex` |
| Logging | caller / `slog` |

## File structure

```text
pkg/syncx/
    syncx.go      -- package doc
    lazy.go       -- Lazy[T], NewLazy(), Get(), Reset()
    group.go      -- Group, NewGroup(), Go(), Wait()
    map.go        -- Map[K,V], NewMap(), Load(), Store(), Delete()
    errors.go     -- DomainSync, Code constants, error constructors
    syncx_test.go -- 17 tests, 100% coverage
    bench_test.go -- 3 benchmarks
    README.md
```
