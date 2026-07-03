# warmupx — Gradual Capacity Ramp-Up with Probabilistic Admission

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/warmupx)
[License: MIT](../LICENSE)

A slow-start admission controller that ramps a service from a minimum to a maximum capacity over a configurable duration, admitting traffic probabilistically while it warms. Four ramp strategies — linear, exponential, logarithmic, step — plus generic `Execute`/`TryExecute` wrappers with per-call control and panic recovery. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **warmupx is probabilistic, not a token bucket.** At capacity `0.5` each `Allow` independently returns true with probability ~0.5 — it does **not** guarantee "exactly half" over any window, and it does **not** queue or pace requests. For hard rate limits use `ratex`; for hard concurrency caps use `bulkx`. warmupx shapes the *fraction* of traffic a cold instance accepts, nothing more.

## The Problem

A freshly started service instance is not ready for its steady-state load, even when its health check passes:

1. **Cold caches.** The first thousand requests all miss the cache and stampede the database, which was provisioned for a warm hit rate. The instance is marked healthy, the load balancer sends full traffic, and the instance falls over.
2. **Unprimed runtime.** JIT compilation, lazy class loading, connection-pool establishment, and TLS session caches all need real traffic to warm. A full spike on a cold process produces latency spikes and timeouts.
3. **Recovery thundering herd.** A circuit breaker reopens, or an autoscaler adds an instance, and 100% of the intended traffic arrives in the same second — exactly when the instance is least able to absorb it.

The fix is *slow start*: accept a small fraction of traffic immediately, then increase the admitted fraction smoothly until the instance carries its full share. warmupx implements this as a standalone, thread-safe admission controller with four ramp curves and a per-call control surface.

## Architectural Position

```text
✅ Warmer            — ramps an admission capacity from min to max over a duration
✅ Allow / MaxRequests — probabilistic gate + capacity-scaled limit
✅ Execute[T] / TryExecute[T] — run fn only if admitted, with a WarmupController
✅ WarmupController  — capacity/progress/strategy at admission + late Reject
✅ panic safety      — a panicking callback becomes a *panix.PanicError, not a crash

❌ NOT a rate limiter — no fixed requests/second guarantee (see ratex)
❌ NOT a bulkhead — no hard concurrency cap (see bulkx)
❌ NOT a circuit breaker — it does not trip on failure (see circuitx); pair them on recovery
❌ NOT a queue — rejected requests are rejected, never buffered or delayed
❌ NOT a health check — readiness is time-driven, not dependency-probed
```

### Position in the urx Stack

```text
┌──────────────────────────────────────────────────────────┐
│  service code: post-deploy rollout, cold-start instances   │
└────────────────────────┬─────────────────────────────────┘
                         │
┌────────────────────────▼─────────────────────────────────┐
│  warmupx  Warmer · Allow · Execute[T]/TryExecute[T] · WarmupController   │
└──────────────┬───────────────────────┬───────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼───────────────────┐
│  panix.Safe            │   │  sync/atomic · time.Ticker   │
│  (panic → PanicError)  │   │  (probabilistic admission)   │
└────────────────────────┘   └──────────────────────────────┘
```

## Architecture

```text
                         Warmer
   ┌────────────────────────────────────────────────────────┐
   │  cfg: strategy, minCap, maxCap, duration, interval     │
   │                                                        │
   │  mu (RWMutex) ── capacity, start, warming, complete    │
   │  gen (uint64) ── identifies the active ramp run        │
   │  allowed / rejected (atomic.Int64)                     │
   └───────────────┬───────────────────────┬────────────────┘
                   │ Start()               │ Allow() / Execute() / TryExecute()
        ┌──────────▼──────────┐   ┌────────▼───────────────┐
        │  loop goroutine     │   │  rand.Float64() < cap? │
        │  ticker @ interval  │   │   yes → admit          │
        │  tick(gen):         │   │   no  → reject         │
        │   capacity =        │   └────────────────────────┘
        │     calculate(t)    │
        │   t = elapsed/dur   │
        └─────────────────────┘
```

## How It Works

