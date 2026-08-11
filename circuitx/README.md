# circuitx — Circuit Breaker with Half-Open Probing

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/circuitx)
[License: MIT](../LICENSE)

A thread-safe circuit breaker that stops hammering a failing downstream: it counts consecutive failures, trips open to reject calls instantly, and after a cooldown admits a bounded number of probe calls to test recovery. Lock-free state machine, `Execute`/`TryExecute` with a controller for per-call decisions, a state-change hook for observability, and panic recovery. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **The breaker counts *consecutive* failures, not a rate.** A single success in the `Closed` state resets the counter to zero. If your downstream fails intermittently (say 1 in 3 calls) the breaker may never trip, because successes keep clearing the count. circuitx protects against *sustained* outages, not flaky tails — pair it with `[retryx](../retryx)` for transient errors and `[hedgex](../hedgex)` for tail latency.



## The Problem

When a downstream dependency goes down, a naive client makes everything worse:

1. **Resource exhaustion via piling.** Every caller keeps dialing the dead service, holding a goroutine, a connection, and a timeout budget for each doomed call. A 30-second timeout × thousands of requests exhausts file descriptors and goroutines on the *caller*, so a downstream outage cascades into a caller outage.
2. **No fast failure.** Without a breaker, callers learn the downstream is dead only after their own timeout fires — paying the full latency penalty on every single request for the entire outage.
3. **Thundering recovery.** The instant the downstream comes back, every queued caller slams it simultaneously, knocking it over again before it has warmed up.

A circuit breaker solves all three. It observes failures, and once they cross a threshold it **trips open**: subsequent calls fail instantly with `[ErrOpen](#errors)` without touching the downstream, freeing caller resources. After a cooldown it admits a *single* probe (or a small bounded number) to test the water before fully re-opening the floodgates, so recovery is gentle rather than a stampede.

## Architectural Position

```text
✅ Breaker            — three-state machine: Closed → Open → HalfOpen → Closed
✅ Execute[T]         — admit + run a callback under the breaker, recording the outcome
✅ TryExecute[T]      — non-blocking admission: run now or return (false, zero, nil)
✅ CircuitController  — state/failure snapshot + SkipFailure() and Trip() per call
✅ WithOnStateChange  — observability hook fired on every state transition
✅ panic safety       — a panicking callback becomes a *panix.PanicError, counted as a failure

❌ NOT a retrier      — it does not re-invoke fn on failure (see retryx)
❌ NOT a rate limiter — it does not bound requests-per-second (see ratex)
❌ NOT a bulkhead     — it does not cap concurrency (see bulkx / shedx)
❌ NOT a timeout      — it does not abort a slow call (compose with toutx)
❌ NOT per-key        — one Breaker guards one logical dependency; shard them yourself
```



### Position in the urx Stack

```text
┌──────────────────────────────────────────────────────────┐
│  service code: HTTP/RPC clients, downstream calls        │
└────────────────────────┬─────────────────────────────────┘
                         │
┌────────────────────────▼────────────────────────────────────────────┐
│  circuitx  Breaker · Execute[T] · TryExecute[T] · CircuitController │
│            trip open on sustained failure, probe to heal            │
└──────────────┬────────────────────────┬─────────────────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼───────────────────┐
│  panix.Safe            │   │  sync/atomic + sync.Mutex    │
│  (panic → PanicError)  │   │  (lock-free state, atomic    │
│                        │   │   transition edges)          │
└────────────────────────┘   └──────────────────────────────┘
```



## Architecture

```text
                         Breaker
   ┌─────────────────────────────────────────────────────┐
   │ cfg              maxFailures, resetTimeout,         │
   │                  halfOpenMax, onStateChange, op     │
   │                                                     │
   │ state            atomic.Uint32  (Closed/Open/Half)  │
   │ failures         atomic.Int32   (consecutive)       │
   │ lastOpen         atomic.Int64   (UnixNano of trip)  │
   │ halfOpenInflight atomic.Int32   (probe budget used) │
   │                                                     │
   │ successes/totalFail/rejected/trips  atomic.Uint64   │
   │ closed           atomic.Bool    (lifecycle)         │
   │ mu               sync.Mutex     (serializes edges)  │
   └─────────────────────────────────────────────────────┘
```

