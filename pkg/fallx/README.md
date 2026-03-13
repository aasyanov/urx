# fallx

Fallback patterns for graceful degradation in industrial Go services.

## Philosophy

One concern — **fallback on failure**. No framework glue, no hidden goroutines
(except the optional cache cleanup ticker). Composes with `retryx`, `circuitx`,
`toutx`, and any other urx primitive in your `main.go`.

## API

| Function / Method | Description |
|---|---|
| `New[T](opts ...Option[T]) *Fallback[T]` | Create a fallback |
| `fb.Do(ctx, fn) (T, error)` | Run fn; on failure use the configured strategy |
| `fb.DoWithKey(ctx, key, fn) (T, error)` | Same, with explicit cache key |
| `fb.Seed(key, value)` | Pre-populate cache |
| `fb.SeedWithTTL(key, value, ttl)` | Pre-populate with custom TTL |
| `fb.ClearCache()` | Remove all cached entries |
| `fb.Stats() Stats` | Snapshot of counters |
| `fb.ResetStats()` | Zero all counters |
| `fb.Close()` | Stop background cleanup (idempotent) |

## Options

| Option | Default | Description |
|---|---|---|
| `WithStatic[T](value)` | — | Return fixed value on failure |
| `WithFunc[T](fn)` | — | Call `fn(ctx, err)` on failure |
| `WithCached[T](ttl, maxSize)` | 5 min, 100 | Cache successful results, replay on failure |
| `WithKeyFunc[T](fn)` | `"default"` | Extract cache key from context |
| `WithShards[T](n)` | 16 | Number of cache shards |
| `WithOnFallback[T](fn)` | nil | Callback on every fallback |

## Strategies

**Static** — simplest: return a predefined value.

```go
fb := fallx.New[string](fallx.WithStatic[string]("service unavailable"))
val, _ := fb.Do(ctx, callAPI)
```

**Func** — call an alternative function that receives the original error.

```go
fb := fallx.New[Response](fallx.WithFunc(func(ctx context.Context, err error) (Response, error) {
    return fetchFromBackup(ctx)
}))
```

**Cached** — stores successful results and replays them on failure.

```go
fb := fallx.New[PriceList](
    fallx.WithCached[PriceList](5*time.Minute, 1000),
    fallx.WithKeyFunc[PriceList](func(ctx context.Context) string {
        return ctx.Value(regionKey{}).(string)
    }),
)
defer fb.Close()
```

## Error Diagnostics

All errors are `*errx.Error` with domain `FALLBACK`:

| Code | Meaning |
|---|---|
| `NO_FUNC` | StrategyFunc configured but no function provided |
| `FUNC_FAILED` | Fallback function returned an error |
| `NO_CACHED` | No cached result available (meta: key) |
| `CLOSED` | Fallback has been closed |

## Thread Safety

All methods are safe for concurrent use. Cache uses sharded locks with
LRU heap eviction (O(log n) per operation).

## Panic Safety

Both primary and fallback functions are wrapped with `recover()`.
Panics are converted to `*errx.Error` with stack trace.

## Tests

**34 tests, 96.2% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/fallx  coverage: 96.2% of statements
```

Coverage includes:
- Do: primary success, primary failure with each strategy
- Static: returns predefined value
- Func: alternative function, func failure
- Cached: cache hit, cache miss, TTL expiry, LRU eviction
- DoWithKey: explicit cache key
- Seed / SeedWithTTL: pre-population
- ClearCache: removes all entries
- Stats / ResetStats: counter tracking
- Close: cleanup ticker stop, idempotent
- Panic recovery: both primary and fallback
- Error constructors and domain/code constants

### Benchmark analysis

**Static fallback:** ~150 ns, 0 allocs — direct value return, effectively free.

**Primary success (cached strategy):** ~350 ns, 0 allocs — primary succeeds, result
is cached for future fallback use.

**Cached fallback:** ~260 ns, 0 allocs — primary fails, cached value is served.
Sharded LRU lookup is fast enough for hot-path usage.

## File structure

```text
pkg/fallx/
    fallx.go       -- Fallback[T], New(), Do(), DoWithKey(), strategies, cache
    errors.go      -- DomainFallback, Code constants, error constructors
    fallx_test.go  -- 34 tests + 3 benchmarks, 96.2% coverage
    README.md
```
