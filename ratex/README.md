# ratex — Token-Bucket Rate Limiter with Panic Safety

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/ratex)
[License: MIT](../LICENSE)

A thread-safe token-bucket rate limiter offering three layers — non-blocking `Allow`, blocking `Wait`, and a panic-safe `Execute` wrapper that hands the callback a `RateController`. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **This is a single-process token-bucket limiter — not a distributed quota, not a concurrency cap (`bulkx`), and not a scheduler.** A `WaitN(ctx, n)` with `n > burst` can never succeed and blocks until the context is cancelled. For cluster-wide limits compose with a shared store (Redis, etc.) and use `ratex` as the local enforcement layer.

## The Problem

A token bucket is a textbook algorithm that production code keeps re-implementing — usually with at least one of these flaws:

1. **No execution wrapper.** A bare `Allow()` leaves every caller to hand-roll the "check, run, account" dance, and to remember to wrap risky work in panic recovery. The rest of the urx resilience family (`retryx`, `circuitx`, `bulkx`, `toutx`) all expose an `Execute`/`Do` that runs your function under the policy — `ratex` should too.
2. **No backpressure signal to the callee.** The function that just got admitted has no idea how close the bucket is to empty, so it cannot shed load gracefully (e.g. return a cheap partial result when spare capacity is low).
3. **No way to "un-spend" a token.** When an admitted call turns out to be a no-op (a cache hit that performed no downstream work), the consumed token is wasted — there is no standard refund hook.
4. **Busy-spin waits and unbounded requests.** Naive `Wait` loops burn CPU, and a request larger than the bucket capacity silently blocks forever instead of honouring the context.

`ratex` provides a single `Limiter` covering all four: `Allow`/`AllowN` (non-blocking), `Wait`/`WaitN` (context-aware blocking with a delay floor), and `Execute`/`TryExecute` (panic-safe, with a `RateController` exposing remaining tokens and a `SkipToken` refund hook) — `-race`-clean and 100% covered.

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
 waitFor (shared)    errors.go
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
| Token refund          | `RateController.SkipToken` or [Limiter.Release] returns tokens and rolls back one `Allowed` count |
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
| `DefaultRate`        | `const DefaultRate = 10.0`                                                                                          | Default sustained rate (req/s) when [WithRate] is omitted   |
| `DefaultBurst`       | `const DefaultBurst = 20`                                                                                           | Default bucket capacity when [WithBurst] is omitted         |
| `ErrCancelled`       | `var ErrCancelled error`                                                                                            | Context cancelled or deadline expired before admission      |
| `ErrNilFunc`         | `var ErrNilFunc error`                                                                                              | Nil callback passed to [Execute] or [TryExecute]            |
| `New`                | `func New(opts ...Option) *Limiter`                                                                                 | Create a limiter; non-positive rate/burst clamped to floors |
| `Limiter.Allow`      | `func (l *Limiter) Allow() bool`                                                                                    | Non-blocking: admit one request, consume one token          |
| `Limiter.AllowN`     | `func (l *Limiter) AllowN(n int) bool`                                                                              | Non-blocking: admit n requests atomically                   |
| `Limiter.Wait`       | `func (l *Limiter) Wait(ctx context.Context) error`                                                                 | Block until one token is available or ctx done              |
| `Limiter.WaitN`      | `func (l *Limiter) WaitN(ctx context.Context, n int) error`                                                         | Block until n tokens are available or ctx done              |
| `Execute`            | `func Execute[T any](l *Limiter, ctx context.Context, fn RateFunc[T]) (T, error)`                                  | Block for a token, then run fn panic-safe                   |
| `TryExecute`         | `func TryExecute[T any](l *Limiter, ctx context.Context, fn RateFunc[T]) (bool, T, error)`                          | Run fn only if a token is immediately available             |
| `Limiter.Tokens`     | `func (l *Limiter) Tokens() float64`                                                                                | Current available tokens (fractional)                       |
| `Limiter.Rate`       | `func (l *Limiter) Rate() float64`                                                                                  | Configured sustained rate                                   |
| `Limiter.Burst`      | `func (l *Limiter) Burst() int`                                                                                     | Configured bucket capacity                                  |
| `Limiter.Release`    | `func (l *Limiter) Release(n float64)`                                                                              | Refund n tokens and roll back one `Allowed` count           |
| `Limiter.Stats`      | `func (l *Limiter) Stats() Stats`                                                                                   | Snapshot of counters                                        |
| `Limiter.ResetStats` | `func (l *Limiter) ResetStats()`                                                                                    | Zero the allowed/limited counters                           |
| `Limiter.Reset`      | `func (l *Limiter) Reset()`                                                                                         | Refill the bucket to capacity                               |
| `Option`             | `type Option func(*config)`                                                                                         | Functional option for [New]                                 |
| `RateController`     | `type RateController interface`                                                                                     | Per-call controller passed to [Execute]/[TryExecute]        |
| `RateFunc`           | `type RateFunc[T any] func(ctx context.Context, rc RateController) (T, error)`                                      | Callback type for [Execute] and [TryExecute]                |
| `Stats`              | `type Stats struct { Rate, Burst, Tokens, Allowed, Limited }`                                                       | Point-in-time limiter snapshot                              |
| `WithRate`           | `func WithRate(r float64) Option`                                                                                   | Set sustained req/s; values ≤ 0 ignored, floor 1 in [New]   |
| `WithBurst`          | `func WithBurst(n int) Option`                                                                                      | Set bucket capacity; values below 1 raised to 1 in [New]    |


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
| `WithBurst(n)` | `DefaultBurst` (20) | Maximum tokens the bucket can hold; values below 1 are raised to 1 in [New] |


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

