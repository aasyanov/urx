# adaptx — Adaptive Concurrency Limiting (AIMD · Vegas · Gradient)

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/adaptx)
[License: MIT](../LICENSE)

A thread-safe concurrency limiter that **discovers** a backend's safe operating limit instead of making you guess it. It starts at a configured limit and continuously moves it up or down from latency and error feedback using one of three control laws — `AIMD`, `Vegas`, or `Gradient` — opening up when the backend is fast and healthy and clamping down the moment latency climbs or errors appear. A controller for load-aware callbacks, panic recovery, and lock-light admission. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **adaptx limits *concurrency*, not request rate, and it adapts the limit itself.** Where `bulkx` enforces a fixed, hand-sized bound, `adaptx` treats the limit as a variable it servo-controls toward the backend's real capacity. Pick the algorithm that matches your overload signal: `AIMD` when *errors* signal overload, `Vegas`/`Gradient` when *latency* does.

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
 sem chan (permits)     initial/min/max,     algorithm,skipped}
 atomic counters,       smoothing,...}       Limit/InFlight/
 mu: limit/debt/        │                    Algorithm/SkipSample
 latency/ring          WithAlgorithm         │
   │                   WithInitial/Min/Max   Algorithm enum (types.go)
 Execute/TryExecute     WithSmoothing        AIMD / Vegas / Gradient
 Acquire/release/Allow  WithJitter / WithOp  AdaptFunc[T] (types.go)
   │                   opOrDefault/Try()    │
 adjust → aimd/vegas/  ringCapacity()        ErrClosed/ErrTimeout
 gradient → permits                          ErrCancelled/ErrNilFunc
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
          ├── skip (latency==0) ? ──► return (no feedback, no history)
          ├── ring buffer ← sample ; EMA avg ; RTT_min decay
          └── seen ≥ warmup ? adjust(success, latency)
                ├── AIMD     : ok → limit+rate ; fail → limit·ratio
                ├── Vegas    : queue=lim·(rtt−min)/min vs target band from targetLatency
                └── Gradient : g=(rtt−avg)/avg ; below → grow, above → back off
              jitter, clamp to [min,max]
              grow → push permits (pay debt first)
              shrink → pull idle permits, else record debt
        inFlight-- ; releasePermit (pay debt or return permit)
