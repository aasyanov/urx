# fallx — Fallback Strategies for Graceful Degradation

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/fallx)
[License: MIT](../LICENSE)

A thread-safe fallback wrapper that turns a primary failure into a degraded-but-useful result. Three strategies — a static value, a fallback function, or a replayed cached success — plus a controller for branching on the primary error, a sharded LRU cache with TTL, and panic recovery. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **fallx degrades a single operation; it does not retry, time out, or trip open.** When the primary fails, fallx runs the fallback *once* and returns. It never re-invokes the primary, never bounds its duration, and never tracks failure rates. Compose it with `retryx` (retries), `toutx` (deadlines), and `circuitx` (failure tripping) — fallx is the last link that answers "and if all that still failed, what do we serve?".

## The Problem

A request hits a dependency that is down, slow, or rate-limited. The naive handler has exactly one move: return the error to the caller. That is rarely the best answer.

1. **A stale answer beats no answer.** A product page that renders yesterday's price is far more useful than a 500. A recommendations widget that shows last-known results degrades gracefully; one that errors out breaks the whole page.
2. **A cheap answer beats an expensive failure.** When the personalization service is down, serving a generic response is better than failing the request — but the handler has no structured place to decide that, so the degraded path gets bolted on with ad-hoc `if err != nil` branches that drift out of sync.
3. **The degraded path needs context.** To choose a sensible fallback the code must know *why* the primary failed and *which* request this is. Passing that through by hand is verbose and error-prone, and panics in the fallback path crash the process just like any other.

`fallx` gives the degraded path a first-class home: pick a strategy once, hand the callback a controller that carries the primary error and the request key, cache successes automatically when that is the right fallback, and recover panics on both paths.

## Architectural Position

```text
✅ Fallback[T]        — wrap a primary op with one degradation strategy
✅ Execute[T]         — run primary; on failure, produce a fallback result
✅ StrategyStatic     — return a fixed value (cannot fail)
✅ StrategyFunc       — run a fallback function with the primary error
✅ StrategyCached     — replay the last success per key (sharded LRU + TTL)
✅ FallController     — strategy, key, on-fallback flag, primary error
✅ panic safety       — a panicking callback becomes a *panix.PanicError

❌ NOT a retrier      — it runs the primary once, never re-invokes it (see retryx)
❌ NOT a timeout      — it does not bound the primary's duration (see toutx)
❌ NOT a circuit breaker — it does not trip open on failure rate (see circuitx)
❌ NOT a general cache — the cache exists only to feed StrategyCached fallbacks (see lrux)
```

### Position in the urx Stack

```text
┌──────────────────────────────────────────────────────────┐
│  service code: HTTP/RPC handlers, job workers            │
└────────────────────────┬─────────────────────────────────┘
                         │
┌────────────────────────▼─────────────────────────────────┐
│  fallx   Fallback[T] · Execute[T] · FallController       │
│          run primary, degrade on failure                 │
└──────────────┬────────────────────────┬──────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼──────────────────┐
│  panix.Safe            │   │  sharded LRU cache          │
│  (panic → PanicError)  │   │  (container/heap + atomics) │
└────────────────────────┘   └─────────────────────────────┘
```

## Architecture

```text
                            fallx
   ┌───────────────────┬───────────────────┬───────────────────┐
   │                   │                   │                   │
 Fallback[T]          Option[T]            FallController
 (fallx.go)           (options.go)         (types.go)
   │                  config{strategy,     execution{strategy,
 atomic counters:      staticValue,fn,      key,err,onFallback}
 calls/primary/        keyFn,cacheTTL,      Strategy/Key/
 fallback/cache*       maxCacheSize,...}    OnFallback/Error
   │                   │                   │
 Execute[T] /          WithStatic           Strategy (types.go)
 ExecuteWithKey        WithFunc              Static/Func/Cached
   │                   WithCached            │
 fallback(strategy)    WithKeyFunc           errors.go
   │                   WithShards            ErrNoFunc/ErrNoCached
 panix.Safe(callback)  WithOnFallback/WithOp ErrClosed/ErrNilFunc
   │                                         ErrFallbackFailed
 sharded LRU cache (cache.go)
 cacheShard{map,lruHeap} · evictIfNeeded · cleanupLoop
```

## How It Works