> [!NOTE]
> **`Release(n)` must match the admission unit.** It returns `n` tokens but rolls back exactly one `Allowed` count — the same contract as `AllowN(n)`/`WaitN(n)`. Use it when aborting a multi-token admission (as `quotax` does on context cancellation); do not call it with arbitrary partial amounts.

## Safety and Concurrency

All `Limiter` state (token balance, accrual clock, counters) is guarded by a single `sync.Mutex`; every public method is safe for concurrent use. `Execute`/`TryExecute` hold the lock only for the brief admission/refund steps — the user function runs **outside** the lock, so a slow callback never blocks other callers. The `RateController` is bound to one call and accessed only from that call's goroutine, requiring no synchronization. Every test runs under `-race`.

## Benchmarks

Three environments, two hardware classes, two operating systems. All values are **medians** of `-count=3` runs. `B/op` and `allocs/op` are deterministic — they depend on code, not hardware.

### Environments

| | Laptop | CI Server (Linux) | CI Server (Windows) |
|---|---|---|---|
| CPU | Intel Core i7-10510U, 4C/8T | AMD EPYC 7763, 4 vCPU | AMD EPYC 9V74, 4 vCPU |
| TDP | 15W (mobile, throttles) | 280W (server, stable) | server, stable |
| OS | Windows 10 | Ubuntu | Windows Server 2022 |
| Go | 1.26 | 1.26 | 1.26 |
| GOMAXPROCS | 8 | 4 | 4 |
| Source | `quality.result` | `benchmark-ubuntu-latest.txt` | `benchmark-windows-latest.txt` |

| Benchmark | What it measures | Laptop | Linux | Windows | B/op | allocs/op |
|---|---|---|---|---|---|---|
| `Allow` | Token-bucket admit (unlimited rate) | 25.3 ns | 87.6 ns | **14.3 ns** | 0 | 0 |
| `Allow_Parallel` | `Allow`, 8/4 goroutines | 62.0 ns | 108.5 ns | **41.5 ns** | 0 | 0 |
| `Execute` | Admit + empty callback | 62.2 ns | 121.6 ns | **59.5 ns** | 32 | 1 |
| `Execute_Parallel` | `Execute`, parallel | 143.5 ns | 169.6 ns | **96.2 ns** | 32 | 1 |
| `TryExecute` | Boolean admit variant | 58.2 ns | 117.9 ns | **56.6 ns** | 32 | 1 |

### Analysis

**Allow: 0 allocs — the architectural floor for a token bucket.** The hot path is a `refill` + compare + subtract under one mutex, with no heap escape. This holds on all three platforms.

**Single mutex, predictable parallel penalty.** `Allow_Parallel` is 1.6–2.6× slower per op than sequential — the single mutex serialises the critical section, which is the expected and intended trade-off for exact accounting (a lock-free atomic bucket cannot enforce an exact burst bound without CAS retries). Under 8 goroutines on the laptop the penalty is 2.4× (25 → 62 ns); under 4 goroutines on CI it is 1.2–2.9× depending on platform.

**CI Linux vs Windows on Allow — mutex fast-path variance.** Linux CI reports 87.6 ns for sequential `Allow` vs 14.3 ns on Windows CI — a 6× spread on identical-looking server VMs. Both paths execute the same Go code; the gap is EPYC 7763 (Linux) vs EPYC 9V74 (Windows) plus `futex(2)` vs `SRWLock` implementation differences on the uncontended mutex acquire/release. Laptop (25 ns) confirms the true cost is in the tens of nanoseconds, not hundreds — treat the Linux CI figure as an outlier of the runner, not the algorithm.

**Execute / TryExecute: 1 alloc / 32 B is the controller-pattern floor.** The per-call `execution` struct escapes to the heap because it is captured by the closure passed to `panix.Safe` — the same pattern as `retryx`, `bulkx`, and `circuitx`. Callers that do not need the controller or panic safety can use `Allow`/`Wait` for the zero-alloc path. `Execute_Parallel` adds mutex contention on top of the controller allocation; the user function itself still runs outside the lock once admitted.

## Quality


| Metric         | Value                          |
| -------------- | ------------------------------ |
| Test functions | 67                             |
| Benchmarks     | 5                              |
| Fuzz targets   | 3                              |
| Examples       | 4                              |
| Coverage       | 100.0%                         |
| Race detector  | All pass                       |
| External deps  | 0 (panix; testify in dev only) |


## File Structure

```text
ratex/
├── ratex.go            # package doc + Limiter, Allow/Wait, Execute/TryExecute
├── options.go          # config, defaults/floors, WithRate/WithBurst
├── types.go            # RateController + private execution impl, RateFunc
├── errors.go           # ErrCancelled, ErrNilFunc
├── ratex_test.go       # unit + table-driven tests
├── errors_test.go      # sentinel error wrapper tests
├── bench_test.go       # benchmarks (sequential + parallel)
├── fuzz_test.go        # FuzzAllowN, FuzzExecute, FuzzTryExecute
├── example_test.go     # runnable GoDoc examples
├── footprint_test.go   # struct size guards
└── README.md           # this file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.