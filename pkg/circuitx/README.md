# circuitx

Thread-safe circuit breaker for industrial Go services.

Part of the **urx** infrastructure ecosystem alongside `bulkx`, `busx`, `clix`,
`ctxx`, `dicx`, `errx`, `healthx`, `i18n`, `logx`, `lrux`, `panix`, `poolx`,
`ratex`, `retryx`, `shedx`, `signalx`, `syncx`, `toutx`, and `validx`.

## Philosophy

**One job: circuit breaking.** `circuitx` wraps calls to external dependencies
and monitors failures. When the failure threshold is reached the circuit opens,
rejecting calls immediately with a structured `errx.Error`. After a cooldown
period the circuit transitions to HalfOpen, allowing a single probe call. It
does not retry, rate-limit, or log. Those are responsibilities of `retryx`,
`ratex`, and the caller.

## Quick start

```go
cb := circuitx.New(
    circuitx.WithMaxFailures(5),
    circuitx.WithResetTimeout(10*time.Second),
)

resp, err := circuitx.Execute(cb, ctx, func(ctx context.Context, cc circuitx.CircuitController) (*Response, error) {
    resp, err := client.Call(ctx, req)
    if isBusinessError(err) {
        cc.SkipFailure()
    }
    return resp, err
})
```

## API

| Function / Method | Description |
|---|---|
| `New(opts ...Option) *Breaker` | Create a circuit breaker (5 failures, 10 s reset) |
| `Execute[T](b, ctx, fn) (T, error)` | Run fn within the breaker; fn receives `CircuitController` |
| `cb.State() State` | Current state; may trigger Open → HalfOpen transition if reset timeout elapsed |
| `cb.Failures() int` | Consecutive failure count |
| `cb.Reset()` | Force back to Closed |
| `cb.Stats() Stats` | Point-in-time counters snapshot |
| `cb.ResetStats()` | Zero all counters |

### Options

| Option | Default | Description |
|---|---|---|
| `WithMaxFailures(n)` | `5` | Failures before opening |
| `WithResetTimeout(d)` | `10s` | Duration in Open before HalfOpen |

## States

```text
Closed ──(failures >= max)──▶ Open ──(timeout elapsed)──▶ HalfOpen
  ▲                                                          │
  └──────────(probe succeeds)────────────────────────────────┘
  ┌──────────(probe fails)───────────────────────────────────┘
  ▼
 Open
```

| State | Behavior |
|---|---|
| **Closed** | Calls pass through; failures are counted |
| **Open** | Calls rejected immediately with `CIRCUIT/OPEN` |
| **HalfOpen** | Exactly one probe call allowed; concurrent callers rejected; success resets, failure reopens |

## Behavior details

- **Failure counting**: each error returned by `fn` increments the failure
  counter. When the count reaches `MaxFailures`, the breaker transitions to
  Open. A successful call resets the counter.

- **State() side-effect**: calling `State()` is not a pure read. If the breaker
  is Open and `ResetTimeout` has elapsed, `State()` performs a `CompareAndSwap`
  to transition to HalfOpen. This is by design — it allows both `Execute` and
  external health monitors to trigger the recovery probe.

- **HalfOpen probe (single-shot)**: after `ResetTimeout` elapses, the breaker
  transitions to HalfOpen via `CompareAndSwap`. Only **one** probe call is
  admitted at a time; concurrent callers receive `CodeOpen` until the probe
  completes. If the probe succeeds, the breaker returns to Closed. If it fails,
  the breaker re-opens for another timeout period.

- **Panic recovery**: every call to `Execute` is wrapped with `panix.Safe`.
  Panics inside the function produce a structured `*errx.Error` and count as
  a failure.

- **SkipFailure**: calling `cc.SkipFailure()` inside the callback sets a flag.
  When set, the returned error does not increment the failure counter.

- **Trip counter**: each transition to Open (from Closed or HalfOpen) increments
  `Stats.Trips`. This is a key metric for monitoring downstream instability.

## Error diagnostics

All errors are `*errx.Error` with `Domain = "CIRCUIT"`.

### Codes

| Code | When |
|---|---|
| `OPEN` | Circuit is open, call rejected |

### Example

```text
CIRCUIT.OPEN: circuit breaker is open
```

## Thread safety

- `State` uses `atomic.Uint32` with `CompareAndSwap` for Open→HalfOpen transitions — lock-free
- `Failures` uses `atomic.Int32` — lock-free
- `Execute` uses `atomic.Bool` (`probing`) to enforce single HalfOpen probe — lock-free
- `recordFailure` acquires `sync.Mutex` only when transitioning to Open — rare path
- `Reset` stores atomics — safe for concurrent use
- `Stats` / `ResetStats` use atomic counters — lock-free

## Tests

**34 tests, 94.4% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/circuitx  coverage: 94.4% of statements
```

Coverage includes:
- Execute: success, failure, open rejection, half-open probe, half-open single-probe enforcement, panic recovery
- State transitions: Closed→Open, Open→HalfOpen, HalfOpen→Closed, HalfOpen→Open
- Reset: forced reset from any state
- Stats: counters increment, reset, trips counter (threshold + half-open reopen)
- SkipFailure: errors that should not count
- Thread safety: concurrent Execute calls

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz.
Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkExecute_Success    ~58 ns/op      24 B/op     1 allocs/op
BenchmarkExecute_Failure    ~58 ns/op      24 B/op     1 allocs/op
BenchmarkExecute_Open      ~157 ns/op     192 B/op     1 allocs/op
BenchmarkState               ~2 ns/op       0 B/op     0 allocs/op
```

### Analysis

**Execute (success/failure):** ~58 ns, 1 alloc. The single allocation is the `panix.Safe` closure. Mutex acquire-release dominates at ~40 ns. Negligible for any real call.

**Execute (open):** ~157 ns, 1 alloc (the error struct). The breaker rejects immediately — no `fn` call, no semaphore. Fast-fail path.

**State:** ~2 ns. Atomic load — free.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| Success / Failure | < 60 ns, 1 alloc | Invisible |
| Open (fast-fail) | < 160 ns, 1 alloc | Sub-microsecond rejection |
| State | < 3 ns, 0 allocs | Free |

## What circuitx does NOT do

| Concern | Owner |
|---------|-------|
| Retry logic | `retryx` |
| Rate limiting | `ratex` |
| Resource isolation | `bulkx` |
| Logging | caller / `slog` |
| Tracing | `ctxx` |
| Error construction | `errx` |
| HTTP middleware | caller |

## File structure

```text
pkg/circuitx/
    circuitx.go      -- Breaker, State, Execute(), CircuitController interface
    errors.go        -- DomainCircuit, Code constants, error constructors
    circuitx_test.go -- 34 tests, 94.4% coverage
    bench_test.go    -- 4 benchmarks
    README.md
```
