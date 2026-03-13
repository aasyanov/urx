# lrux

Generic, thread-safe LRU cache with TTL, sharding, eviction callbacks, singleflight, and batch operations.

## Philosophy

**One job: in-memory caching.** `lrux` provides `LRU[K, V]` (single-lock cache) and `ShardedLRU[K, V]` (partitioned for high-concurrency). Both support TTL, eviction callbacks, `GetOrCompute` with optional singleflight dedup, and bulk operations. They do not persist to disk, distribute across nodes, or manage external cache systems.

## Quick start

```go
// Basic LRU
c := lrux.New[string, User](
    lrux.WithCapacity[string, User](10_000),
    lrux.WithTTL[string, User](5 * time.Minute),
    lrux.WithOnEvict[string, User](func(k string, v User, reason lrux.EvictionReason) {
        log.Info("evicted", "key", k, "reason", reason)
    }),
)
defer c.Close()

c.Set("user:1", alice)
user, ok := c.Get("user:1")

// Compute-on-miss with singleflight
user = c.GetOrCompute("user:2", func() User {
    return db.FindUser(ctx, "user:2")
}, lrux.WithSingleflight())

// Sharded for high concurrency
sc := lrux.NewSharded[string, User](
    lrux.WithShardCount[string, User](16),
    lrux.WithShardCapacity[string, User](1000),
    lrux.WithShardTTL[string, User](5 * time.Minute),
)
defer sc.Close()
```

## API

### LRU

| Function / Method | Description |
|---|---|
| `New[K, V](opts...) *LRU[K, V]` | Create an LRU cache (default: unlimited capacity) |
| `c.Set(key, value)` | Insert or update |
| `c.SetWithTTL(key, value, ttl)` | Insert with per-entry TTL |
| `c.Get(key) (V, bool)` | Get and promote to front |
| `c.Peek(key) (V, bool)` | Get without promotion (read lock) |
| `c.Has(key) bool` | Check existence (read lock) |
| `c.Delete(key) bool` | Remove entry |
| `c.TTL(key) time.Duration` | Remaining TTL (-1 = no TTL, 0 = missing/expired) |
| `c.GetOrCompute(key, fn, opts...) V` | Get or compute on miss |
| `c.SetMulti(items)` | Batch insert |
| `c.GetMulti(keys) map[K]V` | Batch get |
| `c.DeleteMulti(keys) int` | Batch delete |
| `c.Keys() []K` | All keys (most recent first) |
| `c.Values() []V` | All values (most recent first) |
| `c.Range(fn)` | Iterate entries (under lock) |
| `c.Len() int` | Entry count |
| `c.Clear()` | Remove all entries |
| `c.ExpireOld() int` | Remove expired entries |
| `c.Stats() Stats` | Hit/miss/eviction counters |
| `c.ResetStats()` | Zero counters |
| `c.Close()` | Stop cleanup, clear entries, mark closed |
| `c.IsClosed() bool` | Whether cache is closed |

### ShardedLRU

| Function / Method | Description |
|---|---|
| `NewSharded[K, V](opts...) *ShardedLRU[K, V]` | Create sharded cache (default: 16 shards) |
| All LRU methods | Delegated to the appropriate shard |
| Batch ops (>=64 items) | Dispatched to shards in parallel |

### Options (LRU)

| Option | Default | Description |
|---|---|---|
| `WithCapacity(n)` | `0` (unlimited) | Max entries |
| `WithTTL(d)` | no TTL | Default time-to-live |
| `WithOnEvict(fn)` | none | Eviction callback (runs outside lock) |
| `WithCleanupInterval(d)` | disabled | Background expired-entry sweep period |

### Options (ShardedLRU)

| Option | Default | Description |
|---|---|---|
| `WithShardCount(n)` | `16` | Number of shards (rounded up to power of 2) |
| `WithShardCapacity(n)` | `0` (unlimited) | Per-shard capacity |
| `WithShardTTL(d)` | no TTL | Per-shard default TTL |
| `WithShardOnEvict(fn)` | none | Per-shard eviction callback |
| `WithShardCleanupInterval(d)` | disabled | Per-shard cleanup period |

### Options (GetOrCompute)

| Option | Default | Description |
|---|---|---|
| `WithComputeTTL(d)` | cache default | Custom TTL for computed entry |
| `WithSingleflight()` | off | Deduplicate concurrent computes for same key |

### Eviction reasons

| Reason | When |
|---|---|
| `Capacity` | Capacity exceeded, oldest entry removed |
| `Expired` | TTL elapsed |
| `Deleted` | Explicitly removed via `Delete` / `DeleteMulti` |
| `Cleared` | Cache cleared via `Clear` or `Close` |
| `Replaced` | Value overwritten by `Set` / `SetWithTTL` / `SetMulti` |

