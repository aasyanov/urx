# toutx — Deadline-Scoped Timeout Execution with Panic Safety

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/toutx)
[License: MIT](../LICENSE)

A generic timeout wrapper that runs a function under a deadline-scoped context, distinguishes a fired deadline from parent cancellation, recovers panics, and hands the callback a `TimeoutController` for self-throttling. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **toutx** enforces a deadline on the *caller*, not on the *callee*. When the timeout fires, `Execute` returns immediately with `ErrDeadlineExceeded`, but the function goroutine keeps running until it observes its cancelled context. A function that ignores `ctx.Done()` will leak a goroutine. Always honour the context inside the callback.

## The Problem

Wrapping a call in a timeout is a five-line idiom that almost everyone gets subtly wrong:

1. **Distinguishing "my deadline fired" from "my parent was cancelled".** A naked `context.WithTimeout` + `select` on `ctx.Done()` cannot tell whether the timeout you imposed expired or the caller upstream gave up — yet retry, fallback, and circuit-breaker logic need that distinction.
2. **Panics in the callee crashing the service.** The function runs in a goroutine (so the timeout can win the race); an unrecovered panic there takes down the whole process instead of becoming an error.
3. **No budget visibility for the callee.** A function that could return a cheap, partial result when little time is left has no standard way to ask "how much budget do I have?".
4. **Re-implementing defaults everywhere.** Each call site re-types the same 30 s default and operation label; there is no reusable, immutable preset.

`toutx` provides one generic `Execute` that solves all four: precise error classification, panic-to-error conversion, a `TimeoutController`, and a reusable `Timer` preset — `-race`-clean and 100% covered.

## Architectural Position

```text
✅ Execute[T]        — run fn under a deadline; classify deadline vs cancellation
✅ TimeoutController — per-call deadline/elapsed/remaining budget for the callback
✅ Timer + WithTimer — reusable, immutable preset of timeout + op label
✅ panic safety      — a panicking fn becomes a *panix.PanicError, never a crash

❌ NOT a retry engine — one attempt only (compose with retryx)
❌ NOT a rate limiter or bulkhead (see ratex / bulkx)
❌ NOT a way to *kill* a running goroutine — Go cannot; fn must honour its ctx
❌ NOT a scheduler — there is no queue, no background worker
```

### Position in the urx Stack

```text
┌───────────────────────────────────────────────────────────┐
│  service code: db.QueryContext, http client, RPC calls    │
└────────────────────────┬──────────────────────────────────┘
                         │
┌────────────────────────▼──────────────────────────────────┐
│  toutx   Execute[T] · TimeoutController · Timer           │
└──────────────┬───────────────────────┬────────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼───────────────────┐
│  panix.Safe            │   │  context (WithTimeout/Cause) │
│  (panic → PanicError)  │   │  time                        │
└────────────────────────┘   └──────────────────────────────┘
```

## Architecture

```text
                         toutx
   ┌───────────────────┬───────────────────┬───────────────────┐
   │                   │                   │                   │
 Execute[T]          Timer (options.go)   TimeoutController
 (toutx.go)          cfg config           (types.go)
   │                 New / WithTimer       execution{op,timeout,start}
 context.WithTimeout   │                   Op/Timeout/Deadline/
 goroutine + chan      │                   Elapsed/Remaining
 select{done, ctx}     │
   │                 Option (options.go)
 panix.Safe           WithTimeout/WithOp   errors.go
 (panic recovery)                          ErrDeadlineExceeded
                                           ErrCancelled / ErrNilFunc
```

## How It Works

```text
Execute(ctx, timeout, fn, opts...)
  │ cfg = defaults → opts → positional timeout (last wins)
  ├── fn == nil ? ─────────────► return ErrNilFunc
  ├── ctx already done ? ───────► return ErrCancelled(cause)
  │
  ├── tctx, cancel = WithTimeout(ctx, cfg.timeout)
  ├── go { done <- panix.Safe(op, fn(tctx, controller)) }
  └── select:
        ├── <-done       ─► return fn's (val, err)          [fn won the race]
        └── <-tctx.Done() ─► context.Cause(ctx) != nil ?
                              ├── yes ─► ErrCancelled(cause)  [parent cancelled]
                              └── no  ─► ErrDeadlineExceeded  [our timeout fired]
```