```text
Execute(f, ctx, primaryFn)            ExecuteWithKey(f, ctx, key, primaryFn)
  │ closed ? ──────────────────────────────────────► ErrClosed
  │ primaryFn == nil ? ─────────────────────────────► ErrNilFunc
  │ key = WithKeyFunc(ctx) or DefaultKey (Execute only)
  │
  ├── fc = {strategy, key, onFallback:false}
  ├── (val,err) = panix.Safe(op, primaryFn(ctx, fc))
  │
  ├── err == nil ?
  │     ├── primarySuccess++
  │     ├── strategy == Cached ? cache[key] = val (TTL)
  │     └── return val, nil
  │
  └── err != nil  → fallback(ctx, fc, err):
        fallbackUsed++ ; WithOnFallback(err, strategy)
        fc.onFallback = true ; fc.err = err
        switch strategy:
          Static : return staticValue, nil           (always succeeds)
          Func   : fn == nil ? ErrNoFunc
                   (v,e) = panix.Safe(fn(ctx, fc))
                   e != nil ? ErrFallbackFailed(e) : return v
          Cached : cache[key] live ? return it (hit)
                   else ErrNoCached(key) (miss)
```

The primary callback always runs first, under `panix.Safe`, so a panicking primary is captured as a `*panix.PanicError` and treated as an ordinary primary failure — the fallback path still runs. On success the value is returned; under `StrategyCached` it is also written to the cache so the *next* failure for that key has something to replay.

On failure the configured strategy decides the degraded result. `StrategyStatic` can never itself fail. `StrategyFunc` runs the fallback function — also under `panix.Safe` — with the *same* controller, now flipped so `OnFallback()` reports true and `Error()` returns the primary failure; one closure can therefore serve both paths and branch on `OnFallback()`. `StrategyCached` looks up the request key and replays the last live success, or returns `ErrNoCached` on a miss.

### The cache (StrategyCached)

Cached successes live in a **sharded LRU cache with per-entry TTL**. Each key is mapped to one of N shards (inline FNV-1a, default 16) so concurrent callers caching different keys rarely contend on the same lock. Within a shard, a min-heap ordered by last-access time (`container/heap`) makes the coldest entry available in O(log n). When the cache exceeds its capacity, eviction first sweeps expired entries across all shards, then removes the globally coldest live entry until the size is back within bounds. A background goroutine sweeps expired entries twice per TTL; `Close` stops it. The lookup path also lazily evicts an entry it finds expired, so a stale value is never replayed even between sweeps.

## Normative Contracts


| Contract                 | Guarantee                                                                                                                                    |
| ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------- |
| Primary runs once        | The primary callback is invoked exactly once per `Execute`; fallx never retries it                                                           |
| Fallback on failure only | The fallback path runs only when the primary returns a non-nil error (or panics)                                                             |
| Static never fails       | `StrategyStatic` always returns its value with a nil error                                                                                   |
| Controller scope         | A `FallController` is valid only during its callbacks; do not retain it                                                                      |
| Controller threading     | The fallback function sees `OnFallback()==true` and `Error()==primary error`; the primary sees both falsey/nil                               |
| Cache freshness          | `StrategyCached` never replays an entry past its TTL — an expired entry is a miss                                                            |
| Capacity bound           | The live cache size never exceeds the configured `maxSize` after eviction settles                                                            |
| Panic safety             | A panicking primary becomes a `*panix.PanicError` treated as a primary failure; a panicking fallback surfaces wrapped in `ErrFallbackFailed` |
| Context first            | The caller's context is passed unchanged to both callbacks; fallx adds no deadline of its own                                                |
| Close semantics          | After `Close`, `Execute`/`ExecuteWithKey` return `ErrClosed`; `Seed`/`SeedWithTTL`/`ClearCache` are no-ops; cached entries remain readable via `Stats` |
| Idempotent close         | `Close` is safe to call repeatedly and always returns nil                                                                                    |


## Quick Start

```go
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/aasyanov/urx/fallx"
)

func main() {
	fb := fallx.New(fallx.WithStatic("service unavailable"))
	defer fb.Close()

	val, err := fallx.Execute(fb, context.Background(),
		func(ctx context.Context, fc fallx.FallController) (string, error) {
			return callAPI(ctx)
		})

	switch {
	case errors.Is(err, fallx.ErrClosed):
		fmt.Println("closed:", err)
	case err != nil:
		fmt.Println("failed:", err)
	default:
		fmt.Println("ok:", val) // "service unavailable" if callAPI failed
	}
}

func callAPI(context.Context) (string, error) { return "", errors.New("down") }
```

