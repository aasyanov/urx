# lrux — Generic LRU Cache with TTL, Sharding, and Singleflight Compute

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/lrux)
[License: MIT](../LICENSE)

A generic, thread-safe least-recently-used cache for Go 1.24+ with TTL expiration, eviction callbacks, singleflight compute, and an optional sharded variant for high-concurrency workloads. Zero external dependencies beyond `golang.org/x/sync` for singleflight deduplication.

```bash
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> `lrux` is an **in-process, bounded cache**, not a distributed cache and not an unbounded memo table. Capacity eviction and TTL are first-class: a `Cache` with no capacity and no TTL grows without bound. Always set `WithCapacity` (or a TTL plus a cleanup interval) for long-lived caches, and call `Close` to stop the background sweeper.

## The Problem

A production service caches user records, rendered responses, parsed config, and session state in memory. Each cache faces the same recurring requirements that the standard library leaves to the caller:

1. **Bounded memory** — the cache must evict the least-recently-used entry when it reaches capacity, never grow without limit.
2. **Expiration** — entries must disappear after a TTL, both lazily on access and proactively in the background, so stale data is never served and dead memory is reclaimed.
3. **Thundering-herd protection** — when 500 goroutines miss the same key at once, the expensive backing call (database, RPC) must run exactly once, not 500 times.
4. **Concurrency** — under dozens of goroutines, a single global lock becomes the bottleneck; the cache must shard its locks across keys.
5. **Resource cleanup** — when an entry is evicted, the owner must be able to close the connection or file it held.

Without a purpose-built cache, each call site re-implements an LRU list, a TTL sweeper, a `singleflight.Group`, and a shard router — and gets the lock ordering subtly wrong. `lrux` provides all five as one composable, generic type.

## Architectural Position

```text
✅ In-process bounded LRU cache (generic K → V)
✅ TTL expiration: global default + per-entry override
✅ Singleflight compute (compute runs once per key under load)
✅ Sharded variant for high-concurrency, many-key workloads
✅ Eviction callbacks for resource cleanup (run outside the lock)

❌ NOT a distributed / networked cache (no Redis, no gossip)
❌ NOT a frequency-aware cache (no TinyLFU / cost-based eviction)
❌ NOT persistent (memory only, lost on restart)
❌ NOT an unbounded memo table (set a capacity or TTL)
```

## Architecture

```text
                         Cache[K, V]
   ┌──────────────────────────────────────────────────────┐
   │  sync.RWMutex                                        │
   │                                                      │
   │  items: map[K]*node ───────────┐                     │
   │                                ▼                     │
   │  head → node ⇄ node ⇄ node ⇄ node ← tail           │
   │        (MRU)                   (LRU, evicted first)  │
   │                                                      │
   │  atomic: hits · misses · evictions                   │
   │  singleflight.Group (lazy)                           │
   │  cleanupTicker (optional background sweeper)         │
   └──────────────────────────────────────────────────────┘

                      ShardedCache[K, V]
   ┌──────────────────────────────────────────────────────┐
   │  hasher = maphash.Comparable(seed, key)              │
   │  shard  = shards[hash & (shardCount-1)]              │
   │                                                      │
   │  shards[0]  shards[1]  shards[2]  ...  shards[N-1]   │
   │   Cache      Cache      Cache           Cache        │
   │  (own lock) (own lock) (own lock)      (own lock)    │
   └──────────────────────────────────────────────────────┘
```

The list is **intrusive**: the previous/next pointers live inside each `node`, so an entry costs one heap allocation instead of the two a `container/list` wrapper requires.

## How It Works

### Lookup and promotion

```text
Get(key)
  │
  ├── miss            → misses++ → return zero, false
  ├── present, expired → remove (EvictionExpired) → misses++ → return zero, false
  └── present, live    → accessedAt = now → move node to head → hits++ → return value, true
```

`Get` promotes the entry to the head (most recently used) under a write lock. `GetFast` and `Peek` skip promotion and use a read lock, so they run concurrently — prefer them for read-heavy paths where strict recency does not matter.

### Insertion and eviction

```text
Set(key, value)
  │
  ├── key exists → fire EvictionReplaced → overwrite → move to head
  └── new key    → push node at head
                     └── size > capacity → remove tail (EvictionLRU) → evictions++
