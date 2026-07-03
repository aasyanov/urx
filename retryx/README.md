# retryx — Retry with Exponential Backoff, Jitter, and Panic Safety

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/retryx)
[License: MIT](../LICENSE)

A generic retry engine that re-executes a function with exponential backoff, decorrelating jitter, context-aware cancellation, per-attempt control, and panic recovery. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **By default every error is retried until the attempt budget is exhausted.** That is rarely what you want for a 4xx-class permanent failure. Supply `WithRetryIf` to classify errors, or call `RetryController.Abort()` inside the callback. Retrying a non-idempotent operation can duplicate side effects — make the operation idempotent or guard it.

## The Problem

Retry loops look trivial and are almost always wrong in production:

1. **Thundering herd.** A downstream blips, thousands of clients retry in lockstep, and the synchronized wave keeps it down. Fixed delays make this worse; correct retries need exponential backoff *and* jitter to decorrelate callers.
2. **Retrying the unretryable.** A naive loop retries a `400 Bad Request` five times, wasting latency and load on a request that can never succeed. Retryability must be a decision, not an assumption.
3. **Ignoring cancellation.** A loop that sleeps with `time.Sleep` cannot be cancelled — it keeps retrying long after the caller's context is gone, leaking goroutines and deadline budget.
4. **Panics in the callee.** The retried function touches user code and third-party libraries; an unrecovered panic kills the process instead of counting as a failed attempt.

`retryx` solves all four in one generic `Do`: capped exponential backoff with `[0.5, 1.5)` jitter, predicate-based retryability, cancellation honoured both between attempts and during backoff, and per-attempt panic recovery — `-race`-clean and 100% covered.

## Architectural Position

```text
✅ Do[T]            — retry a value-returning function with backoff + jitter
✅ RetryFunc[T]     — callback receives (ctx, RetryController) — consistent with all urx packages
✅ RetryController  — per-attempt number, previous error, elapsed time, Abort
✅ WithRetryIf      — classify which errors are worth retrying
✅ WithOp           — custom operation name in *panix.PanicError
✅ panic safety     — a panicking attempt becomes a *panix.PanicError, not a crash

❌ NOT a circuit breaker — it does not trip open on sustained failure (see circuitx)
❌ NOT a rate limiter or bulkhead (see ratex / bulkx)
❌ NOT a deadline enforcer — wrap each attempt with toutx if you need a per-try timeout
❌ NOT idempotency magic — retrying side effects is the caller's responsibility
```

### Position in the urx Stack

```text
┌───────────────────────────────────────────────────────────┐
│  service code: HTTP/RPC calls, DB queries, publishes      │
└────────────────────────┬──────────────────────────────────┘
                         │
┌────────────────────────▼──────────────────────────────────┐
│  retryx   Do[T] · RetryController · backoff + jitter      │
└──────────────┬────────────────────────┬───────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼───────────────────┐
│  panix.Safe            │   │  context · time · math/rand  │
│  (panic → PanicError)  │   │                              │
└────────────────────────┘   └──────────────────────────────┘
```

## Architecture

```text
                         retryx
   ┌───────────────────┬───────────────────┬───────────────────┐
   │                   │                   │                   │
 Do[T] (retryx.go)   Option (options.go)  RetryController
   │                 config{maxAttempts,   (types.go)
 loop i in 0..max     backoff,maxBackoff,  RetryFunc[T]
   │                  jitter,retryIf,       attempt{number,lastErr,
 panix.Safe(attempt)  onRetry,op}            start,aborted}
   │                   │                   Number/LastErr/
 isRetryable?         WithMaxAttempts       Elapsed/Abort
 abort? cancel?       WithBackoff
   │                  WithMaxBackoff        errors.go
 backoff() + sleep()  WithJitter/RetryIf    ErrExhausted/ErrCancelled
                      WithOnRetry           ErrAborted/ErrNilFunc
                      WithOp
```

## How It Works

