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
│  service code: post-deploy rollout, cold-start instances │
└────────────────────────┬─────────────────────────────────┘
                         │
┌────────────────────────▼─────────────────────────────────────────────────┐
│  warmupx  Warmer · Allow · Execute[T]/TryExecute[T] · WarmupController   │
└──────────────┬────────────────────────┬──────────────────────────────────┘
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

`Start`, `StartAt`, and `Reset` bump `gen` and install a fresh stop channel. A stale loop goroutine from a previous run sees the newer `gen` on its next tick and exits immediately, so overlapping restarts cannot corrupt state or double-close channels. `Stop` closes the stop channel, freezes capacity and progress, and clears it; calling it twice is safe.

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


| Invariant               | Guarantee                                                                                                             |
| ----------------------- | --------------------------------------------------------------------------------------------------------------------- |
| Capacity range          | `Capacity()` is always in `[minCap, maxCap] ⊆ [0, 1]`                                                                 |
| Monotonic ramp          | For all four strategies, capacity is non-decreasing in `t`                                                            |
| Completion latch        | Once complete, `Capacity() == maxCap`, `Progress() == 1`, `IsComplete()` stays true until the next `Start`            |
| `Stop` retains capacity | `Stop` freezes the current capacity; it is not reset to min                                                           |
| `Stop` retains progress | `Stop` freezes `Progress()` at the elapsed fraction observed when Stop was called                                     |
| `Execute` admission     | A rejected `Execute` never invokes the callback                                                                       |
| `Execute` counters      | `allowed` increments only on successful fn return; fn errors, panics, and late rejects do not count as allowed        |
| `TryExecute` rejection  | Probabilistic rejection returns `(false, zero, nil)` and never invokes the callback                                   |
| `WaitForCompletion`     | Never-started returns nil immediately; stopped mid-ramp blocks until ctx is cancelled                                 |
| Cancelled ctx           | `Execute`/`TryExecute` return `ErrCancelled` before admission; counters unchanged                                     |
| Panic safety            | A panicking `Execute`/`TryExecute` callback becomes a `*panix.PanicError`; counters stay at pre-panic admission state |
| Goroutine lifecycle     | Exactly one loop goroutine per active ramp; it exits on `Stop`, completion, or generation change                      |
| Concurrency             | All methods are safe for concurrent use; `-race` clean                                                                |




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