The hot path is lock-free: admission reads `state`, `failures`, and `halfOpenInflight` with plain atomic loads and a compare-and-swap to reserve a probe slot. The single mutex is taken only on a *state transition* (a trip or a close), so it never contends under steady-state traffic — it exists purely to make each edge atomic with respect to the trip counter and the `onStateChange` hook, guaranteeing the hook fires exactly once per edge.

## How It Works

```text
            success (resets failure count)
        ┌─────────────────────────────────────────┐
        │                                         │
        ▼          failures >= maxFailures        │
   ┌─────────┐ ───────────────────────────────► ┌──────┐
   │ Closed  │       or cc.Trip()               │ Open │
   └─────────┘ ◄─────────────────────────────── └──────┘
        ▲   probe success                          │
        │   (HalfOpen → Closed)                    │ resetTimeout elapsed
        │                                          ▼
        │                                    ┌──────────┐
        └──────────── probe success ──────── │ HalfOpen │
                                             └──────────┘
                          probe failure ──────►  Open
                     (any single probe re-opens)
```

A call flows through `[Execute](#api)` or `[TryExecute](#api)` like this:

1. **Guard.** If the breaker is `Close`d → `[ErrClosed](#errors)`; if `fn` is nil → `[ErrNilFunc](#errors)`; if `ctx` is already cancelled → `[ErrCancelled](#errors)`. None of these touch the state machine.
2. **Admission.** Read the state. In `Open`, reject — `[Execute](#api)` returns `[ErrOpen](#errors)`, `[TryExecute](#api)` returns `(false, zero, nil)`. In `HalfOpen`, atomically reserve one of `halfOpenMax` probe slots — if the budget is exhausted, reject the same way. In `Closed`, always admit.
3. **Lazy promotion.** Reading the state while `Open` checks whether `resetTimeout` has elapsed since the trip; if so, a single compare-and-swap promotes `Open → HalfOpen` and fires the hook. No background goroutine or timer is needed.
4. **Run.** `fn` runs under `[panix.Safe](../panix)`; a panic becomes a `*panix.PanicError` treated as a failure.
5. **Record.** On success, reset to a clean `Closed` if we were probing or carrying failures. On failure (not suppressed by `SkipFailure`), increment the consecutive count and trip to `Open` if the threshold is reached, the failure was a `HalfOpen` probe, or the callback called `Trip`. The callback's return value is always passed through together with any error.

```text
TryExecute(b, ctx, fn)
  │ closed ? ───────────────────────────► (false, zero, ErrClosed)
  │ fn == nil ? ────────────────────────► (false, zero, ErrNilFunc)
  │ ctx.Err() ? ────────────────────────► (false, zero, ErrCancelled)
  │
  ├── tryAdmit():  State() + probe CAS
  │     Open ? ──────────────────────────► rejected++ ; (false, zero, nil)
  │     HalfOpen, budget exhausted ? ─────► rejected++ ; (false, zero, nil)
  │
  └── admitted:
        executeRun(opTryExecute, fn) under panix.Safe
        return (true, val, err)
```



### State transitions (precise)


| From       | Event                                                        | To         | Notes                                                      |
| ---------- | ------------------------------------------------------------ | ---------- | ---------------------------------------------------------- |
| `Closed`   | failure, count `< maxFailures`                               | `Closed`   | counter incremented                                        |
| `Closed`   | failure, count `>= maxFailures`                              | `Open`     | trip recorded, timer started                               |
| `Closed`   | `cc.Trip()`                                                  | `Open`     | forced regardless of count                                 |
| `Closed`   | success                                                      | `Closed`   | counter reset to 0                                         |
| `Open`     | call rejected                                                | `Open`     | `Execute` → `ErrOpen`; `TryExecute` → `(false, zero, nil)` |
| `Open`     | `resetTimeout` elapsed (on `State()`/`Execute`/`TryExecute`) | `HalfOpen` | one CAS, hook fires; `Stats()` does **not** promote        |
| `HalfOpen` | probe success                                                | `Closed`   | counter reset, breaker healed                              |
| `HalfOpen` | probe failure or `Trip()`                                    | `Open`     | re-opened immediately, timer restarted                     |
| `HalfOpen` | probe budget exhausted                                       | `HalfOpen` | extra callers rejected (`ErrOpen` or `(false, zero, nil)`) |




## Normative Contracts


