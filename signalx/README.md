# signalx — OS Signal Trapping and Graceful Shutdown for Go

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/signalx)
[License: MIT](../LICENSE)

Signal-driven context cancellation plus bounded, panic-safe shutdown hooks. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package and the standard library; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **signalx** is a shutdown sequencer, not a process supervisor.** It trades the signal into a cancelled `context.Context` and then runs your teardown hooks once, in order, under a deadline. It does **not** restart your process, daemonize it, manage child processes, or re-deliver signals. Trap the signal with [`Trap`], run resources off the returned context, and drain them with [`Wait`].



## The Problem

A production HTTP service holds open connections, in-flight requests, a database pool, a message-broker consumer, and background workers. When the orchestrator sends `SIGTERM` (Kubernetes pod eviction, `systemctl stop`, Ctrl-C in development), the process has a short grace window — typically 10–30 seconds — to stop accepting work, drain what is in flight, flush buffers, and close connections cleanly before it is `SIGKILL`ed.

Done by hand, every service re-implements the same fragile boilerplate: a `signal.Notify` channel, a goroutine that bridges the signal into cancellation, an ad-hoc list of "things to close", a `context.WithTimeout` to bound the drain, and a `defer recover()` in case one of the close functions panics and aborts the rest. The pieces are individually trivial and collectively easy to get subtly wrong — leaked watcher goroutines, hooks that run after the deadline, a single panicking `Close()` that skips every remaining resource, or a forgotten `signal.Stop`.

`signalx` satisfies **five requirements**:

1. **Signal → context** — one call converts `SIGINT`/`SIGTERM` (or any custom set) into a cancellable `context.Context`.
2. **Ordered teardown** — hooks run deterministically in registration order, globals first.
3. **Bounded drain** — every hook runs under a shared deadline; overrun is reported, not hung.
4. **Panic isolation** — a panicking hook is recovered and reported; remaining hooks still run.
5. **No leaks** — the watcher goroutine exits and `signal.Stop` is called when the context is cancelled.



## Architectural Position

```text
✅ A signal-to-context bridge (Trap)
✅ An ordered, bounded, panic-safe shutdown runner (Wait / WaitWith)
✅ A process-global hook registry (OnShutdown)

❌ NOT a process supervisor or init system (no restarts)
❌ NOT a daemon manager (no forking, no PID files)
❌ NOT a lifecycle framework (it sequences teardown; it does not start things)
❌ NOT a signal multiplexer (one cancellation, not per-signal dispatch)
```



### Position in the urx Stack

```text
┌──────────────────────────────────────────────────────────┐
│  main(): servers, pools, consumers started from ctx      │
└────────────────────────┬─────────────────────────────────┘
                         │ Trap → cancelled ctx
┌────────────────────────▼─────────────────────────────────┐
│  signalx  Trap · Wait · OnShutdown                       │
└──────────────┬────────────────────────┬──────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼───────────────────┐
│  panix.SafeVoid        │   │  os/signal · context         │
│  (hook panic recovery) │   │  (SIGINT/SIGTERM → cancel)   │
└────────────────────────┘   └──────────────────────────────┘
```

`signalx` sits at the very top of an application: `main` traps the signal, and `Wait` orchestrates the shutdown of everything `main` started. It depends on `panix` for hook panic recovery and on nothing else.

## Architecture

```text
        OS signal (SIGINT/SIGTERM/…)
                  │
                  ▼
        ┌───────────────────┐        parent ctx
        │       Trap        │◄──────── cancel ──────┐
        │  signal.Notify    │                       │
        │  watcher goroutine│── cancel() ──► derived ctx (Done)
        └───────────────────┘                       │
                  │ signal.Stop on exit             │
                  ▼                                 │
        ┌───────────────────────────────────────────▼───────┐
        │                      Wait                         │
        │   <-ctx.Done()                                    │
        │   shutCtx = WithTimeout(background, timeout)      │
        │   hooks = globalHooks ++ callHooks  (in order)    │
        │   for hook in hooks:                              │
        │       if shutCtx expired → ErrShutdownTimeout     │
        │       runHook(shutCtx, hook)  ── panix.SafeVoid ──┤
        │           panic → ErrHookPanic (joined cause)     │
        └───────────────────────────────────────────────────┘
                  │
                  ▼
        errors.Join(...)  → nil | ErrShutdownTimeout | ErrHookPanic
```



