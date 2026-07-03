# ratex — Token-Bucket Rate Limiter with Panic Safety

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/ratex)
[License: MIT](../LICENSE)

A thread-safe token-bucket rate limiter offering three layers — non-blocking `Allow`, blocking `Wait`, and a panic-safe `Execute` wrapper that hands the callback a `RateController`. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

```
go get github.com/aasyanov/urx
```

## The Problem

A token bucket is a textbook algorithm that production code keeps re-implementing — usually with at least one of these flaws:

1. **No execution wrapper.** A bare `Allow()` leaves every caller to hand-roll the "check, run, account" dance, and to remember to wrap risky work in panic recovery. The rest of the urx resilience family (`retryx`, `circuitx`, `bulkx`, `toutx`) all expose an `Execute`/`Do` that runs your function under the policy — `ratex` should too.
2. **No backpressure signal to the callee.** The function that just got admitted has no idea how close the bucket is to empty, so it cannot shed load gracefully (e.g. return a cheap partial result when spare capacity is low).
3. **No way to "un-spend" a token.** When an admitted call turns out to be a no-op (a cache hit that performed no downstream work), the consumed token is wasted — there is no standard refund hook.
4. **Busy-spin waits and unbounded requests.** Naive `Wait` loops burn CPU, and a request larger than the bucket capacity silently blocks forever instead of honouring the context.

`ratex` provides a single `Limiter` covering all four: `Allow`/`AllowN` (non-blocking), `Wait`/`WaitN` (context-aware blocking with a delay floor), and `Execute`/`TryExecute` (panic-safe, with a `RateController` exposing remaining tokens and a `SkipToken` refund hook) — `-race`-clean and ~98% covered.

## Architectural Position

```text
✅ Limiter            — token bucket: rate (req/s) + burst (capacity)
✅ Allow / AllowN     — non-blocking admission check
✅ Wait / WaitN       — block until tokens available or ctx done
✅ Execute[T] / TryExecute[T] — run fn under the limiter, panic-safe
✅ RateController     — remaining tokens / rate / burst / waited + SkipToken
✅ Stats              — allowed / limited / tokens snapshot

❌ NOT distributed — single-process; no shared store (compose with Redis etc.)
❌ NOT a concurrency limiter — that is bulkx (slots, not tokens/time)
❌ NOT a retry engine — see retryx
❌ NOT a scheduler — there is no queue or background worker
```

### Position in the urx Stack

```text
┌─────────────────────────────────────────────────────────┐
│  service code: API handlers, outbound clients, RPC      │
└────────────────────────┬────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────┐
│  ratex   Limiter · Allow/Wait · Execute[T] · Controller │
└──────────────┬────────────────────────┬─────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼─────────────────┐
│  panix.Safe            │   │  sync.Mutex + time         │
│  (panic → PanicError)  │   │  (token accrual clock)     │
└────────────────────────┘   └────────────────────────────┘
```

## Architecture

```text
                         ratex
   ┌───────────────────┬───────────────────┬───────────────────┐
   │                   │                   │                   │
 Limiter             Option (options.go)  RateController
 (ratex.go)          cfg config           (types.go)
 mu/tokens/clock     WithRate/WithBurst   execution{tokens,rate,
   │                 defaults + floors      burst,waited,skipToken}
 refill / take         │                   Tokens/Rate/Burst/
 Allow/Wait            │                   Waited/SkipToken
 Execute/TryExecute  errors.go
 acquire / run       ErrCancelled / ErrNilFunc
```

## How It Works

```text
Execute(l, ctx, fn)
  │ fn == nil ? ─────────────► return ErrNilFunc
  ├── acquire(ctx):
  │     ├── ctx done ? ───────► ErrCancelled(cause)
  │     ├── take 1 token ? ───► admitted (waited=false)
  │     └── loop: sleep delay(1); ctx done ? → ErrCancelled; retry take
  │
  └── run(l, op, waited, remaining, fn):
        ├── rc = {tokens, rate, burst, waited}
        ├── panix.Safe(op, fn(ctx, rc))   [panic → *panix.PanicError]
        └── rc.SkipToken() called ? ─────► refund 1 token, roll back Allowed
```

Tokens accrue lazily: every operation first calls `refill`, which adds `elapsed × rate` tokens (capped at `burst`) since the last update. There is no background goroutine — the bucket advances only when touched, so an idle limiter costs nothing.

`Wait`/`WaitN` compute the time until enough tokens accrue and sleep for at least `minDelay` (1 ms) per iteration, re-checking the context each loop so cancellation is always honoured. A request larger than `burst` can never be satisfied and therefore blocks until the context is cancelled — by design.

## Normative Contracts