| Invariant                       | Guarantee                                                                                                                                            |
| ------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| Open rejects without `fn`       | A call rejected in `Open` or budget-exhausted `HalfOpen` never invokes `fn`; `Execute` returns `ErrOpen`, `TryExecute` returns `(false, zero, nil)`. |
| Probe budget                    | At most `WithHalfOpenMax` callbacks run concurrently in `HalfOpen`; the rest are rejected the same way.                                              |
| Non-blocking reject             | `TryExecute` returns `(false, zero, nil)` when the circuit rejects — rejection is a return value, not `ErrOpen`.                                     |
| Single trip per edge            | Concurrent failures that cross the threshold record exactly one trip and fire the hook once.                                                         |
| `SkipFailure` excludes counting | A skipped failure reaches the caller unchanged but never increments the failure count or trips.                                                      |
| Callback value pass-through     | `Execute`/`TryExecute` return the callback's `(val, err)` pair; failures do not zero `val`.                                                          |
| `Stats` is read-only            | `Breaker.Stats` reads the live state without lazy `Open → HalfOpen` promotion or hook side-effects.                                                  |
| Probe release is clamped        | Finishing probes never drive `halfOpenInflight` below zero, even after a concurrent `Reset`.                                                         |
| `Trip` overrides everything     | `cc.Trip()` opens the breaker even on success and even with `SkipFailure` set.                                                                       |
| `Close` ≠ `Closed` state        | `Close()` permanently disables the breaker (`ErrClosed`); the `Closed` *state* is the healthy mode.                                                  |
| No background goroutines        | Promotion to `HalfOpen` is lazy, evaluated on `State()`/`Execute`/`TryExecute`; nothing to leak.                                                     |




## Quick Start

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aasyanov/urx/circuitx"
)

func main() {
	cb := circuitx.New(
		circuitx.WithMaxFailures(5),
		circuitx.WithResetTimeout(10*time.Second),
	)

	resp, err := circuitx.Execute(cb, context.Background(),
		func(ctx context.Context, cc circuitx.CircuitController) (string, error) {
			if cc.State() == circuitx.HalfOpen {
				// Probe cheaply while testing recovery.
				return "", healthCheck(ctx)
			}
			return call(ctx)
		})

	switch {
	case errors.Is(err, circuitx.ErrOpen):
		fmt.Println("circuit open — serving fallback")
	case err != nil:
		fmt.Println("call failed:", err)
	default:
		fmt.Println("ok:", resp)
	}
}

func call(context.Context) (string, error) { return "pong", nil }
func healthCheck(context.Context) error    { return nil }
```



## Usage Scenarios



### 1. Guard an HTTP client call

```go
cb := circuitx.New(circuitx.WithMaxFailures(10), circuitx.WithResetTimeout(5*time.Second))