```

### Expiration

Entries carry an absolute `expiresAt`. They are removed **lazily** on the next `Get`/iteration, and **proactively** by the optional background sweeper started with `WithCleanupInterval` (or by calling `ExpireOld`). A zero `expiresAt` means the entry never expires.

### Singleflight compute

```text
GetOrCompute(key, fn, WithSingleflight())
  │
  ├── cache hit → return cached value
  └── miss → singleflight.Do(key):
               ├── leader  → fn() runs once → store → return
               └── waiters → block, then share the leader's result
```

With the context-aware `GetOrComputeCtx` + `WithSingleflight`, the compute function receives a **detached context** so one caller's cancellation cannot abort the shared computation for the other waiters; each caller's own context is still honored for its own return.

## Normative Contracts


| Invariant                | Guarantee                                                                                      |
| ------------------------ | ---------------------------------------------------------------------------------------------- |
| Capacity bound           | With `WithCapacity(n > 0)`, `Len()` never exceeds `n` after any operation returns              |
| LRU order                | `Get`/`Set`/`Touch` move the entry to MRU; the tail is always the next eviction victim         |
| Eviction callback timing | `OnEvict` runs **after** the lock is released; it may call back into the cache                 |
| Callback panic isolation | A panic in the eviction callback or compute function is recovered and cannot corrupt the cache |
| Close idempotency        | `Close` is safe to call any number of times; post-close mutations are no-ops                   |
| Singleflight             | `WithSingleflight` guarantees `compute` runs at most once per in-flight key                    |
| TTL semantics            | `TTL(key)` returns `-1` for no-expiry, `0` for missing/expired, positive remaining otherwise   |


## Quick Start

```go
package main

import (
	"fmt"
	"time"

	"github.com/aasyanov/urx/lrux"
)

func main() {
	c := lrux.New[string, int](
		lrux.WithCapacity[string, int](1000),
		lrux.WithTTL[string, int](time.Hour),
	)
	defer c.Close()

	c.Set("answer", 42)

	if v, ok := c.Get("answer"); ok {
		fmt.Println("value:", v)
	}
}
```

## Usage Scenarios

### HTTP response cache (read-heavy, sharded)

```go
var responses = lrux.NewSharded[string, []byte](
	lrux.WithShardCount[string, []byte](32),
	lrux.WithShardCapacity[string, []byte](1000),
	lrux.WithShardTTL[string, []byte](5*time.Minute),
)

func handler(w http.ResponseWriter, r *http.Request) {
	if body, ok := responses.GetFast(r.URL.Path); ok { // read lock, no promotion
		_, _ = w.Write(body)
		return
	}
	body := render(r)
	responses.Set(r.URL.Path, body)
	_, _ = w.Write(body)
}
```

### Database memoization with thundering-herd protection

```go
var users = lrux.NewSharded[string, *User](
	lrux.WithShardCapacity[string, *User](10000),
	lrux.WithShardTTL[string, *User](time.Minute),
)

func GetUser(ctx context.Context, id string) (*User, error) {
	return users.GetOrComputeCtx(ctx, id, func(ctx context.Context) (*User, error) {
		return db.QueryUser(ctx, id) // runs once per id even under 1000 concurrent misses
	}, lrux.WithSingleflight())
}
```

### Connection pool with eviction cleanup

```go
pool := lrux.New[string, *sql.Conn](
	lrux.WithCapacity[string, *sql.Conn](100),
	lrux.WithTTL[string, *sql.Conn](30*time.Minute),
	lrux.WithCleanupInterval[string, *sql.Conn](time.Minute),
	lrux.WithOnEvict[string, *sql.Conn](func(_ string, conn *sql.Conn, _ lrux.EvictionReason) {
		_ = conn.Close() // runs outside the lock; safe to block
	}),
)
defer pool.Close()
```

### Sliding-window session store

```go
sessions := lrux.NewSharded[string, *Session](
	lrux.WithShardCapacity[string, *Session](10000),
	lrux.WithShardTTL[string, *Session](24*time.Hour),
)