The function runs in its own goroutine so the timeout can win the race even when `fn` blocks. The result travels back on a buffered channel of capacity 1, so the goroutine never blocks on send even if `Execute` has already returned via the deadline branch.

The crucial step is classification. Both a parent cancellation and the locally-imposed deadline close `tctx`, so `Execute` inspects `context.Cause(ctx)` on the *parent*: if the parent carries a cancellation cause, the failure is `ErrCancelled` (wrapping that cause); otherwise it was this call's own deadline, reported as `ErrDeadlineExceeded`. This lets upstream resilience layers react correctly — retry on a local timeout, abort on an upstream cancel.

## Normative Contracts


| Contract                | Guarantee                                                                                                                 |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| Deadline classification | A fired local timeout returns `ErrDeadlineExceeded`; a cancelled/expired parent returns `ErrCancelled` wrapping the cause |
| Panic safety            | A panicking `fn` becomes a `*panix.PanicError`; the process never crashes                                                 |
| Nil guard               | A nil `fn` returns `ErrNilFunc` without launching a goroutine                                                             |
| Option precedence       | defaults → options (in order) → positional `timeout` argument (when > 0) wins last                                        |
| `Timer` immutability    | A `Timer` is never mutated after `New`; safe for concurrent reuse                                                         |
| Goroutine model         | On a fired deadline `Execute` returns immediately; `fn` must honour its context or its goroutine leaks                    |
| Result passthrough      | If `fn` returns before the deadline, its `(val, err)` is returned unchanged                                               |


## Quick Start

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aasyanov/urx/toutx"
)

func main() {
	resp, err := toutx.Execute(context.Background(), 5*time.Second,
		func(ctx context.Context, tc toutx.TimeoutController) (string, error) {
			return call(ctx)
		}, toutx.WithOp("api.fetch"))

	switch {
	case errors.Is(err, toutx.ErrDeadlineExceeded):
		fmt.Println("timed out:", err)
	case errors.Is(err, toutx.ErrCancelled):
		fmt.Println("cancelled upstream:", err)
	case err != nil:
		fmt.Println("call failed:", err)
	default:
		fmt.Println("ok:", resp)
	}
}

func call(ctx context.Context) (string, error) { return "ok", nil }
```

## Usage Scenarios

### Per-call timeout with precise error handling

```go
rows, err := toutx.Execute(ctx, 3*time.Second,
	func(ctx context.Context, _ toutx.TimeoutController) (*sql.Rows, error) {
		return db.QueryContext(ctx, query)
	}, toutx.WithOp("db.query"))
if errors.Is(err, toutx.ErrDeadlineExceeded) {
	metrics.Inc("db.timeout")
}
```

### Reusable preset with a Timer

```go
var apiTimer = toutx.New(
	toutx.WithTimeout(2*time.Second),
	toutx.WithOp("api"),
)

func Fetch(ctx context.Context, id string) (*Item, error) {
	return toutx.Execute(ctx, 0, func(ctx context.Context, _ toutx.TimeoutController) (*Item, error) {
		return client.Get(ctx, id)
	}, toutx.WithTimer(apiTimer))
}
```

### Budget-aware self-throttling via the controller

```go
result, err := toutx.Execute(ctx, 100*time.Millisecond,
	func(ctx context.Context, tc toutx.TimeoutController) (Report, error) {
		if tc.Remaining() < 20*time.Millisecond {
			return cheapReport(ctx) // not enough budget for the full computation
		}
		return fullReport(ctx)
	})