## Usage Scenarios

### Static default under failure

```go
fb := fallx.New(fallx.WithStatic(Recommendations{}))

recs, _ := fallx.Execute(fb, ctx,
	func(ctx context.Context, _ fallx.FallController) (Recommendations, error) {
		return recoService.Get(ctx, userID) // empty slice on failure — page still renders
	})
```

### Computed fallback that inspects the cause

```go
fb := fallx.New(fallx.WithFunc(
	func(ctx context.Context, fc fallx.FallController) (Price, error) {
		if errors.Is(fc.Error(), context.DeadlineExceeded) {
			return lastKnownPrice(ctx) // slow upstream: serve cached price
		}
		return Price{}, fc.Error()      // hard error: propagate it
	}))

price, err := fallx.Execute(fb, ctx,
	func(ctx context.Context, _ fallx.FallController) (Price, error) {
		return pricing.Quote(ctx, sku)
	})
```

### Replay the last good response per user

```go
fb := fallx.New(
	fallx.WithCached[Profile](5*time.Minute, 10_000),
)
defer fb.Close()

profile, err := fallx.ExecuteWithKey(fb, ctx, userID,
	func(ctx context.Context, _ fallx.FallController) (Profile, error) {
		return profiles.Load(ctx, userID) // success cached; failure replays last success
	})
if errors.Is(err, fallx.ErrNoCached) {
	// first-ever call for this user also failed — nothing to replay
}
```

### One closure for both paths

```go
fb := fallx.New(fallx.WithFunc(serve))

func serve(ctx context.Context, fc fallx.FallController) (Page, error) {
	if fc.OnFallback() {
		return renderCachedPage(ctx) // degraded: skip personalization
	}
	return renderFullPage(ctx)       // primary
}

page, _ := fallx.Execute(fb, ctx, serve)
```

### Compose: retry, then fall back

```go
fb := fallx.New(fallx.WithStatic(defaultConfig))

cfg, _ := fallx.Execute(fb, ctx,
	func(ctx context.Context, _ fallx.FallController) (Config, error) {
		return retryx.Do(ctx, func(ctx context.Context, rc retryx.RetryController) (Config, error) {
			return loadRemoteConfig(ctx)
		}, retryx.WithMaxAttempts(3))
	})
// retryx exhausts its attempts → fallx serves defaultConfig
```

## API


| Symbol                 | Signature                                                                                                          | Description                               |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------ | ----------------------------------------- |
| `New`                  | `func New[T any](opts ...Option[T]) *Fallback[T]`                                                                  | Create a fallback with defaults + options |
| `Execute`              | `func Execute[T any](f *Fallback[T], ctx context.Context, primaryFn PrimaryFunc[T]) (T, error)`                    | Run primary; degrade on failure           |
| `ExecuteWithKey`       | `func ExecuteWithKey[T any](f *Fallback[T], ctx context.Context, key string, primaryFn PrimaryFunc[T]) (T, error)` | Execute with an explicit cache key        |
| `Fallback.Strategy`    | `func (f *Fallback[T]) Strategy() Strategy`                                                                        | Configured strategy                       |
| `Fallback.Seed`        | `func (f *Fallback[T]) Seed(key string, value T)`                                                                  | Warm the cache with a value (default TTL); no-op after `Close` |
| `Fallback.SeedWithTTL` | `func (f *Fallback[T]) SeedWithTTL(key string, value T, ttl time.Duration)`                                        | Warm the cache with an explicit TTL; no-op after `Close`       |
| `Fallback.ClearCache`  | `func (f *Fallback[T]) ClearCache()`                                                                               | Remove all cached entries; no-op after `Close`                 |
| `Fallback.Stats`       | `func (f *Fallback[T]) Stats() Stats`                                                                              | Counter snapshot                          |
| `Fallback.ResetStats`  | `func (f *Fallback[T]) ResetStats()`                                                                               | Zero cumulative counters (keeps cache)    |
| `Fallback.Close`       | `func (f *Fallback[T]) Close() error`                                                                              | Idempotent shutdown; stops cleanup        |
| `Fallback.IsClosed`    | `func (f *Fallback[T]) IsClosed() bool`                                                                            | Report closed state                       |
| `Strategy`             | `type Strategy uint8`                                                                                              | Static / Func / Cached                    |
| `PrimaryFunc[T]`       | `func(context.Context, FallController) (T, error)`                                                                 | Primary unit of work                      |
| `FallbackFunc[T]`      | `func(context.Context, FallController) (T, error)`                                                                 | Fallback unit of work (WithFunc)          |