```text
Do(ctx, fn, opts...)
  │ cfg = defaults → opts ; maxAttempts floored to 1
  ├── fn == nil ? ─────────────────────► ErrNilFunc
  │
  └── for i in 0 .. maxAttempts-1:
        ├── ctx cancelled ? ───────────► ErrCancelled(ctx.Err())
        ├── (val, err) = panix.Safe(fn(ctx,rc))  rc = {i+1, lastErr, start}
        ├── err == nil ? ──────────────► return val, nil
        ├── rc.Abort() called ? ───────► ErrAborted(i+1, err)
        ├── !retryable(err) ? ─────────► ErrExhausted(i+1, err)
        ├── last attempt ? ────────────► break
        ├── onRetry(i+1, err)
        └── sleep(ctx, backoff(i))     cancellable
             └── cancelled ? ──────────► ErrCancelled(ctx.Err())

  return ErrExhausted(maxAttempts, lastErr)
```

Each iteration builds a fresh `RetryController` carrying the 1-based attempt number, the previous attempt's error (`nil` on the first), and a shared start time for `Elapsed`. The callback runs under `panix.Safe`, so a panic is converted into a `*panix.PanicError` and treated as an ordinary failure — it can be retried or classified by `WithRetryIf` like any error.

After a retryable failure that is not the last attempt, `Do` sleeps for the computed backoff using a cancellable timer: if the context is cancelled mid-sleep, `Do` returns `ErrCancelled` immediately rather than waiting out the delay.

### Backoff

The nominal delay before the retry following attempt `i` (0-based) is `base * 2^i`, capped at `maxBackoff`. The cap is applied **before** jitter, then the capped delay is multiplied by a random factor in `[0.5, 1.5)`. Applying the cap first keeps the jitter window proportional and decorrelates retries across callers; the jittered value can briefly exceed `maxBackoff` by up to the jitter span, which is the intended spread.

## Normative Contracts


| Contract                   | Guarantee                                                                        |
| -------------------------- | -------------------------------------------------------------------------------- |
| Attempt budget             | `fn` is invoked at most `maxAttempts` times (floored to 1)                       |
| First-attempt cancellation | A pre-cancelled context returns `ErrCancelled` without invoking `fn`             |
| Backoff cancellation       | Cancellation during a backoff sleep returns `ErrCancelled` immediately           |
| Retryability               | Without `WithRetryIf`, every error is retried; with it, the predicate decides    |
| Abort                      | `RetryController.Abort` stops after the current attempt and returns `ErrAborted` |
| Panic safety               | A panicking attempt becomes a `*panix.PanicError`, handled as a normal failure   |
| Error wrapping             | Every terminal error joins the underlying cause (test with `errors.Is`)          |
| Controller scope           | A `RetryController` is valid only during its attempt; do not retain it           |


## Quick Start

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aasyanov/urx/retryx"
)

func main() {
	resp, err := retryx.Do(context.Background(),
		func(ctx context.Context, rc retryx.RetryController) (string, error) {
			return call(ctx)
		},
		retryx.WithMaxAttempts(5),
		retryx.WithBackoff(200*time.Millisecond),
	)

	switch {
	case errors.Is(err, retryx.ErrExhausted):
		fmt.Println("gave up:", err)
	case errors.Is(err, retryx.ErrCancelled):
		fmt.Println("cancelled:", err)
	case err != nil:
		fmt.Println("failed:", err)
	default:
		fmt.Println("ok:", resp)
	}
}