## Behavior details

- **LRU ordering**: `Get` promotes the entry to the front of the intrusive doubly-linked list. `Peek` reads without promotion (uses read lock). When capacity is exceeded, the tail (least recently used) entry is evicted.

- **Intrusive list**: each `node[K, V]` embeds `prev`/`next` pointers directly, avoiding wrapper allocations. `Set` on a non-full cache is 0 allocs.

- **TTL**: entries have an optional expiration time. Expired entries are removed lazily on `Get` / `Keys` / `Values` / `Range`, or eagerly via `ExpireOld` / background cleanup goroutine (`WithCleanupInterval`).

- **GetOrCompute**: if the key is missing or expired, calls the compute function, caches the result, and returns it. Uses double-checked locking: checks after acquiring lock (before compute) and again after compute (before store) to handle concurrent writes. With `WithSingleflight()`, concurrent calls for the same key share a single compute via `singleflight.Group`. Singleflight uses `keyToString` internally — fast-path for `string`, `int`, `int64`, `uint64`; falls back to `fmt.Sprint` for other key types (one allocation per call).

- **Sharding**: `ShardedLRU` partitions keys by `maphash.Hash` across N independent LRU instances (N rounded up to power of 2, bitmask routing). Reduces lock contention under high concurrency.

- **Eviction callbacks**: `WithOnEvict` is called outside the lock for every evicted entry, with the eviction reason. Panics in callbacks are recovered silently to protect the cache.

- **Panic recovery**: eviction callbacks and compute functions are wrapped with `defer`-based recovery. If a compute function panics, the zero value is returned. Panics are recovered silently — logging is the caller's responsibility (wrap callbacks with `panix.Safe` if observability is needed). This follows the urx convention that no package writes to `slog` directly.

- **Batch operations**: `SetMulti`, `GetMulti`, `DeleteMulti` operate under a single lock acquisition on `LRU`. On `ShardedLRU`, batches >=64 items are split by shard and dispatched in parallel via goroutines.

## Thread safety

- `LRU.Get` / `Set` / `Delete` / `Clear` / `GetOrCompute`: `sync.Mutex` (write lock)
- `LRU.Peek` / `Has` / `Len`: `sync.RWMutex` read lock
- `LRU.Stats` / `ResetStats`: atomic counters — lock-free
- `LRU.Close` / `IsClosed`: `atomic.Bool` — lock-free
- `ShardedLRU`: each shard has its own `sync.RWMutex` — parallel access across shards
- Eviction callbacks: run outside lock — safe to access cache from callback

## Tests

**139 tests, 98.8% statement coverage.**

```text
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/lrux  coverage: 98.8% of statements
```

Coverage includes:
- LRU: Set, Get, Peek, Has, Delete, Clear, Len, Keys, Values, Range
- TTL: SetWithTTL, global expiration, per-entry override, TTL query, ExpireOld
- GetOrCompute: miss, hit, singleflight dedup, custom TTL, expired entry
- GetOrCompute double-check: hit after lock, re-check finds live entry,
  re-check finds expired entry, eviction during compute store
- Batch: SetMulti (with eviction, with overwrite), GetMulti, DeleteMulti
- Eviction: capacity, TTL, callback invocation, all 5 reason values
- ShardedLRU: all operations, parallel batch dispatch (>=64 items),
  power-of-two rounding, default/negative shard count
- Stats: hit/miss/eviction counters, hit rate, reset
- Lifecycle: Close (idempotent, fires callbacks, stops ticker), IsClosed
- Thread safety: concurrent Get/Set, concurrent mixed ops (race detector)
- Intrusive list: single element, order after access
- Hasher: all type branches (string, int, int32, int64, uint, uint32, uint64,
  float32, float64, bool, struct), distribution test
- keyToString: all type branches, fmt.Sprint fallback

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz. Each benchmark was run 3 times (`-count=3`); the table shows median values.

### LRU (single-lock)

```text
BenchmarkLRU_Set                        ~134 ns/op       0 B/op     0 allocs/op
BenchmarkLRU_Get_Hit                     ~89 ns/op       0 B/op     0 allocs/op
BenchmarkLRU_Get_Miss                    ~73 ns/op       7 B/op     0 allocs/op
BenchmarkLRU_Peek                        ~53 ns/op       0 B/op     0 allocs/op
BenchmarkLRU_SetWithTTL                 ~109 ns/op       0 B/op     0 allocs/op
BenchmarkLRU_GetOrCompute_Hit            ~80 ns/op       0 B/op     0 allocs/op
BenchmarkLRU_GetOrCompute_Miss           ~80 ns/op       0 B/op     0 allocs/op
BenchmarkLRU_GetOrCompute_Singleflight   ~79 ns/op       0 B/op     0 allocs/op
BenchmarkLRU_Delete                      ~38 ns/op       0 B/op     0 allocs/op
BenchmarkLRU_Stats                       ~18 ns/op       0 B/op     0 allocs/op
BenchmarkLRU_Set_WithEviction           ~384 ns/op     160 B/op     2 allocs/op
BenchmarkLRU_Set_Allocs                 ~162 ns/op       0 B/op     0 allocs/op
```

