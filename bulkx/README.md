# bulkx — Concurrency Limiter (Bulkhead Isolation)

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/bulkx)
[Changelog](../CHANGELOG.md)
[License: MIT](../LICENSE)

A thread-safe bulkhead that caps the number of operations running concurrently, isolating a slow or failing dependency so it cannot drain the resources of the whole process. Blocking and non-blocking admission, a bounded wait with timeout, a controller for load-aware degradation, and panic recovery. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **A bulkhead bounds concurrency, not queue depth.** `Execute` waits up to `[WithTimeout](#configuration)` for a slot (or the context deadline, whichever is sooner), then rejects — it does **not** buffer unbounded work. Size `[WithMaxConcurrent](#configuration)` to the concurrency the protected dependency can actually sustain. A bulkhead larger than the downstream can handle admits everything and isolates nothing. `[TryExecute](#api)` will not take a free slot while another caller is already waiting.

## The Problem

A single misbehaving dependency can take down an entire service. The failure mode is mechanical:

1. **Resource monopolization.** When one downstream (a database, a third-party API) slows down, its calls pile up. Every goroutine waiting on it holds a connection, a buffer, and a stack. Without a cap, *all* of the service's goroutines end up blocked on the one slow dependency, starving every unrelated request.
2. **Cascading failure.** The starved service now fails its *own* health checks and times out *its* callers, who retry, adding load. One slow dependency becomes a service-wide outage.
3. **No isolation between workloads.** A burst of expensive batch calls and a stream of cheap user requests share the same unbounded concurrency, so the batch work crowds out the latency-sensitive traffic.

`bulkx` solves this by partitioning concurrency: each dependency gets its own `[Bulkhead](#api)` with a fixed number of slots. When the slots are full, new calls wait briefly and then fail fast with `[ErrTimeout](#errors)` instead of piling up — the blast radius of a slow dependency is contained to the calls that use it.

## Architectural Position

```text
✅ Bulkhead           — cap concurrent operations behind a semaphore
✅ Execute[T]         — wait for a slot + run a callback, releasing on return/panic
✅ TryExecute[T]      — non-blocking admission: run now or reject immediately
✅ Acquire / Token    — manual slot ownership when a callback does not fit
✅ BulkController     — occupancy snapshot at admission for load-aware callbacks
✅ panic safety       — a panicking callback becomes a *panix.PanicError, not a crash

❌ NOT a queue           — it rejects excess load past the wait timeout, it does not buffer it
❌ NOT a rate limiter    — it bounds in-flight concurrency, not requests-per-second (see ratex)
❌ NOT a load shedder    — it has no priorities; every caller is equal (see shedx)
❌ NOT a circuit breaker — it does not trip open on downstream failure (see circuitx)
❌ NOT a deadline        — the wait timeout does not bound the callback runtime (compose with toutx)
```

### Position in the urx Stack

```text
┌────────────────────────────────────────────────────────┐
│  service code: HTTP/RPC handlers, job workers          │
└────────────────────────┬───────────────────────────────┘
                         │
┌────────────────────────▼───────────────────────────────┐
│  bulkx   Bulkhead · Execute[T] · BulkController        │
│          cap concurrency per dependency, fail fast     │
└──────────────┬────────────────────────┬────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼────────────────┐
│  panix.Safe            │   │  chan semaphore + atomics │
│  (panic → PanicError)  │   │  (slot bound + counters)  │
└────────────────────────┘   └───────────────────────────┘
```

## Architecture

```text
                          bulkx
   ┌───────────────────┬───────────────────┬───────────────────┐
   │                   │                   │                   │
 Bulkhead (bulkx.go)  Option (options.go)  BulkController
   │                  config{maxConcurrent, (types.go)
 sem chan + atomics:   timeout, maxWaiters,  execution{active,
 active/waiters/       op}                   maxConcurrent,
 executed/rejected/    │                     waitedSlot}
 timeouts/closed       WithMaxConcurrent     Active/MaxConcurrent/
   │                   WithTimeout           Load/WaitedSlot
 Execute[T] /          WithMaxWaiters
 TryExecute[T] /       WithOp
 Acquire+Token         │
   │                   errors.go
 panix.Safe(callback)  ErrTimeout/ErrClosed/
                       ErrCancelled/ErrNilFunc/
                       ErrWaitersExceeded
```

## How It Works