### FallController


| Method       | Signature             | Description                                  |
| ------------ | --------------------- | -------------------------------------------- |
| `Strategy`   | `Strategy() Strategy` | Strategy the fallback was built with         |
| `Key`        | `Key() string`        | Cache key resolved for this call             |
| `OnFallback` | `OnFallback() bool`   | True only on the fallback path               |
| `Error`      | `Error() error`       | Primary error on the fallback path, else nil |


## Configuration


| Option                     | Default                                              | Description                                              |
| -------------------------- | ---------------------------------------------------- | -------------------------------------------------------- |
| `WithStatic(v)`            | strategy + zero value                                | Select `StrategyStatic`; return `v` on failure           |
| `WithFunc(fn)`             | —                                                    | Select `StrategyFunc`; run `fn` on failure (nil ignored) |
| `WithCached(ttl, maxSize)` | `DefaultCacheTTL` (5m), `DefaultMaxCacheSize` (1000) | Select `StrategyCached`; non-positive args use defaults  |
| `WithKeyFunc(fn)`          | `DefaultKey` ("default")                             | Derive the cache key from context (Cached only)          |
| `WithShards(n)`            | `DefaultShards` (16)                                 | Cache shard count; < 1 ignored, capped at capacity       |
| `WithOnFallback(fn)`       | —                                                    | Hook fired on every fallback (err, strategy)             |
| `WithOp(s)`                | `"fallx.Execute"` / `"fallx.Fallback"`               | Operation name in panic reports; empty ignored           |


## Errors


| Error               | Condition                                                                           |
| ------------------- | ----------------------------------------------------------------------------------- |
| `ErrNilFunc`        | `Execute`/`ExecuteWithKey` was given a nil primary function                         |
| `ErrClosed`         | The fallback has been closed                                                        |
| `ErrNoFunc`         | `StrategyFunc` is active but no fallback function was configured                    |
| `ErrNoCached`       | `StrategyCached` failed and no live cached value exists for the key (wraps the key) |
| `ErrFallbackFailed` | The `WithFunc` fallback returned an error or panicked (joins the cause)             |


A panicking callback surfaces as a `*panix.PanicError`: a primary panic is handled as a primary failure (triggering the fallback); a fallback panic is wrapped in `ErrFallbackFailed`. Reach the `*panix.PanicError` with `errors.As`.

## Pitfalls

> [!WARNING]
> **StrategyCached** only has something to replay after a success.** The very first call for a key — or the first after the entry expired — has an empty cache, so a failure there returns `ErrNoCached`. Use `Seed`/`SeedWithTTL` to warm critical keys, or pair cached with a static default in a composed fallback.

> [!WARNING]
> **The cache key defaults to a single shared slot.** Without `WithKeyFunc` or `ExecuteWithKey`, every call uses `DefaultKey`, so all callers share one cached value. That is correct for a global resource (a feature-flag blob) but wrong for per-entity data — supply a key there or one user will replay another's response.

> [!NOTE]
> **A panicking primary is not an error you can see.** It is converted to a `*panix.PanicError` and treated as a primary failure, so the fallback runs and (for `StrategyStatic`/`StrategyFunc`) you may get a successful degraded result with the panic invisible to the caller. Use `WithOnFallback` or the controller's `Error()` to observe it.

> [!NOTE]
> **`Close` does not clear the cache.** It stops the cleanup goroutine and rejects new work (`Execute` returns `ErrClosed`; `Seed`/`ClearCache` become no-ops), but cached entries remain readable via `Stats().CacheSize`. Call `ClearCache` before `Close` if you need the memory released.

## Safety and Concurrency