| Contract              | Guarantee                                                                                                                                                                       |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Burst bound           | The bucket never holds more than `burst` tokens, regardless of idle time                                                                                                        |
| Atomic admission      | A failed `AllowN`/`take` consumes **zero** tokens                                                                                                                               |
| Context honoured      | `Wait`/`WaitN`/`Execute` return `ErrCancelled` (wrapping `ctx.Err()`) on cancellation                                                                                           |
| No busy-spin          | Each wait iteration sleeps at least `minDelay` (1 ms)                                                                                                                           |
| Panic safety          | A panicking `fn` becomes a `*panix.PanicError`; the process never crashes                                                                                                       |
| Nil guard             | A nil `fn` returns `ErrNilFunc` without consuming a token                                                                                                                       |
| Token refund          | `RateController.SkipToken` returns the token and rolls back the `Allowed` counter                                                                                               |
| `n < 1` normalisation | `AllowN`/`WaitN` treat non-positive `n` as 1                                                                                                                                    |
| Outcome-based stats   | `Allowed`/`Limited` count *final outcomes*, never internal wait-loop probes: a blocking `Wait`/`Execute` adds exactly one `Allowed` on success or one `Limited` on cancellation |
| Thread safety         | All `Limiter` methods are safe for concurrent use                                                                                                                               |


## Quick Start

```go
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/aasyanov/urx/ratex"
)

func main() {
	rl := ratex.New(
		ratex.WithRate(100), // 100 req/s sustained
		ratex.WithBurst(20), // allow short bursts up to 20
	)

	resp, err := ratex.Execute(rl, context.Background(),
		func(ctx context.Context, rc ratex.RateController) (string, error) {
			if rc.Tokens() < 5 {
				return cheap(ctx) // shed load while spare capacity is low
			}
			return call(ctx)
		})

	switch {
	case errors.Is(err, ratex.ErrCancelled):
		fmt.Println("cancelled:", err)
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

### Non-blocking admission (drop on overflow)

```go
if !rl.Allow() {
	http.Error(w, "rate limited", http.StatusTooManyRequests)
	return
}
serve(w, r)
```

### Blocking until admitted (smooth outbound traffic)

```go
if err := rl.Wait(ctx); err != nil {
	return err // context cancelled while waiting
}
resp, err := client.Do(req)
```

### Panic-safe execution with backpressure

```go
report, err := ratex.Execute(rl, ctx,
	func(ctx context.Context, rc ratex.RateController) (Report, error) {
		if rc.Tokens() < 1 {
			return partialReport(ctx), nil // degrade near the limit
		}
		return fullReport(ctx)
	})
```

### Refunding a token for a no-op

```go
val, err := ratex.Execute(rl, ctx,
	func(ctx context.Context, rc ratex.RateController) (Value, error) {
		if v, ok := cache.Get(key); ok {
			rc.SkipToken() // cache hit did no downstream work — don't charge it
			return v, nil
		}
		return fetch(ctx, key)
	})
```

### Non-blocking execution

```go
ok, val, err := ratex.TryExecute(rl, ctx,
	func(ctx context.Context, _ ratex.RateController) (Value, error) {
		return fetch(ctx)
	})