```text
Execute(b, ctx, fn)
  │ closed ? ───────────────────────────► ErrClosed
  │ fn == nil ? ────────────────────────► ErrNilFunc
  │ ctx.Err() ? ────────────────────────► ErrCancelled (no slot consumed)
  │
  ├── phase 2: if waiters==0, optimistic non-blocking send to sem
  │      slot free ? ──► finishReserve ──► closed ? ──► ErrClosed
  │                                    └──► ctx.Err() ? ──► refund ; ErrCancelled
  │                                    └──► run(waited=false)
  │      waiters>0 ? skip fast path (anti-barge)
  │
  └── phase 3: waiters++ ; if maxWaiters>0 && n>max ? ErrWaitersExceeded
        arm timer for min(timeout, ctx remaining), then select:
        slot frees up ──► finishReserve ──► run(waited=true)
        ctx.Done()    ──► rejected++ ; ErrCancelled
        timer fires   ──► ctx.Err() set ? ErrCancelled : timeouts++ ; ErrTimeout
        closedCh      ──► rejected++ ; ErrClosed

TryExecute(b, ctx, fn)
  │ closed / nil fn / ctx.Err() ? ──────► ErrClosed / ErrNilFunc / ErrCancelled
  ├── waiters>0 ? ──► rejected++ ; (false, zero, nil)   // never barges
  ├── sem free ? ──► finishReserve ──► run(opTryExecute)
  └── sem full  ? ──► rejected++ ; (false, zero, nil)

run(b, ctx, waited, fn)
  active++ ; (defer releaseSlot: active-- THEN <-sem) ; executed++
  bc = {active, maxConcurrent, waitedSlot=waited}
  (val,err) = panix.Safe(op, fn(ctx, bc))
  return val, err
```

Admission is a three-phase strategy tuned for the common case. A pre-cancelled context is rejected before any work. The **optimistic phase** attempts a non-blocking send on the semaphore channel — when a slot is free **and no one is already waiting**, the call proceeds with **zero timer allocation**. If `waiters > 0`, the fast path is skipped so [TryExecute] and a lucky [Execute] cannot barge ahead of a blocked waiter. Only when every slot is busy (or waiters already exist) does the **slow phase** increment the waiter count, arm a `time.Timer` for `min(timeout, ctx remaining)`, and block on a four-way select: a freed slot, context cancellation, the wait timeout, or close.

On timer expiry the wait re-checks `ctx.Err()`: if the context is done it returns `ErrCancelled`, otherwise `ErrTimeout`. After winning the semaphore, a cancelled context refunds the slot instead of running the callback.

The semaphore is a buffered channel of capacity `maxConcurrent`: a send occupies a slot, a receive frees one. `releaseSlot` decrements `active` **before** freeing the semaphore so a spinner on `Active()` cannot observe a count above `maxConcurrent` during handover. The callback runs under `panix.Safe`, so a panic becomes a `*panix.PanicError` and the slot is still released — a panicking handler can never leak capacity.

### Load-aware callbacks

The `BulkController` handed to the callback carries the occupancy snapshot taken at admission: `Active()`, `MaxConcurrent()`, `Load()` (the active/max fraction), and `WaitedSlot()` (whether the call had to wait for a slot to free up). A handler can read `bc.Load()` and choose a cheaper path under pressure — serve from cache, skip enrichment, return a partial result — turning a hard concurrency wall into a graceful slope.

## Normative Contracts


| Contract             | Guarantee                                                                                                 |
| -------------------- | --------------------------------------------------------------------------------------------------------- |
| Concurrency bound    | The number of in-flight callbacks never exceeds `maxConcurrent`, even under contention (channel-enforced) |
| Bounded wait         | `Execute` and `Acquire` wait at most `min(timeout, ctx remaining)` for a slot, then return `ErrTimeout` or `ErrCancelled` if the context is done |
| Context first        | A pre-cancelled context returns `ErrCancelled` without invoking fn or consuming a slot (`Execute`, `TryExecute`, `Acquire`) |
| Cancel after claim   | Winning a slot on a cancelled context refunds it and returns `ErrCancelled` |
| Anti-barge           | `TryExecute` and the reserve fast path refuse a free slot while `waiters > 0` |
| Waiter cap           | `WithMaxWaiters(n)` (`n > 0`) rejects additional slow-path waiters with `ErrWaitersExceeded`; `0` (default) is unlimited |
| Cancel while waiting | A context cancelled during the wait returns `ErrCancelled` and consumes no slot                           |
| Non-blocking variant | `TryExecute` never blocks: it runs immediately, rejects with `(false, zero, nil)`, or returns `ErrCancelled`/`ErrClosed` |
| Slot release         | The slot is released when the callback returns **or panics**                                              |
| Panic safety         | A panicking callback becomes a `*panix.PanicError`, slot still freed                                      |
| Admission purity     | `Allow` reports a best-effort decision without mutating any counter or slot                               |
| Token release        | `Token.Release` is idempotent; a double release never drives active negative or double-frees a slot       |
| Close semantics      | After `Close`, new admissions return `ErrClosed`; optimistic admissions re-check closed via `commitSlot`; blocked slow-path waiters wake on `closedCh`; in-flight work is unaffected |
| Idempotent close     | `Close` is safe to call repeatedly and always returns nil                                                 |
| Controller scope     | A `BulkController` is valid only during its callback; do not retain it                                    |