| Symbol                      | Signature                                                                                   | Description                                                          |
| --------------------------- | ------------------------------------------------------------------------------------------- | -------------------------------------------------------------------- |
| `New`                       | `func New(opts ...Option) *Warmer`                                                          | Create a warmer; does not ramp until `Start`                         |
| `Warmer.Start`              | `func (w *Warmer) Start()`                                                                  | Begin/restart ramp from `minCap`                                     |
| `Warmer.StartAt`            | `func (w *Warmer) StartAt(capacity float64)`                                                | Begin/restart ramp from a clamped capacity                           |
| `Warmer.Stop`               | `func (w *Warmer) Stop()`                                                                   | Halt ramp, retain capacity and progress (idempotent)                 |
| `Warmer.Reset`              | `func (w *Warmer) Reset()`                                                                  | Stop and restart from `minCap`                                       |
| `Warmer.Capacity`           | `func (w *Warmer) Capacity() float64`                                                       | Current capacity in `[0, 1]`                                         |
| `Warmer.Progress`           | `func (w *Warmer) Progress() float64`                                                       | Warmup progress in `[0, 1]`; frozen after `Stop`                     |
| `Warmer.Strategy`           | `func (w *Warmer) Strategy() Strategy`                                                      | Configured ramp strategy                                             |
| `Warmer.IsWarming`          | `func (w *Warmer) IsWarming() bool`                                                         | Whether a ramp is in progress                                        |
| `Warmer.IsComplete`         | `func (w *Warmer) IsComplete() bool`                                                        | Whether warmup reached full capacity                                 |
| `Warmer.Allow`              | `func (w *Warmer) Allow() bool`                                                             | Probabilistic admission decision                                     |
| `Warmer.AllowOrError`       | `func (w *Warmer) AllowOrError() error`                                                     | nil if admitted, else wraps `ErrRejected`                            |
| `Warmer.MaxRequests`        | `func (w *Warmer) MaxRequests(baseLimit int) int`                                           | `baseLimit · capacity`, rounded up                                   |
| `Warmer.WaitForCompletion`  | `func (w *Warmer) WaitForCompletion(ctx context.Context) error`                             | Block until complete or ctx done                                     |
| `Warmer.Stats`              | `func (w *Warmer) Stats() Stats`                                                            | Snapshot of state and counters                                       |
| `Warmer.ResetStats`         | `func (w *Warmer) ResetStats()`                                                             | Zero allowed/rejected counters                                       |
| `Execute`                   | `func Execute[T any](w *Warmer, ctx context.Context, fn WarmupFunc[T]) (T, error)`          | Run fn only if admitted; rejection is an error                       |
| `TryExecute`                | `func TryExecute[T any](w *Warmer, ctx context.Context, fn WarmupFunc[T]) (bool, T, error)` | Run fn only if admitted; rejection is `(false, zero, nil)`           |
| `WarmupController.Capacity` | `Capacity() float64`                                                                        | Capacity in `[0, 1]` at admission time                               |
| `WarmupController.Progress` | `Progress() float64`                                                                        | Warmup progress in `[0, 1]` at admission time                        |
| `WarmupController.Strategy` | `Strategy() Strategy`                                                                       | Configured ramp strategy at admission time                           |
| `WarmupController.Reject`   | `Reject()`                                                                                  | Late reject: discard result and return `ErrRejected`                 |
| `WarmupFunc`                | `type WarmupFunc[T any] func(context.Context, WarmupController) (T, error)`                 | Unit of work for `Execute` and `TryExecute`                          |
| `Strategy`                  | `type Strategy uint8`                                                                       | Ramp curve selector (`Linear`, `Exponential`, `Logarithmic`, `Step`) |
| `Strategy.String`           | `func (s Strategy) String() string`                                                         | Human-readable label                                                 |
| `Stats`                     | `type Stats struct { ... }`                                                                 | Observability snapshot                                               |




## Configuration


| Option                     | Default                                 | Description                                                                                |
| -------------------------- | --------------------------------------- | ------------------------------------------------------------------------------------------ |
| `WithStrategy(s)`          | `Linear`                                | Ramp curve: `Linear`, `Exponential`, `Logarithmic`, `Step`                                 |
| `WithMinCapacity(v)`       | `0.1`                                   | Starting capacity, clamped to `[0, 1]`                                                     |
| `WithMaxCapacity(v)`       | `1.0`                                   | Target capacity, clamped to `(0, 1]`                                                       |
| `WithDuration(d)`          | `1m`                                    | Total ramp duration                                                                        |
| `WithInterval(d)`          | `duration/100`, clamped to `[10ms, 1s]` | Capacity-update tick interval                                                              |
| `WithStepCount(n)`         | `10`                                    | Discrete jumps for `Step`                                                                  |
| `WithExpFactor(f)`         | `3.0`                                   | Steepness of `Exponential`                                                                 |
| `WithOnCapacityChange(fn)` | nil                                     | Async callback on >1% capacity change                                                      |
| `WithOnComplete(fn)`       | nil                                     | Async callback on completion                                                               |
| `WithOp(s)`                | `[opExecute]` / `[opTryExecute]`        | Operation name attached to panic reports (`TryExecute` defaults to `"warmupx.TryExecute"`) |




## Errors