func call(_ context.Context) (string, error) { return "ok", nil }
```

## Usage Scenarios

### Classify retryable vs permanent errors

```go
_, err := retryx.Do(ctx, func(ctx context.Context, _ retryx.RetryController) (*Resp, error) {
	return client.Call(ctx, req)
}, retryx.WithRetryIf(func(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Status >= 500 // retry 5xx, not 4xx
}))
```

### Abort from inside the callback

```go
_, err := retryx.Do(ctx, func(ctx context.Context, rc retryx.RetryController) (Token, error) {
	tok, err := login(ctx)
	if errors.Is(err, ErrBadCredentials) {
		rc.Abort() // never going to succeed
	}
	return tok, err
}, retryx.WithMaxAttempts(5))
```

### Adapt behaviour using the controller

```go
_, err := retryx.Do(ctx, func(ctx context.Context, rc retryx.RetryController) (Data, error) {
	endpoint := primary
	if rc.Number() > 1 && isUnavailable(rc.LastErr()) {
		endpoint = secondary // fail over after the first failure
	}
	if rc.Elapsed() > budget {
		rc.Abort() // stop spending time
	}
	return fetch(ctx, endpoint)
})
```

### Observe retries for metrics

```go
_, err := retryx.Do(ctx, fetch,
	retryx.WithMaxAttempts(4),
	retryx.WithOnRetry(func(attempt int, err error) {
		metrics.Inc("fetch.retry")
		log.Printf("attempt %d failed: %v", attempt, err)
	}),
)
```

### Per-attempt timeout (compose with toutx)

```go
_, err := retryx.Do(ctx, func(ctx context.Context, _ retryx.RetryController) (Report, error) {
	return toutx.Execute(ctx, 500*time.Millisecond,
		func(ctx context.Context, _ toutx.TimeoutController) (Report, error) {
			return build(ctx)
		})
}, retryx.WithRetryIf(func(err error) bool {
	return errors.Is(err, toutx.ErrDeadlineExceeded) // retry slow attempts
}))
```

## API


| Symbol            | Signature                                                                                                | Description                                                             |
| ----------------- | -------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------- |
| `Do`              | `func Do[T any](ctx context.Context, fn RetryFunc[T], opts ...Option) (T, error)` | Retry fn with backoff until success, exhaustion, abort, or cancellation |
| `RetryFunc[T]`    | `func(ctx context.Context, rc RetryController) (T, error)`                        | Callback type handed to `Do`                                            |
| `WithMaxAttempts` | `func WithMaxAttempts(n int) Option`                                              | Total attempts including the first; ≤ 0 → 1                             |
| `WithBackoff`     | `func WithBackoff(d time.Duration) Option`                                        | Base backoff; ≤ 0 ignored                                               |
| `WithMaxBackoff`  | `func WithMaxBackoff(d time.Duration) Option`                                     | Cap on any single delay; ≤ 0 ignored                                    |
| `WithJitter`      | `func WithJitter(enabled bool) Option`                                            | Enable/disable `[0.5, 1.5)` jitter                                      |
| `WithRetryIf`     | `func WithRetryIf(fn func(error) bool) Option`                                   | Predicate deciding retryability                                         |
| `WithOnRetry`     | `func WithOnRetry(fn func(attempt int, err error)) Option`                        | Callback after each retryable failure                                   |
| `WithOp`          | `func WithOp(op string) Option`                                                  | Operation name in `*panix.PanicError`; empty ignored                     |
| `Option`          | `type Option func(*config)`                                                       | Functional option for `Do`                                              |
| `RetryController` | `interface{ Number; LastErr; Elapsed; Abort }`                                    | Per-attempt control handed to the callback                              |


### RetryController


| Method    | Signature                 | Description                                 |
| --------- | ------------------------- | ------------------------------------------- |
| `Number`  | `Number() int`            | 1-based attempt number                      |
| `LastErr` | `LastErr() error`         | Previous attempt's error (nil on the first) |
| `Elapsed` | `Elapsed() time.Duration` | Time spent in `Do` so far                   |
| `Abort`   | `Abort()`                 | Stop after the current attempt (idempotent) |


## Configuration


| Option               | Default                    | Description                                                     |
| -------------------- | -------------------------- | --------------------------------------------------------------- |
| `WithMaxAttempts(n)` | `DefaultMaxAttempts` (3)   | Total attempts including the first; ≤ 0 degrades to 1           |
| `WithBackoff(d)`     | `DefaultBackoff` (100 ms)  | Base for exponential backoff; ≤ 0 ignored                       |
| `WithMaxBackoff(d)`  | `DefaultMaxBackoff` (10 s) | Upper bound on a single delay; ≤ 0 ignored                      |
| `WithJitter(b)`      | enabled                    | Multiply the capped delay by a random `[0.5, 1.5)` factor       |
| `WithRetryIf(fn)`    | nil (retry all)            | Predicate deciding whether an error is retryable                |
| `WithOnRetry(fn)`    | nil                        | Callback invoked after each retryable failure, before the sleep |
| `WithOp(op)`         | `"retryx.Do"`              | Operation name in `*panix.PanicError`; empty strings ignored    |


## Errors


| Error          | Condition                                                                                   |
| -------------- | ------------------------------------------------------------------------------------------- |
| `ErrExhausted` | Every attempt failed, or a non-retryable error stopped the loop (wraps the last cause)      |
| `ErrCancelled` | The context was cancelled or expired, before an attempt or during backoff (wraps ctx.Err()) |
| `ErrAborted`   | The callback called `RetryController.Abort` (wraps the last cause)                          |
| `ErrNilFunc`   | The supplied function was nil                                                               |


A panicking attempt that exhausts the budget surfaces as `ErrExhausted` wrapping a `*panix.PanicError` (reach it with `errors.As`).

## Pitfalls

> [!WARNING]
> **Retries amplify load and duplicate side effects.** Retrying a non-idempotent write can double-charge, double-send, or double-insert. Make the operation idempotent (idempotency keys, conditional writes) before enabling retries.

> [!WARNING]
> **The default retries everything.** Without `WithRetryIf`, permanent failures (validation, auth) burn the full attempt budget. Always classify errors in production.

> [!NOTE]
> **retryx** does not impose a per-attempt deadline.** A single attempt can block indefinitely if `fn` ignores `ctx`. Wrap each attempt with `toutx.Execute` for a hard per-try timeout.

> [!NOTE]
> **Jitter can exceed `maxBackoff` slightly.** Because jitter is applied after the cap, a delay can reach up to `1.5 × maxBackoff`. This is intentional decorrelation, not a bug.

## Safety and Concurrency

`Do` is a pure function over its arguments and holds no shared state, so it is safe to call from any number of goroutines concurrently. Each call owns its `config`, its per-attempt `RetryController`, and its backoff timer; nothing is shared between calls. The `RetryController` is accessed only from the single goroutine running the callback and needs no synchronization. Jitter uses `math/rand/v2`, whose top-level functions are safe for concurrent use. Every test runs under `-race`.

## Benchmarks

> CPU: Intel i7-10510U · OS: Windows 10 · Go 1.24 · `-benchmem -count=1`


| Benchmark               | ns/op  | B/op | allocs/op |
| ----------------------- | ------ | ---- | --------- |
| Do_Success              | 90     | 112  | 2         |
| Do_Success_Parallel     | 61     | 112  | 2         |
| Do_SuccessAfterOneRetry | 392644 | 424  | 6         |
| Backoff                 | 24     | 0    | 0         |


### Analysis

- **Do_Success**: 2 allocs / 112 B is the success-path floor. One allocation is the `attempt` controller (it escapes because it is passed through the `panix.Safe` closure as an interface); the other is that closure itself. Both are inherent to handing the callback a controller under panic recovery. A controller-free, panic-free fast path could reach 0 allocs but would drop two of the package's guarantees.
- **Do_Success_Parallel**: ~61 ns, *faster* per op than serial — the path takes no lock and touches no shared counter, so it scales cleanly across cores; `math/rand/v2` is not even reached on the success path.
- **Do_SuccessAfterOneRetry**: ~~393 µs is dominated entirely by the real backoff sleep. Even with a 1 ns configured backoff, `time.NewTimer` cannot fire faster than the OS timer resolution (~~1 ms on Windows, coarser under load). This benchmark measures wall-clock sleep, not CPU work — the per-retry CPU cost is a handful of allocations for the timer and error wrapping.
- **Backoff**: 0 allocs, 24 ns — pure float math plus one `math/rand/v2` call; no heap involvement. This is the per-retry compute cost, dwarfed by the sleep it schedules.
- **Allocation floor**: the success path's 2 allocs are architectural (controller + recovery closure). The retry path adds the timer and error-wrapping allocations, which only occur on failure and are negligible next to the backoff sleep.

## Quality


| Metric         | Value                          |
| -------------- | ------------------------------ |
| Test functions | 26                             |
| Benchmarks     | 4                              |
| Fuzz targets   | 1                              |
| Examples       | 4                              |
| Coverage       | 100.0%                         |
| Race detector  | All pass                       |
| External deps  | 0 (panix; testify in dev only) |


## File Structure

```text
retryx/
├── retryx.go           # package doc + Do[T] + backoff/sleep/isRetryable
├── options.go          # config, Option, defaults, WithXxx
├── types.go            # RetryController + private attempt impl
├── errors.go           # ErrExhausted, ErrCancelled, ErrAborted, ErrNilFunc
├── retryx_test.go      # unit + table-driven tests
├── bench_test.go       # benchmarks (sequential + parallel)
├── fuzz_test.go        # FuzzDo — attempt-budget invariants
├── example_test.go     # runnable GoDoc examples
├── footprint_test.go   # struct size guards
└── README.md           # this file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.