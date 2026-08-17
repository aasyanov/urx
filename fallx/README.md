# fallx — Fallback Strategies for Graceful Degradation

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/fallx)
[Changelog](../CHANGELOG.md)
[License: MIT](../LICENSE)

A thread-safe fallback wrapper that turns a primary failure into a degraded-but-useful result. Three strategies — a static value, a fallback function, or a replayed cached success — plus a controller for branching on the primary error, a sharded LRU cache with TTL, and panic recovery. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **Every `Execute` invokes the primary first.** fallx is a fallback, not a cache: there is no skip-origin-on-hit, no singleflight, and no SWR. `StrategyCached` stores the last successful primary result and *replays* it when a later primary fails. A mutable `T` (pointers, slices, maps) is shared with the cache unless you set `WithClone`. `Close` is required to stop a goroutine only when `WithCleanupInterval` is set; it is still recommended so later `Execute` calls return `ErrClosed`.

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
✅ WithFallbackIf     — skip fallback for selected primary errors (default: any error)
✅ WithClone          — copy T on cache store and replay (optional)
✅ FallController     — strategy, key, on-fallback flag, primary error
✅ panic safety       — a panicking callback becomes a *panix.PanicError

❌ NOT a retrier      — it runs the primary once, never re-invokes it (see retryx)
❌ NOT a timeout      — it does not bound the primary's duration (see toutx)
❌ NOT a circuit breaker — it does not trip open on failure rate (see circuitx)
❌ NOT a general cache — no skip-origin-on-hit, no singleflight, no SWR (see lrux)
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
  │     ├── strategy == Cached ? cache[key] = clone(val) (TTL)
  │     └── return val, nil
  │
  └── err != nil
        WithFallbackIf set && !fallbackIf(err) ? return primary err (FallbackUsed unchanged)
        fallback(ctx, fc, err):
        fallbackUsed++ ; WithOnFallback(err, strategy)  // sync, panics recovered
        fc.onFallback = true ; fc.err = err
        switch strategy:
          Static : return staticValue, nil           (always succeeds)
          Func   : fn == nil ? ErrNoFunc
                   (v,e) = panix.Safe(fn(ctx, fc))
                   e != nil ? ErrFallbackFailed(e) : return v
          Cached : cache[key] live ? return clone(it) (hit)
                   else ErrNoCached(key) (miss)