## How It Works



### Trap: signal → cancellation

`Trap(parent, signals...)` derives a cancellable context from `parent` and registers a buffered `signal.Notify` channel. A single watcher goroutine selects on either the signal channel or `ctx.Done()`:

```text
go func() {
    defer signal.Stop(ch)
    select {
    case <-ch:        → cancel()          // signal arrived
    case <-ctx.Done():                    // parent cancelled or CancelFunc called
    }
}()
```

Whichever happens first, the goroutine calls `signal.Stop` and exits — no leak. The returned `CancelFunc` is the standard `context.WithCancel` cancel: idempotent and safe to call from any goroutine. A `nil` parent is promoted to `context.Background()`; an empty signal set defaults to `SIGINT, SIGTERM`.

### Wait: ordered, bounded, panic-safe drain

`Wait(ctx, hooks...)` blocks on `<-ctx.Done()`, then builds a single ordered hook list — every hook registered via `OnShutdown` first (snapshotted under a mutex), then the per-call hooks. It creates a shutdown context with the configured timeout (default 15s) and runs each hook through `panix.SafeVoid`:

```text
1. <-ctx.Done()
2. shutCtx, cancel = WithTimeout(background, timeout)   // timeout ≤ 0 ⇒ no deadline
3. hooks = OnShutdown-hooks ++ call-hooks
4. for hook in hooks:
     ├── shutCtx expired?  → stop, record ErrShutdownTimeout
     └── runHook(shutCtx, hook)
            ├── ok          → continue
            └── panic       → record ErrHookPanic ⊕ *panix.PanicError, continue
5. if a hook overran the deadline → record ErrShutdownTimeout
6. return errors.Join(records...)
```

The deadline is checked **before each hook** and **once more after the loop**, so a single long-running hook that overruns the deadline is reported even when no subsequent hook exists to observe it. A panicking hook does **not** abort the sequence — the panic is recovered, recorded, and the remaining hooks still run, because shutdown is exactly when you most want every resource to get its chance to close.

### Timeout semantics

`WithTimeout(0)` (or any non-positive duration) disables the deadline: hooks run to completion with a plain cancellable context. With a positive timeout, hooks share one deadline — it bounds the **total** drain, not each hook individually.

## Normative Contracts


| Contract                                       | Guarantee                                                                              |
| ---------------------------------------------- | -------------------------------------------------------------------------------------- |
| `Trap` never leaks its goroutine               | The watcher exits on signal, parent cancel, or `CancelFunc`; `signal.Stop` always runs |
| `Trap` CancelFunc is idempotent                | Calling it repeatedly is safe (standard `context` semantics)                           |
| `Trap(nil, …)` is safe                         | A nil parent becomes `context.Background()`                                            |
| `Wait` runs hooks in order                     | Global hooks (registration order) precede per-call hooks (argument order)              |
| `Wait` runs every non-skipped hook             | A panicking hook does not abort the remaining hooks                                    |
| `Wait` bounds the drain                        | Hooks that exceed the timeout yield `ErrShutdownTimeout`                               |
| `Wait` never panics from hook execution        | Hook panics become `ErrHookPanic` joined with `*panix.PanicError`                      |
| `Wait(nil, …)` is safe                         | A nil context becomes `context.Background()` (and thus blocks forever)                 |
| Nil hooks panic at registration/call time      | `OnShutdown(nil)`, `Wait(ctx, nil)`, and `Trap(ctx, nil signal)` are programmer errors |
| `OnShutdown`/`ResetHooks` are concurrency-safe | The global registry is mutex-protected                                                 |




## Quick Start

```go
package main

import (
	"context"
	"log"
	"net/http"

	"github.com/aasyanov/urx/signalx"
)

func main() {
	ctx, cancel := signalx.Trap(context.Background())
	defer cancel()

	srv := &http.Server{Addr: ":8080"}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	// Blocks until SIGINT/SIGTERM, then drains within 15s (default).
	if err := signalx.Wait(ctx, func(ctx context.Context) {
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("graceful shutdown failed: %v", err)
		}
	}); err != nil {
		log.Printf("shutdown completed with errors: %v", err)
	}
}
```



## Usage Scenarios



### Multi-resource drain with a custom budget

Stop accepting traffic first, then close downstream dependencies, all within a 30-second budget:

