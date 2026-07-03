# shedx — Priority-Based Load Shedding with Graceful Degradation

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/shedx)
[License: MIT](https://opensource.org/licenses/MIT)

A thread-safe load shedder that tracks in-flight work and rejects new requests when the system is overloaded, shedding the least important traffic first so the rest can succeed. Lock-free admission, per-priority cutoffs, a controller for graceful degradation, and panic recovery. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **Shedding is an admission decision, not a runtime one.** Once a request is admitted the callback runs to completion — `shedx` never aborts work mid-flight. Set `[WithCapacity](#configuration)` to the real concurrency your service can sustain, not the number of connections it can accept; a shedder sized larger than the downstream can handle protects nothing.

## The Problem

Every service has a finite amount of concurrent work it can finish before latency explodes. Past that point, naive systems fail in the worst possible way:

1. **Congestion collapse.** When arrivals exceed capacity, queues grow without bound. Latency climbs until clients time out and retry — adding *more* load. Throughput of *useful* (un-timed-out) work collapses toward zero while CPU stays pinned at 100%.
2. **Indiscriminate failure.** Without prioritization, an overloaded service drops health checks and auth probes alongside analytics batch jobs. Orchestrators then kill "unhealthy" instances, shrinking capacity exactly when it is most needed.
3. **All-or-nothing.** A request that cannot run the full expensive path is simply failed, even when a cheap cached answer would satisfy the caller. There is no built-in way to *degrade*.

`shedx` solves all three: it caps in-flight work at a configured capacity, sheds the lowest-priority requests first as load climbs past a threshold, **never** sheds `PriorityCritical` traffic, and hands the callback a controller so it can serve a degraded response under load and record that it did.

## Architectural Position

```text
✅ Shedder            — track in-flight work, admit/reject by priority and load
✅ Execute[T]         — admit + run a callback, releasing the slot on return/panic
✅ Acquire / Token    — manual admission when a callback does not fit
✅ ShedController     — load snapshot at admission + Shed() to record degradation
✅ panic safety       — a panicking callback becomes a *panix.PanicError, not a crash

❌ NOT a queue        — it rejects excess load, it does not buffer it
❌ NOT a rate limiter — it bounds concurrency, not requests-per-second (see ratex)
❌ NOT a bulkhead     — it does not partition capacity per tenant (see bulkx)
❌ NOT a circuit breaker — it does not trip open on downstream failure (see circuitx)
❌ NOT a deadline     — it does not abort admitted work (compose with toutx)
```

### Position in the urx Stack

```text
┌──────────────────────────────────────────────────────────┐
│  service code: HTTP/RPC handlers, job workers            │
└────────────────────────┬─────────────────────────────────┘
                         │
┌────────────────────────▼─────────────────────────────────┐
│  shedx   Shedder · Execute[T] · ShedController           │
│          admit by priority + load, shed the rest         │
└──────────────┬───────────────────────┬───────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼───────────────────┐
│  panix.Safe            │   │  sync/atomic                 │
│  (panic → PanicError)  │   │  (lock-free admission state) │
└────────────────────────┘   └──────────────────────────────┘
```

## Architecture

```text
                          shedx
   ┌───────────────────┬───────────────────┬───────────────────┐
   │                   │                   │                   │
 Shedder (shedx.go)   Option (options.go)  ShedController
   │                  config{capacity,     (types.go)
 atomic counters:      threshold,op}       execution{priority,
 inflight/admitted/    │                    load,inFlight,
 shed/degraded/closed  WithCapacity         capacity,degraded}
   │                   WithThreshold        Priority/Load/
 Execute[T] / Acquire  WithOp               InFlight/Capacity/Shed
   │                   │
 shouldAdmit(priority) Priority (types.go)   errors.go
   │                   Low/Normal/High/      ErrRejected
 panix.Safe(callback)  Critical              ErrClosed/ErrNilFunc
```

## How It Works

```text
Execute(s, ctx, priority, fn)
  │ closed ? ───────────────────────────► ErrClosed
  │ fn == nil ? ────────────────────────► ErrNilFunc
  │ ctx.Err() ? ────────────────────────► ErrCancelled (no slot consumed)
  │
  ├── tryReserve(priority):  CAS loop
  │     next = inflight + 1
  │     admits(priority, next) ?
  │       ├── priority == Critical ──────────────► commit (bypass)
  │       ├── next/capacity < threshold ─────────► commit
  │       └── overload = (load-thr)/(1-thr)
  │             Low    : overload < 0.25 ? commit : shed
  │             Normal : overload < 0.60 ? commit : shed
  │             High   : overload < 0.90 ? commit : shed
  │       CAS(inflight, next) — retry on contention
  │
  ├── shed ? ──────────────► shed++ ; ErrRejected(priority)
  │
  └── commit:
        admitted++ ; (defer inflight--)
        sc = {priority, load=(next-1)/cap, inflight=next-1, capacity}
        (val,err) = panix.Safe(op, fn(ctx, sc))
        sc.Shed() called ? ─► degraded++
        return val, err
```

Admission is the whole game. Below the threshold every request passes. Above it, each non-critical priority is admitted only while the **overload fraction** — how far into the `[threshold, 1.0]` band the current load sits — stays under that priority's cutoff. As load climbs, `Low` drops out first (at 25 % into the band), then `Normal` (60 %), then `High` (90 %); `Critical` never drops. This produces a smooth, monotonic shed order rather than a cliff.

The reservation uses a lock-free **compare-and-swap loop**: the candidate slot is committed only if admission holds for the *exact* post-increment count being stored. This is what makes the capacity bound hold under concurrency — two goroutines racing for the last slot read distinct counts, so at most one commits, and the in-flight counter is never transiently inflated past what is admitted. The loop retries only under genuine contention on the counter and allocates nothing.

The callback runs under `panix.Safe`, so a panic becomes a `*panix.PanicError` and the in-flight slot is still released by the deferred decrement — a panicking handler can never leak capacity.

### Graceful degradation

The `ShedController` handed to the callback carries the load snapshot taken at admission. A handler can read `sc.Load()` and choose a cheaper path under pressure (serve from cache, skip enrichment, return a partial result), then call `sc.Shed()` to record that it degraded. Degradations are counted separately in `Stats.Degraded` — distinct from `Shed`, which counts *rejected* requests. This lets you observe "how often did we serve, but cheaply?" independently from "how often did we turn work away?".

## Normative Contracts


| Contract             | Guarantee                                                                                                                          |
| -------------------- | ---------------------------------------------------------------------------------------------------------------------------------- |
| Capacity bound       | The committed non-critical in-flight count never exceeds the per-priority admission ceiling, even under concurrency (CAS-enforced) |
| Critical never shed  | `PriorityCritical` is admitted at any load, even above capacity                                                                    |
| Monotonic shed order | As load rises, lower priorities are shed before higher ones                                                                        |
| Context first        | A pre-cancelled context returns `ErrCancelled` without invoking fn or consuming a slot                                             |
| Slot release         | The in-flight slot is released when the callback returns **or panics**                                                             |
| Shed rollback        | A shed request consumes no slot — a rejected reservation is never committed                                                        |
| Panic safety         | A panicking callback becomes a `*panix.PanicError`, slot still freed                                                               |
| Admission purity     | `Allow` reports a best-effort decision without mutating any counter or slot                                                        |
| Token release        | `Token.Release` is idempotent; a double release never drives in-flight negative                                                    |
| Close semantics      | After `Close`, `Execute`/`Acquire` return `ErrClosed`; in-flight work is unaffected                                                |
| Idempotent close     | `Close` is safe to call repeatedly and always returns nil                                                                          |
| Controller scope     | A `ShedController` is valid only during its callback; do not retain it                                                             |


## Quick Start

```go
package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/aasyanov/urx/shedx"
)

func main() {
	s := shedx.New(
		shedx.WithCapacity(1000),
		shedx.WithThreshold(0.8),
	)
	defer s.Close()

	resp, err := shedx.Execute(s, context.Background(), shedx.PriorityNormal,
		func(ctx context.Context, sc shedx.ShedController) (string, error) {
			if sc.Load() > 0.9 {
				sc.Shed()
				return "cached", nil // degrade gracefully under load
			}
			return serve(ctx)
		})

	switch {
	case errors.Is(err, shedx.ErrRejected):
		fmt.Println("shed:", err)
	case errors.Is(err, shedx.ErrClosed):
		fmt.Println("closed:", err)
	case err != nil:
		fmt.Println("failed:", err)
	default:
		fmt.Println("ok:", resp)
	}
}

func serve(context.Context) (string, error) { return "fresh", nil }
```

## Usage Scenarios

### Prioritize traffic classes

```go
// Health checks and auth must always run.
shedx.Execute(s, ctx, shedx.PriorityCritical, healthCheck)

// Paid-tier writes survive moderate overload.
shedx.Execute(s, ctx, shedx.PriorityHigh, processPayment)

// Background analytics is shed first.
shedx.Execute(s, ctx, shedx.PriorityLow, ingestEvent)
```

### Degrade instead of fail

```go
resp, _ := shedx.Execute(s, ctx, shedx.PriorityNormal,
	func(ctx context.Context, sc shedx.ShedController) (*Page, error) {
		if sc.Load() > 0.85 {
			sc.Shed()
			return renderCachedPage(ctx) // skip personalization under load
		}
		return renderFullPage(ctx)
	})
```

### Manual admission with a Token

```go
tok, err := s.Acquire(shedx.PriorityHigh)
if errors.Is(err, shedx.ErrRejected) {
	http.Error(w, "overloaded", http.StatusServiceUnavailable)
	return
}
defer tok.Release()
streamLargeResponse(w, r) // in-flight tracked across many statements
```

### Cheap pre-flight check

```go
// Reject at the edge without entering the handler at all.
if !s.Allow(shedx.PriorityLow) {
	return errBusy
}
```

### Compose with a per-request timeout (toutx)

```go
resp, err := shedx.Execute(s, ctx, shedx.PriorityNormal,
	func(ctx context.Context, _ shedx.ShedController) (Report, error) {
		return toutx.Execute(ctx, 500*time.Millisecond,
			func(ctx context.Context, _ toutx.TimeoutController) (Report, error) {
				return build(ctx)
			})
	})
```

## API


| Symbol               | Signature                                                                                                                                 | Description                              |
| -------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------- |
| `New`                | `func New(opts ...Option) *Shedder`                                                                                                       | Create a shedder with defaults + options |
| `Execute`            | `func Execute[T any](s *Shedder, ctx context.Context, priority Priority, fn func(context.Context, ShedController) (T, error)) (T, error)` | Admit + run a callback                   |
| `Shedder.Acquire`    | `func (s *Shedder) Acquire(priority Priority) (*Token, error)`                                                                            | Manual admission returning a Token       |
| `Shedder.Allow`      | `func (s *Shedder) Allow(priority Priority) bool`                                                                                         | Non-mutating admission check             |
| `Shedder.Load`       | `func (s *Shedder) Load() float64`                                                                                                        | Current inflight/capacity, in [0, 1+]    |
| `Shedder.InFlight`   | `func (s *Shedder) InFlight() int64`                                                                                                      | Current in-flight count                  |
| `Shedder.Capacity`   | `func (s *Shedder) Capacity() int`                                                                                                        | Configured capacity                      |
| `Shedder.Threshold`  | `func (s *Shedder) Threshold() float64`                                                                                                   | Configured shed threshold                |
| `Shedder.Stats`      | `func (s *Shedder) Stats() Stats`                                                                                                         | Counter snapshot                         |
| `Shedder.ResetStats` | `func (s *Shedder) ResetStats()`                                                                                                          | Zero cumulative counters                 |
| `Shedder.Close`      | `func (s *Shedder) Close() error`                                                                                                         | Idempotent shutdown                      |
| `Shedder.IsClosed`   | `func (s *Shedder) IsClosed() bool`                                                                                                       | Report closed state                      |
| `Token.Release`      | `func (t *Token) Release()`                                                                                                               | Free an acquired slot (idempotent)       |
| `Priority`           | `type Priority uint8`                                                                                                                     | Low / Normal / High / Critical           |


### ShedController


| Method     | Signature             | Description                                                      |
| ---------- | --------------------- | ---------------------------------------------------------------- |
| `Priority` | `Priority() Priority` | Priority the request was admitted with                           |
| `Load`     | `Load() float64`      | Load snapshot at admission                                       |
| `InFlight` | `InFlight() int64`    | In-flight count at admission (excludes self)                     |
| `Capacity` | `Capacity() int`      | Configured capacity                                              |
| `Shed`     | `Shed()`              | Record that the callback served a degraded response (idempotent) |


## Configuration


| Option             | Default                  | Description                                                     |
| ------------------ | ------------------------ | --------------------------------------------------------------- |
| `WithCapacity(n)`  | `DefaultCapacity` (1000) | Max in-flight operations; ≤ 0 ignored, final value floored to 1 |
| `WithThreshold(t)` | `DefaultThreshold` (0.8) | Load fraction at which shedding begins; outside (0, 1] ignored  |
| `WithOp(s)`        | `"shedx.Execute"`        | Operation name attached to panic reports                        |


### Shed cutoffs

Above the threshold, the overload fraction `(load − threshold) / (1 − threshold)` drives admission per priority:


| Priority           | Admitted while overload < | Effect                       |
| ------------------ | ------------------------- | ---------------------------- |
| `PriorityLow`      | 0.25                      | Shed first, at mild overload |
| `PriorityNormal`   | 0.60                      | Shed at moderate overload    |
| `PriorityHigh`     | 0.90                      | Shed only near saturation    |
| `PriorityCritical` | —                         | Never shed                   |


## Errors


| Error          | Condition                                                                                            |
| -------------- | ---------------------------------------------------------------------------------------------------- |
| `ErrRejected`  | The request was shed for its priority at the current load (wraps the priority)                       |
| `ErrClosed`    | The shedder has been closed                                                                          |
| `ErrNilFunc`   | `Execute` was given a nil function                                                                   |
| `ErrCancelled` | The context was already cancelled or expired at admission time (wraps `ctx.Err()`); no slot consumed |


A panicking callback surfaces as a `*panix.PanicError` returned by `Execute` (reach it with `errors.As`); the in-flight slot is still released.

## Pitfalls

> [!WARNING]
> `**shedx` bounds concurrency, not request rate.** A flood of fast requests can churn through the in-flight slots without ever raising load enough to shed. If you need requests-per-second limiting, use `ratex`; combine the two for both axes.

> [!WARNING]
> **Sizing capacity wrong defeats the purpose.** Capacity must reflect the concurrency your *downstream* can sustain, not your accept queue. A shedder sized to 10× the real limit admits everything and protects nothing.

> [!NOTE]
> **Critical traffic is uncapped by design.** `PriorityCritical` requests are admitted even above capacity, so `Load()` can exceed 1.0. Reserve `Critical` for genuinely must-run, low-volume traffic (health, auth, control plane).

> [!NOTE]
> **Shedding does not abort admitted work.** A request admitted just before load spiked still runs to completion. For a hard per-request deadline, wrap the callback with `toutx.Execute`.

## Safety and Concurrency

`Shedder` is safe for concurrent use from any number of goroutines. All admission state (`inflight`, `admitted`, `shed`, `degraded`, `closed`) lives in `sync/atomic` values; the hot path takes no lock. Admission reserves a slot with a compare-and-swap loop that commits only when the post-increment count is admissible, so the in-flight counter never exceeds the per-priority ceiling even when many goroutines race for the last slot — and it is never transiently inflated for observers. `Token.Release` likewise uses an atomic CAS, so a double release is a no-op and can never drive the counter negative. The `ShedController` is touched only by the single goroutine running its callback and needs no synchronization. Every test runs under `-race`, including a 64-goroutine capacity-bound stress test that asserts the observed in-flight count never exceeds capacity.

## Benchmarks

> CPU: Intel i7-10510U · OS: Windows 10 · Go 1.26 · `-benchmem -count=3` (best of 3)


| Benchmark              | ns/op | B/op | allocs/op |
| ---------------------- | ----- | ---- | --------- |
| Execute_Admit          | 90    | 48   | 1         |
| Execute_Admit_Parallel | 244   | 48   | 1         |
| Execute_Shed           | 312   | 80   | 2         |
| Acquire                | 84    | 16   | 1         |
| Acquire_Parallel       | 247   | 16   | 1         |
| Allow                  | 6     | 0    | 0         |


### Analysis

- **Execute_Admit**: 1 alloc / 48 B is the admit-path floor — the `execution` controller, which escapes because it is handed to the callback through the `panix.Safe` closure as an interface. The CAS reservation, the `admitted` increment, and the deferred decrement are all alloc-free. A controller-free fast path could reach 0 allocs but would drop the load snapshot and the `Shed` degradation hook that justify the package.
- **Execute_Admit_Parallel**: ~244 ns, ~2.7× the serial cost at 8 goroutines. The slowdown is contention on the shared `inflight` counter: under parallelism the CAS loop occasionally retries when two goroutines race the same value, and the `admitted` counter is a second contended cache line. There is no mutex — this is the inherent cost of a single global concurrency counter, and it scales predictably.
- **Execute_Shed**: ~312 ns / 2 allocs is the *rejection* path. The two allocs are `fmt.Errorf` wrapping the priority into `ErrRejected` (the error value plus the formatted string). Rejection only fires when the system is already overloaded, so the diagnostic-rich error costs nothing on the hot path. Use `Allow` for an alloc-free, error-free pre-check.
- **Acquire**: 1 alloc / 16 B is the `Token` itself, which must escape to the caller. Everything else is the CAS reservation and counter work.
- **Allow**: 0 allocs, ~6 ns — a single atomic load plus float math, no heap and no reservation. This is the cheapest way to ask "would this be admitted?" and is the right tool for edge rejection, at the cost of being a best-effort hint rather than a binding decision.
- **Allocation floor**: the admit path's 1 alloc is architectural (the controller). `Allow` proves the underlying admission decision is alloc-free; the controller and token allocations exist only because those APIs hand an object back to the caller.

## Quality


| Metric         | Value                          |
| -------------- | ------------------------------ |
| Test functions | 35                             |
| Benchmarks     | 6                              |
| Fuzz targets   | 2                              |
| Examples       | 4                              |
| Coverage       | 100.0%                         |
| Race detector  | All pass                       |
| External deps  | 0 (panix; testify in dev only) |


## File Structure

```text
shedx/
├── shedx.go            # package doc + Shedder + Execute[T] + Acquire/Token + admission
├── options.go          # config, Option, defaults, WithXxx
├── types.go            # Priority enum + ShedController + private execution impl
├── errors.go           # ErrRejected, ErrClosed, ErrNilFunc, ErrCancelled
├── shedx_test.go       # unit + table-driven tests
├── bench_test.go       # benchmarks (sequential + parallel)
├── fuzz_test.go        # FuzzExecute, FuzzAcquireRelease — admission invariants
├── example_test.go     # runnable GoDoc examples
├── footprint_test.go   # struct size guards
└── README.md           # this file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.