resp, err := circuitx.Execute(cb, ctx, func(ctx context.Context, _ circuitx.CircuitController) (*http.Response, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	return http.DefaultClient.Do(req)
})
if errors.Is(err, circuitx.ErrOpen) {
	return serveFromCache() // fail fast, no waiting on a dead upstream
}
```



### 2. Keep business errors from tripping the breaker

A `404 Not Found` means the downstream is *healthy* — it should not count toward the failure threshold. Mark it with `SkipFailure`:

```go
user, err := circuitx.Execute(cb, ctx, func(ctx context.Context, cc circuitx.CircuitController) (*User, error) {
	u, err := repo.Find(ctx, id)
	if errors.Is(err, ErrUserNotFound) {
		cc.SkipFailure() // a normal "miss", not an outage
	}
	return u, err
})
```



### 3. Trip eagerly on an unrecoverable signal

When the callback learns further calls are pointless (revoked credentials, a hard `503 Service Unavailable` with a long `Retry-After`), force the breaker open instead of waiting for the threshold:

```go
_, err := circuitx.Execute(cb, ctx, func(ctx context.Context, cc circuitx.CircuitController) (Resp, error) {
	resp, err := client.Call(ctx)
	if resp.StatusCode == http.StatusServiceUnavailable {
		cc.Trip() // open now; do not let more callers through
	}
	return resp, err
})
```



### 4. Export state transitions to metrics

```go
cb := circuitx.New(
	circuitx.WithMaxFailures(5),
	circuitx.WithOnStateChange(func(from, to circuitx.State) {
		metrics.Counter("circuit_transition", "from", from.String(), "to", to.String()).Inc()
	}),
)
```



### 5. Try without error handling on an open circuit

When the open circuit is the common case and you prefer a boolean over `errors.Is`, use `TryExecute`:

```go
ok, resp, err := circuitx.TryExecute(cb, ctx, callDownstream)
if err != nil {
	return err // closed breaker, nil fn, or cancelled context
}
if !ok {
	return serveFromCache() // circuit open — no ErrOpen to unwrap
}
return use(resp)
```



## API


| Symbol                          | Signature                                                                                | Description                                                                |
| ------------------------------- | ---------------------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| `New`                           | `New(opts ...Option) *Breaker`                                                           | Construct a breaker (defaults applied, options clamped).                   |
| `Execute`                       | `Execute[T any](b *Breaker, ctx context.Context, fn CircuitFunc[T]) (T, error)`          | Admit and run `fn` under the breaker; reject with `ErrOpen`.               |
| `TryExecute`                    | `TryExecute[T any](b *Breaker, ctx context.Context, fn CircuitFunc[T]) (bool, T, error)` | Non-blocking admit; reject with `(false, zero, nil)`.                      |
| `Breaker.State`                 | `State() State`                                                                          | Current state; lazily promotes `Open → HalfOpen` when the timeout elapses. |
| `Breaker.Failures`              | `Failures() int`                                                                         | Current consecutive failure count.                                         |
| `Breaker.Reset`                 | `Reset()`                                                                                | Force back to `Closed`, clear the failure count.                           |
| `Breaker.Stats`                 | `Stats() Stats`                                                                          | Read-only snapshot; does not promote state or fire hooks.                  |
| `Breaker.ResetStats`            | `ResetStats()`                                                                           | Zero the cumulative counters (state untouched).                            |
| `Breaker.Close`                 | `Close() error`                                                                          | Permanently disable; subsequent `Execute`/`TryExecute` return `ErrClosed`. |
| `Breaker.IsClosed`              | `IsClosed() bool`                                                                        | Whether `Close` was called (≠ `Closed` state).                             |
| `CircuitController.State`       | `State() State`                                                                          | State at admission (`Closed` or `HalfOpen`).                               |
| `CircuitController.Failures`    | `Failures() int`                                                                         | Failure count at admission.                                                |
| `CircuitController.MaxFailures` | `MaxFailures() int`                                                                      | Configured failure threshold.                                              |
| `CircuitController.SkipFailure` | `SkipFailure()`                                                                          | Do not count this call's error as a failure.                               |
| `CircuitController.Trip`        | `Trip()`                                                                                 | Force the breaker `Open` after this call.                                  |
| `State.String`                  | `String() string`                                                                        | `"closed"`, `"open"`, `"half_open"`.                                       |
| `CircuitFunc[T]`                | `func(ctx, cc CircuitController) (T, error)`                                             | The unit of work run by `Execute` and `TryExecute`.                        |




## Configuration


| Option                                       | Default                                        | Description                                                                                   |
| -------------------------------------------- | ---------------------------------------------- | --------------------------------------------------------------------------------------------- |
| `WithMaxFailures(n int)`                     | `5`                                            | Consecutive failures that trip `Closed → Open`. Values `< 1` floored to 1.                    |
| `WithResetTimeout(d time.Duration)`          | `10s`                                          | How long `Open` lasts before a probe is admitted. Values `<= 0` ignored.                      |
| `WithHalfOpenMax(n int)`                     | `1`                                            | Concurrent probes admitted in `HalfOpen`. Values `< 1` floored to 1.                          |
| `WithOnStateChange(fn func(from, to State))` | none                                           | Hook fired on each transition (not by `Stats`). Must not block or panic.                      |
| `WithOp(op string)`                          | `"circuitx.Execute"` / `"circuitx.TryExecute"` | Operation label attached to panic reports (`TryExecute` defaults to `"circuitx.TryExecute"`). |




## Errors


| Error          | Condition                                                                                                                                                 |
| -------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ErrOpen`      | `Execute` when the circuit is `Open`, or `HalfOpen` with the probe budget in use; `fn` is not invoked. `TryExecute` returns `(false, zero, nil)` instead. |
| `ErrNilFunc`   | `fn` passed to `Execute` or `TryExecute` is nil.                                                                                                          |
| `ErrClosed`    | `Execute` or `TryExecute` called after `Breaker.Close`.                                                                                                   |
| `ErrCancelled` | `ctx` is already cancelled/expired at admission; wraps `ctx.Err()`, state untouched.                                                                      |


