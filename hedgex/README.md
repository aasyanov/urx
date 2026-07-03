# hedgex — Request Hedging (Speculative Execution) for Tail-Latency Control

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/hedgex)
[License: MIT](https://opensource.org/licenses/MIT)

A thread-safe request hedger that launches the same logical request as several copies with staggered delays and keeps the first success, cancelling the losers. It turns a fat latency tail into a tight one by racing a slow request against a fresh copy instead of waiting it out. Staggered fan-out, a controller for per-copy adaptation and voluntary withdrawal, panic recovery, and a zero-goroutine fast path when hedging is disabled. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **Hedging multiplies load — bound it and make copies idempotent.** Every hedge copy is a *real* request to your backend. A request that is hedged 3-way can triple the work for that one call. Only hedge operations that are safe to run more than once (reads, idempotent writes) and size `[WithMaxParallel](#configuration)` and `[WithDelay](#configuration)` so the extra load is worth the latency you buy back.

## The Problem

A service that fans out to a database, a cache, or a downstream RPC inherits that backend's *tail*, not its median. A backend with a 1 ms median but a 200 ms p99 — caused by a GC pause, a cold replica, a packet loss, a noisy neighbour — drags every dependent request's p99 to 200 ms even though almost all calls are instant. Three problems compound:

1. **The tail dominates aggregates.** A request that fans out to 10 such backends sees the *worst* of 10 tails: the probability that at least one is slow approaches 1. A 1-in-100 slow backend becomes a 1-in-10 slow request.
2. **Retries are too late and too blunt.** Classic retry waits for a timeout (often set near the p99) before acting, so it *adds* latency to the very requests that were already slow. By the time it fires, the damage is done.
3. **You cannot tell "slow" from "failed" in time.** A stalled request is indistinguishable from a hung one until it either returns or times out. Waiting to find out is exactly the latency you are trying to avoid.

`hedgex` attacks the tail directly: it starts a request, and if that copy has not answered within a short delay (well below the timeout — typically near the p95), it launches a *second* copy without waiting for the first to fail. The first copy to succeed wins; the rest are cancelled. A stall on one backend is rescued by a fresh copy on another, so the request's latency tracks the *best* of N copies rather than the worst of one.

## Architectural Position

```text
✅ Hedger             — immutable config + counters, shared across a service
✅ Execute[T]         — hedge one function across N staggered copies
✅ ExecuteMulti[T]    — hedge across heterogeneous backends (primary/replica/cache)
✅ HedgeController    — per-copy attempt info + Cancel() to withdraw from the race
✅ sync fast path     — one backend ⇒ no goroutine, no channel, no timer
✅ panic safety       — a panicking copy becomes a *panix.PanicError, not a crash

❌ NOT a retry        — copies run concurrently, not sequentially after failure (see retryx)
❌ NOT a timeout      — it does not bound total latency; compose with toutx
❌ NOT a load balancer — it does not pick one backend, it races several
❌ NOT a fallback     — losers are cancelled, not substituted (see fallx)
❌ NOT a deduplicator — it multiplies a request on purpose; it does not collapse duplicates
```

### Position in the urx Stack

```text
┌──────────────────────────────────────────────────────────┐
│  service code: read paths, fan-out RPC, replicated reads │
└────────────────────────┬─────────────────────────────────┘
                         │
┌────────────────────────▼─────────────────────────────────┐
│  hedgex   Hedger · Execute[T] · HedgeController          │
│           race N staggered copies, keep the first win    │
└──────────────┬───────────────────────┬───────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼──────────────────┐
│  panix.Safe            │   │  context · time · atomic    │
│  (panic → PanicError)  │   │  (cancel fan-out, stagger)  │
└────────────────────────┘   └─────────────────────────────┘
```

## Architecture

```text
                          hedgex
   ┌───────────────────┬───────────────────┬───────────────────┐
   │                   │                   │                   │
 Hedger (hedgex.go)   Option (options.go)  HedgeController
   │                  config{maxParallel,  (types.go)
 atomic counters:      delay,maxDelay,      execution{attempt,
 calls/wins/           onHedge,op}          backends,start,
 hedges/failures       │                    withdrawn}
   │                   WithMaxParallel       Attempt/IsHedge/
 Execute[T] /          WithDelay             Backends/Elapsed/
 ExecuteMulti[T]       WithMaxDelay          Cancel
   │                   WithOnHedge / WithOp
 lone? → runSync       │                    errors.go
 else  → dispatch      delays() schedule    ErrNoFunc/ErrAllFailed
   │                   (linear → spread)     ErrCancelled
 panix.Safe(copy)
```

## How It Works

```text
Execute(h, ctx, fn)            ExecuteMulti(h, ctx, fns)
  │ fn == nil ? ─► ErrNoFunc      │ cap fns at maxParallel
  │ fill maxParallel slots ───────┤ no non-nil entry ? ─► ErrNoFunc
                                  │ ctx already done ? ─► ErrCancelled
                                  │
                  ┌───────────────┴────────────────┐
        exactly one non-nil?                  two or more?
                  │                                 │
              runSync (no goroutine)            dispatch
                  │                                 │
        panix.Safe(fn) ─► win/fail        launch copy 0 immediately
                                          start timer at delays[0]
                                          ┌──────── loop ─────────┐
                                          │ ctx.Done()  ─► ErrCancelled
                                          │ result in:
                                          │   success ─► cancel all, WIN
                                          │   fail/withdraw ─► record
                                          │     if no copy in flight:
                                          │        launch next NOW
                                          │     all done & none left ─►
                                          │        ErrAllFailed
                                          │ timer fires ─► launch next copy,
                                          │        rearm at delays[k]
                                          └───────────────────────┘
```

A hedge is launched on **either** of two triggers, whichever comes first: the stagger timer elapses (the in-flight copy is taking too long), or every in-flight copy has already finished without a win (a fast *failure* should accelerate the next copy, not make it wait out the full delay). The first copy to return a nil error wins; `dispatch` calls the shared `cancel()`, which tears down the context handed to every other copy, and returns the winning value. Losing copies observe `ctx.Done()` and exit; their late results are dropped.

### The stagger schedule

`delays()` computes when each copy launches, relative to the start. The first hedge fires one `delay` after the original, the second two delays after, and so on — a linear ramp. Once a copy's scheduled time would exceed `maxDelay`, the remaining copies are *spread* thinly (`delay/4` apart, floored at 1 ms) instead of all piling up at the cap. This keeps a large `MaxParallel` from collapsing into a synchronized burst that hammers the backend at one instant. The schedule is monotonic by construction.

### Voluntary withdrawal

A copy that learns it cannot win — its chosen replica is unreachable, its shard is being rebalanced — can call `HedgeController.Cancel()` and return promptly. A withdrawn copy's result is discarded: it counts as neither the winner nor a failure, exactly as if a sibling had won and cancelled it. This lets a copy free its slot honestly instead of returning a spurious error that would be recorded as the first failure.

### The synchronous fast path

When a call has exactly one launchable backend (`MaxParallel == 1`, or an `ExecuteMulti` slice with a single non-nil entry) there is nothing to race. `hedgex` detects this and runs the function inline via `runSync` — no goroutine, no channel, no cancel context, no timer. The only allocation is the `HedgeController` handed to the callback. This makes "hedging configured but disabled for this path" essentially free (~57 ns, 1 alloc) so a single `Hedger` can guard both hedged and un-hedged call sites without a tax on the latter.

## Normative Contracts


| Contract              | Guarantee                                                                                                                                     |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| First win returns     | The first copy to return a nil error wins; its value is returned and all other copies are cancelled                                           |
| Loser teardown        | When a winner arrives, every other in-flight copy's context is cancelled before `Execute` returns                                             |
| No goroutine leak     | On any exit (win, all-fail, cancellation) the shared cancel fires; copies observe `ctx.Done()` and the buffered channel never blocks a sender |
| Failure accelerates   | If every in-flight copy finishes without a win, the next copy launches immediately rather than waiting out its delay                          |
| Withdrawal neutrality | A copy that calls `Cancel` is neither winner nor failure; its result is discarded                                                             |
| Panic safety          | A panicking copy becomes a `*panix.PanicError`, handled like an ordinary copy failure — never a crash                                         |
| Monotonic schedule    | Hedge launch times are non-decreasing; copies past `maxDelay` are spread, not bunched                                                         |
| Parallelism cap       | At most `MaxParallel` copies run; `ExecuteMulti` truncates a longer slice                                                                     |
| Controller scope      | A `HedgeController` is valid only during its copy's callback; do not retain it                                                                |
| Construction safety   | `New` floors a non-positive `MaxParallel` to 1 and raises a too-small `MaxDelay` to `Delay`; it never returns an unusable hedger              |


## Quick Start

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aasyanov/urx/hedgex"
)