### LRU concurrent

```text
BenchmarkLRU_Concurrent_Get             ~167 ns/op       0 B/op     0 allocs/op
BenchmarkLRU_Concurrent_Set             ~182 ns/op       0 B/op     0 allocs/op
BenchmarkLRU_Concurrent_Mixed           ~179 ns/op       0 B/op     0 allocs/op
```

### Sharded

```text
BenchmarkSharded_Set                    ~266 ns/op      57 B/op     0 allocs/op
BenchmarkSharded_Get_Hit                ~139 ns/op       0 B/op     0 allocs/op
BenchmarkSharded_Concurrent_Get         ~107 ns/op       0 B/op     0 allocs/op
BenchmarkSharded_Concurrent_Set         ~134 ns/op       6 B/op     0 allocs/op
BenchmarkSharded_Concurrent_Mixed       ~125 ns/op       0 B/op     0 allocs/op
BenchmarkSharded_Concurrent_Contention  ~190 ns/op       0 B/op     0 allocs/op
BenchmarkSharded_SetMulti             ~37307 ns/op    5096 B/op    75 allocs/op
```

### Parallel scaling

```text
BenchmarkLRU_ParallelScaling/1          ~197 ns/op
BenchmarkLRU_ParallelScaling/4          ~276 ns/op
BenchmarkLRU_ParallelScaling/8          ~356 ns/op
BenchmarkLRU_ParallelScaling/16         ~364 ns/op
BenchmarkSharded_ParallelScaling/1      ~170 ns/op
BenchmarkSharded_ParallelScaling/4      ~179 ns/op
BenchmarkSharded_ParallelScaling/8      ~170 ns/op
BenchmarkSharded_ParallelScaling/16     ~180 ns/op
```

### Analysis

**LRU Set:** ~134 ns, 0 allocs. Mutex lock + intrusive list insert + map store. Zero allocations for updates (node reused) and for new inserts within capacity (node allocated on heap, amortized by Go runtime).

**LRU Get (hit):** ~89 ns, 0 allocs. Mutex lock + map lookup + list promotion + `accessedAt` update.

**LRU Peek:** ~53 ns. Read lock only, no promotion — cheaper than Get.

**LRU Set (with eviction):** ~384 ns, 2 allocs. Tail removal + eviction event allocation + callback invocation.

**Sharded concurrent Get:** ~107 ns. Better than LRU concurrent (~167 ns) because different keys hit different shards with independent locks.

**Parallel scaling (LRU):** degrades from ~197 ns (1 goroutine) to ~364 ns (16 goroutines). Single mutex becomes a bottleneck.

**Parallel scaling (Sharded):** stays at ~170–180 ns from 1 to 16 goroutines. Sharding eliminates the bottleneck.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| LRU Get (hit) | < 90 ns, 0 allocs | Hot-path friendly |
| LRU Set | < 140 ns, 0 allocs | Zero-alloc insert |
| LRU Peek | < 55 ns, 0 allocs | Cheapest read path |
| Sharded Get (concurrent) | < 110 ns | Scales linearly |
| Sharded Set (concurrent) | < 140 ns | Scales linearly |
| GetOrCompute (hit) | < 80 ns | Cache hit fast path |
| Eviction | < 400 ns | Callback cost |

**When to use ShardedLRU:** when you have > 4 concurrent goroutines accessing the cache. Below that, single-lock LRU is faster due to no hashing overhead.

## What lrux does NOT do

| Concern | Owner |
|---------|-------|
| Disk persistence | BadgerDB / BoltDB |
| Distributed caching | Redis / Memcached |
| LFU policy | caller implements custom eviction |
| Size-based eviction | caller tracks memory |
| Compression | caller compresses values |
| TTL refresh on access | caller calls `SetWithTTL` |

## File structure

```text
pkg/lrux/
    lrux.go       -- LRU[K,V], New(), Get(), Set(), GetOrCompute(), etc.
    sharded.go    -- ShardedLRU[K,V], NewSharded(), shard routing, batch dispatch
    types.go      -- Options, EvictionReason, Stats, node, config, cleanupTicker
    lrux_test.go  -- 139 tests, 98.8% coverage
    bench_test.go -- 29 benchmarks
    README.md
```