A `Warmer` runs a single background goroutine while ramping. On `Start`, it records the start time, sets `warming = true`, bumps a generation counter, and spawns a loop that ticks at the update interval:

```text
Start → spawn loop(gen)
  │
  └── every interval: tick(gen)
        ├── gen mismatch / stopped / complete → exit loop
        ├── elapsed ≥ duration → capacity = maxCap; complete = true;
        │                        close(completeCh); fire onComplete; exit
        └── else → t = elapsed/duration; capacity = calculate(t);
                   fire onCapacityChange if |Δ| > 1%
```

Admission is independent of the loop. `Allow` reads the current capacity under a read lock and compares it to `rand.Float64()`; `Execute` and `TryExecute` do the same, then run the callback under `panix.Safe`. The loop only *moves* the capacity; reads never block on it.

### Generation guard

`Start`, `StartAt`, and `Reset` bump `gen` and install a fresh stop channel. A stale loop goroutine from a previous run sees the newer `gen` on its next tick and exits immediately, so overlapping restarts cannot corrupt state or double-close channels. `Stop` closes the stop channel and clears it; calling it twice is safe.

### Ramp strategies

For fractional progress `t ∈ [0, 1]` and `delta = maxCap − minCap`, capacity is `minCap + delta · f(t)`:


| Strategy      | f(t)                      | Shape                                     |
| ------------- | ------------------------- | ----------------------------------------- |
| `Linear`      | `t`                       | Uniform — equal increments per unit time  |
| `Exponential` | `1 − e^(−k·t)`            | Fast early, flattens late (k = ExpFactor) |
| `Logarithmic` | `ln(1 + t·e) / ln(1 + e)` | Slow early, accelerates late              |
| `Step`        | `⌊t·n⌋ / n`               | n discrete jumps (n = StepCount)          |


`t` is clamped to `[0, 1]` inside `calculate`, so the curve functions always stay in their valid domain.

## Normative Contracts


| Invariant               | Guarantee                                                                                                  |
| ----------------------- | ---------------------------------------------------------------------------------------------------------- |
| Capacity range          | `Capacity()` is always in `[minCap, maxCap] ⊆ [0, 1]`                                                      |
| Monotonic ramp          | For all four strategies, capacity is non-decreasing in `t`                                                 |
| Completion latch        | Once complete, `Capacity() == maxCap`, `Progress() == 1`, `IsComplete()` stays true until the next `Start` |
| `Stop` retains capacity | `Stop` freezes the current capacity; it is not reset to min                                                |
| `Execute` rejection     | A rejected `Execute` never invokes the callback                                                            |
| `TryExecute` rejection  | Probabilistic rejection returns `(false, zero, nil)` and never invokes the callback                        |
| Cancelled ctx           | `Execute`/`TryExecute` return `ErrCancelled` before admission; counters unchanged                         |
| Panic safety            | A panicking `Execute`/`TryExecute` callback becomes a `*panix.PanicError`; counters stay at pre-panic admission state |
| Goroutine lifecycle     | Exactly one loop goroutine per active ramp; it exits on `Stop`, completion, or generation change           |
| Concurrency             | All methods are safe for concurrent use; `-race` clean                                                     |


## Quick Start

```go
package main

import (
	"context"
	"time"

	"github.com/aasyanov/urx/warmupx"
)

func main() {
	w := warmupx.New(
		warmupx.WithDuration(30*time.Second),
		warmupx.WithStrategy(warmupx.Exponential),
	)
	w.Start()
	defer w.Stop()

	out, err := warmupx.Execute(w, context.Background(),
		func(ctx context.Context, wc warmupx.WarmupController) (int, error) {
			// Scale work to readiness: smaller batches while cold.
			batch := int(wc.Capacity() * 100)
			return serve(ctx, batch)
		})
	if err != nil {
		return
	}
	_ = out
}

func serve(ctx context.Context, batch int) (int, error) { return batch, nil }
```

## Usage Scenarios

### Try without error handling during warmup

When rejection is the common case and a boolean is clearer than `errors.Is`, use `TryExecute`:

```go
ok, batch, err := warmupx.TryExecute(w, ctx,
	func(ctx context.Context, wc warmupx.WarmupController) (int, error) {
		return prefetch(ctx, int(wc.Capacity()*100))
	})
if err != nil {
	return err // nil fn or callback/panic error
}
if !ok {
	return nil // still warming — no ErrRejected to unwrap
}
_ = batch
```

### Gate an HTTP handler during cold start

```go
w := warmupx.New(warmupx.WithDuration(20 * time.Second))
w.Start()

func(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if err := w.AllowOrError(); err != nil {
			rw.Header().Set("Retry-After", "1")
			http.Error(rw, "warming up", http.StatusServiceUnavailable)
			return
		}
		next.ServeHTTP(rw, r)
	})
}
```

### Scale a worker pool to readiness

```go
w := warmupx.New(warmupx.WithStrategy(warmupx.Step), warmupx.WithStepCount(5))
w.Start()

for range time.Tick(time.Second) {
	workers := w.MaxRequests(runtime.NumCPU() * 8) // grows as the instance warms
	pool.Resize(workers)
	if w.IsComplete() {
		break
	}
}
```

### Recover after a circuit breaker reopens

```go
// On Open → HalfOpen → Closed transition, restart the ramp so the recovered
// downstream is not hit with full traffic at once.
breaker.OnClose(func() { w.StartAt(0.05) })

resp, err := warmupx.Execute(w, ctx, func(ctx context.Context, wc warmupx.WarmupController) (*Response, error) {
	if wc.Progress() < 0.2 {
		wc.Reject() // too early; shed this request
		return nil, nil
	}
	return downstream.Call(ctx)
})
```

### Block startup until fully warm

```go
w := warmupx.New(warmupx.WithDuration(time.Minute))
w.Start()
ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
defer cancel()
if err := w.WaitForCompletion(ctx); err != nil {
	log.Fatalf("warmup did not complete: %v", err)
}
```

## API


| Symbol                     | Signature                                                                                                               | Description                                  |
| -------------------------- | ----------------------------------------------------------------------------------------------------------------------- | -------------------------------------------- |
| `New`                      | `func New(opts ...Option) *Warmer`                                                                                      | Create a warmer; does not ramp until `Start` |
| `Warmer.Start`             | `func (w *Warmer) Start()`                                                                                              | Begin/restart ramp from `minCap`             |
| `Warmer.StartAt`           | `func (w *Warmer) StartAt(capacity float64)`                                                                            | Begin/restart ramp from a clamped capacity   |
| `Warmer.Stop`              | `func (w *Warmer) Stop()`                                                                                               | Halt ramp, retain capacity (idempotent)      |
| `Warmer.Reset`             | `func (w *Warmer) Reset()`                                                                                              | Stop and restart from `minCap`               |
| `Warmer.Capacity`          | `func (w *Warmer) Capacity() float64`                                                                                   | Current capacity in `[0, 1]`                 |
| `Warmer.Progress`          | `func (w *Warmer) Progress() float64`                                                                                   | Warmup progress in `[0, 1]`                  |
| `Warmer.Strategy`          | `func (w *Warmer) Strategy() Strategy`                                                                                  | Configured ramp strategy                     |
| `Warmer.IsWarming`         | `func (w *Warmer) IsWarming() bool`                                                                                     | Whether a ramp is in progress                |
| `Warmer.IsComplete`        | `func (w *Warmer) IsComplete() bool`                                                                                    | Whether warmup reached full capacity         |
| `Warmer.Allow`             | `func (w *Warmer) Allow() bool`                                                                                         | Probabilistic admission decision             |
| `Warmer.AllowOrError`      | `func (w *Warmer) AllowOrError() error`                                                                                 | nil if admitted, else wraps `ErrRejected`    |
| `Warmer.MaxRequests`       | `func (w *Warmer) MaxRequests(baseLimit int) int`                                                                       | `baseLimit · capacity`, rounded up           |
| `Warmer.WaitForCompletion` | `func (w *Warmer) WaitForCompletion(ctx context.Context) error`                                                         | Block until complete or ctx done             |
| `Warmer.Stats`             | `func (w *Warmer) Stats() Stats`                                                                                        | Snapshot of state and counters               |
| `Warmer.ResetStats`        | `func (w *Warmer) ResetStats()`                                                                                         | Zero allowed/rejected counters               |
| `Execute`                   | `func Execute[T any](w *Warmer, ctx context.Context, fn WarmupFunc[T]) (T, error)` | Run fn only if admitted; rejection is an error |
| `TryExecute`                | `func TryExecute[T any](w *Warmer, ctx context.Context, fn WarmupFunc[T]) (bool, T, error)` | Run fn only if admitted; rejection is `(false, zero, nil)` |
| `WarmupController.Capacity` | `Capacity() float64` | Capacity in `[0, 1]` at admission time |
| `WarmupController.Progress` | `Progress() float64` | Warmup progress in `[0, 1]` at admission time |
| `WarmupController.Strategy` | `Strategy() Strategy` | Configured ramp strategy at admission time |
| `WarmupController.Reject`   | `Reject()` | Late reject: discard result and return `ErrRejected` |
| `WarmupFunc`                | `type WarmupFunc[T any] func(context.Context, WarmupController) (T, error)` | Unit of work for `Execute` and `TryExecute` |
| `Strategy`                  | `type Strategy uint8` | Ramp curve selector (`Linear`, `Exponential`, `Logarithmic`, `Step`) |
| `Strategy.String`          | `func (s Strategy) String() string`                                                                                     | Human-readable label                         |
| `Stats`                    | `type Stats struct { ... }`                                                                                             | Observability snapshot                       |