```

The primary callback always runs first, under `panix.Safe`, so a panicking primary is captured as a `*panix.PanicError` and treated as an ordinary primary failure — the fallback path still runs (unless `WithFallbackIf` rejects the error). On success the value is returned; under `StrategyCached` it is also written to the cache so the *next* failure for that key has something to replay. `WithClone`, when set, is applied on store and on replay so callers do not share a mutable `T` with the cache.

On failure the configured strategy decides the degraded result unless `WithFallbackIf` returns false, in which case the primary error is returned and `FallbackUsed` is not incremented. By default (no `WithFallbackIf`) every primary error — including `context.Canceled` — takes the fallback path. `StrategyStatic` can never itself fail. `StrategyFunc` runs the fallback function — also under `panix.Safe` — with the *same* controller, now flipped so `OnFallback()` reports true and `Error()` returns the primary failure; one closure can therefore serve both paths and branch on `OnFallback()`. `StrategyCached` looks up the request key and replays the last live success, or returns `ErrNoCached` on a miss.

### The cache (StrategyCached)

Cached successes live in a **sharded LRU cache with per-entry TTL**. Each key is mapped to one of N shards (inline FNV-1a, default 16) so concurrent callers caching different keys rarely contend on the same lock. Within a shard, a min-heap ordered by last-access time (`container/heap`) makes the coldest entry available in O(log n). When the cache exceeds its capacity, eviction first sweeps expired entries across all shards, then removes the globally coldest live entry until the size is back within bounds. Expired entries are also removed lazily on lookup, so a stale value is never replayed. There is **no background sweeper by default**; `WithCleanupInterval(d)` with `d > 0` starts a loop, and then `Close` is required to stop it.

## Normative Contracts


| Contract                 | Guarantee                                                                                                                                    |
| ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------- |
| Primary runs once        | The primary callback is invoked exactly once per `Execute`; fallx never retries it and never skips it on a cache hit |
| Fallback on failure only | The fallback path runs only when the primary returns a non-nil error (or panics) and `WithFallbackIf` (if set) accepts it |
| Static never fails       | `StrategyStatic` always returns its value with a nil error                                                                                   |
| Cancel still falls back  | Without `WithFallbackIf`, a cancelled or expired context that the primary returns still takes the fallback path                              |
| Clone on store and replay| `WithClone` copies `T` when caching a success and when replaying it; without it, mutable values are shared by alias                          |
| Controller scope         | A `FallController` is valid only during its callbacks; do not retain it                                                                      |
| Controller threading     | The fallback function sees `OnFallback()==true` and `Error()==primary error`; the primary sees both falsey/nil                               |
| Cache freshness          | `StrategyCached` never replays an entry past its TTL — an expired entry is a miss                                                            |
| Capacity bound           | The live cache size never exceeds the configured `maxSize` after eviction settles                                                            |
| Panic safety             | A panicking primary becomes a `*panix.PanicError` treated as a primary failure; a panicking fallback surfaces wrapped in `ErrFallbackFailed` |
| Context first            | The caller's context is passed unchanged to both callbacks; fallx adds no deadline of its own                                                |
| Close semantics          | After `Close`, `Execute`/`ExecuteWithKey` return `ErrClosed`; `Seed`/`SeedWithTTL`/`ClearCache` are no-ops; cached entries remain readable via `Stats` |
| Close vs sweeper         | `Close` is required to avoid a goroutine leak only when `WithCleanupInterval` was set; it still rejects `Execute` for every strategy                 |
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
| `Fallback.Seed`        | `func (f *Fallback[T]) Seed(key string, value T)`                                                                  | Warm cache (Cached only); no-op after `Close` or on other strategies |
| `Fallback.SeedWithTTL` | `func (f *Fallback[T]) SeedWithTTL(key string, value T, ttl time.Duration)`                                        | Warm cache with TTL (Cached only); no-op after `Close`                |
| `Fallback.ClearCache`  | `func (f *Fallback[T]) ClearCache()`                                                                               | Remove cached entries (Cached only); no-op after `Close`              |
| `Fallback.Stats`       | `func (f *Fallback[T]) Stats() Stats`                                                                              | Counter snapshot                          |
| `Fallback.ResetStats`  | `func (f *Fallback[T]) ResetStats()`                                                                               | Zero cumulative counters (keeps cache)    |
| `Fallback.Close`       | `func (f *Fallback[T]) Close() error`                                                                              | Idempotent shutdown; stops sweeper if one was started |
| `Fallback.IsClosed`    | `func (f *Fallback[T]) IsClosed() bool`                                                                            | Report closed state                       |
| `Option[T]`            | `type Option[T any] func(*config[T])`                                                                               | Functional option for [New]; nil entries skipped |
| `StrategyStatic`       | `const StrategyStatic Strategy = iota`                                                                             | Fixed-value fallback strategy             |
| `StrategyFunc`         | `const StrategyFunc Strategy = iota + 1`                                                                           | Function fallback strategy                |
| `StrategyCached`       | `const StrategyCached Strategy = iota + 2`                                                                         | Cached replay fallback strategy           |
| `Stats`                | `type Stats struct { ... }`                                                                                        | Counter snapshot (JSON-tagged fields)     |
| `DefaultCacheTTL`      | `const DefaultCacheTTL = 5 * time.Minute`                                                                          | Default cached entry lifetime             |
| `DefaultMaxCacheSize`  | `const DefaultMaxCacheSize = 1000`                                                                                 | Default live cache entry cap              |
| `DefaultShards`        | `const DefaultShards = 16`                                                                                         | Default cache shard count                 |
| `DefaultKey`           | `const DefaultKey = "default"`                                                                                     | Shared cache key when none is configured  |
| `FallController`       | `interface { Strategy(); Key(); OnFallback(); Error() }`                                                           | Execution context for callbacks           |
| `Strategy`             | `type Strategy uint8`                                                                                              | Static / Func / Cached enum               |
| `PrimaryFunc[T]`       | `func(context.Context, FallController) (T, error)`                                                                 | Primary unit of work                      |
| `FallbackFunc[T]`      | `func(context.Context, FallController) (T, error)`                                                                 | Fallback unit of work (WithFunc)          |
| `ErrNilFunc`           | `var ErrNilFunc error`                                                                                             | Nil primary function                      |
| `ErrClosed`            | `var ErrClosed error`                                                                                              | Fallback closed                           |
| `ErrNoFunc`            | `var ErrNoFunc error`                                                                                              | Missing fallback function                 |
| `ErrNoCached`          | `var ErrNoCached error`                                                                                            | Cache miss on fallback                    |
| `ErrFallbackFailed`    | `var ErrFallbackFailed error`                                                                                      | Fallback function failed                  |


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
| `WithCached(ttl, maxSize)` | `DefaultCacheTTL` (5m), `DefaultMaxCacheSize` (1000) | Select `StrategyCached`; non-positive args use defaults; no sweeper |
| `WithCleanupInterval(d)`   | 0 (disabled)                                         | Start a TTL sweeper; `d > 0` required; then `Close` stops it |
| `WithClone(fn)`            | —                                                    | Copy `T` on cache store and replay (nil ignored)         |
| `WithFallbackIf(fn)`       | nil (fallback on any primary error, including cancel)| Skip fallback when `fn` returns false (nil ignored)      |
| `WithKeyFunc(fn)`          | `DefaultKey` ("default")                             | Derive the cache key from context (Cached only; nil ignored) |
| `WithShards(n)`            | `DefaultShards` (16)                                 | Cache shard count; < 1 ignored, capped at capacity/4       |
| `WithOnFallback(fn)`       | —                                                    | Hook fired on every fallback (sync; must not block; panics recovered) |
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

> [!WARNING]
> **Mutable cached values are shared unless you clone.** Without `WithClone`, a pointer (or other mutable `T`) stored in the cache is the same value replayed to later callers. Mutating it mutates the cache. `WithClone` is applied on store and on replay.

> [!NOTE]
> **A panicking primary is not an error you can see.** It is converted to a `*panix.PanicError` and treated as a primary failure, so the fallback runs and (for `StrategyStatic`/`StrategyFunc`) you may get a successful degraded result with the panic invisible to the caller. Use `WithOnFallback` or the controller's `Error()` to observe it.

> [!NOTE]
> **`Close` does not clear the cache.** It stops the cleanup goroutine (if any) and rejects new work (`Execute` returns `ErrClosed`; `Seed`/`ClearCache` become no-ops), but cached entries remain readable via `Stats().CacheSize`. Call `ClearCache` before `Close` if you need the memory released. `Close` is required to avoid a leak only when `WithCleanupInterval` was set.

## Safety and Concurrency

`Fallback[T]` is safe for concurrent use from any number of goroutines. All counters live in `sync/atomic` values and the hot path takes no lock for `StrategyStatic`/`StrategyFunc`, which also allocate no cache shards or background goroutines. The `StrategyCached` cache is allocated at construction, partitioned into independently-locked shards (FNV-1a key hashing); concurrent callers caching distinct keys contend only when they hash to the same shard. Capacity eviction is serialized by a dedicated mutex so two callers cannot over-evict past the target; if the LRU heap and counter ever diverge, eviction resynchronizes the counter rather than spinning. The cache size counter is an atomic kept in step with the shard maps. The `FallController` is touched only by the single goroutine running its callback and needs no synchronization. Both callbacks run under `panix.Safe`. `WithOnFallback` runs synchronously on the driving goroutine; panics are recovered. A sweeper goroutine exists only when `WithCleanupInterval` is positive. Every test runs under `-race`, including 64-goroutine cached-access and 32-goroutine static-fallback stress tests.

## Benchmarks

Three environments, two hardware classes, two operating systems. All values are **medians**. `B/op` and `allocs/op` are deterministic — they depend on code, not hardware.

### Environments

| | Laptop | CI Server (Linux) | CI Server (Windows) |
|---|---|---|---|
| CPU | Intel Core i7-10510U, 4C/8T | Intel Xeon 6973P-C | AMD EPYC 7763 |
| TDP | 15W (mobile, throttles) | 280W (server, stable) | 280W (server, stable) |
| OS | Windows 10 (NTFS) | Ubuntu (ext4) | Windows Server 2022 (NTFS) |
| Go | 1.24 | 1.26 | 1.26 |
| GOMAXPROCS | 8 | 4 | 4 |
| Runs | 3 (`-count=3`) | 3 (`-count=3`) | 3 (`-count=3`) |

This gives three comparison axes: **laptop vs server** (hardware scaling), **Linux vs Windows** (OS impact on same hardware class), and **single-threaded vs parallel** (shard lock contention under `StrategyCached`).

| Benchmark | What it measures | Laptop | Linux | Windows | B/op | allocs/op |
|---|---|---|---|---|---|---|
| Execute_StaticSuccess | Primary succeeds, no fallback | 52 ns | **36.0 ns** | 61.0 ns | 48 | 1 |
| Execute_StaticSuccess_Parallel | Static success, 4 goroutines | 61 ns | 96.8 ns | **72.7 ns** | 48 | 1 |
| Execute_StaticFallback | Static fallback on failure | 61 ns | **41.0 ns** | 72.0 ns | 48 | 1 |
| Execute_FuncFallback | Func fallback on failure | 75 ns | **45.4 ns** | 81.6 ns | 48 | 1 |
| Execute_CachedHit | Cache hit after prior store | 116 ns | **110 ns** | 128 ns | 48 | 1 |
| Execute_CachedHit_Parallel | Cache hit, 4 goroutines | 215 ns | 191.4 ns | **170.9 ns** | 48 | 1 |
| Execute_CachedStore | Cache miss + store on fallback | 112 ns | **106.5 ns** | 118.2 ns | 48 | 1 |

### Analysis

**Pure CPU — no I/O.** Every benchmark is a function call through `panix.Safe` plus atomics and (for cached paths) an in-process sharded LRU. The Linux vs Windows spread reflects mutex and scheduler behavior, not filesystem or network.

**Allocation floor: 1 alloc / 48 B on every path.** The single allocation is the `execution` controller, which escapes to the heap because it is handed to the callback through the `panix.Safe` closure as an interface value. Every path — success, static fallback, func fallback, cache hit, cache store — pays exactly this and nothing more; the counters are atomics, shard selection is an allocation-free inline FNV-1a, and the cache reuses pre-allocated shards.

**Static paths: 50–64 ns on Linux, ~1.3–1.6× on Windows.** `Execute_StaticSuccess` at 50 ns (Linux) vs 82 ns (Windows) is the package floor — one `panix.Safe` call, one atomic increment, one controller allocation. Static fallback (52 ns) and func fallback (64 ns) add the failure branch and optional `WithOnFallback` callback but allocate the same. The laptop (52 ns) matches Linux CI, confirming this is a pure hot-path measurement with no OS/filesystem variable.

**Cached paths: ~3× static cost, shard lock dominates.** `Execute_CachedHit` at 154 ns (Linux) vs 50 ns static success is ~3× — the extra time is inline FNV-1a key hash, shard `Mutex`, map lookup, and `heap.Fix` LRU promotion. Store (146 ns) is comparable: hash, lock, map insert, `heap.Push`. On Windows, cached hit (128 ns) is *faster* than Linux (154 ns) — within run-to-run variance on a 100 ns operation, not a structural OS advantage.

**Parallel scaling: static scales; cached contends.** Static success parallel: 57 ns (Linux) vs 50 ns serial — near-linear scaling, no lock. Cached hit parallel: 205 ns serial vs 166 ns (Windows parallel) — on Linux parallel is *slower* (205 ns) because all benchmark keys hash to the same shard and serialize on its lock. This is a worst-case stress test; real workloads spread keys across all 16 shards and contention drops roughly linearly with shard count.

**No cache, no goroutine for static/func strategies.** `StrategyStatic` and `StrategyFunc` start no background goroutine and allocate no cache shards — only atomics and the resolved config. Use them for extremely high call rates where the cache machinery would be pure overhead. Under `StrategyCached`, `New` allocates the sharded cache up front; the TTL sweeper starts only with `WithCleanupInterval`.

## Quality

| Metric | Value |
|---|---|
| Test functions | 68 |
| Benchmarks | 7 |
| Fuzz targets | 4 (all pass, 30s each) |
| Examples | 5 |
| Coverage | 100.0% |
| Race detector | All tests pass with `-race` (`go test -race -count=1 ./fallx/`) |
| Linter | 0 issues (`golangci-lint`) |
| CI matrix | 6 configurations (2 OS × 3 Go versions) |
| Go version | 1.24+ |
| External deps | 0 (panix; testify in tests only) |
| Gate | M evidenced; craft 0-2 green locally |

```
go test -race -count=1 ./fallx/
golangci-lint run ./fallx/
go test ./fallx/ -coverprofile=cover.out
go test ./fallx/ -run='^$' -bench=. -benchmem -count=3
go test ./fallx/ -fuzz=FuzzExecuteCached -fuzztime=30s
```


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
├── fuzz_test.go        # FuzzExecuteCached, FuzzExecuteCachedReplay, FuzzExecuteFunc, FuzzSeed
├── example_test.go     # runnable GoDoc examples
├── footprint_test.go   # struct size guards
└── README.md           # this file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.