```go
ctx, cancel := signalx.Trap(context.Background())
defer cancel()

err := signalx.WaitWith(ctx, []signalx.Option{signalx.WithTimeout(30 * time.Second)},
	func(ctx context.Context) { httpServer.Shutdown(ctx) }, // drain HTTP first
	func(ctx context.Context) { consumer.Close() },         // stop the broker consumer
	func(ctx context.Context) { db.Close() },               // close the pool last
)
if err != nil {
	if errors.Is(err, signalx.ErrShutdownTimeout) {
		log.Println("drain exceeded budget — forcing exit")
	}
	os.Exit(1)
}
```



### Decentralized hook registration

Subsystems register their own teardown at construction time; `main` never needs to know about them:

```go
// in metrics package init
signalx.OnShutdown(func(ctx context.Context) { metrics.Flush(ctx) })

// in tracing package init
signalx.OnShutdown(func(ctx context.Context) { tracer.Shutdown(ctx) })

// in main
ctx, cancel := signalx.Trap(context.Background())
defer cancel()
signalx.Wait(ctx) // global hooks run automatically, in registration order
```



### Custom signal set

React only to `SIGHUP` (e.g. for a reload-as-restart strategy) alongside the defaults:

```go
ctx, cancel := signalx.Trap(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
defer cancel()
signalx.Wait(ctx, drainFn)
```



### Composing with other cancellation

`Trap` derives from any parent context, so an internal fatal error can trigger the same shutdown path as a signal:

```go
root, rootCancel := context.WithCancel(context.Background())
ctx, cancel := signalx.Trap(root) // cancelled by signal OR rootCancel()
defer cancel()

go func() {
	if fatal := runSupervisor(); fatal != nil {
		rootCancel() // triggers the same Wait drain as a SIGTERM
	}
}()

signalx.Wait(ctx, drainFn)
```



## API


| Symbol               | Signature                                                                                       | Description                                                                                                                |
| -------------------- | ----------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| `Trap`               | `func Trap(parent context.Context, signals ...os.Signal) (context.Context, context.CancelFunc)` | Derive a context cancelled when one of `signals` arrives (default `SIGINT, SIGTERM`; panics if any explicit signal is nil) |
| `Wait`               | `func Wait(ctx context.Context, hooks ...func(ctx context.Context)) error`                      | Block on `ctx`, then run global + per-call hooks under the default 15s timeout                                             |
| `WaitWith`           | `func WaitWith(ctx context.Context, opts []Option, hooks ...func(ctx context.Context)) error`   | Configurable form of `Wait`                                                                                                |
| `OnShutdown`         | `func OnShutdown(fn func(ctx context.Context))`                                                 | Register a process-global hook (runs before per-call hooks; panics if fn is nil)                                           |
| `ResetHooks`         | `func ResetHooks()`                                                                             | Clear the global hook registry (intended for tests)                                                                        |
| `Option`             | `type Option func(*config)`                                                                     | Functional option for `WaitWith`                                                                                           |
| `WithTimeout`        | `func WithTimeout(d time.Duration) Option`                                                      | Set the total hook-drain timeout; `≤ 0` disables it                                                                        |
| `ErrShutdownTimeout` | `var ErrShutdownTimeout error`                                                                  | Hooks did not finish within the timeout                                                                                    |
| `ErrHookPanic`       | `var ErrHookPanic error`                                                                        | One or more hooks panicked (joined with `*panix.PanicError`)                                                               |




## Configuration


| Option           | Default | Description                                                             |
| ---------------- | ------- | ----------------------------------------------------------------------- |
| `WithTimeout(d)` | `15s`   | Total time budget for all hooks. `0` or negative disables the deadline. |




## Errors


| Error                | Condition                                                                           |
| -------------------- | ----------------------------------------------------------------------------------- |
| `ErrShutdownTimeout` | The configured timeout elapsed before every hook completed                          |
| `ErrHookPanic`       | A shutdown hook panicked; the joined error carries the `*panix.PanicError` cause(s) |


Both may appear in the same returned error when a hook panics and the drain budget is exhausted before later hooks finish. Use `errors.Is` for each sentinel independently.

Both are sentinel errors created with `errors.New`; compare with `errors.Is`. When a hook panics, `errors.As(err, &pe)` extracts the underlying `*panix.PanicError` with `Op == "signalx.Wait"`.

