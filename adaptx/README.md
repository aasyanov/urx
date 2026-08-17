# adaptx — Adaptive Concurrency Limiting (AIMD · Vegas · Gradient)

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/adaptx)
[License: MIT](../LICENSE)
[Changelog](../CHANGELOG.md)

A thread-safe concurrency limiter that **discovers** a backend's safe operating limit instead of making you guess it. It starts at a configured limit and moves that limit **once per sample window** (default 1s) from latency and error feedback using one of three control laws — `AIMD`, `Vegas`, or `Gradient` — opening up when the backend is fast and healthy and clamping down the moment a window shows latency climb or errors. A controller for load-aware callbacks, panic recovery, and lock-light admission. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **adaptx limits *concurrency*, not request rate, and it adapts the limit itself — windowed, not per request.** Where `bulkx` enforces a fixed, hand-sized bound, `adaptx` treats the limit as a variable it servo-controls toward the backend's real capacity. Pick the algorithm that matches your overload signal: `AIMD` when *errors* signal overload, `Vegas`/`Gradient` when *latency* does.

## The Problem

A static concurrency bound forces an impossible choice. Set it too low and you leave throughput on the table when the backend is healthy. Set it too high and you flood the backend the moment it slows down, driving it into congestion collapse — the exact failure a limiter is supposed to prevent. And the "right" number is never stable: it drifts with backend deploys, cache state, neighbouring tenants, and time of day.

1. **Capacity is unknown and moving.** The safe concurrency for a database or downstream service is rarely documented and changes constantly. A hand-tuned bulkhead is stale the day after you tune it.
2. **Latency is the early warning, errors are the late one.** A backend under stress slows down *before* it starts failing. A limiter that only reacts to errors acts too late; one that reads latency can back off while there is still time.
3. **Lockstep amplification.** Many clients stepping their limits up at the same instant produce a synchronized surge — a self-inflicted thundering herd.

`adaptx` solves all three: it servo-controls the limit toward observed capacity, offers latency-driven laws (`Vegas`, `Gradient`) that react before failures appear, and jitters increases so a fleet of limiters does not march in lockstep.

## Architectural Position

```text
✅ Limiter              — servo-control a concurrency limit from feedback
✅ Execute[T]           — admit + run a callback, releasing the permit on return/panic
✅ Acquire / release    — manual admission when a callback does not fit
✅ Allow                — non-blocking probe without admission
✅ AdaptController      — limit + in-flight snapshot, SkipSample() to drop outliers
✅ AIMD/Vegas/Gradient  — three control laws for error- or latency-driven overload
✅ panic safety         — a panicking callback becomes a *panix.PanicError, not a crash

❌ NOT a rate limiter   — it bounds concurrency, not requests-per-second (see ratex)
❌ NOT a static bulkhead — the limit moves; for a fixed bound use bulkx
❌ NOT a circuit breaker — it throttles, it does not trip fully open (see circuitx)
❌ NOT a load shedder    — it has no per-request priority (see shedx)
❌ NOT a deadline        — it does not abort admitted work (compose with toutx)
```

### Position in the urx Stack

```text
┌─────────────────────────────────────────────────────────────┐
│  service code: DB pools, RPC clients, job dispatchers       │
└────────────────────────┬────────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────────┐
│  adaptx   Limiter · Execute[T] · AdaptController            │
│           move the limit toward real capacity               │
└──────────────┬────────────────────────┬─────────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼─────────────────────┐
│  panix.Safe            │   │  chan + sync/atomic            │
│  (panic → PanicError)  │   │  (permit semaphore + counters) │
└────────────────────────┘   └────────────────────────────────┘
```

## Architecture

```text
                           adaptx
   ┌────────────────────┬────────────────────┬────────────────────┐
   │                    │                    │                    │
 Limiter (adaptx.go)   Option (options.go)  AdaptController (types.go)
   │                   config{algorithm,    execution{limit,inFlight,
 sem chan (permits)     utilization,         algorithm,skipped}
 atomic counters,       sampleWindow,...}    Limit/InFlight/
 mu: limit/debt/        │                    Algorithm/SkipSample
 window/estimators     WithAlgorithm         │
   │                   WithUtilization       Algorithm enum (types.go)
 Execute/TryExecute     WithSampleWindow     AIMD / Vegas / Gradient
 Acquire/release/Allow  WithOnLimitChange    AdaptFunc[T] (types.go)
   │                   (sync, must not block)│
 window snapshot →     ringCapacity()        ErrClosed/ErrTimeout
 aimd/vegas/gradient                         ErrCancelled/ErrNilFunc
 → permits                                   ErrDrainTimeout
```