```

### Composing with retryx (retry on local timeout only)

```go
report, err := retryx.Do(ctx, func(ctx context.Context, rc retryx.RetryController) (Report, error) {
	return toutx.Execute(ctx, 500*time.Millisecond,
		func(ctx context.Context, _ toutx.TimeoutController) (Report, error) {
			return build(ctx)
		})
}, retryx.WithRetryIf(func(err error) bool {
	return errors.Is(err, toutx.ErrDeadlineExceeded) // retry timeouts, not cancellations
}))
```

## API


| Symbol              | Signature                                                                                                                                                   | Description                                                                |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| `Execute`           | `func Execute[T any](ctx context.Context, timeout time.Duration, fn TimeoutFunc[T], opts ...Option) (T, error)`                                             | Run fn under a deadline; classify deadline vs cancellation; recover panics |
| `New`               | `func New(opts ...Option) *Timer`                                                                                                                           | Create a reusable preset of timeout + op                                   |
| `Timer.Timeout`     | `func (t *Timer) Timeout() time.Duration`                                                                                                                   | The timer's configured timeout                                             |
| `Timer.Op`          | `func (t *Timer) Op() string`                                                                                                                               | The timer's configured operation name                                      |
| `WithTimeout`       | `func WithTimeout(d time.Duration) Option`                                                                                                                  | Set the time budget; values ≤ 0 ignored                                    |
| `WithOp`            | `func WithOp(op string) Option`                                                                                                                             | Set the operation label for errors and panics                              |
| `WithTimer`         | `func WithTimer(t *Timer) Option`                                                                                                                           | Seed a call from a `Timer` preset (nil ignored)                            |
| `Option`            | `type Option func(*config)`                                                                                                                                 | Functional option for `Execute` and `New`                                  |
| `TimeoutController` | `interface{ Op; Timeout; Deadline; Elapsed; Remaining }`                                                                                                    | Per-call budget exposed to the callback                                    |
| `TimeoutFunc`       | `type TimeoutFunc[T any] func(ctx context.Context, tc TimeoutController) (T, error)`                                                                      | Unit of work executed by `Execute`                                         |
| `DefaultTimeout`    | `const = 30 * time.Second`                                                                                                                                  | Timeout applied when none is configured                                    |


### TimeoutController


| Method      | Signature                   | Description                                    |
| ----------- | --------------------------- | ---------------------------------------------- |
| `Op`        | `Op() string`               | Logical operation name for this call           |
| `Timeout`   | `Timeout() time.Duration`   | Total budget configured for this call          |
| `Deadline`  | `Deadline() time.Time`      | Absolute instant the context is cancelled      |
| `Elapsed`   | `Elapsed() time.Duration`   | Time passed since the call started             |
| `Remaining` | `Remaining() time.Duration` | Time left before the deadline (clamped to ≥ 0) |


## Configuration


| Option           | Default                 | Description                                                                    |
| ---------------- | ----------------------- | ------------------------------------------------------------------------------ |
| `WithTimeout(d)` | `DefaultTimeout` (30 s) | Maximum execution time; `d ≤ 0` ignored                                        |
| `WithOp(op)`     | `"toutx.Execute"`       | Operation label in errors and panic reports; empty ignored                     |
| `WithTimer(t)`   | —                       | Seed config from a preset; positional timeout and later options still override |


## Errors


| Error                 | Condition                                                                                   |
| --------------------- | ------------------------------------------------------------------------------------------- |
| `ErrDeadlineExceeded` | The function did not complete before the configured timeout fired                           |
| `ErrCancelled`        | The parent context was cancelled or expired before the function completed (wraps the cause) |
| `ErrNilFunc`          | The supplied function was nil                                                               |


A panicking function does not produce a sentinel — it returns a `*panix.PanicError` (test with `errors.As`).

## Pitfalls

> [!WARNING]
> **A fired deadline does not stop `fn`.** `Execute` returns on timeout, but the goroutine running `fn` continues until it observes `ctx.Done()`. Functions that ignore their context leak goroutines. Every blocking operation inside `fn` must take and honour `ctx`.

> [!WARNING]
> **Option precedence is "positional timeout wins".** The `timeout` argument to `Execute` (when > 0) overrides `WithTimeout` and `WithTimer`. Pass `0` to defer entirely to options or a `Timer`.

> [!NOTE]
> **ErrDeadlineExceeded** vs `context.DeadlineExceeded`.** When the *parent* context carries a deadline that expires first, `toutx` reports `ErrCancelled` wrapping `context.DeadlineExceeded`. `ErrDeadlineExceeded` is reserved for the timeout *this* call imposed.

## Safety and Concurrency

`Execute` is a pure function over its arguments and holds no shared state, so it is inherently safe to call concurrently. Each call allocates its own `context.WithTimeout`, goroutine, and result channel. `Timer` is immutable after `New`, so a single `Timer` can be shared across any number of concurrent `Execute` calls. The `TimeoutController` is bound to one call and accessed only from that call's callback goroutine, requiring no synchronization. Every test runs under `-race`.

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

### Execute Path

| Benchmark | What it measures | Laptop | Linux | Windows | B/op | allocs/op |
|---|---|---|---|---|---|---|
| Execute_Fast | Instant `fn` under deadline | 1.88 µs | **1.71 µs** | 3.80 µs | 672 | 10 |
| Execute_WithTimer | Same + `WithTimer` preset copy | 1.86 µs | **1.74 µs** | 3.83 µs | 672 | 10 |
| Execute_Fast_Parallel | Fast execute, parallel | **654 ns** | 693 ns | 777 ns | 672 | 10 |

### Analysis

**10 allocs / 672 B is the architectural floor for timeout enforcement.** Every execute pays for `context.WithTimeout` (timer context + internal `time.Timer` + cancel closure), a worker goroutine, a buffered result channel, and the `panix.Safe` deferred-recover frame. None is removable without dropping a guarantee: no goroutine means the timeout cannot preempt a blocking `fn`; no timer context means no deadline propagation.

**Windows sequential execute is ~2.2× slower than Linux.** `Execute_Fast` is 1.71 µs (Linux) vs 3.80 µs (Windows) — the benchmark's sub-microsecond `fn` completes instantly, so measured time is almost entirely goroutine spawn + timer setup + scheduler handoff. Windows timer and goroutine creation latency dominates when `fn` adds no work. Laptop (1.88 µs) tracks Linux, not Windows CI.

**Parallel is faster per op — setup parallelizes.** `Execute_Fast_Parallel` is 693 ns (Linux) vs 1.71 µs serial — **2.5× faster** because goroutine/timer setup scales across cores. There is no shared lock on the hot path. Windows parallel (777 ns) still beats its own serial number (3.80 µs) by **4.9×**.

**WithTimer adds no allocations.** The ~20 ns delta over bare `Execute_Fast` on Linux is copying the preset timer config into the execution struct — within noise.

**Allocation floor is inherent to the model.** A caller that already holds a deadline-scoped context and trusts `fn` not to block can invoke `fn` directly. `toutx` trades 10 heap objects for race-correct timeout enforcement and panic safety.

## Quality


| Metric         | Value                          |
| -------------- | ------------------------------ |
| Test functions | 38                             |
| Benchmarks     | 3                              |
| Fuzz targets   | 1                              |
| Examples       | 5                              |
| Coverage       | 100.0%                         |
| Race detector  | All pass                       |
| External deps  | 0 (panix; testify in dev only) |


## File Structure

```text
toutx/
├── toutx.go            # package doc + Execute[T] + resolveDeadline
├── options.go          # config, Option, WithTimeout/WithOp, Timer, WithTimer
├── types.go            # TimeoutController, TimeoutFunc, private execution impl
├── errors.go           # ErrDeadlineExceeded, ErrCancelled, ErrNilFunc
├── toutx_test.go       # unit + table-driven tests
├── await_test.go       # white-box awaitResult / normalizeResult / resolveDeadline
├── errors_test.go      # sentinel wrapper unit tests
├── bench_test.go       # benchmarks (sequential + parallel)
├── fuzz_test.go        # FuzzExecute — timeout/duration/cancellation invariants
├── example_test.go     # runnable GoDoc examples
├── footprint_test.go   # struct size guards
└── README.md           # this file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.