## Configuration


| Option                     | Default                                 | Description                                                |
| -------------------------- | --------------------------------------- | ---------------------------------------------------------- |
| `WithStrategy(s)`          | `Linear`                                | Ramp curve: `Linear`, `Exponential`, `Logarithmic`, `Step` |
| `WithMinCapacity(v)`       | `0.1`                                   | Starting capacity, clamped to `[0, 1]`                     |
| `WithMaxCapacity(v)`       | `1.0`                                   | Target capacity, clamped to `(0, 1]`                       |
| `WithDuration(d)`          | `1m`                                    | Total ramp duration                                        |
| `WithInterval(d)`          | `duration/100`, clamped to `[10ms, 1s]` | Capacity-update tick interval                              |
| `WithStepCount(n)`         | `10`                                    | Discrete jumps for `Step`                                  |
| `WithExpFactor(f)`         | `3.0`                                   | Steepness of `Exponential`                                 |
| `WithOnCapacityChange(fn)` | nil                                     | Async callback on >1% capacity change                      |
| `WithOnComplete(fn)`       | nil                                     | Async callback on completion                               |
| `WithOp(s)`                | `[opExecute]` / `[opTryExecute]`        | Operation name attached to panic reports (`TryExecute` defaults to `"warmupx.TryExecute"`) |


## Errors


| Error         | Condition                                                                                                        |
| ------------- | ---------------------------------------------------------------------------------------------------------------- |
| `ErrRejected` | `AllowOrError` / `Execute` probabilistic rejection, or `WarmupController.Reject` on an admitted `Execute`/`TryExecute` (wraps capacity + progress) |
| `ErrNilFunc`  | `Execute` / `TryExecute` was called with a nil function                                                         |
| `ErrCancelled`| `Execute` / `TryExecute` when ctx is already cancelled or its deadline has expired at admission time (no admission attempted) |

`TryExecute` does not return `ErrRejected` for probabilistic rejection — when admission fails it returns `(false, zero, nil)`, leaving the decision to the caller. A panicking callback surfaces as a `*panix.PanicError` returned by `Execute`/`TryExecute` (reach it with `errors.As`); counters reflect the admission outcome.


## Pitfalls

> [!WARNING]
> **Allow is independent per call.** It is a Bernoulli trial, not a counter. Across 100 calls at capacity `0.3` you will see roughly 30 admissions, but any individual window can deviate. Do not rely on it for exact quotas — use `ratex`/`bulkx`.

> [!WARNING]
> **Stop does not reset capacity.** After `Stop`, the warmer keeps admitting at the frozen capacity. Call `Reset` (or `Start`) to ramp again from the minimum.