## How It Works

```text
Execute(l, ctx, fn)
  │ fn == nil ? ───────────────────────────► ErrNilFunc
  │ closed ? ──────────────────────────────► ErrClosed
  │ ctx already cancelled ? ────────────────► ErrCancelled / ErrTimeout (no permit)
  │
  ├── Acquire: <-sem (block until a permit is free, ctx, or close)
  │     inFlight++ ; total++
  │
  ├── ac = {limit, inFlight, algorithm}     (admission snapshot)
  ├── (val,err) = panix.Safe(op, fn(ctx, ac))
  │
  └── release(success, latency):            (idempotent, runs once)
        record(success, latency)
          ├── success/fail counters++
          ├── peak in-flight for this window
          ├── skip (latency==0) ? ──► no ring, no window n
          └── else: ring ← sample ; window n/fails/meanRTT/minRTT
        if now−windowStart ≥ sampleWindow AND seen ≥ warmup:
          ONE adjust from snapshot {n, fails, maxInFlight, meanRTT, minRTT}
            ├── AIMD     : fails>0 → ×ratio once
            │              else if maxInFlight ≥ ceil(limit·utilization)
            │                → accumulate increaseRate credit, step=int(credit)
            │              else hold
            ├── Vegas    : queue = limit·(1 − minRTT/rtt)   rtt = window mean
            │              α = limit·tol [, ×(1 − minRTT/targetLatency)]
            │              queue<α → +1 ; queue>α·2 → ×ratio ; else hold
            └── Gradient : first window holds (avgLat unset); then avgLat = rtt
                           later avgLat = s·rtt + (1−s)·avgLat
                           fails>0 → ×ratio once
                           g=(rtt−avg)/avg ; g<−tol → +2 ; g≤tol → +1
                           else ×max(1−g·ratio, ratio)
          jitter, clamp to [min,max]
          grow → push permits (pay debt first)
          shrink → pull idle permits, else record debt
          onLimitChange(old,new) synchronously under recover (must not block)
          reset window counters
        inFlight-- ; releasePermit (pay debt or return permit)
```

The limit is a **windowed servo**, not a per-request counter. Samples accumulate for `WithSampleWindow` (default 1s). When the window elapses and warmup is done, the configured law runs **once** on the snapshot, then the window resets. Ten failures in one window are one multiplicative decrease, not ten halvings.

Admission rides a buffered-channel semaphore whose buffer is the configured **maximum**; the number of values currently buffered is the count of permits available to acquire. Acquiring receives a permit; releasing returns one. To **grow** the limit the controller pushes new permits into the channel; to **shrink** it, it pulls idle permits out, and for permits that are currently held it records *debt* — the next releases retire those permits instead of returning them. That is what makes a multiplicative decrease actually remove capacity without blocking a release.

**In-flight vs live limit.** Admission never takes a permit that is not in the semaphore, so in-flight never exceeds `maxLimit`. After a shrink, in-flight work that was already admitted **may exceed the live limit** until those releases pay the debt. `Allow` compares in-flight to the live limit without claiming a slot — it is a hint, not an admission.

Only the windowed adaptation step and the percentile snapshot take the mutex; the success/failure/reject counters are lock-free atomics. The callback runs under `panix.Safe`, so a panic becomes a `*panix.PanicError` and the permit is still released — a panicking handler can never leak capacity.

### The three control laws


| Algorithm      | Signal                    | Grows when                                      | Backs off when                     | Best for                                  |
| -------------- | ------------------------- | ----------------------------------------------- | ---------------------------------- | ----------------------------------------- |
| **AIMD**     | window success / failure  | successful window at ≥ `ceil(limit·utilization)` | any failure in the window (`×ratio`, once) | failure-driven overload; the safe default |
| **Vegas**    | window mean RTT vs RTT_min | estimated queue below α                        | estimated queue above β = 2α       | a stable backend floor latency            |
| **Gradient** | window mean vs EMA average | g at/below tolerance                           | g above tolerance (proportional)   | drifting floor latency                    |


