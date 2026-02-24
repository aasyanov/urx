# quotax

Per-key rate limiting for industrial Go services.

## Philosophy

One concern — **per-key rate limiting**. Each key (user ID, IP, API key)
gets its own independent token-bucket limiter backed by [ratex]. Inactive
keys are evicted automatically. Composes with `ratex` (global limiter),
`shedx` (load shedding), and other urx primitives in your `main.go`.

## API

| Function / Method | Description |
|---|---|
| `New(opts ...Option) *Limiter` | Create a per-key limiter, starts background eviction |
| `l.Allow(key) bool` | Non-blocking check for one token |
| `l.AllowN(key, n) bool` | Non-blocking check for n tokens |
| `l.AllowOrError(key) error` | Like Allow but returns `*errx.Error` on rejection |
| `l.Wait(ctx, key) error` | Block until one token is available |
| `l.WaitN(ctx, key, n) error` | Block until n tokens are available |
| `l.Remove(key) bool` | Delete a key's bucket |
| `l.Exists(key) bool` | Check if a key is tracked |
| `l.KeyCount() int64` | Number of tracked keys |
| `l.Reset()` | Remove all keys |
| `l.ForceEviction()` | Run eviction immediately (for testing) |
| `l.Stats() Stats` | Snapshot of counters |
| `l.ResetStats()` | Zero counters |
| `l.Close()` | Stop background eviction (idempotent) |

## Options

| Option | Default | Description |
|---|---|---|
| `WithRate(r)` | 10 | Sustained rate per key (req/s) |
| `WithBurst(n)` | 20 | Token bucket capacity per key |
| `WithShards(n)` | 64 | Internal shards to reduce lock contention |
| `WithMaxKeys(n)` | 0 (unlimited) | Maximum tracked keys (memory protection) |
| `WithEvictionTTL(d)` | 15 min | Inactive key lifetime before eviction |
| `WithEvictionInterval(d)` | 1 min | Background eviction frequency |
| `WithOnMaxKeys(fn)` | nil | Callback when max keys reached |

## Error Diagnostics

All errors are `*errx.Error` with domain `QUOTA`:

| Code | Meaning |
|---|---|
| `LIMITED` | Rate limit exceeded for key (meta: key) |
| `MAX_KEYS` | Maximum tracked keys reached (meta: key) |
| `CANCELLED` | Wait cancelled via context |
| `CLOSED` | Limiter has been closed |

## Thread Safety

All methods are safe for concurrent use. Keys are distributed across
sharded maps. MaxKeys enforcement uses atomic CAS to prevent races.
The FNV hasher used for shard routing is pooled via `sync.Pool` to avoid
per-call allocation on the hot path. The `WithOnMaxKeys` callback is invoked
outside the shard lock to prevent user-code blocking from causing contention.

## Eviction

Background goroutine runs every `evictionInterval`, removing keys whose
`lastAccess` is older than `evictionTTL`. Two-phase approach:
1. `RLock` — collect stale keys.
2. `Lock` — delete with double-check.

## Tests

**33 tests, 95.8% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/quotax  coverage: 95.8% of statements
```

Coverage includes:
- Allow / AllowN: existing key, new key, burst exhaustion
- AllowOrError: rejection with errx.Error
- Wait / WaitN: blocking, context cancellation
- MaxKeys: limit enforcement, OnMaxKeys callback
- Eviction: TTL-based removal, ForceEviction
- Remove / Exists / KeyCount / Reset
- Stats / ResetStats: counter tracking
- Close: idempotent cleanup
- Sharding: correct key distribution
- Concurrent operations (parallel benchmarks)
- Error constructors and domain/code constants

### Benchmark analysis

**Existing key:** ~100 ns, 0 allocs — sharded map lookup + ratex.Allow. Fast
enough for per-request admission checks at 10M+ req/s.

**Parallel:** ~160 ns, 1 alloc — contention across shards adds minimal overhead.
The single allocation is the shard lock ticket.

**New keys:** ~1.8 us, 5 allocs — creating a new ratex.Limiter with its internal
state. This is the cold path; in production, most requests hit existing keys.

## File structure

```text
pkg/quotax/
    quotax.go      -- Limiter, New(), Allow(), Wait(), eviction, sharding
    errors.go      -- DomainQuota, Code constants, error constructors
    quotax_test.go -- 33 tests + 3 benchmarks, 95.8% coverage
    README.md
```