if !ok {
	return fallback() // no token available, fn was not run
}
```

## API


| Symbol               | Signature                                                                                                           | Description                                                 |
| -------------------- | ------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------- |
| `New`                | `func New(opts ...Option) *Limiter`                                                                                 | Create a limiter; non-positive rate/burst clamped to floors |
| `Limiter.Allow`      | `func (l *Limiter) Allow() bool`                                                                                    | Non-blocking: admit one request, consume one token          |
| `Limiter.AllowN`     | `func (l *Limiter) AllowN(n int) bool`                                                                              | Non-blocking: admit n requests atomically                   |
| `Limiter.Wait`       | `func (l *Limiter) Wait(ctx context.Context) error`                                                                 | Block until one token is available or ctx done              |
| `Limiter.WaitN`      | `func (l *Limiter) WaitN(ctx context.Context, n int) error`                                                         | Block until n tokens are available or ctx done              |
| `Execute`            | `func Execute[T any](l *Limiter, ctx context.Context, fn func(ctx, RateController) (T, error)) (T, error)`          | Block for a token, then run fn panic-safe                   |
| `TryExecute`         | `func TryExecute[T any](l *Limiter, ctx context.Context, fn func(ctx, RateController) (T, error)) (bool, T, error)` | Run fn only if a token is immediately available             |
| `Limiter.Tokens`     | `func (l *Limiter) Tokens() float64`                                                                                | Current available tokens (fractional)                       |
| `Limiter.Rate`       | `func (l *Limiter) Rate() float64`                                                                                  | Configured sustained rate                                   |
| `Limiter.Burst`      | `func (l *Limiter) Burst() int`                                                                                     | Configured bucket capacity                                  |
| `Limiter.Stats`      | `func (l *Limiter) Stats() Stats`                                                                                   | Snapshot of counters                                        |
| `Limiter.ResetStats` | `func (l *Limiter) ResetStats()`                                                                                    | Zero the allowed/limited counters                           |
| `Limiter.Reset`      | `func (l *Limiter) Reset()`                                                                                         | Refill the bucket to capacity                               |
| `WithRate`           | `func WithRate(r float64) Option`                                                                                   | Set sustained req/s; values ≤ 0 ignored                     |
| `WithBurst`          | `func WithBurst(n int) Option`                                                                                      | Set bucket capacity; values ≤ 0 ignored                     |


### RateController


| Method      | Signature          | Description                                                               |
| ----------- | ------------------ | ------------------------------------------------------------------------- |
| `Tokens`    | `Tokens() float64` | Tokens remaining after this call's token was consumed                     |
| `Rate`      | `Rate() float64`   | Limiter's sustained rate                                                  |
| `Burst`     | `Burst() int`      | Limiter's bucket capacity                                                 |
| `Waited`    | `Waited() bool`    | Whether the call blocked before admission (always false for `TryExecute`) |
| `SkipToken` | `SkipToken()`      | Refund the consumed token for a no-op call                                |


## Configuration


| Option         | Default             | Description                                                  |
| -------------- | ------------------- | ------------------------------------------------------------ |
| `WithRate(r)`  | `DefaultRate` (10)  | Sustained tokens added per second; `r ≤ 0` ignored, floor 1  |
| `WithBurst(n)` | `DefaultBurst` (20) | Maximum tokens the bucket can hold; `n ≤ 0` ignored, floor 1 |


## Errors


| Error          | Condition                                                                          |
| -------------- | ---------------------------------------------------------------------------------- |
| `ErrCancelled` | The context was cancelled or expired before a token was acquired (wraps the cause) |
| `ErrNilFunc`   | The supplied function was nil                                                      |


`TryExecute` does not return a "rate limited" error — when no token is available it returns `(false, zero, nil)`, leaving the decision to the caller.

A panicking function does not produce a sentinel — it returns a `*panix.PanicError` (test with `errors.As`).

## Pitfalls

> [!WARNING]
> **A request larger than `burst` can never succeed.** `WaitN(ctx, n)` with `n > burst` blocks until the context is cancelled, because the bucket can never hold that many tokens. Size your burst accordingly.

> [!NOTE]
> **The limiter is single-process.** Each `Limiter` tracks its own bucket in memory. For a cluster-wide limit, run a shared store (Redis, etc.) — `ratex` is the local enforcement layer, not a distributed coordinator.

> [!NOTE]
> **SkipToken** refunds at most one token and rolls back one `Allowed` count.** Use it for genuine no-ops; calling it on real work undercounts your throughput statistics.

## Safety and Concurrency

All `Limiter` state (token balance, accrual clock, counters) is guarded by a single `sync.Mutex`; every public method is safe for concurrent use. `Execute`/`TryExecute` hold the lock only for the brief admission/refund steps — the user function runs **outside** the lock, so a slow callback never blocks other callers. The `RateController` is bound to one call and accessed only from that call's goroutine, requiring no synchronization. Every test runs under `-race`.

## Benchmarks

> CPU: Intel i7-10510U · OS: Windows 10 · Go 1.24 · `-benchmem -count=3`


| Benchmark        | ns/op | B/op | allocs/op |
| ---------------- | ----- | ---- | --------- |
| Allow            | ~40   | 0    | 0         |
| Allow_Parallel   | ~78   | 0    | 0         |
| Execute          | ~180  | 32   | 1         |
| Execute_Parallel | ~185  | 32   | 1         |
| TryExecute       | ~165  | 32   | 1         |


### Analysis

- **Allow**: 0 allocs — the hot path is a `refill` + compare + subtract under one mutex, with no heap escape. This is the architectural floor for a token bucket.
- **Allow_Parallel**: ~2× slower per op than the sequential path under 8 goroutines — the single mutex serialises the critical section, which is the expected and intended trade-off for exact accounting (a lock-free atomic bucket cannot enforce an exact burst bound without CAS retries).
- **Execute / TryExecute**: 1 alloc / 32 B is the cost of the per-call `execution` controller struct, which escapes to the heap because it is captured by the closure passed to `panix.Safe`. It is the same controller-pattern floor seen across `retryx`, `bulkx`, and `circuitx`. Callers that do not need the controller or panic safety can use `Allow`/`Wait` for the zero-alloc path.
- **Parallel scaling**: `Execute_Parallel` barely differs from the sequential figure because the lock is held only for the admission step; the (empty) user function and the controller allocation parallelise cleanly across cores.

## Quality


| Metric         | Value                          |
| -------------- | ------------------------------ |
| Test functions | 36                             |
| Benchmarks     | 5                              |
| Fuzz targets   | 2                              |
| Examples       | 4                              |
| Coverage       | 98.0%                          |
| Race detector  | All pass                       |
| External deps  | 0 (panix; testify in dev only) |


## File Structure

```text
ratex/
├── ratex.go            # package doc + Limiter, Allow/Wait, Execute/TryExecute
├── options.go          # config, defaults/floors, WithRate/WithBurst
├── types.go            # RateController + private execution impl
├── errors.go           # ErrCancelled, ErrNilFunc
├── ratex_test.go       # unit + table-driven tests
├── bench_test.go       # benchmarks (sequential + parallel)
├── fuzz_test.go        # FuzzAllowN, FuzzExecute — bucket invariants
├── example_test.go     # runnable GoDoc examples
├── footprint_test.go   # struct size guards
└── README.md           # this file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.