`AIMD` is the TCP congestion-avoidance law, applied per window and **gated on utilization**: idle successes do not inflate the limit. Fractional `WithIncreaseRate` (for example 0.5) keeps a remainder so the limit grows by 1 every two eligible windows. `Vegas` infers queued work as `limit·(1 − minRTT/rtt)` — the denominator is the window mean RTT, not RTT_min — and holds the limit inside a tolerance band scaled by `WithTargetLatency` when the target sits above RTT_min. `Gradient` compares each window mean to an EMA that is initialized to the first window's RTT (not blended with zero). The first window **holds** the limit so a high opening RTT cannot grow concurrency before an average exists.

### Keeping the feedback honest

Two mechanisms stop the controller from chasing noise. **Warmup** (`WithWarmupSamples`) ignores the first N samples so an unrepresentative cold start does not move the limit — windows still roll, but no adjust runs until `seen ≥ warmup`. **RTT_min decay** (`WithMinLatencyDecay`) drifts the recorded minimum toward the average on each completed window so `Vegas` cannot stick forever to one anomalously fast sample. The callback can also call `AdaptController.SkipSample()` to exclude a known outlier (a cache miss, a cold connection) from both the latency feedback and the percentile history — it still counts toward the success/failure totals.

## Normative Contracts


| Contract            | Guarantee                                                                                         |
| ------------------- | ------------------------------------------------------------------------------------------------- |
| Bounded admission   | A missing permit is never taken; in-flight never exceeds `maxLimit`; the live limit stays in `[min, max]` |
| Shrink debt         | After a shrink, in-flight **may exceed the live limit** until released permits pay the debt       |
| Allow is a hint     | `Allow` does not claim a slot; a concurrent admit may change the outcome                          |
| Limit floor         | `min` is floored to 1, so the limiter always makes forward progress                               |
| Context first       | A pre-cancelled context returns `ErrCancelled`/`ErrTimeout` without consuming a permit            |
| Permit release      | The permit is released when the callback returns **or panics**                                    |
| Idempotent release  | The release function runs its effect once; extra calls are no-ops                                 |
| Windowed adjust     | The control law runs at most once per sample window, after warmup                                 |
| Skip honesty        | `SkipSample` removes a call from latency feedback and history but not from success/failure totals |
| Warmup              | No adaptation occurs until `warmupSamples` samples have been recorded                             |
| Panic safety        | A panicking callback becomes a `*panix.PanicError`, permit still freed                            |
| Close semantics     | After close, admission returns `ErrClosed`; blocked waiters wake immediately                      |
| Idempotent `Close`  | `Close()` returns nil on the first and every later call                                           |
| Drain timeout       | `CloseWithTimeout` returns `ErrDrainTimeout` if in-flight remains; the limiter stays closed       |
| Limit-change hook   | Runs synchronously under recover; must not block or panic                                         |
| Controller scope    | An `AdaptController` is valid only during its callback; do not retain it                          |


## Quick Start

```go
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/aasyanov/urx/adaptx"
)

func main() {
	l := adaptx.New(
		adaptx.WithAlgorithm(adaptx.Gradient),
		adaptx.WithInitialLimit(10),
		adaptx.WithMaxLimit(200),
	)
	defer l.Close()

	rows, err := adaptx.Execute(l, context.Background(),
		func(ctx context.Context, ac adaptx.AdaptController) (*sql.Rows, error) {
			if ac.InFlight() > ac.Limit()/2 {
				return queryCheap(ctx) // shed load near saturation
			}
			return queryFull(ctx)
		})

	switch {
	case errors.Is(err, adaptx.ErrClosed):
		fmt.Println("closed")
	case err != nil:
		fmt.Println("failed:", err)
	default:
		_ = rows
		fmt.Println("ok")
	}
}

func queryCheap(context.Context) (*sql.Rows, error) { return nil, nil }
func queryFull(context.Context) (*sql.Rows, error)  { return nil, nil }
```