`Fallback[T]` is safe for concurrent use from any number of goroutines. All counters live in `sync/atomic` values and the hot path takes no lock for `StrategyStatic`/`StrategyFunc`. The `StrategyCached` cache is partitioned into independently-locked shards (FNV-1a key hashing); concurrent callers caching distinct keys contend only when they hash to the same shard. Capacity eviction is serialized by a dedicated mutex so two callers cannot over-evict past the target, and the cache size counter is an atomic kept in step with the shard maps. The `FallController` is touched only by the single goroutine running its callback and needs no synchronization. Both callbacks run under `panix.Safe`. Every test runs under `-race`, including 64-goroutine cached-access and 32-goroutine static-fallback stress tests.

## Benchmarks

> CPU: Intel i7-10510U · OS: Windows 10 · Go 1.24 · `-benchmem -count=3` (best of 3)


| Benchmark                      | ns/op | B/op | allocs/op |
| ------------------------------ | ----- | ---- | --------- |
| Execute_StaticSuccess          | 52    | 48   | 1         |
| Execute_StaticSuccess_Parallel | 61    | 48   | 1         |
| Execute_StaticFallback         | 61    | 48   | 1         |
| Execute_FuncFallback           | 75    | 48   | 1         |
| Execute_CachedHit              | 116   | 48   | 1         |
| Execute_CachedHit_Parallel     | 215   | 48   | 1         |
| Execute_CachedStore            | 112   | 48   | 1         |


### Analysis

- **Allocation floor (1 alloc / 48 B everywhere)**: the single allocation is the `execution` controller, which escapes to the heap because it is handed to the callback through the `panix.Safe` closure as an interface value. Every path — success, static fallback, func fallback, cache hit, cache store — pays exactly this and nothing more; the counters are atomics, shard selection is an allocation-free inline FNV-1a, and the cache reuses pre-allocated shards. A controller-free fast path could reach 0 allocs but would drop the `OnFallback`/`Error`/`Key` context that is the reason to use fallx over a bare `if err != nil`.
- **Execute_StaticSuccess (52 ns)**: the floor for the whole package — one `panix.Safe` call, one atomic increment, one controller allocation. The static-fallback variant (61 ns) adds the `WithOnFallback` nil-check and the second atomic path but allocates the same.
- **Execute_CachedHit (116 ns)**: ~2.2× the static success cost. The extra time is the inline FNV-1a key hash, the shard lock, the map lookup, and the `heap.Fix` that re-orders the LRU on access. The store path (112 ns) is comparable: hash, lock, map insert, and `heap.Push`. Hashing the key inline (rather than via `hash/fnv`) keeps both paths free of interface dispatch and key copies.
- **Parallel scaling**: cached hits under 8 goroutines run ~215 ns, ~1.9× the serial cost, because all benchmark keys hash to the same shard and serialize on its lock — a worst case. Real workloads spread keys across all 16 shards, so contention drops roughly linearly with shard count. The static path scales far better (61 ns parallel vs 52 ns serial) since it touches no lock, only the shared atomic counters.
- **No cache, no goroutine**: `StrategyStatic` and `StrategyFunc` start no background goroutine and allocate no shards beyond the (unused) empty slice, so they are appropriate for extremely high call rates where the cache machinery would be pure overhead.

## Quality


| Metric         | Value                          |
| -------------- | ------------------------------ |
| Test functions | 43                             |
| Benchmarks     | 7                              |
| Fuzz targets   | 3                              |
| Examples       | 4                              |
| Coverage       | 98.7%                          |
| Race detector  | All pass                       |
| External deps  | 0 (panix; testify in dev only) |


## File Structure

```text
fallx/
├── fallx.go            # package doc + Fallback[T] + Execute/ExecuteWithKey + fallback dispatch + lifecycle
├── options.go          # config, Option[T], defaults, WithXxx
├── types.go            # Strategy enum + FallController + execution impl + PrimaryFunc/FallbackFunc + Stats
├── cache.go            # sharded LRU cache, eviction, cleanup loop, lruHeap
├── errors.go           # ErrNoFunc, ErrNoCached, ErrClosed, ErrNilFunc, ErrFallbackFailed
├── fallx_test.go       # unit + table-driven + concurrent tests
├── bench_test.go       # benchmarks (sequential + parallel)
├── fuzz_test.go        # FuzzExecuteCached, FuzzExecuteFunc, FuzzSeed
├── example_test.go     # runnable GoDoc examples
├── footprint_test.go   # struct size guards
└── README.md           # this file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.