func main() {
	h := hedgex.New(
		hedgex.WithDelay(50*time.Millisecond), // launch a hedge if no answer in 50ms
		hedgex.WithMaxParallel(3),             // at most 3 copies in flight
	)

	val, err := hedgex.Execute(h, context.Background(),
		func(ctx context.Context, hc hedgex.HedgeController) (string, error) {
			if hc.IsHedge() {
				return readReplica(ctx) // copy 2+ goes to a replica
			}
			return readPrimary(ctx)
		})

	switch {
	case errors.Is(err, hedgex.ErrAllFailed):
		fmt.Println("every copy failed:", err)
	case errors.Is(err, hedgex.ErrCancelled):
		fmt.Println("cancelled:", err)
	case err != nil:
		fmt.Println("error:", err)
	default:
		fmt.Println("ok:", val)
	}
}

func readPrimary(context.Context) (string, error) { return "primary", nil }
func readReplica(context.Context) (string, error) { return "replica", nil }
```

## Usage Scenarios

### Hedge a replicated read

```go
// The primary stalls occasionally; a hedge to a replica rescues the tail.
val, err := hedgex.Execute(h, ctx,
	func(ctx context.Context, hc hedgex.HedgeController) (Row, error) {
		replica := replicas[(hc.Attempt()-1)%len(replicas)] // pick by attempt
		return queryRow(ctx, replica, id)
	})