```

The limit is a **servo-controlled variable**, not a constant. Admission rides a buffered-channel semaphore whose buffer is the configured maximum; the number of values currently buffered is the count of permits available to acquire. Acquiring receives a permit; releasing returns one. To **grow** the limit the controller pushes new permits into the channel; to **shrink** it, it pulls idle permits out, and for permits that are currently held it records *debt* — the next releases retire those permits instead of returning them. This is what makes a multiplicative decrease actually remove capacity without ever blocking a release.

Only the periodic adaptation step and the percentile snapshot take the mutex; the success/failure/reject counters are lock-free atomics. The callback runs under `panix.Safe`, so a panic becomes a `*panix.PanicError` and the permit is still released — a panicking handler can never leak capacity.

### The three control laws


| Algorithm      | Signal                 | Grows when                   | Backs off when                      | Best for                                  |
| -------------- | ---------------------- | ---------------------------- | ----------------------------------- | ----------------------------------------- |
| **AIMD**     | success/failure        | every success (`+rate`)      | any failure (`×ratio`)              | failure-driven overload; the safe default |
| **Vegas**    | latency vs RTT_min     | estimated queue below target | estimated queue above `2×target`    | a stable backend floor latency            |
| **Gradient** | latency vs EMA average | sample at/below average      | sample above average (proportional) | drifting floor latency                    |


`AIMD` is the TCP congestion-avoidance law: additive increase, multiplicative decrease. `Vegas` infers queued work from how far the current round-trip time sits above the best ever seen (`RTT_min`) and holds the limit inside a tolerance band. `Gradient` compares each sample to a smoothed running average, so it adapts when the backend's baseline latency itself moves.

### Keeping the feedback honest

Two mechanisms stop the controller from chasing noise. **Warmup** (`WithWarmupSamples`) ignores the first N samples so an unrepresentative cold start does not move the limit. **RTT_min decay** (`WithMinLatencyDecay`) drifts the recorded minimum slowly toward the average so `Vegas` cannot stick forever to one anomalously fast sample. The callback can also call `AdaptController.SkipSample()` to exclude a known outlier (a cache miss, a cold connection) from both the latency feedback and the percentile history — it still counts toward the success/failure totals.

## Normative Contracts


| Contract            | Guarantee                                                                                         |
| ------------------- | ------------------------------------------------------------------------------------------------- |
| Bounded concurrency | Admitted in-flight work never exceeds the live limit; the limit never leaves `[min, max]`         |
| Limit floor         | `min` is floored to 1, so the limiter always makes forward progress                               |
| Context first       | A pre-cancelled context returns `ErrCancelled`/`ErrTimeout` without consuming a permit            |
| Permit release      | The permit is released when the callback returns **or panics**                                    |
| Idempotent release  | The release function runs its effect once; extra calls are no-ops                                 |
| Debt accounting     | A shrink retires held permits as they are released; in-flight never exceeds the new limit         |
| Skip honesty        | `SkipSample` removes a call from latency feedback and history but not from success/failure totals |
| Warmup              | No adaptation occurs until `warmupSamples` samples have been recorded                             |
| Panic safety        | A panicking callback becomes a `*panix.PanicError`, permit still freed                            |
| Close semantics     | After `Close`, admission returns `ErrClosed`; blocked waiters wake immediately; in-flight work drains before permits retire |
| Idempotent close    | `Close` is safe to call repeatedly; the first call wins, later calls return `ErrClosed`           |
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
| `Limiter.Close`            | `func (l *Limiter) Close() error`                                                                                       | Idempotent shutdown (30s drain)                 |
| `Limiter.CloseWithTimeout` | `func (l *Limiter) CloseWithTimeout(d time.Duration) error`                                                             | Shutdown with custom drain window               |
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
| `WithSmoothing(f)`       | `0.2`              | EMA weight per latency sample; outside (0, 1] ignored           |
| `WithIncreaseRate(r)`    | `1.0`              | AIMD additive step on success; ≤ 0 ignored                      |
| `WithDecreaseRatio(r)`   | `0.5`              | Multiplicative backoff factor; outside (0, 1) ignored           |
| `WithTargetLatency(d)`   | `100ms`            | Vegas operating-point RTT; scales the queue target band; ≤ 0 ignored |
| `WithTolerance(f)`       | `0.1`              | Vegas/Gradient deviation band; outside (0, 1] ignored           |
| `WithSampleWindow(d)`    | `1s`               | Percentile window; ≤ 0 ignored                                  |
| `WithWarmupSamples(n)`   | `10`               | Samples before adaptation; 0 disables warmup                    |
| `WithMinLatencyDecay(f)` | `0.001`            | RTT_min drift toward average; 0 disables, outside [0,1] ignored |
| `WithJitter(f)`          | `0.1`              | Fraction of an increase that may be withheld; 0 disables        |
| `WithOp(s)`              | `[opExecute]` / `[opTryExecute]` | Operation name attached to panic reports                        |
| `WithOnLimitChange(fn)`  | none               | Async callback on every limit change                            |


## Errors


| Error          | Condition                                                                         |
| -------------- | --------------------------------------------------------------------------------- |
| `ErrClosed`    | Admission attempted after `Close`                                                 |
| `ErrTimeout`   | Blocking acquire exceeded its context deadline (wraps `context.DeadlineExceeded`) |
| `ErrCancelled` | Context cancelled before a permit was available (`Execute`, `Acquire`, `TryExecute`; wraps `ctx.Err()`) |
| `ErrNilFunc`   | `Execute`/`TryExecute` given a nil function                                       |


A panicking callback surfaces as a `*panix.PanicError` returned by `Execute` (reach it with `errors.As`); the permit is still released.

## Pitfalls

> [!WARNING]
> **adaptx bounds concurrency, not request rate.** A flood of fast successes will *raise* the limit, not throttle arrivals. For requests-per-second limiting use `ratex`; compose the two for both axes.

> [!WARNING]
> **Choose the algorithm to match your overload signal.** `Vegas` and `Gradient` need representative latency: if your work has wildly bimodal latency (cache hit vs miss) without `SkipSample`, they will thrash. When failures — not latency — are the overload signal, prefer `AIMD`.

> [!NOTE]
> **`ResetStats` snaps the limit immediately.** Counters, latency estimators, and the permit pool are reset to the configured initial limit in one step. When in-flight work exceeds that initial limit the live limit is raised to the in-flight count so permits never go negative.

> [!NOTE]
> **SkipSample keeps the success/failure totals.** A skipped call is removed from latency feedback and percentile history only — it still counts as a success or failure in `Stats`. Use it for outlier *latency*, not to hide errors.

## Safety and Concurrency

`Limiter` is safe for concurrent use from any number of goroutines. Admission rides a buffered-channel semaphore; the success, failure, reject, and adjustment counters are `sync/atomic`. A single mutex guards the adaptive state (limit, shrink debt, latency estimators, sample ring) and is taken only on the periodic adaptation step and the percentile snapshot — never on the fast admission path beyond a single `Limit()` read. Growing the limit pushes permits into the channel; shrinking pulls idle permits and records *debt* so held permits are retired on release, which keeps in-flight work from ever exceeding the new limit. The release function uses an atomic compare-and-swap so a double call is a no-op. The `AdaptController` is touched only by the goroutine running its callback. Every test runs under `-race`, including 50-goroutine admission stress that asserts in-flight returns to zero.

## Benchmarks

> CPU: Intel i7-10510U · OS: Windows 10 · Go 1.24 · `-benchmem -count=3` (best of 3)


| Benchmark        | ns/op | B/op | allocs/op |
| ---------------- | ----- | ---- | --------- |
| Execute          | 652   | 52   | 3         |
| Execute_Parallel | 930   | 52   | 3         |
| Acquire          | 305   | 28   | 2         |
| Acquire_Parallel | 653   | 28   | 2         |
| TryAcquire       | 207   | 28   | 2         |
| TryExecute       | 643   | 52   | 3         |
| Allow            | 28    | 0    | 0         |
| Limit            | 26    | 0    | 0         |


### Analysis

- **Execute**: ~652 ns / 3 allocs is the admit-path floor. The three allocations are the release closure, the `atomic.Bool` it captures for double-call safety, and the `execution` controller — all of which escape to the heap because the closure outlives the stack frame and the controller is handed to the callback through the `panix.Safe` boundary as an interface. The semaphore receive, the atomic counter bumps, and (post-warmup) the mutex-guarded adaptation step are otherwise alloc-free.
- **Acquire / TryAcquire**: 2 allocs / 28 B — the release closure and its captured `atomic.Bool`. `TryAcquire` is ~30 % cheaper than `Acquire` because it skips the context pre-check and the blocking `select`. Both are the right primitive when a single callback does not fit the call shape.
- **TryExecute**: same 3-allocation floor as `Execute`; uses `[opTryExecute]` for panic attribution unless `WithOp` overrides both entry points.
- **Execute_Parallel**: ~930 ns, ~1.4× the serial cost at 8 goroutines. The slowdown is the shared `inFlight`/`total` counters and the channel send/receive contending on the same cache lines; there is no mutex on the admission path, so it scales predictably with core count.
- **Allow / Limit**: 0 allocs, ~26–28 ns — a single mutex lock/unlock around limit and in-flight reads. Safe to poll from a metrics loop; `Allow` is cheaper when you only need a yes/no without running a callback.
- **Allocation floor**: the admit path's 3 allocs are architectural (closure + atomic + controller). They are the cost of the controller API and the idempotent release guarantee, not avoidable bookkeeping; a controller-free, fire-and-forget API could reach fewer allocs but would drop the load snapshot and `SkipSample` that justify the package.

## Quality


| Metric         | Value                          |
| -------------- | ------------------------------ |
| Test functions | 71                             |
| Benchmarks     | 8                              |
| Fuzz targets   | 2                              |
| Examples       | 4                              |
| Coverage       | 96.3%                          |
| Race detector  | All pass                       |
| External deps  | 0 (panix; testify in dev only) |


## File Structure

```text
adaptx/
├── adaptx.go           # package doc + Limiter + Execute/TryExecute + Acquire + adaptation
├── options.go          # config, Option, defaults, WithXxx, ring sizing
├── types.go            # Algorithm enum + AdaptController + private execution impl + sample
├── errors.go           # ErrClosed, ErrTimeout, ErrCancelled, ErrNilFunc
├── adaptx_test.go      # unit + table-driven + race tests
├── bench_test.go       # benchmarks (sequential + parallel)
├── fuzz_test.go        # FuzzNew, FuzzExecute — construction + permit-accounting invariants
├── example_test.go     # runnable GoDoc examples
├── footprint_test.go   # struct size guards
└── README.md           # this file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.