func keepAlive(id string) bool {
	return sessions.Touch(id) // refresh TTL + promote to MRU on activity
}
```

## API

### Constructors


| Symbol       | Signature                                                                               | Description                |
| ------------ | --------------------------------------------------------------------------------------- | -------------------------- |
| `New`        | `func New[K comparable, V any](opts ...Option[K, V]) *Cache[K, V]`                      | Create a single-lock cache |
| `NewSharded` | `func NewSharded[K comparable, V any](opts ...ShardedOption[K, V]) *ShardedCache[K, V]` | Create a sharded cache     |


### Cache / ShardedCache methods


| Symbol            | Signature                                                                                                           | Description                            |
| ----------------- | ------------------------------------------------------------------------------------------------------------------- | -------------------------------------- |
| `Set`             | `func (c *Cache[K, V]) Set(key K, value V)`                                                                         | Insert/update with global TTL          |
| `SetWithTTL`      | `func (c *Cache[K, V]) SetWithTTL(key K, value V, ttl time.Duration)`                                               | Insert/update with per-entry TTL       |
| `Get`             | `func (c *Cache[K, V]) Get(key K) (V, bool)`                                                                        | Lookup + promote to MRU                |
| `GetFast`         | `func (c *Cache[K, V]) GetFast(key K) (V, bool)`                                                                    | Lookup without promotion (read lock)   |
| `Peek`            | `func (c *Cache[K, V]) Peek(key K) (V, bool)`                                                                       | Lookup without promotion or stats      |
| `Has`             | `func (c *Cache[K, V]) Has(key K) bool`                                                                             | Existence check (non-expired)          |
| `Touch`           | `func (c *Cache[K, V]) Touch(key K) bool`                                                                           | Refresh TTL + promote to MRU           |
| `Delete`          | `func (c *Cache[K, V]) Delete(key K) bool`                                                                          | Remove a key                           |
| `GetEntry`        | `func (c *Cache[K, V]) GetEntry(key K) *Entry[K, V]`                                                                | Immutable snapshot with timestamps     |
| `TTL`             | `func (c *Cache[K, V]) TTL(key K) time.Duration`                                                                    | Remaining time-to-live                 |
| `Clear`           | `func (c *Cache[K, V]) Clear()`                                                                                     | Remove all entries                     |
| `Resize`          | `func (c *Cache[K, V]) Resize(capacity int)`                                                                        | Change capacity, evicting as needed    |
| `Len`             | `func (c *Cache[K, V]) Len() int`                                                                                   | Entry count (includes unswept expired) |
| `LenValid`        | `func (c *Cache[K, V]) LenValid() int`                                                                              | Non-expired entry count (O(n))         |
| `SetMulti`        | `func (c *Cache[K, V]) SetMulti(items map[K]V)`                                                                     | Bulk insert/update                     |
| `GetMulti`        | `func (c *Cache[K, V]) GetMulti(keys []K) map[K]V`                                                                  | Bulk lookup                            |
| `DeleteMulti`     | `func (c *Cache[K, V]) DeleteMulti(keys []K) int`                                                                   | Bulk delete                            |
| `Keys`            | `func (c *Cache[K, V]) Keys() []K`                                                                                  | All live keys (MRU first)              |
| `Values`          | `func (c *Cache[K, V]) Values() []V`                                                                                | All live values (MRU first)            |
| `Snapshot`        | `func (c *Cache[K, V]) Snapshot() []*Entry[K, V]`                                                                   | All live entries as snapshots          |
| `Range`           | `func (c *Cache[K, V]) Range(fn func(K, V) bool)`                                                                   | Iterate, stop when fn returns false    |
| `ExpireOld`       | `func (c *Cache[K, V]) ExpireOld() int`                                                                             | Remove all expired entries             |
| `GetOrCompute`    | `func (c *Cache[K, V]) GetOrCompute(key K, compute func() V, opts ...ComputeOption) V`                              | Lazy populate                          |
| `GetOrComputeCtx` | `func (c *Cache[K, V]) GetOrComputeCtx(ctx, key K, compute func(ctx) (V, error), opts ...ComputeOption) (V, error)` | Context + error aware populate         |
| `Stats`           | `func (c *Cache[K, V]) Stats() Stats`                                                                               | Counter snapshot                       |
| `ResetStats`      | `func (c *Cache[K, V]) ResetStats()`                                                                                | Zero counters                          |
| `Close`           | `func (c *Cache[K, V]) Close()`                                                                                     | Stop sweeper, drain, mark closed       |
| `IsClosed`        | `func (c *Cache[K, V]) IsClosed() bool`                                                                             | Closed-state check                     |


`ShardedCache` exposes the same surface (with `Resize(perShardCapacity int)`).

### Types


| Symbol               | Description                                                                                     |
| -------------------- | ----------------------------------------------------------------------------------------------- |
| `Cache[K, V]`        | Single-lock LRU cache                                                                           |
| `ShardedCache[K, V]` | Lock-sharded LRU cache                                                                          |
| `Entry[K, V]`        | Immutable snapshot: `Key`, `Value`, `CreatedAt`, `AccessedAt`, `ExpiresAt`                      |
| `Stats`              | `Size`, `Capacity`, `Hits`, `Misses`, `Evictions`, `HitRate` (JSON-tagged)                      |
| `EvictionReason`     | `EvictionCapacity`, `EvictionExpired`, `EvictionDeleted`, `EvictionCleared`, `EvictionReplaced` |
| `OnEvictFunc[K, V]`  | `func(key K, value V, reason EvictionReason)`                                                   |


## Configuration


| Option                        | Default        | Description                                       |
| ----------------------------- | -------------- | ------------------------------------------------- |
| `WithCapacity(n)`             | 0 (unbounded)  | Maximum entries; negatives clamp to 0             |
| `WithTTL(d)`                  | 0 (no expiry)  | Default per-entry TTL; negatives clamp to 0       |
| `WithOnEvict(fn)`             | nil            | Eviction callback (runs outside lock, panic-safe) |
| `WithCleanupInterval(d)`      | 0 (lazy)       | Background expired-entry sweep interval           |
| `WithShardCount(n)`           | 16             | Shard count, rounded up to a power of two         |
| `WithShardCapacity(n)`        | 0 (unbounded)  | Per-shard capacity (total = shards × n)           |
| `WithShardTTL(d)`             | 0              | Default TTL for all shards                        |
| `WithShardOnEvict(fn)`        | nil            | Shared eviction callback for all shards           |
| `WithShardCleanupInterval(d)` | 0              | Background sweep for all shards                   |
| `WithComputeTTL(d)`           | 0 (global TTL) | Per-call TTL for a computed value                 |
| `WithSingleflight()`          | off            | Deduplicate concurrent computes per key           |


## Errors


| Error         | Condition                                                            |
| ------------- | -------------------------------------------------------------------- |
| `ErrClosed`   | Returned by `GetOrComputeCtx` when the cache has been closed         |
| `ErrNotFound` | Available sentinel for compute functions that signal an absent value |


## Pitfalls

> [!WARNING]
> **An uncapped cache grows forever.** `New[K, V]()` with no `WithCapacity` and no `WithTTL` is an unbounded map with LRU bookkeeping. For any long-lived cache, set a capacity or a TTL plus a cleanup interval.

> [!WARNING]
> `**Range` holds the lock.** The callback passed to `Range` runs while the cache lock is held, so it must not call back into the same cache (deadlock) or block. For non-blocking iteration, use `Snapshot` and iterate the result.

> [!WARNING]
> `**Len` counts unswept expired entries.** `Len` is O(1) but includes entries whose TTL has elapsed yet have not been accessed or swept. Use `LenValid` for an exact live count (O(n)).

> [!WARNING]
> **Call `Close`.** A cache created with a cleanup interval owns a background goroutine. Failing to `Close` it leaks that goroutine for the lifetime of the process.

## Safety and Concurrency

All methods on `Cache` and `ShardedCache` are safe for concurrent use. Mutations and promotions take a write lock; `GetFast`, `Peek`, `Has`, `GetEntry`, `TTL`, and `Len` take a read lock. Statistics are maintained with `sync/atomic` and read without locking. Eviction callbacks and compute functions are invoked outside the lock and wrapped with `recover`, so user-code panics cannot corrupt cache state or crash the sweeper. `Close` is idempotent and gated by an atomic flag; every operation short-circuits once the cache is closed. The full suite passes under `-race`.

## Benchmarks

> CPU: Intel i7-10510U · OS: Windows 10 · Go 1.24 · `-benchmem -count=1`


| Benchmark                 | ns/op | B/op | allocs/op |
| ------------------------- | ----- | ---- | --------- |
| Cache_Set                 | 54    | 0    | 0         |
| Cache_Get_Hit             | 58    | 0    | 0         |
| Cache_Get_Miss            | 37    | 0    | 0         |
| Cache_GetFast_Hit         | 34    | 0    | 0         |
| Cache_Mixed (90% read)    | 62    | 0    | 0         |
| Cache_Get_Parallel        | 136   | 0    | 0         |
| Cache_GetOrCompute_Hit    | 74    | 0    | 0         |
| ShardedCache_Get_Parallel | 55    | 0    | 0         |
| ShardedCache_Set_Parallel | 147   | 71   | 0         |
| Hasher_String             | 11    | 0    | 0         |
| Hasher_Int                | 8     | 0    | 0         |


### Analysis

- **Allocation floor (reads): 0 allocs.** The intrusive list stores links inside the node, so a hit promotes an existing node with no allocation. `Get`, `GetFast`, `Peek`, and cache-hit `GetOrCompute` all hit zero.
- **Allocation floor (writes): 1 node per *new* key, amortized to 0.** `Cache_Set` reports 0 allocs/op because the benchmark reuses a fixed key set within capacity; inserting a genuinely new key costs exactly one `node`. `ShardedCache_Set_Parallel` shows 71 B/op (rounding to 0 allocs/op) from node churn under capacity pressure.
- **Hashing is free.** `maphash.Comparable` (Go 1.24) hashes any comparable key via the runtime's type hasher with no allocation — `Hasher_Int` at 8 ns/op replaced an earlier hand-rolled hasher that allocated 160 B/op per call.
- **Bottleneck (single cache): the write lock.** `Cache_Get_Parallel` at 136 ns/op vs 58 ns/op sequential reflects contention on the single `RWMutex` when `Get` promotes under a write lock.
- **Sharding scales reads.** `ShardedCache_Get_Parallel` at 55 ns/op is **2.5× faster** than the single-cache parallel read because 16 independent locks spread the contention. Use the sharded variant whenever more than a few goroutines hit the cache concurrently.

## Quality


| Metric         | Value                                                 |
| -------------- | ----------------------------------------------------- |
| Test functions | 94                                                    |
| Benchmarks     | 11                                                    |
| Fuzz targets   | 3                                                     |
| Examples       | 5                                                     |
| Coverage       | 95.5%                                                 |
| Race detector  | All pass                                              |
| External deps  | golang.org/x/sync (singleflight); testify (test only) |


## File Structure

```text
lrux/
├── doc.go              # package-level GoDoc
├── lrux.go             # Cache: core ops, intrusive list, lifecycle, sweeper
├── sharded.go          # ShardedCache: shard routing, parallel batch ops
├── compute.go          # GetOrCompute(Ctx) + singleflight dedup
├── hash.go             # maphash.Comparable shard hasher, keyString, nextPow2
├── options.go          # Option / ShardedOption / ComputeOption + defaults
├── errors.go           # ErrClosed, ErrNotFound sentinels
├── types.go            # EvictionReason, Entry, Stats, node
├── lrux_test.go        # Cache unit + table-driven + concurrent tests
├── sharded_test.go     # ShardedCache tests
├── compute_test.go     # compute double-check, singleflight, context tests
├── edge_test.go        # closed-state, expiry sweep, capacity edge cases
├── options_test.go     # option default/override/edge tests
├── hash_test.go        # hasher + keyString type coverage
├── bench_test.go       # sequential + parallel benchmarks
├── fuzz_test.go        # set/get round-trip + key-string fuzz targets
├── footprint_test.go   # struct size regression guards
├── example_test.go     # runnable GoDoc examples
└── README.md           # this file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.