```

### Race heterogeneous backends with ExecuteMulti

```go
// Try the cache and the database at once; whichever answers first wins.
val, err := hedgex.ExecuteMulti(h, ctx, []hedgex.HedgeFunc[Value]{
	func(ctx context.Context, _ hedgex.HedgeController) (Value, error) { return db.Get(ctx, k) },
	func(ctx context.Context, _ hedgex.HedgeController) (Value, error) { return cache.Get(ctx, k) },
})
```

### Withdraw a copy that cannot win

```go
val, err := hedgex.Execute(h, ctx,
	func(ctx context.Context, hc hedgex.HedgeController) (Resp, error) {
		host := pickHost(hc.Attempt())
		if !healthy(host) {
			hc.Cancel() // do not count this copy as a failure
			return Resp{}, nil
		}
		return call(ctx, host)
	})
```

### Bound total latency by composing with toutx

```go
// hedgex cuts the tail; toutx enforces a hard ceiling on the whole operation.
val, err := toutx.Execute(ctx, 300*time.Millisecond,
	func(ctx context.Context, _ toutx.TimeoutController) (Resp, error) {
		return hedgex.Execute(h, ctx,
			func(ctx context.Context, _ hedgex.HedgeController) (Resp, error) {
				return fetch(ctx)
			})
	})