All errors are comparable with `==` and `errors.Is`. `ErrCancelled` wraps the underlying `context` cause; reach it with `errors.Unwrap` or test with `errors.Is(err, context.Canceled)`.

`TryExecute` does not return `ErrOpen` — when the circuit rejects a call it returns `(false, zero, nil)`, leaving the decision to the caller. A panicking callback surfaces as a `*panix.PanicError` returned by `Execute` or `TryExecute` (reach it with `errors.As`); the probe slot is still released.

## Pitfalls

> [!WARNING]
> **Close()** is not the `Closed` state.** `Breaker.Close()` permanently shuts the breaker down — every later `Execute`/`TryExecute` returns `ErrClosed`. The `Closed` *state* (`Breaker.State() == Closed`) is the healthy operating mode. They are deliberately separate concepts; do not conflate `IsClosed()` (lifecycle) with `State() == Closed` (health).

> [!WARNING]
> **Intermittent failures may never trip.** Because a success resets the consecutive counter, a downstream that fails 1-in-2 with `WithMaxFailures(5)` can run forever without tripping. If you need to react to a failure *rate*, track it yourself and call `cc.Trip()` from the callback.

> [!WARNING]
> `Stats()` **is not** `State()`**.** `Breaker.Stats` returns a read-only snapshot and never promotes `Open → HalfOpen` or fires `onStateChange`. Use `Stats` for metrics middleware; use `State()` when admission logic needs the live (possibly promoted) state.

> [!WARNING]
> **The** `onStateChange` **hook runs on the caller's goroutine.** It fires synchronously inside `Execute`/`TryExecute`/`State`/`Reset` while no lock relevant to the hot path is held, but a slow or panicking hook will stall the calling request. Keep it to a counter increment or a non-blocking send.

> [!NOTE]
> **One breaker guards one dependency.** A `Breaker` aggregates failures globally. To isolate failures per host, per tenant, or per endpoint, create one breaker per key (e.g. in a `syncx.Map[string, *circuitx.Breaker]`).



## Safety and Concurrency

A `Breaker` is safe for concurrent use from any number of goroutines. State, failure count, probe budget, and all counters are `sync/atomic` values. The hot paths are lock-free: a successful call through a healthy `Closed` breaker, and a rejection by an `Open` breaker, take no mutex at all. The single `sync.Mutex` is acquired only when a state transition or failure settlement actually occurs (`recordFailure`, `recordSuccess` healing/forgiving, `openFrom`, `Reset`), making each edge atomic so the trip counter and `WithOnStateChange` hook fire exactly once. Crucially, every state-changing helper re-reads the live state under the lock, so a success can never silently re-close a breaker that a concurrent failure just tripped. Lazy `Open → HalfOpen` promotion rides a single compare-and-swap with no lock and is driven only by `State()`, `Execute`, and `TryExecute` — never by `Stats`. Half-open probe slots use a clamped release so `Reset` during an in-flight probe cannot corrupt the budget. There are no background goroutines and no timers: promotion is evaluated whenever the state is read for admission, so a `Breaker` needs no `Close` to avoid leaks (`Close` exists only to disable it).

## Benchmarks

Three environments, two hardware classes, two operating systems. All values are **medians**. `B/op` and `allocs/op` are deterministic — they depend on code, not hardware.

### Environments


|            | Laptop                      | CI Server (Linux)     | CI Server (Windows)   |
| ---------- | --------------------------- | --------------------- | --------------------- |
| CPU | Intel Core i7-10510U, 4C/8T | Intel Xeon 6973P-C | AMD EPYC 7763 |
| TDP        | 15W (mobile, throttles)     | 280W (server, stable) | server, stable        |
| OS         | Windows 10                  | Ubuntu                | Windows Server 2022   |
| Go         | 1.26.2                      | 1.26                  | 1.26                  |
| GOMAXPROCS | 8                           | 4                     | 4                     |
| Runs       | 3 (`-count=3`)              | 3 (`-count=3`)        | 3 (`-count=3`)        |