> [!WARNING]
> **Callbacks run in their own goroutines and may arrive out of order.** `WithOnCapacityChange` fires asynchronously; do not assume strictly increasing `newCap` values across deliveries, and keep callbacks fast and panic-free.

> [!NOTE]
> **WithMaxCapacity below WithMinCapacity is corrected at construction.** `maxCap` is raised to `minCap`, yielding a flat (already-warm) ramp rather than an error.

## Safety and Concurrency

The `Warmer` is safe for concurrent use. State (`capacity`, `start`, `warming`, `complete`, `gen`) is protected by a `sync.RWMutex`; admission counters use `sync/atomic`. Reads (`Allow`, `Capacity`, `Progress`, `Stats`) take the read lock and never block on the ramp loop. A single background goroutine drives capacity updates while warming and exits on `Stop`, completion, or a generation change — no goroutine leaks across restarts. The `Execute`/`TryExecute` callback runs on the caller's goroutine under `panix.Safe`; its `WarmupController` is single-call scoped and must not be retained.

## Benchmarks

> CPU: Intel i7-10510U · OS: Windows 10 · Go 1.24 · `-benchmem -count=3`


| Benchmark             | ns/op | B/op | allocs/op |
| --------------------- | ----- | ---- | --------- |
| Warmer_Allow          | 29    | 0    | 0         |
| Warmer_Allow_Parallel | 61    | 0    | 0         |
| Warmer_Capacity       | 26    | 0    | 0         |
| Warmer_MaxRequests    | 22    | 0    | 0         |
| Execute               | 64    | 24   | 1         |
| Execute_Parallel      | 84    | 24   | 1         |
| TryExecute            | 60    | 24   | 1         |
| TryExecute_Parallel   | 84    | 24   | 1         |
| TryExecute_Reject     | 27    | 0    | 0         |
| Warmer_Stats          | 45    | 0    | 0         |


### Analysis

- **Allow / Capacity / MaxRequests**: 0 allocs — these take only the `RWMutex` read lock (or a single atomic) and return a stack value. `Allow` is dominated by the `rand.Float64()` call; the lock acquisition is uncontended and nearly free.
- **Execute / TryExecute**: 1 alloc (24 B) is the architectural floor. The per-call `*execution` controller is passed through the `WarmupController` interface into the callback closure, which forces it to escape to the heap. This mirrors `circuitx.Execute`. The hot admission paths (`Allow`) stay alloc-free precisely because they do not allocate a controller.
- **TryExecute_Reject**: 0 allocs — at zero capacity the admission check fails before a controller is allocated; only the rejected counter increments. This is the fast "skip optional work" path.
- **TryExecute vs Execute**: identical cost on the admit path — the only semantic difference is that probabilistic rejection returns `(false, zero, nil)` instead of wrapping `ErrRejected`.
- **Parallel scaling**: the read-lock paths scale near-linearly under contention because `math/rand/v2` keeps per-P state, eliminating the global RNG lock older generators suffered. `Execute_Parallel` stays close to its sequential cost; the single heap allocation is the only contended resource.
- **Bottleneck**: random number generation and the controller allocation dominate; the capacity math and lock are negligible.

## Quality


| Metric         | Value                          |
| -------------- | ------------------------------ |
| Test functions | 67                             |
| Benchmarks     | 10                             |
| Fuzz targets   | 4                              |
| Examples       | 6                              |
| Coverage       | 98.7%                          |
| Race detector  | All pass                       |
| External deps  | 0 (panix; testify in dev only) |


## File Structure

```text
warmupx/
├── warmupx.go         # Warmer, New, lifecycle, admission, Execute/TryExecute, ramp loop
├── options.go         # Option, config, defaults, WithXxx
├── types.go           # Strategy enum, WarmupController + private execution
├── errors.go          # ErrRejected, ErrNilFunc sentinels
├── warmupx_test.go    # Unit + table-driven + concurrent tests
├── bench_test.go      # Sequential + parallel benchmarks
├── fuzz_test.go       # FuzzCalculate, FuzzMaxRequests, FuzzExecute, FuzzTryExecute
├── footprint_test.go  # Struct size regression guards
├── example_test.go    # Runnable GoDoc examples
└── README.md          # This file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.