```

### Observe hedge activity

```go
h := hedgex.New(
	hedgex.WithDelay(20*time.Millisecond),
	hedgex.WithOnHedge(func(attempt int) {
		metrics.Counter("hedge_launched").Inc() // a hedge fired for this call
	}),
)
// ... later ...
s := h.Stats() // {Calls, Wins, Hedges, Failures}
```

## API


| Symbol               | Signature                                                                                 | Description                                  |
| -------------------- | ----------------------------------------------------------------------------------------- | -------------------------------------------- |
| `New`                | `func New(opts ...Option) *Hedger`                                                        | Create a hedger with defaults + options      |
| `Execute`            | `func Execute[T any](h *Hedger, ctx context.Context, fn HedgeFunc[T]) (T, error)`         | Hedge one function across N staggered copies |
| `ExecuteMulti`       | `func ExecuteMulti[T any](h *Hedger, ctx context.Context, fns []HedgeFunc[T]) (T, error)` | Hedge across distinct backends               |
| `HedgeFunc[T]`       | `type HedgeFunc[T any] func(context.Context, HedgeController) (T, error)`                 | The hedged unit of work                      |
| `Hedger.MaxParallel` | `func (h *Hedger) MaxParallel() int`                                                      | Configured max concurrent copies             |
| `Hedger.Delay`       | `func (h *Hedger) Delay() time.Duration`                                                  | Configured stagger between copies            |
| `Hedger.MaxDelay`    | `func (h *Hedger) MaxDelay() time.Duration`                                               | Configured cap on the stagger window         |
| `Hedger.Stats`       | `func (h *Hedger) Stats() Stats`                                                          | Counter snapshot                             |
| `Hedger.ResetStats`  | `func (h *Hedger) ResetStats()`                                                           | Zero cumulative counters                     |
| `Stats`              | `type Stats struct { Calls, Wins, Hedges, Failures int64 }`                               | Observability snapshot                       |


### HedgeController


| Method     | Signature                 | Description                                    |
| ---------- | ------------------------- | ---------------------------------------------- |
| `Attempt`  | `Attempt() int`           | 1-based copy number (1 = original, 2+ = hedge) |
| `IsHedge`  | `IsHedge() bool`          | Whether this copy is a speculative hedge       |
| `Backends` | `Backends() int`          | Total copies scheduled for this call           |
| `Elapsed`  | `Elapsed() time.Duration` | Time since the first copy launched             |
| `Cancel`   | `Cancel()`                | Withdraw this copy from the race (idempotent)  |


## Configuration


| Option               | Default                  | Description                                                                       |
| -------------------- | ------------------------ | --------------------------------------------------------------------------------- |
| `WithMaxParallel(n)` | `DefaultMaxParallel` (3) | Max concurrent copies; ≤ 0 ignored, final value floored to 1 (1 disables hedging) |
| `WithDelay(d)`       | `DefaultDelay` (100ms)   | Stagger before launching the next copy; ≤ 0 ignored                               |
| `WithMaxDelay(d)`    | `DefaultMaxDelay` (1s)   | Cap on the stagger window; copies past it are spread `delay/4` apart; ≤ 0 ignored |
| `WithOnHedge(fn)`    | none                     | Async, panic-safe callback fired with the attempt number when a hedge launches    |
| `WithOp(s)`          | `"hedgex.Execute"`       | Operation name attached to panic reports                                          |


## Errors


| Error          | Condition                                                                       |
| -------------- | ------------------------------------------------------------------------------- |
| `ErrNoFunc`    | `Execute` got a nil function, or `ExecuteMulti` got an empty / all-nil slice    |
| `ErrAllFailed` | Every launched copy failed (or withdrew); wraps the first failure               |
| `ErrCancelled` | The caller's context was cancelled before any copy succeeded; wraps `ctx.Err()` |


A panicking copy surfaces as a `*panix.PanicError` joined under `ErrAllFailed` (reach it with `errors.As`).

## Pitfalls

> [!WARNING]
> **Hedging non-idempotent work double-executes side effects.** A hedge copy is a second real request. Hedging a "charge the card" or "send the email" path can charge twice or send twice. Hedge reads and idempotent operations only; make the copy adapt (e.g. skip writes) via `HedgeController.IsHedge`.

> [!WARNING]
> **A delay set too low turns into a load multiplier.** If `WithDelay` is below the backend's *median*, almost every call hedges, multiplying load `MaxParallel`-fold for little tail benefit. Set the delay near the p95 you want to cut, not the median.

> [!NOTE]
> `**hedgex` does not impose a timeout.** It bounds the *number* of copies, not the total time. A request where every copy stalls runs until the context is cancelled. Wrap with `toutx` for a hard deadline.

> [!NOTE]
> **Losing copies are cancelled, not awaited.** When a winner returns, the other copies' context is cancelled and `Execute` returns immediately; it does not wait for the losers to observe cancellation. Ensure your function respects `ctx.Done()` so cancelled copies release their resources promptly.

## Safety and Concurrency

`Hedger` is safe for concurrent use from any number of goroutines and is meant to be created once and shared. It holds only immutable configuration plus four `sync/atomic` counters (`calls`, `wins`, `hedges`, `failures`); there is no lock on any path. Each call gets its own cancellable child context, buffered result channel, and timer, so concurrent calls never interfere. The `HedgeController`'s `withdrawn` flag is an `atomic.Bool` because `Cancel` may be observed across the copy and dispatch goroutines; its other fields are immutable for the copy's lifetime. Every copy runs under `panix.Safe`. The test suite exercises 50-goroutine × 200-iteration `Execute` stress, mid-flight cancellation, and panic recovery, all under `-race`.

## Benchmarks

> CPU: Intel i7-10510U · OS: Windows 10 · Go 1.26 · `-benchmem -count=3`


| Benchmark                    | ns/op | B/op | allocs/op |
| ---------------------------- | ----- | ---- | --------- |
| Execute_NoHedging            | 57    | 48   | 1         |
| Execute_PrimaryWins          | 2035  | 792  | 11        |
| Execute_PrimaryWins_Parallel | 570   | 792  | 11        |
| ExecuteMulti_PrimaryWins     | 2060  | 792  | 11        |
| Delays                       | 37    | 64   | 1         |


### Analysis

- **Execute_NoHedging**: 57 ns / 1 alloc is the synchronous fast path (`MaxParallel == 1`), taken directly by `Execute` without even building a fan-out slice. The single allocation is the `HedgeController` handed to the callback — the same architectural floor as `shedx`'s admit path. No goroutine, channel, cancel context, or timer is created, so a `Hedger` guarding an un-hedged call site costs almost nothing. This is the dominant case for "hedging available but disabled here".
- **Execute_PrimaryWins**: ~2.1 µs / 11 allocs is the hedged path when the original wins before any hedge fires (the common good case). The allocations are inherent to speculative concurrency: `context.WithCancel` (2), the buffered result channel (1), the `delays` slice (1), the spawned goroutine and its `execution` controller, and the timer. CPU is dominated by `context.WithCancel` and goroutine scheduling, not by `hedgex` logic. This is the price of being *able* to race; it is paid once per hedged call regardless of how many copies ultimately launch.
- **Execute_PrimaryWins_Parallel**: ~600 ns/op — *faster* per-op than serial because `b.RunParallel` spreads the goroutine-creation and context cost across 8 cores; the atomic counter contention is negligible (four independent `Add`s on separate cache lines). Throughput scales near-linearly with cores.
- **ExecuteMulti_PrimaryWins**: matches `Execute` — the dispatch machinery is identical; only the function source differs.
- **Delays**: ~37 ns / 1 alloc — the schedule slice (`count-1` durations) is the only allocation; the computation is integer arithmetic. It runs once per hedged call.
- **Allocation floor**: the hedged path's 11 allocs are architectural — racing N copies *requires* a cancel context, a goroutine, and a result channel. The fast path proves the non-racing case reaches the 1-alloc controller floor. There is no allocation-free way to hedge.

## Quality


| Metric         | Value                          |
| -------------- | ------------------------------ |
| Test functions | 35                             |
| Benchmarks     | 5                              |
| Fuzz targets   | 2                              |
| Examples       | 4                              |
| Coverage       | 100.0%                         |
| Race detector  | All pass                       |
| External deps  | 0 (panix; testify in dev only) |


## File Structure

```text
hedgex/
├── hedgex.go           # package doc + Hedger + Execute/ExecuteMulti + dispatch + sync fast path
├── options.go          # config, Option, defaults, WithXxx, delay-schedule constants
├── types.go            # HedgeController + private execution impl + HedgeFunc + result
├── errors.go           # ErrNoFunc, ErrAllFailed, ErrCancelled
├── hedgex_test.go      # unit + table-driven + concurrent + panic + withdrawal tests
├── bench_test.go       # benchmarks (sync, hedged, parallel, multi, delays)
├── fuzz_test.go        # FuzzExecute, FuzzDelays — termination + schedule invariants
├── example_test.go     # runnable GoDoc examples
├── footprint_test.go   # struct size guards
└── README.md           # this file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.