## Usage Scenarios

### Protect a database connection pool

```go
l := adaptx.New(adaptx.WithAlgorithm(adaptx.Vegas), adaptx.WithMaxLimit(maxConns))
defer l.Close()

rows, err := adaptx.Execute(l, ctx,
	func(ctx context.Context, _ adaptx.AdaptController) (*sql.Rows, error) {
		return db.QueryContext(ctx, q, args...)
	})
```

### Manual admission with a release function

```go
release, err := l.Acquire(ctx)
if err != nil {
	return err // ErrClosed / ErrTimeout / ErrCancelled
}
start := time.Now()
err = stream(ctx) // in-flight tracked across many statements
release(err == nil, time.Since(start))
```

### Drop outlier latencies from the feedback

```go
resp, _ := adaptx.Execute(l, ctx,
	func(ctx context.Context, ac adaptx.AdaptController) (*Resp, error) {
		if coldStart {
			ac.SkipSample() // do not let a cold connection move the limit
		}
		return call(ctx)
	})
```

### Non-blocking fast path

```go
ran, val, err := adaptx.TryExecute(l, ctx,
	func(ctx context.Context, _ adaptx.AdaptController) (Result, error) {
		return compute(ctx)
	})
if !ran {
	return serveStale() // limiter saturated, no permit available
}
```

### Probe without admission

```go
if !l.Allow() {
	return serveCached() // at capacity, skip the expensive path
}
```

### Observe limit changes

```go
l := adaptx.New(adaptx.WithOnLimitChange(func(old, new int) {
	metrics.Gauge("adaptx.limit").Set(float64(new))
}))
```

The hook runs **synchronously** on the goroutine that closed the sample window. It must not block or panic; a panic is recovered and discarded.

## API


| Symbol                     | Signature                                                                                                               | Description                                     |
| -------------------------- | ----------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------- |
| `New`                      | `func New(opts ...Option) *Limiter`                                                                                     | Create a limiter with defaults + options        |
| `Execute`                  | `func Execute[T any](l *Limiter, ctx context.Context, fn AdaptFunc[T]) (T, error)`                                      | Admit + run a callback (recommended)            |
| `TryExecute`               | `func TryExecute[T any](l *Limiter, ctx context.Context, fn AdaptFunc[T]) (bool, T, error)`                              | Non-blocking variant; (false, …) when saturated |
| `Limiter.Allow`            | `func (l *Limiter) Allow() bool`                                                                                        | Probe whether a permit is free (no admission)   |
| `Limiter.Acquire`          | `func (l *Limiter) Acquire(ctx context.Context) (release func(bool, time.Duration), err error)`                         | Blocking manual admission                       |
| `Limiter.TryAcquire`       | `func (l *Limiter) TryAcquire() (release func(bool, time.Duration), ok bool)`                                           | Non-blocking manual admission                   |
| `Limiter.Limit`            | `func (l *Limiter) Limit() int`                                                                                         | Current adaptive limit                          |
| `Limiter.InFlight`         | `func (l *Limiter) InFlight() int`                                                                                      | Current in-flight count                         |
| `Limiter.Stats`            | `func (l *Limiter) Stats() Stats`                                                                                       | Counter + latency-percentile snapshot           |
| `Limiter.ResetStats`       | `func (l *Limiter) ResetStats()`                                                                                        | Zero counters, reset adaptive state             |
| `Limiter.Close`            | `func (l *Limiter) Close() error`                                                                                       | Idempotent shutdown; always returns nil (does not wait) |
| `Limiter.CloseWithTimeout` | `func (l *Limiter) CloseWithTimeout(d time.Duration) error`                                                             | Shutdown with custom drain; `ErrDrainTimeout` / `ErrClosed` |
| `Limiter.IsClosed`         | `func (l *Limiter) IsClosed() bool`                                                                                     | Report closed state                             |
| `Algorithm`                | `type Algorithm uint8`                                                                                                  | `AIMD` / `Vegas` / `Gradient`                   |
| `AIMD`, `Vegas`, `Gradient`| constants                                                                                                               | Control-law selectors                           |
| `AdaptFunc[T]`             | `func(ctx context.Context, ac AdaptController) (T, error)`                                                              | Unit of work for `Execute` / `TryExecute`       |