### State Machine Hot Paths


| Benchmark                  | What it measures                   | Laptop      | Linux       | Windows     | B/op | allocs/op |
| -------------------------- | ---------------------------------- | ----------- | ----------- | ----------- | ---- | --------- |
| Execute_Closed             | Success through healthy breaker    | 47.7 ns     | **28.1 ns** | 52.8 ns | 32 | 1 |
| Execute_Closed_Parallel    | Closed execute, parallel           | 44.9 ns     | 55.0 ns | **52.0 ns** | 32 | 1 |
| Execute_Open               | Reject before callback (open)      | 30.9 ns     | 47.5 ns | **27.2 ns** | 0 | 0 |
| Execute_Open_Parallel      | Open reject, parallel              | 29.7 ns     | 51.3 ns | **21.9 ns** | 0 | 0 |
| TryExecute_Closed          | Non-blocking closed path           | 48.0 ns     | **27.5 ns** | 53.0 ns | 32 | 1 |
| TryExecute_Open            | Non-blocking open reject           | 31.1 ns     | 48.5 ns | **27.7 ns** | 0 | 0 |
| TryExecute_Open_Parallel   | Open reject, parallel              | 30.1 ns     | 52.0 ns | **22.2 ns** | 0 | 0 |
| TryExecute_Closed_Parallel | Closed try, parallel               | 45.0 ns     | 55.9 ns | **40.5 ns** | 32 | 1 |
| State                      | Atomic state read (+ lazy promote) | **1.8 ns**  | 1.0 ns | 2.3 ns | 0 | 0 |
| Stats                      | Lock-free counter snapshot         | **1.4 ns*** | 7.0 ns | 11.8 ns | 0 | 0 |


 Laptop `Stats` is an outlier vs CI (~11 ns on both servers) — likely turbo + very short loop on the first run; treat CI numbers as the stable baseline.

### Analysis

**Closed-path execute — 1 alloc (32 B), lock-free on success.** The allocation is the `*execution` controller passed through the `CircuitController` interface into the `panix.Safe` closure. A success in a healthy `Closed` breaker never touches the mutex — only atomic loads and the controller hand-off. Sequential and parallel numbers stay within ~20% on CI because the hot path shares only atomics.

**Open-path reject — 0 allocs, ~26–72 ns.** When the circuit is open the call is rejected before any controller is built. Windows CI is **2.7× faster** than Linux on `Execute_Open` (26 ns vs 71 ns) — both are lock-free, so the spread is CPU micro-architecture on the runner, not an OS mutex tax. The reject path is exactly what you want when shedding a flood of doomed calls.

**Parallel scaling is flat on open, modest on closed.** `Execute_Open_Parallel` (~25–32 ns) shows no mutex convoy. `Execute_Closed_Parallel` is faster than serial on Linux (28 ns vs 44 ns) because four goroutines hide callback latency; Windows parallel closed (37 ns) sits between.

**State — ~2 ns on CI.** One atomic load; when `Open` and expired, an extra clock read and CAS drive half-open promotion. Cheap enough for admission polling.

**Stats — ~11 ns on CI, 0 allocs.** A handful of atomic loads; no clock read, no promotion, no hook — safe for high-frequency metrics scraping.

## Quality


| Metric         | Value                          |
| -------------- | ------------------------------ |
| Test functions | 60                             |
| Benchmarks     | 10                             |
| Fuzz targets   | 2                              |
| Examples       | 5                              |
| Coverage       | 100.0%                         |
| Race detector  | All pass                       |
| External deps  | 0 (panix; testify in dev only) |




## File Structure

```text
circuitx/
├── circuitx.go        # Breaker, New, Execute, TryExecute, state machine, lifecycle
├── types.go           # State, CircuitController + execution impl, CircuitFunc, Stats
├── options.go         # config, defaults, WithXxx options
├── errors.go          # sentinel errors + context wrapper
├── circuitx_test.go   # unit + table-driven + edge + concurrency tests
├── bench_test.go      # sequential + parallel benchmarks
├── fuzz_test.go       # FuzzExecute, FuzzTryExecute — state-machine invariants
├── example_test.go    # runnable GoDoc examples
├── footprint_test.go  # struct size guards
└── README.md          # this document
```



## License

MIT — see [LICENSE](../LICENSE) in the repository root.