| Error          | Condition                                                                                                                                          |
| -------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ErrRejected`  | `AllowOrError` / `Execute` probabilistic rejection, or `WarmupController.Reject` on an admitted `Execute`/`TryExecute` (wraps capacity + progress) |
| `ErrNilFunc`   | `Execute` / `TryExecute` was called with a nil function                                                                                            |
| `ErrCancelled` | `Execute` / `TryExecute` when ctx is already cancelled or its deadline has expired at admission time (no admission attempted)                      |


`TryExecute` does not return `ErrRejected` for probabilistic rejection — when admission fails it returns `(false, zero, nil)`, leaving the decision to the caller. A panicking callback surfaces as a `*panix.PanicError` returned by `Execute`/`TryExecute` (reach it with `errors.As`); counters reflect the admission outcome. A callback that returns a non-nil error after admission does not increment the `allowed` counter.

## Pitfalls

> [!WARNING]
> **Allow is independent per call.** It is a Bernoulli trial, not a counter. Across 100 calls at capacity `0.3` you will see roughly 30 admissions, but any individual window can deviate. Do not rely on it for exact quotas — use `ratex`/`bulkx`.

> [!WARNING]
> **Stop does not reset capacity or progress.** After `Stop`, the warmer keeps admitting at the frozen capacity and reports the frozen progress fraction. Call `Reset` (or `Start`) to ramp again from the minimum.

> [!WARNING]
> **WaitForCompletion after Stop blocks until the context ends.** A ramp halted before completion never closes its completion channel; use a deadline or cancel the context.

> [!WARNING]
> **Callbacks run in their own goroutines and may arrive out of order.** `WithOnCapacityChange` fires asynchronously; do not assume strictly increasing `newCap` values across deliveries. Keep callbacks fast — panics are not recovered and will crash the process.

> [!NOTE]
> **WithMaxCapacity below WithMinCapacity is corrected at construction.** `maxCap` is raised to `minCap`, yielding a flat (already-warm) ramp rather than an error.



## Safety and Concurrency

The `Warmer` is safe for concurrent use. State (`capacity`, `progress`, `start`, `warming`, `complete`, `gen`) is protected by a `sync.RWMutex`; admission counters use `sync/atomic`. Reads (`Allow`, `Capacity`, `Progress`, `Stats`) take the read lock and never block on the ramp loop. A single background goroutine drives capacity updates while warming and exits on `Stop`, completion, or a generation change — no goroutine leaks across restarts. The `Execute`/`TryExecute` callback runs on the caller's goroutine under `panix.Safe`; its `WarmupController` is single-call scoped and must not be retained.

## Benchmarks

Three environments, two hardware classes, two operating systems. All values are **medians**. `B/op` and `allocs/op` are deterministic — they depend on code, not hardware.

### Environments

| | Laptop | CI Server (Linux) | CI Server (Windows) |
|---|---|---|---|
| CPU | Intel Core i7-10510U, 4C/8T | AMD EPYC 7763, 4 vCPU | AMD EPYC 9V74, 4 vCPU |
| TDP | 15W (mobile, throttles) | 280W (server, stable) | server, stable |
| OS | Windows 10 | Ubuntu | Windows Server 2022 |
| Go | 1.26.2 | 1.26 | 1.26 |
| GOMAXPROCS | 8 | 4 | 4 |
| Runs | 3 (`-count=3`) | 3 (`-count=3`) | 3 (`-count=3`) |

### Admission / Query

| Benchmark | What it measures | Laptop | Linux | Windows | B/op | allocs/op |
|---|---|---|---|---|---|---|
| Warmer_Allow | Probabilistic admit check | 24.7 ns | **17.6 ns** | 18.8 ns | 0 | 0 |
| Warmer_Allow_Parallel | Allow, 8/4 goroutines | 47.9 ns | 43.7 ns | **36.7 ns** | 0 | 0 |
| Warmer_Capacity | Current capacity snapshot | 14.3 ns | **5.5 ns** | 5.7 ns | 0 | 0 |
| Warmer_MaxRequests | Ramp budget query | 17.1 ns | **7.3 ns** | 7.9 ns | 0 | 0 |
| Warmer_Stats | Full stats snapshot | 35.8 ns | 89.7 ns | **34.1 ns** | 0 | 0 |

### Execute Path

| Benchmark | What it measures | Laptop | Linux | Windows | B/op | allocs/op |
|---|---|---|---|---|---|---|
| Execute | Admit + callback | 60.2 ns | **49.5 ns** | 54.2 ns | 24 | 1 |
| Execute_Parallel | Execute, parallel | 63.5 ns | **49.1 ns** | 55.2 ns | 24 | 1 |
| TryExecute | Non-blocking execute | 61.5 ns | **50.0 ns** | 58.4 ns | 24 | 1 |
| TryExecute_Parallel | TryExecute, parallel | 64.9 ns | **48.6 ns** | 53.9 ns | 24 | 1 |
| TryExecute_Reject | Zero capacity reject | 28.3 ns | **15.9 ns** | 16.7 ns | 0 | 0 |

### Analysis

**Allow / Capacity / MaxRequests — 0 allocs, read-lock or atomic paths.** `Warmer_Allow` (~18 ns on CI) is dominated by `rand.Float64()` under an `RWMutex` read lock; the lock itself is uncontended in the benchmark. `Warmer_Capacity` and `MaxRequests` are single atomic or read-lock snapshots at ~5–8 ns on CI — essentially free for metrics polling.

**Execute / TryExecute — 1 alloc / 24 B is the controller floor.** The `*execution` controller escapes through the `WarmupController` interface into the callback closure — same pattern as `circuitx` and `bulkx`. Admission checks (`Allow`) stay alloc-free because they never build a controller.

**TryExecute_Reject — 0 allocs, ~16 ns on CI.** At zero capacity the admission check fails before controller allocation; only the rejected counter increments. The fast "skip optional work" path.

**Parallel scaling is flat — by design.** `Execute_Parallel` (49 ns Linux) matches serial (50 ns) within noise. `math/rand/v2` keeps per-P state, so parallel `Allow` does not serialize on a global RNG lock. The single heap allocation per execute is the only per-call cost; no mutex on the admit fast path.

**Warmer_Stats spread on Linux CI (90 ns vs ~34 ns elsewhere).** Linux takes the full stats snapshot under read lock with more field copies on the EPYC 7763 runner; Windows and laptop cluster at ~35 ns. All platforms remain 0 alloc — safe for periodic scraping.

**Linux slightly wins serial execute.** ~49–50 ns (Linux) vs ~54–58 ns (Windows) — atomic admit + callback dispatch with no timer or channel; difference is CPU micro-architecture, not OS I/O.



## Quality


| Metric         | Value                          |
| -------------- | ------------------------------ |
| Test functions | 77                             |
| Benchmarks     | 10                             |
| Fuzz targets   | 4                              |
| Examples       | 7                              |
| Coverage       | 99.2%                          |
| Race detector  | All pass                       |
| External deps  | 0 (panix; testify in dev only) |




## File Structure

```text
warmupx/
├── warmupx.go         # Warmer, New, lifecycle, admission, Execute/TryExecute, ramp loop
├── options.go         # Option, config, defaults, WithXxx
├── types.go           # Strategy enum, WarmupController + private execution
├── errors.go          # ErrRejected, ErrNilFunc, ErrCancelled sentinels
├── errors_test.go     # Sentinel and wrapper error contract tests
├── warmupx_test.go    # Unit + table-driven + concurrent tests
├── bench_test.go      # Sequential + parallel benchmarks
├── fuzz_test.go       # FuzzCalculate, FuzzMaxRequests, FuzzExecute, FuzzTryExecute
├── footprint_test.go  # Struct size regression guards (config, Warmer, execution, Stats)
├── example_test.go    # Runnable GoDoc examples
└── README.md          # This file
```



## License

MIT — see [LICENSE](../LICENSE) in the repository root.