### AdaptController


| Method       | Signature               | Description                                          |
| ------------ | ----------------------- | ---------------------------------------------------- |
| `Limit`      | `Limit() int`           | Limit at admission time                              |
| `InFlight`   | `InFlight() int`        | In-flight count at admission (excludes self)         |
| `Algorithm`  | `Algorithm() Algorithm` | Active adaptation algorithm                          |
| `SkipSample` | `SkipSample()`          | Exclude this call from latency feedback (idempotent) |


## Configuration


| Option                   | Default            | Description                                                     |
| ------------------------ | ------------------ | --------------------------------------------------------------- |
| `WithAlgorithm(a)`       | `AIMD`             | Control law; unknown values behave as `AIMD`                    |
| `WithInitialLimit(n)`    | `10`               | Starting limit; clamped into `[min, max]`                       |
| `WithMinLimit(n)`        | `1`                | Floor; ≤ 0 ignored, final value floored to 1                    |
| `WithMaxLimit(n)`        | `1000`             | Ceiling and semaphore capacity; below min raised to min         |
| `WithSmoothing(f)`       | `0.2`              | EMA weight per **window mean** RTT; outside (0, 1] ignored      |
| `WithIncreaseRate(r)`    | `1.0`              | AIMD additive **credit per eligible window**; ≤ 0 ignored; fractional rates accumulate |
| `WithDecreaseRatio(r)`   | `0.5`              | Multiplicative backoff factor per backoff window; outside (0, 1) ignored |
| `WithUtilization(f)`     | `0.9`              | AIMD increase gate: peak in-flight ≥ `ceil(limit·f)`; outside (0, 1] ignored |
| `WithTargetLatency(d)`   | `100ms`            | Vegas operating-point RTT; scales the queue target band; ≤ 0 ignored |
| `WithTolerance(f)`       | `0.1`              | Vegas/Gradient deviation band; outside (0, 1] ignored           |
| `WithSampleWindow(d)`    | `1s`               | Aggregation window for one adjust **and** Stats percentiles; ≤ 0 ignored |
| `WithWarmupSamples(n)`   | `10`               | Samples before adaptation; 0 disables warmup                    |
| `WithMinLatencyDecay(f)` | `0.001`            | RTT_min drift toward average per window; 0 disables, outside [0,1] ignored |
| `WithJitter(f)`          | `0.1`              | Fraction of an increase that may be withheld; 0 disables        |
| `WithOp(s)`              | `[opExecute]` / `[opTryExecute]` | Operation name attached to panic reports                        |
| `WithOnLimitChange(fn)`  | none               | Synchronous callback on every limit change; must not block      |


## Errors


| Error             | Condition                                                                         |
| ----------------- | --------------------------------------------------------------------------------- |
| `ErrClosed`       | Admission attempted after close; also a second `CloseWithTimeout`                 |
| `ErrTimeout`      | Blocking acquire exceeded its context deadline (wraps `context.DeadlineExceeded`) |
| `ErrCancelled`    | Context cancelled before a permit was available (`Execute`, `Acquire`, `TryExecute`; wraps `ctx.Err()`) |
| `ErrNilFunc`      | `Execute`/`TryExecute` given a nil function                                       |
| `ErrDrainTimeout` | `CloseWithTimeout` deadline elapsed while in-flight work remains; limiter stays closed |


A panicking callback surfaces as a `*panix.PanicError` returned by `Execute` (reach it with `errors.As`); the permit is still released. `Close()` itself always returns nil; use `CloseWithTimeout` when a drain failure must be visible.

## Pitfalls

> [!WARNING]
> **adaptx bounds concurrency, not request rate.** A flood of fast successes at high utilization will *raise* the limit, not throttle arrivals. Serial traffic well below `ceil(limit·utilization)` holds AIMD still. For requests-per-second limiting use `ratex`; compose the two for both axes.

> [!WARNING]
> **After a shrink, in-flight may exceed the live limit.** Already-admitted work is not cancelled. New admissions wait for debt to be paid. `Allow` is a hint: it compares in-flight to the live limit without taking a permit.