## Quick Start

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aasyanov/urx/bulkx"
)

func main() {
	bh := bulkx.New(
		bulkx.WithMaxConcurrent(10),
		bulkx.WithTimeout(5*time.Second),
	)
	defer bh.Close()

	resp, err := bulkx.Execute(bh, context.Background(),
		func(ctx context.Context, bc bulkx.BulkController) (string, error) {
			if bc.Load() > 0.8 {
				return "lightweight", nil // degrade near saturation
			}
			return call(ctx)
		})

	switch {
	case errors.Is(err, bulkx.ErrTimeout):
		fmt.Println("no slot available:", err)
	case errors.Is(err, bulkx.ErrClosed):
		fmt.Println("closed:", err)
	case err != nil:
		fmt.Println("failed:", err)
	default:
		fmt.Println("ok:", resp)
	}
}

func call(context.Context) (string, error) { return "full", nil }
```

## Usage Scenarios

### Isolate each dependency with its own bulkhead

```go
// One slow dependency cannot starve the others — each gets a private slot pool.
var (
	dbBulk  = bulkx.New(bulkx.WithMaxConcurrent(20))
	apiBulk = bulkx.New(bulkx.WithMaxConcurrent(5))
)

resp, err := bulkx.Execute(apiBulk, ctx, callThirdParty)
row, err := bulkx.Execute(dbBulk, ctx, queryDatabase)
```

### Fail fast instead of blocking

```go
// Non-blocking admission: shed immediately when the pool is full.
ok, resp, err := bulkx.TryExecute(bh, ctx, expensiveCall)
if !ok && err == nil {
	return cachedResponse() // no slot now; do not wait
}
```

### Manual slot ownership with a Token

```go
tok, err := bh.Acquire(ctx)
if errors.Is(err, bulkx.ErrTimeout) {
	http.Error(w, "busy", http.StatusServiceUnavailable)
	return
}
defer tok.Release()
streamLargeResponse(w, r) // concurrency tracked across many statements
```

### Cheap pre-flight check

```go
// Reject at the edge without entering the handler at all.
if !bh.Allow() {
	return errBusy
}
```

### Compose with a per-request timeout (toutx)

```go
resp, err := bulkx.Execute(bh, ctx,
	func(ctx context.Context, _ bulkx.BulkController) (Report, error) {
		return toutx.Execute(ctx, 500*time.Millisecond,
			func(ctx context.Context, _ toutx.TimeoutController) (Report, error) {
				return build(ctx)
			})
	})
