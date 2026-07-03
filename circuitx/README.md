# circuitx — Circuit Breaker with Half-Open Probing

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/circuitx)
[License: MIT](https://opensource.org/licenses/MIT)

A thread-safe circuit breaker that stops hammering a failing downstream: it counts consecutive failures, trips open to reject calls instantly, and after a cooldown admits a bounded number of probe calls to test recovery. Lock-free state machine, a controller for per-call decisions, a state-change hook for observability, and panic recovery. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

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
┌────────────────────────▼─────────────────────────────────┐
│  circuitx  Breaker · Execute[T] · CircuitController      │
│            trip open on sustained failure, probe to heal │
└──────────────┬───────────────────────┬───────────────────┘
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

A call flows through `[Execute](#api)` like this:

1. **Guard.** If the breaker is `Close`d → `[ErrClosed](#errors)`; if `fn` is nil → `[ErrNilFunc](#errors)`; if `ctx` is already cancelled → `[ErrCancelled](#errors)`. None of these touch the state machine.
2. **Admission.** Read the state. In `Open`, reject with `[ErrOpen](#errors)` (no `fn`). In `HalfOpen`, atomically reserve one of `halfOpenMax` probe slots — if the budget is exhausted, reject with `ErrOpen`. In `Closed`, always admit.
3. **Lazy promotion.** Reading the state while `Open` checks whether `resetTimeout` has elapsed since the trip; if so, a single compare-and-swap promotes `Open → HalfOpen` and fires the hook. No background goroutine or timer is needed.
4. **Run.** `fn` runs under `[panix.Safe](../panix)`; a panic becomes a `*panix.PanicError` treated as a failure.
5. **Record.** On success, reset to a clean `Closed` if we were probing or carrying failures. On failure (not suppressed by `SkipFailure`), increment the consecutive count and trip to `Open` if the threshold is reached, the failure was a `HalfOpen` probe, or the callback called `Trip`.

### State transitions (precise)


| From       | Event                                           | To         | Notes                                  |
| ---------- | ----------------------------------------------- | ---------- | -------------------------------------- |
| `Closed`   | failure, count `< maxFailures`                  | `Closed`   | counter incremented                    |
| `Closed`   | failure, count `>= maxFailures`                 | `Open`     | trip recorded, timer started           |
| `Closed`   | `cc.Trip()`                                     | `Open`     | forced regardless of count             |
| `Closed`   | success                                         | `Closed`   | counter reset to 0                     |
| `Open`     | call admitted                                   | `Open`     | rejected with `ErrOpen`                |
| `Open`     | `resetTimeout` elapsed (on `State()`/`Execute`) | `HalfOpen` | one CAS, hook fires                    |
| `HalfOpen` | probe success                                   | `Closed`   | counter reset, breaker healed          |
| `HalfOpen` | probe failure or `Trip()`                       | `Open`     | re-opened immediately, timer restarted |
| `HalfOpen` | probe budget exhausted                          | `HalfOpen` | extra callers get `ErrOpen`            |


## Normative Contracts


| Invariant                       | Guarantee                                                                                           |
| ------------------------------- | --------------------------------------------------------------------------------------------------- |
| Open rejects without `fn`       | A call admitted in `Open` never invokes `fn`; it returns `ErrOpen`.                                 |
| Probe budget                    | At most `WithHalfOpenMax` callbacks run concurrently in `HalfOpen`; the rest get `ErrOpen`.         |
| Single trip per edge            | Concurrent failures that cross the threshold record exactly one trip and fire the hook once.        |
| `SkipFailure` excludes counting | A skipped failure reaches the caller unchanged but never increments the failure count or trips.     |
| `Trip` overrides everything     | `cc.Trip()` opens the breaker even on success and even with `SkipFailure` set.                      |
| `Close` ≠ `Closed` state        | `Close()` permanently disables the breaker (`ErrClosed`); the `Closed` *state* is the healthy mode. |
| No background goroutines        | Promotion to `HalfOpen` is lazy, evaluated on `State()`/`Execute`; nothing to leak.                 |


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

## API


| Symbol                          | Signature                                                                       | Description                                                                |
| ------------------------------- | ------------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| `New`                           | `New(opts ...Option) *Breaker`                                                  | Construct a breaker (defaults applied, options clamped).                   |
| `Execute`                       | `Execute[T any](b *Breaker, ctx context.Context, fn CircuitFunc[T]) (T, error)` | Admit and run `fn` under the breaker.                                      |
| `Breaker.State`                 | `State() State`                                                                 | Current state; lazily promotes `Open → HalfOpen` when the timeout elapses. |
| `Breaker.Failures`              | `Failures() int`                                                                | Current consecutive failure count.                                         |
| `Breaker.Reset`                 | `Reset()`                                                                       | Force back to `Closed`, clear the failure count.                           |
| `Breaker.Stats`                 | `Stats() Stats`                                                                 | Snapshot of state + cumulative counters.                                   |
| `Breaker.ResetStats`            | `ResetStats()`                                                                  | Zero the cumulative counters (state untouched).                            |
| `Breaker.Close`                 | `Close() error`                                                                 | Permanently disable; subsequent `Execute` returns `ErrClosed`. Idempotent. |
| `Breaker.IsClosed`              | `IsClosed() bool`                                                               | Whether `Close` was called (≠ `Closed` state).                             |
| `CircuitController.State`       | `State() State`                                                                 | State at admission (`Closed` or `HalfOpen`).                               |
| `CircuitController.Failures`    | `Failures() int`                                                                | Failure count at admission.                                                |
| `CircuitController.MaxFailures` | `MaxFailures() int`                                                             | Configured failure threshold.                                              |
| `CircuitController.SkipFailure` | `SkipFailure()`                                                                 | Do not count this call's error as a failure.                               |
| `CircuitController.Trip`        | `Trip()`                                                                        | Force the breaker `Open` after this call.                                  |
| `State.String`                  | `String() string`                                                               | `"closed"`, `"open"`, `"half_open"`.                                       |
| `CircuitFunc[T]`                | `func(ctx, cc CircuitController) (T, error)`                                    | The unit of work run by `Execute`.                                         |


## Configuration


| Option                                       | Default              | Description                                                                |
| -------------------------------------------- | -------------------- | -------------------------------------------------------------------------- |
| `WithMaxFailures(n int)`                     | `5`                  | Consecutive failures that trip `Closed → Open`. Values `< 1` floored to 1. |
| `WithResetTimeout(d time.Duration)`          | `10s`                | How long `Open` lasts before a probe is admitted. Values `<= 0` ignored.   |
| `WithHalfOpenMax(n int)`                     | `1`                  | Concurrent probes admitted in `HalfOpen`. Values `< 1` floored to 1.       |
| `WithOnStateChange(fn func(from, to State))` | none                 | Hook fired on each transition. Must not block or panic.                    |
| `WithOp(op string)`                          | `"circuitx.Execute"` | Operation label attached to panic reports. Empty ignored.                  |


## Errors


| Error          | Condition                                                                               |
| -------------- | --------------------------------------------------------------------------------------- |
| `ErrOpen`      | The circuit is `Open`, or `HalfOpen` with the probe budget in use; `fn` is not invoked. |
| `ErrNilFunc`   | `fn` passed to `Execute` is nil.                                                        |
| `ErrClosed`    | `Execute` called after `Breaker.Close`.                                                 |
| `ErrCancelled` | `ctx` is already cancelled/expired at admission; wraps `ctx.Err()`, state untouched.    |


All errors are comparable with `==` and `errors.Is`. `ErrCancelled` wraps the underlying `context` cause; reach it with `errors.Unwrap` or test with `errors.Is(err, context.Canceled)`.

## Pitfalls

> [!WARNING]
> `**Close()` is not the `Closed` state.** `Breaker.Close()` permanently shuts the breaker down — every later `Execute` returns `ErrClosed`. The `Closed` *state* (`Breaker.State() == Closed`) is the healthy operating mode. They are deliberately separate concepts; do not conflate `IsClosed()` (lifecycle) with `State() == Closed` (health).

> [!WARNING]
> **Intermittent failures may never trip.** Because a success resets the consecutive counter, a downstream that fails 1-in-2 with `WithMaxFailures(5)` can run forever without tripping. If you need to react to a failure *rate*, track it yourself and call `cc.Trip()` from the callback.

> [!WARNING]
> **The `onStateChange` hook runs on the caller's goroutine.** It fires synchronously inside `Execute`/`State`/`Reset` while no lock relevant to the hot path is held, but a slow or panicking hook will stall the calling request. Keep it to a counter increment or a non-blocking send.

> [!NOTE]
> **One breaker guards one dependency.** A `Breaker` aggregates failures globally. To isolate failures per host, per tenant, or per endpoint, create one breaker per key (e.g. in a `syncx.Map[string, *circuitx.Breaker]`).

## Safety and Concurrency

A `Breaker` is safe for concurrent use from any number of goroutines. State, failure count, probe budget, and all counters are `sync/atomic` values. The hot paths are lock-free: a successful call through a healthy `Closed` breaker, and a rejection by an `Open` breaker, take no mutex at all. The single `sync.Mutex` is acquired only when a state transition or failure settlement actually occurs (`recordFailure`, `recordSuccess` healing/forgiving, `openFrom`, `Reset`), making each edge atomic so the trip counter and `WithOnStateChange` hook fire exactly once. Crucially, every state-changing helper re-reads the live state under the lock, so a success can never silently re-close a breaker that a concurrent failure just tripped. Lazy `Open → HalfOpen` promotion rides a single compare-and-swap with no lock. There are no background goroutines and no timers: promotion is evaluated whenever the state is read, so a `Breaker` needs no `Close` to avoid leaks (`Close` exists only to disable it).

## Benchmarks

> CPU: Intel i7-10510U · OS: Windows 10 · Go 1.24 · `-benchmem -count=3`

| Benchmark                 | ns/op   | B/op | allocs/op |
| ------------------------- | ------- | ---- | --------- |
| `Execute_Closed`          | ~48     | 32   | 1         |
| `Execute_Closed_Parallel` | ~48–70  | 32   | 1         |
| `Execute_Open`            | ~70–96  | 0    | 0         |
| `Execute_Open_Parallel`   | ~76–105 | 0    | 0         |
| `State`                   | ~12–42  | 0    | 0         |
| `Stats`                   | ~15–22  | 0    | 0         |

### Analysis

- **`Execute_Closed` — 1 alloc (32 B):** the single allocation is the `*execution` controller handed to the callback. Because it is passed through the `CircuitController` interface into a closure captured by `panix.Safe`, it escapes to the heap. This is the architectural allocation floor for the success path and matches the sibling resilience packages (`shedx`, `fallx`). It can only be removed by giving up the controller abstraction.
- **`Execute_Closed` lock-free fast path:** a success in a healthy `Closed` breaker (the overwhelmingly common case) returns after two atomic loads — it never touches the mutex, because there is no failure run to forgive and no transition to make. This keeps the sequential and parallel numbers nearly identical (~48 ns), so a hot breaker adds almost no contention regardless of goroutine count.
- **`Execute_Open` — 0 allocs:** when the circuit is open the call is rejected before any controller is built, so the reject path is fully allocation-free and lock-free — exactly the property you want when a breaker is shedding a flood of doomed calls.
- **Parallel scaling:** both hot paths scale flat because they share only atomic counters, never a mutex. The lock is reserved for the rare transition events (trips, probe settlement), which by definition do not happen on every call.
- **`State` — ~12 ns:** a single atomic load on the common path; when `Open` and expired, an extra clock read and a compare-and-swap drive the promotion. Cheap enough to poll freely from metrics.

## Quality


| Metric         | Value                          |
| -------------- | ------------------------------ |
| Test functions | 39                             |
| Benchmarks     | 6                              |
| Fuzz targets   | 1                              |
| Examples       | 4                              |
| Coverage       | 100.0%                         |
| Race detector  | All pass                       |
| External deps  | 0 (panix; testify in dev only) |


## File Structure

```text
circuitx/
├── circuitx.go        # Breaker, New, Execute, state machine, lifecycle
├── types.go           # State, CircuitController + execution impl, CircuitFunc, Stats
├── options.go         # config, defaults, WithXxx options
├── errors.go          # sentinel errors + context wrapper
├── circuitx_test.go   # unit + table-driven + edge + concurrency tests
├── bench_test.go      # sequential + parallel benchmarks
├── fuzz_test.go       # FuzzExecute — state-machine invariants
├── example_test.go    # runnable GoDoc examples
├── footprint_test.go  # struct size guards
└── README.md          # this document
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.