> [!WARNING]
> **Choose the algorithm to match your overload signal.** `Vegas` and `Gradient` need representative latency: if your work has wildly bimodal latency (cache hit vs miss) without `SkipSample`, they will thrash. When failures — not latency — are the overload signal, prefer `AIMD`.

> [!WARNING]
> **`WithOnLimitChange` must not block.** The hook runs on the goroutine that closed the window. A slow hook stalls every subsequent `record`. Panics are recovered and discarded.

> [!NOTE]
> **`ResetStats` snaps the limit immediately.** Counters, latency estimators, window credit, and the permit pool are reset to the configured initial limit in one step. When in-flight work exceeds that initial limit the live limit is raised to the in-flight count so permits never go negative.

> [!NOTE]
> **SkipSample keeps the success/failure totals.** A skipped call is removed from latency feedback, percentile history, and the AIMD window peak in-flight — it still counts as a success or failure in `Stats`. Use it for outlier *latency*, not to hide errors.

> [!NOTE]
> **`Close()` does not wait.** It is `CloseWithTimeout(0)`: in-flight work is not joined, drain timeout is swallowed, and the call always returns nil. Use `CloseWithTimeout(DefaultCloseTimeout)` when shutdown must wait up to 30s. `CloseWithTimeout(0)` itself returns `ErrDrainTimeout` if in-flight work remains; the limiter stays closed.

## Safety and Concurrency

`Limiter` is safe for concurrent use from any number of goroutines. Admission rides a buffered-channel semaphore; the success, failure, reject, and adjustment counters are `sync/atomic`. A single mutex guards the adaptive state (limit, shrink debt, latency estimators, sample ring, window counters) and is taken on each completed sample and on the percentile snapshot — never on the fast admission path beyond a single `Limit()` read. Growing the limit pushes permits into the channel; shrinking pulls idle permits and records *debt* so held permits are retired on release. Already-admitted in-flight work may exceed the live limit until that debt is paid; it never exceeds `maxLimit`. The release function uses an atomic compare-and-swap so a double call is a no-op. The `AdaptController` is touched only by the goroutine running its callback. `CloseWithTimeout` waits with a timer/select on a drain channel, not `time.Sleep`. Every test runs under `-race`, including 50-goroutine admission stress that asserts in-flight returns to zero.

## Benchmarks

Three environments, two hardware classes, two operating systems. All values are **medians**. `B/op` and `allocs/op` are deterministic — they depend on code, not hardware.

### Environments

| | Laptop | CI Server (Linux) | CI Server (Windows) |
|---|---|---|---|
| CPU | Intel Core i7-10510U, 4C/8T | Intel Xeon 6973P-C | AMD EPYC 7763 |
| TDP | 15W (mobile, throttles) | 280W (server, stable) | server, stable |
| OS | Windows 10 | Ubuntu | Windows Server 2022 |
| Go | 1.26.2 | 1.26 | 1.26 |
| GOMAXPROCS | 8 | 4 | 4 |
| Runs | 3 (`-count=3`) | 3 (`-count=3`) | 3 (`-count=3`) |

This gives three comparison axes: **laptop vs server** (hardware scaling), **Linux vs Windows** (OS mutex/timer impact), and **serial vs parallel** (channel + atomic contention under load).

### Admission Path

| Benchmark | What it measures | Laptop | Linux | Windows | B/op | allocs/op |
|---|---|---|---|---|---|---|
| Execute | Admit + callback + release | 250 ns | 254.8 ns | **221 ns** | 52 | 3 |
| Execute_Parallel | Execute, 8/4 goroutines | 497 ns | 507.2 ns | **399.5 ns** | 52 | 3 |
| TryExecute | Non-blocking admit path | 184 ns | 237.1 ns | **175.4 ns** | 52 | 3 |
| Acquire | Semaphore only (no callback) | 164 ns | **103.2 ns** | 147.8 ns | 28 | 2 |
| Acquire_Parallel | Acquire, 8/4 goroutines | 344 ns | **174.6 ns** | 341.5 ns | 28 | 2 |
| TryAcquire | Non-blocking acquire | 123 ns | **83.8 ns** | 109.8 ns | 28 | 2 |
| Allow | Read-only admission check | 13.4 ns | 11.4 ns | **5.4 ns** | 0 | 0 |
| Limit | Current limit snapshot | 13.5 ns | 12.3 ns | **7.2 ns** | 0 | 0 |