```

## API


| Symbol                   | Signature                                                                                                                        | Description                               |
| ------------------------ | -------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------- |
| `New`                    | `func New(opts ...Option) *Bulkhead`                                                                                             | Create a bulkhead with defaults + options |
| `Execute`                | `func Execute[T any](b *Bulkhead, ctx context.Context, fn func(context.Context, BulkController) (T, error)) (T, error)`          | Wait for a slot + run a callback          |
| `TryExecute`             | `func TryExecute[T any](b *Bulkhead, ctx context.Context, fn func(context.Context, BulkController) (T, error)) (bool, T, error)` | Non-blocking admission                    |
| `Bulkhead.Acquire`       | `func (b *Bulkhead) Acquire(ctx context.Context) (*Token, error)`                                                                | Manual admission returning a Token        |
| `Bulkhead.Allow`         | `func (b *Bulkhead) Allow() bool`                                                                                                | Non-mutating "is a slot free?" check      |
| `Bulkhead.Active`        | `func (b *Bulkhead) Active() int`                                                                                                | Current in-flight count                   |
| `Bulkhead.MaxConcurrent` | `func (b *Bulkhead) MaxConcurrent() int`                                                                                         | Configured slot count                     |
| `Bulkhead.Load`          | `func (b *Bulkhead) Load() float64`                                                                                              | Current active/max, in [0, 1]             |
| `Bulkhead.Stats`         | `func (b *Bulkhead) Stats() Stats`                                                                                               | Counter snapshot (`Waiters` is the live slow-path count) |
| `Bulkhead.ResetStats`    | `func (b *Bulkhead) ResetStats()`                                                                                                | Zero cumulative counters (not `Active` / `Waiters`) |
| `Bulkhead.Close`         | `func (b *Bulkhead) Close() error`                                                                                               | Idempotent shutdown                       |
| `Bulkhead.IsClosed`      | `func (b *Bulkhead) IsClosed() bool`                                                                                             | Report closed state                       |
| `Token.Release`          | `func (t *Token) Release()`                                                                                                      | Free an acquired slot (idempotent)        |


### BulkController


| Method          | Signature             | Description                                                         |
| --------------- | --------------------- | ------------------------------------------------------------------- |
| `Active`        | `Active() int`        | In-flight count at admission (includes self), in [1, MaxConcurrent] |
| `MaxConcurrent` | `MaxConcurrent() int` | Configured slot count                                               |
| `Load`          | `Load() float64`      | Occupancy fraction at admission, in (0, 1]                          |
| `WaitedSlot`    | `WaitedSlot() bool`   | Whether the call had to wait for a slot to free up                  |


## Configuration


| Option                 | Default                     | Description                                                      |
| ---------------------- | --------------------------- | ---------------------------------------------------------------- |
| `WithMaxConcurrent(n)` | `DefaultMaxConcurrent` (10) | Max concurrent operations; ≤ 0 ignored, final value floored to 1 |
| `WithTimeout(d)`       | `DefaultTimeout` (30s)      | Max wait for a slot before `ErrTimeout`; ≤ 0 ignored. Actual wait is `min(d, ctx remaining)` |
| `WithMaxWaiters(n)`    | `0` (unlimited)             | Cap on slow-path waiters; ≤ 0 ignored. Excess waiters get `ErrWaitersExceeded` |
| `WithOp(s)`            | `[opExecute]` / `[opTryExecute]` | Operation name attached to panic reports (`TryExecute` defaults to `"bulkx.TryExecute"`) |


## Errors


| Error                | Condition                                                                                             |
| -------------------- | ----------------------------------------------------------------------------------------------------- |
| `ErrTimeout`         | The wait timeout elapsed before a slot became available (context not done)                            |
| `ErrClosed`          | The bulkhead has been closed                                                                          |
| `ErrCancelled`       | The context was cancelled or expired before a slot was acquired, or after the slot was claimed and refunded (wraps `ctx.Err()`) |
| `ErrNilFunc`         | `Execute`/`TryExecute` was given a nil function                                                       |
| `ErrWaitersExceeded` | `WithMaxWaiters` is set and another slow-path waiter would exceed the cap                             |


A panicking callback surfaces as a `*panix.PanicError` returned by `Execute`/`TryExecute` (reach it with `errors.As`); the slot is still released.

## Pitfalls

> [!WARNING]
> **The wait timeout is not a request deadline.** `WithTimeout` bounds only how long `Execute` waits for a *slot*, and the actual wait is `min(timeout, ctx remaining)`. Once admitted, the callback runs as long as it wants. For a hard per-operation deadline, wrap the callback with `toutx.Execute`.

> [!WARNING]
> **TryExecute does not steal slots from waiters.** If another caller is blocked on `Execute`/`Acquire`, `TryExecute` returns `(false, zero, nil)` even when a slot is about to free. That is intentional anti-barge, not a full semaphore.

> [!WARNING]
> **Sizing concurrency wrong defeats the purpose.** `WithMaxConcurrent` must reflect the concurrency the *downstream* can sustain, not your accept queue. A bulkhead sized to 10× the real limit admits everything and isolates nothing.

> [!NOTE]
> **bulkx bounds concurrency, not request rate.** A flood of fast operations can churn through the slots without ever filling them. For requests-per-second limiting, use `ratex`; combine the two for both axes.

> [!NOTE]
> **No priorities.** Every caller competes for the same slots equally. If you need to shed low-priority traffic first while protecting critical traffic, use `shedx`.

## Safety and Concurrency

`Bulkhead` is safe for concurrent use from any number of goroutines. The concurrency bound is enforced by a buffered channel semaphore, so the number of in-flight callbacks is structurally incapable of exceeding `maxConcurrent` regardless of contention. `releaseSlot` decrements `active` before freeing the semaphore so observers never see a spike above the cap during handover. The surrounding counters (`active`, `waiters`, `executed`, `rejected`, `timeouts`, `closed`) live in `sync/atomic` values; there is no mutex. `Token.Release` uses an atomic compare-and-swap, so a double release is a no-op that can neither drive the active count negative nor double-free a slot. The `BulkController` is touched only by the single goroutine running its callback and needs no synchronization. Every test runs under `-race`, including a 64-goroutine stress test that asserts the observed in-flight count never exceeds the configured maximum.

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

### Admission Path

| Benchmark | What it measures | Laptop | Linux | Windows | B/op | allocs/op |
|---|---|---|---|---|---|---|
| Execute | Admit + callback + release | 97.5 ns | **66.4 ns** | 81.2 ns | 24 | 1 |
| Execute_Parallel | Execute, 8/4 goroutines | 240 ns | 182.2 ns | **155.8 ns** | 24 | 1 |
| Execute_Reject | Full bulkhead, non-blocking reject | 12.6 ns | **7.7 ns** | 9.9 ns | 0 | 0 |
| TryExecute | Non-blocking execute | 102 ns | **63.0 ns** | 80.5 ns | 24 | 1 |
| Acquire | Token hand-off (no callback) | 83.8 ns | **55.4 ns** | 68.3 ns | 16 | 1 |
| Acquire_Parallel | Acquire, 8/4 goroutines | 206 ns | 144.2 ns | **140 ns** | 16 | 1 |
| Allow | Best-effort slot check | **1.3 ns** | 0.7 ns | 1.8 ns | 0 | 0 |

### Analysis

**Linux and Windows CI are within ~3% on serial paths.** `Execute` (77 ns vs 78 ns), `Acquire` (65 ns), and `TryExecute` (75 ns vs 77 ns) differ only within run-to-run noise. The bulkhead hot path is a buffered channel plus atomics — no mutex — so OS impact is minimal compared to packages that take locks on every call.

**Parallel cost is predictable: ~1.5–2× serial on CI, ~2.5× on laptop.** `Execute_Parallel` is 158 ns vs 77 ns serial on Linux (2.0×). The slowdown is contention on the shared semaphore channel and the `active` counter cache line, not a lock convoy. Laptop parallel numbers are higher (240 ns) because eight benchmark goroutines compete for four CI-equivalent slots.

**Execute_Reject proves the shed path is alloc-free.** 8 ns on CI, 0 B/op — a failed non-blocking channel send plus a `rejected` counter increment. No timer, no callback, no controller. This is the cheapest way to turn away load when the bulkhead is full.

**Execute / TryExecute — 1 alloc / 24 B is the controller floor.** The `execution` controller escapes because it is handed through the `panix.Safe` closure as an interface. Channel send, `active`/`executed` counters, and deferred release are otherwise alloc-free.

**Acquire — 1 alloc / 16 B is the Token.** The token must escape to the caller; everything else is channel + counter work.

**Allow — 0 allocs, ~1.3–1.8 ns.** A single atomic load and comparison — no channel reservation. A hint only, not a binding decision; pair with `TryExecute` when you need an atomic admit/reject.

## Quality


| Metric         | Value                          |
| -------------- | ------------------------------ |
| Test functions | 64                             |
| Benchmarks     | 7                              |
| Fuzz targets   | 3                              |
| Examples       | 6                              |
| Coverage       | 100.0%                         |
| Race detector  | All pass                       |
| External deps  | 0 (panix; testify in dev only) |


## File Structure

```text
bulkx/
├── bulkx.go            # package doc + Bulkhead + Execute[T]/TryExecute[T] + Acquire/Token + admission
├── options.go          # config, Option, defaults, WithXxx
├── types.go            # BulkController + private execution impl
├── errors.go           # ErrTimeout, ErrClosed, ErrCancelled, ErrNilFunc, ErrWaitersExceeded
├── bulkx_test.go       # unit + table-driven tests
├── errors_test.go      # sentinel wrapping tests
├── bench_test.go       # benchmarks (sequential + parallel)
├── fuzz_test.go        # FuzzExecute, FuzzTryExecute, FuzzAcquireRelease — admission invariants
├── example_test.go     # runnable GoDoc examples
├── footprint_test.go   # struct size guards
└── README.md           # this file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.