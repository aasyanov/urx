# quotax — Per-Key Rate Limiting with Auto-Eviction

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/quotax)
[License: MIT](https://opensource.org/licenses/MIT)

Per-key token-bucket rate limiting: each key (user, IP, API key, tenant) gets its own independent bucket, keys are sharded to spread lock contention, and inactive keys are evicted automatically so memory stays bounded. Offers non-blocking `Allow`, blocking `Wait`, and a panic-safe `Execute` wrapper that hands the callback a `QuotaController`. Go 1.24+. Zero external dependencies (depends only on the urx `ratex` and `panix` packages; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> `quotax` is **per-key, single-process**. It does not coordinate across machines. For a cluster-wide per-key budget, back it with a shared store (Redis, etc.); `quotax` is the local enforcement layer. The global `ratex` limiter handles the single-bucket case — reach for `quotax` only when the limit is *per key*.

## The Problem

A single token bucket (`ratex`) limits an entire process. But real services must limit *per identity*: 100 req/s **per user**, 1000 req/s **per tenant**, 10 req/s **per IP**. Hand-rolling this keeps re-introducing the same defects:

1. **Unbounded memory.** A naive `map[string]*bucket` grows forever — every IP that ever connected stays resident. A botnet probing random keys becomes a memory-exhaustion vector.
2. **Lock contention.** One global mutex around the key map serialises every request across every key, destroying throughput under load.
3. **No execution wrapper.** A bare per-key `Allow()` leaves every caller to hand-roll the "check, run, account, recover from panic" dance, unlike the rest of the urx resilience family (`retryx`, `circuitx`, `ratex`, `shedx`).
4. **No per-key backpressure signal.** The admitted callback has no idea how close *this key* is to its limit, so it cannot degrade gracefully (serve a cheap partial result when the key is nearly throttled).

`quotax` solves all four: a sharded key map (configurable shard count), a background sweeper that evicts keys idle past a TTL, an optional hard cap on tracked keys, and an `Execute`/`TryExecute` layer with a `QuotaController` exposing the key's remaining tokens and a `SkipToken` refund hook — `-race`-clean and ~99% covered.

## Architectural Position

```text
✅ Quota                — per-key token buckets, sharded, auto-evicting
✅ Allow / AllowN       — non-blocking per-key admission check
✅ Wait / WaitN         — block until a key's tokens are available or ctx done
✅ Execute[T] / TryExecute[T] — run fn under a key's bucket, panic-safe
✅ QuotaController      — key / remaining tokens / rate / burst / waited + SkipToken
✅ MaxKeys + OnMaxKeys  — bounded key cardinality with a rejection callback
✅ Auto-eviction        — background sweeper removes idle keys

❌ NOT distributed — single-process; no shared store (compose with Redis etc.)
❌ NOT a global limiter — that is ratex (one bucket for the whole process)
❌ NOT a concurrency limiter — that is bulkx (slots, not tokens/time)
❌ NOT a cache — keys hold buckets, not values (see lrux)
```

### Position in the urx Stack

```text
┌──────────────────────────────────────────────────────────────┐
│  service code: API handlers, multi-tenant gateways           │
└────────────────────────┬─────────────────────────────────────┘
                         │ key = user / IP / tenant
┌────────────────────────▼─────────────────────────────────────┐
│  quotax  Quota · shards · eviction · Execute[T] · Controller │
└──────────────┬────────────────────────┬──────────────────────┘
               │ one bucket per key     │
┌──────────────▼─────────┐   ┌──────────▼──────────────────────┐
│  ratex.Limiter         │   │  panix.Safe                     │
│  (token bucket / key)  │   │  (panic → PanicError)           │
└────────────────────────┘   └─────────────────────────────────┘
```

## Architecture

```text
                            quotax
   ┌───────────────────┬───────────────────┬───────────────────┐
   │                   │                   │                   │
 Quota               Option (options.go)  QuotaController
 (quotax.go)         cfg config           (types.go)
 []shard / keyCount  rate/burst/shards    execution{key,tokens,
 allowed/limited     maxKeys/ttl/interval   rate,burst,waited,inner}
   │                 defaults              Key/Tokens/Rate/Burst/
 shardFor(maphash)     │                   Waited/SkipToken→ratex
 Allow/Wait            │                 errors.go
 Execute/TryExecute  evict() sweeper      ErrLimited/ErrMaxKeys/
 getOrCreate/reserve  evictLoop()         ErrCancelled/ErrClosed/ErrNilFunc
```

Each `shard` is `{sync.RWMutex; map[string]*bucket}`. A key hashes (via `hash/maphash`, allocation-free) to exactly one shard, so concurrent requests for keys on different shards never contend. Each `bucket` wraps a `ratex.Limiter` plus an atomic `lastAccess` timestamp the sweeper reads.

## How It Works

```text
Allow(key)
  │ closed ? ───────────────► false
  ├── shardFor(key) → shard
  ├── lookup (RLock): bucket exists ? ──► touch + ratex.AllowN  → record
  └── getOrCreate (Lock):
        ├── re-check under write lock (another goroutine may have created it)
        ├── reserveKey(): maxKeys cap reached ? ──► OnMaxKeys(key); return nil → false
        └── new bucket = ratex.New(rate, burst); store; ratex.AllowN → record

Execute(q, ctx, key, fn)
  │ fn == nil ? ──────────► ErrNilFunc
  │ closed ?    ──────────► ErrClosed
  ├── bucketForWait(key): create if absent (or ErrMaxKeys)
  ├── ratex.Execute(bucket.limiter, ctx, adapt(key, fn)):
  │     ├── blocks for a token (ctx-aware) — ratex owns the wait loop
  │     ├── qc = {key, tokens, rate, burst, waited, inner: ratex controller}
  │     ├── panix.Safe(fn(ctx, qc))   [panic → *panix.PanicError]
  │     └── qc.SkipToken() → delegates to ratex → refund token to key's bucket
  └── normalizeErr: ratex.ErrCancelled → quotax.ErrCancelled;
        retag PanicError.Op "ratex.Execute" → "quotax.Execute"

eviction (background ticker, interval = WithEvictionInterval)
  └── for each shard: RLock-collect keys with lastAccess < now-TTL,
        then Lock-delete each (re-checking lastAccess so freshly-touched keys survive)
```

Token accrual is delegated entirely to `ratex`: each key's bucket refills lazily (no per-key goroutine). The only background goroutine is the single eviction sweeper, started in `New` and stopped by `Close`. Blocking `WaitN` computes the time until the key's bucket holds enough tokens and sleeps at least `minWaitDelay` (1 ms) per iteration, re-checking the context each loop.

## Normative Contracts


| Contract            | Guarantee                                                                                                                                                                              |
| ------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Per-key isolation   | Each key has an independent bucket; draining one key never affects another                                                                                                             |
| Bounded cardinality | With `WithMaxKeys(n)`, the tracked key count never exceeds `n`, even under concurrency (CAS-guarded)                                                                                   |
| Memory reclaim      | A key idle longer than the eviction TTL is removed by the background sweeper                                                                                                           |
| Atomic admission    | A failed `AllowN` consumes **zero** tokens from the key's bucket                                                                                                                       |
| Context honoured    | `Wait`/`WaitN`/`Execute` return `ErrCancelled` (wrapping `ctx.Err()`) on cancellation                                                                                                  |
| No busy-spin        | Each wait iteration sleeps at least `minWaitDelay` (1 ms)                                                                                                                              |
| Panic safety        | A panicking `fn` becomes a `*panix.PanicError` tagged with the quotax op (`quotax.Execute` / `quotax.TryExecute`); the process never crashes; the key's slot is accounted correctly    |
| Nil guard           | A nil `fn` returns `ErrNilFunc` without consuming a token                                                                                                                              |
| Token refund        | `QuotaController.SkipToken` returns the token to the *key's* bucket                                                                                                                    |
| Eviction safety     | A key touched between the collect and delete phases survives the sweep; a key with an active blocked `WaitN` is touched each iteration, so it is never evicted out from under a waiter |
| Idempotent close    | `Close` is safe to call repeatedly; it stops the sweeper exactly once                                                                                                                  |
| Thread safety       | All `Quota` methods are safe for concurrent use                                                                                                                                        |


## Quick Start

```go
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/aasyanov/urx/quotax"
)

func main() {
	q := quotax.New(
		quotax.WithRate(100),          // 100 req/s per key
		quotax.WithBurst(20),          // burst up to 20 per key
		quotax.WithMaxKeys(1_000_000), // cap tracked keys (DoS protection)
	)
	defer q.Close()

	resp, err := quotax.Execute(q, context.Background(), "user:123",
		func(ctx context.Context, qc quotax.QuotaController) (string, error) {
			if qc.Tokens() < 5 {
				return cheap(ctx) // degrade while this user is near their limit
			}
			return call(ctx)
		})

	switch {
	case errors.Is(err, quotax.ErrCancelled):
		fmt.Println("cancelled:", err)
	case errors.Is(err, quotax.ErrMaxKeys):
		fmt.Println("too many distinct keys:", err)
	case err != nil:
		fmt.Println("failed:", err)
	default:
		fmt.Println("ok:", resp)
	}
}

func call(ctx context.Context) (string, error)  { return "full", nil }
func cheap(ctx context.Context) (string, error) { return "cheap", nil }
```

## Usage Scenarios

### Per-user API rate limit (drop on overflow)

```go
q := quotax.New(quotax.WithRate(100), quotax.WithBurst(20))
defer q.Close()

func handler(w http.ResponseWriter, r *http.Request) {
	if !q.Allow(userID(r)) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
		return
	}
	serve(w, r)
}
```

### Per-IP limit with cardinality cap (DoS protection)

```go
q := quotax.New(
	quotax.WithRate(10),
	quotax.WithBurst(5),
	quotax.WithMaxKeys(500_000), // refuse to track more than 500k IPs
	quotax.WithOnMaxKeys(func(ip string) { metrics.KeyCapHit.Inc() }),
)
defer q.Close()

if err := q.AllowOrError(clientIP); err != nil {
	// errors.Is(err, quotax.ErrMaxKeys) || errors.Is(err, quotax.ErrLimited)
	reject(err)
}
```

### Smooth per-tenant outbound traffic (block until admitted)

```go
if err := q.Wait(ctx, tenantID); err != nil {
	return err // context cancelled while waiting for the tenant's bucket
}
resp, err := upstream.Do(req)
```

### Panic-safe execution with per-key backpressure

```go
report, err := quotax.Execute(q, ctx, tenantID,
	func(ctx context.Context, qc quotax.QuotaController) (Report, error) {
		if qc.Tokens() < 1 {
			return partialReport(ctx), nil // degrade near the tenant's limit
		}
		return fullReport(ctx)
	})
```

### Refunding a token for a no-op (cache hit)

```go
val, err := quotax.Execute(q, ctx, userID,
	func(ctx context.Context, qc quotax.QuotaController) (Value, error) {
		if v, ok := cache.Get(key); ok {
			qc.SkipToken() // cache hit did no downstream work — don't charge the user
			return v, nil
		}
		return fetch(ctx, key)
	})
```

## API


| Symbol                | Signature                                                                                      | Description                                                    |
| --------------------- | ---------------------------------------------------------------------------------------------- | -------------------------------------------------------------- |
| `New`                 | `func New(opts ...Option) *Quota`                                                              | Create a quota and start the eviction sweeper                  |
| `Quota.Allow`         | `func (q *Quota) Allow(key string) bool`                                                       | Non-blocking: admit one request for key                        |
| `Quota.AllowN`        | `func (q *Quota) AllowN(key string, n int) bool`                                               | Non-blocking: admit n requests for key atomically              |
| `Quota.AllowOrError`  | `func (q *Quota) AllowOrError(key string) error`                                               | Like `Allow` but returns `ErrLimited`/`ErrMaxKeys`/`ErrClosed` |
| `Quota.Wait`          | `func (q *Quota) Wait(ctx, key) error`                                                         | Block until one token for key is available or ctx done         |
| `Quota.WaitN`         | `func (q *Quota) WaitN(ctx, key, n) error`                                                     | Block until n tokens for key are available or ctx done         |
| `Execute`             | `func Execute[T any](q *Quota, ctx, key, fn func(ctx, QuotaController) (T, error)) (T, error)` | Block for a token, then run fn panic-safe                      |
| `TryExecute`          | `func TryExecute[T any](q *Quota, ctx, key, fn) (bool, T, error)`                              | Run fn only if a token is immediately available                |
| `Quota.Remove`        | `func (q *Quota) Remove(key string) bool`                                                      | Delete a key's bucket; reports whether it existed              |
| `Quota.Exists`        | `func (q *Quota) Exists(key string) bool`                                                      | Whether a bucket is currently tracked for key                  |
| `Quota.KeyCount`      | `func (q *Quota) KeyCount() int64`                                                             | Number of currently tracked keys                               |
| `Quota.Reset`         | `func (q *Quota) Reset()`                                                                      | Remove all tracked keys                                        |
| `Quota.Stats`         | `func (q *Quota) Stats() Stats`                                                                | Aggregate counters snapshot                                    |
| `Quota.ResetStats`    | `func (q *Quota) ResetStats()`                                                                 | Zero the allowed/limited counters                              |
| `Quota.ForceEviction` | `func (q *Quota) ForceEviction()`                                                              | Run one eviction pass now (testing hook)                       |
| `Quota.Close`         | `func (q *Quota) Close() error`                                                                | Stop the sweeper; idempotent; always nil                       |
| `Quota.IsClosed`      | `func (q *Quota) IsClosed() bool`                                                              | Whether `Close` has been called                                |


### QuotaController


| Method      | Signature          | Description                                                        |
| ----------- | ------------------ | ------------------------------------------------------------------ |
| `Key`       | `Key() string`     | The key the call was admitted under                                |
| `Tokens`    | `Tokens() float64` | Tokens remaining in the key's bucket after this call               |
| `Rate`      | `Rate() float64`   | Per-key sustained rate                                             |
| `Burst`     | `Burst() int`      | Per-key bucket capacity                                            |
| `Waited`    | `Waited() bool`    | Whether the call blocked before admission (false for `TryExecute`) |
| `SkipToken` | `SkipToken()`      | Refund the consumed token to the key's bucket                      |


## Configuration


| Option                    | Default                        | Description                                        |
| ------------------------- | ------------------------------ | -------------------------------------------------- |
| `WithRate(r)`             | `DefaultRate` (10)             | Per-key sustained tokens/s; `r ≤ 0` ignored        |
| `WithBurst(n)`            | `DefaultBurst` (20)            | Per-key bucket capacity; `n ≤ 0` ignored           |
| `WithShards(n)`           | `DefaultShards` (64)           | Internal shard count; `n ≤ 0` ignored              |
| `WithMaxKeys(n)`          | `0` (unlimited)                | Hard cap on tracked keys; `n < 0` ignored          |
| `WithEvictionTTL(d)`      | `DefaultEvictionTTL` (15m)     | Idle time before a key is evicted; `d ≤ 0` ignored |
| `WithEvictionInterval(d)` | `DefaultEvictionInterval` (1m) | How often the sweeper runs; `d ≤ 0` ignored        |
| `WithOnMaxKeys(fn)`       | none                           | Callback on a key rejected by the cap              |


## Errors


| Error          | Condition                                                                     |
| -------------- | ----------------------------------------------------------------------------- |
| `ErrLimited`   | `AllowOrError`: the key's bucket is empty (wraps the key)                     |
| `ErrMaxKeys`   | A new key cannot be admitted because `WithMaxKeys` is reached (wraps the key) |
| `ErrCancelled` | The context was cancelled before a token was acquired (wraps the cause)       |
| `ErrClosed`    | An admission method was called after `Close`                                  |
| `ErrNilFunc`   | `Execute`/`TryExecute` received a nil function                                |


`Allow`/`AllowN` return a bare `bool` (no error). `TryExecute` returns `(false, zero, nil)` when no token is available — the rejection is a return value, not an error. A panicking function yields a `*panix.PanicError` (test with `errors.As`) whose `Op` is `quotax.Execute` or `quotax.TryExecute`, even though admission is delegated to `ratex` internally.

## Pitfalls

> [!WARNING]
> **A request larger than `burst` can never succeed.** `WaitN(ctx, key, n)` with `n > burst` blocks until the context is cancelled, because the key's bucket can never hold that many tokens. Size your burst accordingly.

> [!WARNING]
> `**WithMaxKeys` protects memory but degrades new keys.** Once the cap is hit, every *new* key is rejected (existing keys keep working). Pair it with a generous eviction TTL so idle keys free slots, and watch `OnMaxKeys` to detect cap saturation.

> [!NOTE]
> **Eviction is periodic, not immediate.** A key is reclaimed on the next sweep after its TTL elapses, so `KeyCount` can briefly exceed the steady-state working set. Use `ForceEviction` in tests for determinism.

> [!NOTE]
> `**quotax` is single-process.** Each `Quota` tracks its keys in memory. For a cluster-wide per-key limit, run a shared store — `quotax` is the local enforcement layer, not a distributed coordinator.

## Safety and Concurrency

Keys are partitioned across `WithShards` shards, each guarded by its own `sync.RWMutex`; requests for keys on different shards never contend. The read path (`lookup`) takes a read lock; only first-touch creation and removal take the write lock. The aggregate counters (`allowed`, `limited`, `keyCount`) and the closed flag are lock-free atomics, and the `WithMaxKeys` cap is enforced with a compare-and-swap loop so the bound holds exactly under contention. The user function in `Execute` runs **outside** all shard locks (the per-key `ratex.Limiter` owns its own brief lock), so a slow callback never blocks other keys. A single background sweeper goroutine performs eviction; `Close` stops it and blocks until it exits. Every test runs under `-race`.

## Benchmarks

> CPU: Intel i7-10510U · OS: Windows 10 · Go 1.24 · `-benchmem -count=3`


| Benchmark                   | ns/op | B/op | allocs/op |
| --------------------------- | ----- | ---- | --------- |
| Allow_Hit                   | ~62   | 0    | 0         |
| Allow_Hit_Parallel          | ~146  | 0    | 0         |
| Allow_DistinctKeys_Parallel | ~47   | 0    | 0         |
| Execute                     | ~212  | 96   | 2         |
| Execute_Parallel            | ~246  | 96   | 2         |
| TryExecute                  | ~219  | 96   | 2         |


### Analysis

- **Allow_Hit**: 0 allocs — the steady-state path is `maphash` (allocation-free) + an `RLock` map read + the underlying `ratex.AllowN` (a refill + compare + subtract under one short mutex). Key creation is the only allocating path, and it happens once per key, not per request. This is the architectural floor for per-key token-bucket admission.
- **Allow_DistinctKeys_Parallel** (~~47 ns) is *faster* than the single-key parallel benchmark (~~146 ns): spreading requests across 1024 keys spreads them across all 64 shards, so the per-shard `RWMutex` and each key's `ratex` mutex are essentially uncontended. The single-key parallel case funnels every goroutine through one shard and one bucket mutex — the ~2.3× slowdown versus the sequential hit is exactly that serialisation, and it is the intended trade-off for exact per-key accounting.
- **Execute / TryExecute**: 2 allocs / 96 B is the controller-pattern floor: one alloc for quotax's per-call `execution` struct (carries the key) and one for the inner `ratex` controller it wraps, both escaping to the heap because they are captured by the closure handed to `panix.Safe`. Callers that do not need the controller or panic safety should use `Allow`/`Wait` for the zero-alloc path.
- **Parallel scaling**: `Execute_Parallel` tracks the sequential figure closely because the shard and bucket locks are held only for the brief admission step; the (empty) user function and the two controller allocations parallelise cleanly across cores.

## Quality


| Metric         | Value                                 |
| -------------- | ------------------------------------- |
| Test functions | 39                                    |
| Benchmarks     | 6                                     |
| Fuzz targets   | 2                                     |
| Examples       | 4                                     |
| Coverage       | 99.6%                                 |
| Race detector  | All pass                              |
| External deps  | 0 (ratex, panix; testify in dev only) |


## File Structure

```text
quotax/
├── quotax.go           # package doc + Quota, Allow/Wait, Execute/TryExecute, eviction
├── options.go          # config, defaults, WithRate/WithBurst/WithShards/WithMaxKeys/...
├── types.go            # QuotaController + private execution impl + Stats
├── errors.go           # ErrLimited, ErrMaxKeys, ErrCancelled, ErrClosed, ErrNilFunc
├── quotax_test.go      # unit + table-driven tests
├── bench_test.go       # benchmarks (sequential + parallel + distinct keys)
├── fuzz_test.go        # FuzzAllow, FuzzExecute — cardinality + execution invariants
├── example_test.go     # runnable GoDoc examples
├── footprint_test.go   # struct size guards
└── README.md           # this file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.