### Analysis

**Pure CPU + channel + atomic — no I/O.** Every benchmark is in-process: buffered-channel semaphore, atomic counters, and (on the adaptation step only) a mutex. The Linux vs Windows gap on the same server class is dominated by mutex and channel fast-path cost, not filesystem or timer resolution.

**Windows CI is faster on the admit path.** `Execute` is 254.8 ns (Linux) vs 221 ns (Windows) — a **1.2×** spread on identical `-count=3` methodology. `TryExecute` shows the same pattern (237.1 ns vs 175.4 ns). The three heap allocations (release closure, captured `atomic.Bool`, `execution` controller) are fixed on every admit; the remaining time is channel send/receive and atomic bumps. EPYC 7763 on the Windows runner appears to win on this hot path despite the OS overhead seen in other urx packages.

**Parallel acquire is OS-sensitive.** `Acquire_Parallel` is 174.6 ns (Linux) vs 341.5 ns (Windows) — Linux **1.8× faster** under four goroutines contending on the same semaphore. `Execute_Parallel` inverts again (507.2 ns Linux vs 399.5 ns Windows), because the execute path adds callback setup work that amortizes channel contention differently. Pick `TryAcquire`/`Acquire` when you need raw slot reservation without the 52 B controller overhead.

**Laptop sits between CI platforms on serial paths, worse on parallel.** Serial `Execute` (250 ns) beats Linux CI but loses to Windows CI. `Acquire_Parallel` at 344 ns matches Windows, not Linux — the 8-thread laptop runs more goroutines than the 4-slot semaphore can serve without queueing, inflating parallel numbers versus the 4-vCPU CI matrix.

**Execute / TryExecute — 3 allocs is the architectural floor.** The three allocations are the release closure, the `atomic.Bool` it captures for double-call safety, and the `execution` controller handed to the callback through the `panix.Safe` boundary as an interface. The semaphore receive and atomic counter bumps are otherwise alloc-free.

**Acquire / TryAcquire — 2 allocs / 28 B.** Only the release closure and its captured `atomic.Bool` escape. `TryAcquire` is ~25% cheaper than `Acquire` on CI because it skips the context pre-check and blocking `select`.

**Allow / Limit — 0 allocs, ~5–13 ns on CI.** A mutex lock/unlock around limit and in-flight reads. Safe to poll from a metrics loop; `Allow` is cheaper when you only need a yes/no without running a callback.

## Quality


| Metric         | Value                          |
| -------------- | ------------------------------ |
| Test functions | 88                             |
| Benchmarks     | 8                              |
| Fuzz targets   | 2                              |
| Examples       | 4                              |
| Coverage       | 95.2%                          |
| Race detector  | All pass (`go test -race -count=1 ./adaptx/`) |
| External deps  | 0 (panix; testify in dev only) |


```text
go test -race -count=1 ./adaptx/
go test -run='^$' -bench=. -benchmem -count=5 ./adaptx/
go test -fuzz=FuzzNew -fuzztime=30s ./adaptx/
go test -fuzz=FuzzExecute -fuzztime=30s ./adaptx/
```


## File Structure

```text
adaptx/
├── adaptx.go           # package doc + Limiter + Execute/TryExecute + Acquire + windowed adaptation
├── options.go          # config, Option, defaults, WithXxx, withClock (tests)
├── types.go            # Algorithm enum + AdaptController + private execution impl + sample
├── errors.go           # ErrClosed, ErrTimeout, ErrCancelled, ErrNilFunc, ErrDrainTimeout
├── adaptx_test.go      # unit + table-driven + race tests
├── errors_test.go      # sentinel errors.Is coverage
├── bench_test.go       # benchmarks (sequential + parallel)
├── fuzz_test.go        # FuzzNew, FuzzExecute — construction + permit-accounting invariants
├── example_test.go     # runnable GoDoc examples
├── footprint_test.go   # struct size guards
└── README.md           # this file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.