## Pitfalls

> [!WARNING]
> **Hooks do not return errors to** `Wait`**.** Shutdown hooks are `func(context.Context)` with no return value. Log or record failures inside the hook (`if err := srv.Shutdown(ctx); err != nil { log.Printf(...) }`). Only timeout overruns and recovered panics surface through `Wait`'s return value.

> [!WARNING]
> **Nil hooks panic at registration or call time.** `OnShutdown(nil)`, `Wait(ctx, nil)`, and `Trap(ctx, nil signal)` are programmer errors and panic immediately — the same contract as `healthx.Register` with a nil check function.

> [!WARNING]
> **Hooks must respect their context — the timeout does not forcibly kill them.** Hooks run synchronously in the caller's goroutine; `WithTimeout` cancels the context passed to each hook but cannot interrupt a hook that ignores it. A hook that blocks on a non-context-aware call (e.g. a `Close()` with no deadline) will hang `Wait` indefinitely. Always thread the hook's `ctx` into the operations it performs (`srv.Shutdown(ctx)`, `db.PingContext(ctx)`), or wrap a stubborn call in its own goroutine + `select`.

> [!WARNING]
> **Wait(nil, …)** blocks forever.** A nil context is promoted to `context.Background()`, which is never done. Always pass the context returned by `Trap` (or another cancellable context).

> [!WARNING]
> **The timeout bounds the total drain, not each hook.** Ten hooks sharing a 15s budget must collectively finish in 15s. `ErrShutdownTimeout` is reported once the budget is exhausted, and any not-yet-started hooks are skipped. Size the timeout for the slowest realistic full drain, or split long-running teardown into its own watchdog.

> [!WARNING]
> **A panicking hook is recovered, not propagated.** `Wait` records `ErrHookPanic` and continues with the next hook. Inspect the returned error after shutdown — do not assume a clean teardown just because the process did not crash.

> [!WARNING]
> **OnShutdown** registers globally for the process lifetime.** Hooks accumulate across the registry until `ResetHooks` is called. In tests, always `ResetHooks()` (e.g. via `t.Cleanup`) to avoid cross-test contamination.

> [!WARNING]
> **Trap**'s `CancelFunc` must be called.** Even though the watcher goroutine also exits on signal, leaking the cancel func leaks the derived context. Always `defer cancel()`.



## Safety and Concurrency

**Thread safety.** `OnShutdown` and `ResetHooks` serialize access to the global registry with a `sync.Mutex`. `Wait`/`WaitWith` snapshot the registry under that lock before running hooks, so concurrent registration during a drain is race-free (a hook registered after the snapshot simply runs on the next `Wait`). All functions pass the `-race` detector.

**Goroutine model.** `Trap` launches exactly one watcher goroutine per call; it terminates on signal delivery, parent cancellation, or `CancelFunc`, always calling `signal.Stop`. `Wait` runs hooks synchronously in the caller's goroutine — no goroutines are spawned to run hooks, so a hook that respects its context deadline keeps the drain bounded.

**Race detector.** The suite hammers `OnShutdown` from 50 goroutines and runs 20 concurrent `Wait` calls sharing a global hook, all under `-race`.

## Benchmarks

Three environments, two hardware classes, two operating systems. All values are **medians** of `-count=3` runs. `B/op` and `allocs/op` are deterministic — they depend on code, not hardware.

### Environments


|            | Laptop                      | CI Server (Linux)             | CI Server (Windows)            |
| ---------- | --------------------------- | ----------------------------- | ------------------------------ |
| CPU | Intel Core i7-10510U, 4C/8T | Intel Xeon 6973P-C | AMD EPYC 7763 |
| TDP        | 15W (mobile, throttles)     | 280W (server, stable)         | server, stable                 |
| OS         | Windows 10                  | Ubuntu                        | Windows Server 2022            |
| Go         | 1.26                        | 1.26                          | 1.26                           |
| GOMAXPROCS | 8                           | 4                             | 4                              |
| Source | `local laptop` | CI benchmark job (count=3) | CI benchmark job (count=3) |

| Benchmark                  | What it measures                 | Laptop       | Linux        | Windows  | B/op | allocs/op |
| -------------------------- | -------------------------------- | ------------ | ------------ | -------- | ---- | --------- |
| `RunHook`                  | Single hook via `panix.SafeVoid` | **13.0 ns**  | 6.8 ns | 13.7 ns | 0 | 0 |
| `OnShutdown`               | Global hook registration         | **51.7 ns**  | 44.8 ns | 60.3 ns | 40 | 0 |
| `Wait_NoHooks`             | Full drain, no hooks             | 636.3 ns     | **482.2 ns** | 726.3 ns | 376 | 7 |
| `Wait_SingleHook`          | Drain with one hook              | 680.6 ns     | **486.8 ns** | 803.7 ns | 384 | 8 |
| `Wait_TenHooks`            | Drain with ten hooks             | 848.7 ns     | **597.5 ns** | 1062.0 ns | 456 | 8 |
| `Wait_SingleHook_Parallel` | Drain, 8/4 goroutines            | **417.9 ns** | 300.8 ns | 512.9 ns | 384 | 8 |




### Analysis

**RunHook: 13–14 ns, 0 allocs — the architecturally meaningful hot path.** The per-hook execution path — `panix.SafeVoid` wrapping the hook — adds only the cost of a deferred `recover()` over a direct call, with zero heap escape on the happy path. This is allocation-free on all three platforms.

**OnShutdown: cold-path registration.** 52–70 ns, 0 amortized allocs/op — a mutex lock plus a slice append. The reported B/op (41–48) is amortized slice-growth backing-array reallocation across the benchmark's append sequence; once the registry is sized, steady-state appends do not allocate. Registration happens at startup, so this cost is irrelevant to request latency.

**Wait_*: context construction dominates, not hook execution.** 609–990 ns, 7–8 allocs — these benchmarks intentionally include `context.WithCancel` + `cancel()` and the internal `context.WithTimeout` per iteration, because that is the realistic unit of work: one full drain. The allocation floor (7 allocs for the no-hook case) is dominated by the two context constructions and the channel/timer they create — not by `signalx` logic itself. Adding ten hooks adds ~230 ns (651 → 838 ns on Linux) and the same 8 allocs as the single-hook case, because the hook slice is pre-sized with known capacity and each hook runs allocation-free.

**Laptop vs CI — 2× improvement on Wait since last measurement.** Previous laptop figures (~1,300 ns) reflected older Go/runtime behavior or noisier runs. Current medians (636 ns laptop, 609 ns Linux) are stable across CI (< 15% spread). The shutdown drain is a once-per-process cold path; sub-microsecond differences are irrelevant in production.

**Parallel Wait is faster than sequential on the laptop.** `Wait_SingleHook_Parallel` (418 ns) beats sequential (681 ns) because each goroutine constructs its own cancelled context independently — no mutex contention on the registry snapshot, and the critical section (slice copy under lock) completes in nanoseconds.

**Allocation floor.** Shutdown is a once-per-process cold path; there is no value in driving `Wait` to 0 allocs. The architecturally meaningful number is `RunHook` at 0 allocs — the only operation that could conceivably run in a tight loop.

## Quality


| Metric                | Value                                                                         |
| --------------------- | ----------------------------------------------------------------------------- |
| Test functions        | 30                                                                            |
| Table-driven subtests | 4                                                                             |
| Benchmarks            | 6                                                                             |
| Fuzz targets          | 1                                                                             |
| Examples              | 3                                                                             |
| Coverage              | 100% (Linux/CI); 98.6% (Windows, signal-delivery branch is `//go:build unix`) |
| Race detector         | All pass                                                                      |
| External deps         | 0 (urx/panix internally; testify in tests only)                               |




## File Structure

```text
signalx/
├── signalx.go            # Trap, Wait, WaitWith, OnShutdown, ResetHooks, hook runner
├── errors.go             # ErrShutdownTimeout, ErrHookPanic sentinels
├── errors_test.go        # Sentinel identity tests
├── options.go            # Option, config, WithTimeout, defaults
├── signalx_test.go       # Unit + table-driven + concurrency tests (cross-platform)
├── signalx_unix_test.go  # Real signal-delivery tests (//go:build unix)
├── bench_test.go         # 6 benchmarks: hook run, registration, drain, parallel
├── fuzz_test.go          # FuzzWaitWith — hook drain + panic recovery
├── example_test.go       # 3 runnable GoDoc examples
├── footprint_test.go     # config struct size guard
└── README.md             # This file
```



## License

MIT — see [LICENSE](../LICENSE) in the repository root.