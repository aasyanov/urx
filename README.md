# URX — Unified Runtime eXtensions

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx)
[License: MIT](LICENSE)

Composable infrastructure primitives for production Go services. Each package does one thing, composes with the rest through `context.Context` and plain interfaces, and ships with exhaustive tests, benchmarks, and documentation. No framework runtime, no central dependency, no code generation.

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> This is a greenfield project optimized for correctness and quality, not backward compatibility. APIs change freely; there are no deprecated shims or migration guides.

---

## The Problem

Production services accumulate the same concerns — retry logic, circuit breaking, rate limiting, concurrency control, graceful shutdown — implemented differently across teams and repositories. One service retries with `fmt.Errorf`, another with sentinel errors; circuit breakers track failures in ad-hoc counters; rate limiters share no common diagnostics.

URX extracts these patterns into focused, single-purpose packages with shared conventions:


| Convention                       | In practice                                                                             |
| -------------------------------- | --------------------------------------------------------------------------------------- |
| `context.Context` everywhere     | All blocking operations respect cancellation and deadlines                              |
| Sentinel errors via `errors.New` | Comparable with `==` and `errors.Is`; no string matching                                |
| Generic `Execute` / `Do`         | Type-safe `(T, error)` wrappers; no `interface{}`                                       |
| Panic recovery via `panix`       | Every callback path runs under `panix.Safe`                                             |
| Functional options               | All configuration via `WithXxx` functions on private `config` structs                   |
| Execution controllers            | Callbacks receive a controller interface for observing and influencing wrapper behavior |


### Design principles


| #   | Principle                 | In practice                                                                                                 |
| --- | ------------------------- | ----------------------------------------------------------------------------------------------------------- |
| 1   | **Single responsibility** | One package, one concern. `retryx` retries. `circuitx` breaks circuits. They compose; they don't merge.     |
| 2   | **Generic-first API**     | All execution wrappers are package-level generic functions. Type safety without reflection.                 |
| 3   | **Panic safety**          | Every `Execute`/`Do` path is wrapped with `panix.Safe`. Panics become `*panix.PanicError`.                  |
| 4   | **Allocation-conscious**  | Admission checks, rate limiting, and circuit state reads avoid heap allocations. Hot paths benchmarked.     |
| 5   | **Execution controllers** | Callbacks receive a controller interface — read state, influence behavior. Private impl, public interface.  |
| 6   | **Testable by design**    | Injectable functions, `internal/testx` failure simulators, no global state.                                 |
| 7   | **Minimal deps**          | stdlib + `panix` for most packages. External deps: `yaml`, `toml`, `crypto` (where structurally necessary). |


---

## Quick start

```go
import (
    "github.com/aasyanov/urx/retryx"
    "github.com/aasyanov/urx/circuitx"
    "github.com/aasyanov/urx/bulkx"
)

// Compose: bulkhead → circuit breaker → retry
resp, err := bulkx.Execute(bh, ctx,
    func(ctx context.Context, bc bulkx.BulkController) (*Response, error) {
        return circuitx.Execute(cb, ctx,
            func(ctx context.Context, cc circuitx.CircuitController) (*Response, error) {
                return retryx.Do(ctx,
                    func(ctx context.Context, rc retryx.RetryController) (*Response, error) {
                        resp, err := client.Call(ctx, req)
                        if isBusinessError(err) {
                            cc.SkipFailure() // don't trip the circuit
                            rc.Abort()       // don't retry
                        }
                        return resp, err
                    })
            })
    })
```

---

## Packages

### Resilience


| Package                   | Description                                                           |
| ------------------------- | --------------------------------------------------------------------- |
| **[retryx](retryx/)**     | Retry with exponential backoff, jitter, context-aware cancellation    |
| **[circuitx](circuitx/)** | Circuit breaker (closed → open → half-open) with Trip/SkipFailure     |
| **[bulkx](bulkx/)**       | Concurrency limiter (bulkhead isolation) with load-aware callbacks    |
| **[shedx](shedx/)**       | Priority-based load shedding with graceful degradation                |
| **[adaptx](adaptx/)**     | Adaptive concurrency limiting (AIMD, Vegas, Gradient)                 |
| **[hedgex](hedgex/)**     | Hedged requests (speculative execution) with voluntary withdrawal     |
| **[toutx](toutx/)**       | Timeout enforcement with deadline/remaining budget                    |
| **[fallx](fallx/)**       | Fallback strategies (static, func, cached) with primary-error context |
| **[ratex](ratex/)**       | Token-bucket rate limiter with token refund                           |
| **[quotax](quotax/)**     | Per-key rate limiting with auto-eviction                              |
| **[warmupx](warmupx/)**   | Gradual capacity ramp-up (slow start) with post-admission rejection   |


### Infrastructure


| Package                 | Description                                          |
| ----------------------- | ---------------------------------------------------- |
| **[panix](panix/)**     | Panic recovery → `*PanicError` conversion            |
| **[signalx](signalx/)** | OS signal trapping and graceful shutdown hooks       |
| **[healthx](healthx/)** | Liveness and readiness probes with HTTP handlers     |
| **[syncx](syncx/)**     | Generic `Lazy[T]`, error group, typed concurrent map |
| **[poolx](poolx/)**     | Worker pool, object pool, batch collector            |


### Configuration


| Package           | Description                                                  |
| ----------------- | ------------------------------------------------------------ |
| **[cfgx](cfgx/)** | File → struct loader (YAML, JSON, TOML)                      |
| **[envx](envx/)** | Typed environment variable binding (generics, no reflection) |
| **[clix](clix/)** | CLI flag parser with subcommands                             |


### Data


| Package           | Description                                                  |
| ----------------- | ------------------------------------------------------------ |
| **[lrux](lrux/)** | Generic LRU cache with sharded variant for concurrent access |


### Internal


| Package                               | Description                                                                                        |
| ------------------------------------- | -------------------------------------------------------------------------------------------------- |
| **[internal/testx](internal/testx/)** | Deterministic failure/latency simulation, footprint helpers, concurrency stress, context factories |


---

## Controller pattern

Eleven resilience packages pass an **execution controller** into the callback — a public interface backed by a private struct that exposes execution state and, where applicable, lets the function influence the wrapper's behavior from the inside.

```text
Execute/Do  ──creates──▶  private struct
                               │
                          satisfies
                               │
                          public interface  ──passed to──▶  user fn
                               │                              │
                          read methods ◀──────────────────────┘
                          write methods ◀─────────────────────┘
```

Read methods return execution state (attempt number, failure count, current limit, elapsed time). Write methods change wrapper behavior (abort retry, skip failure recording, exclude sample, force trip, withdraw from race, refund token).


| Controller          | Package  | Read                                                          | Write                     |
| ------------------- | -------- | ------------------------------------------------------------- | ------------------------- |
| `RetryController`   | retryx   | `Number()`, `LastErr()`, `Elapsed()`                          | `Abort()`                 |
| `TimeoutController` | toutx    | `Op()`, `Timeout()`, `Deadline()`, `Elapsed()`, `Remaining()` | —                         |
| `CircuitController` | circuitx | `State()`, `Failures()`, `MaxFailures()`                      | `SkipFailure()`, `Trip()` |
| `BulkController`    | bulkx    | `Active()`, `MaxConcurrent()`, `Load()`, `WaitedSlot()`       | —                         |
| `ShedController`    | shedx    | `Priority()`, `Load()`, `InFlight()`, `Capacity()`            | `Shed()`                  |
| `AdaptController`   | adaptx   | `Limit()`, `InFlight()`, `Algorithm()`                        | `SkipSample()`            |
| `HedgeController`   | hedgex   | `Attempt()`, `IsHedge()`, `Backends()`, `Elapsed()`           | `Cancel()`                |
| `FallController`    | fallx    | `Strategy()`, `Key()`, `OnFallback()`, `Error()`              | —                         |
| `RateController`    | ratex    | `Tokens()`, `Rate()`, `Burst()`, `Waited()`                   | `SkipToken()`             |
| `QuotaController`   | quotax   | `Key()`, `Tokens()`, `Rate()`, `Burst()`, `Waited()`          | `SkipToken()`             |
| `WarmupController`  | warmupx  | `Capacity()`, `Progress()`, `Strategy()`                      | `Reject()`                |


Every controller:

- Lives in `types.go` with a compile-time assertion `var _ XxxController = (*execution)(nil)`
- Is created per `Execute`/`Do` call and bound to a single callback invocation
- Must not be retained after the callback returns
- Is documented with full godoc on every method

---

## When to use URX (and when not to)

**Good fit**: production Go services where you need retry logic, circuit breaking, rate limiting, or concurrency control — and you want consistent conventions across all of these without pulling in a framework.

**Not needed**: small scripts, CLI tools with no resilience requirements, or projects that already use a framework covering the same concerns.

**Adopt incrementally**: each package is self-contained. Import only `retryx`, or only `ratex`, or only `lrux`. There is no "install URX" step — pick the packages you need and ignore the rest.

---

## Project layout

```text
urx/
├── retryx/         # Retry with backoff
├── circuitx/       # Circuit breaker
├── bulkx/          # Bulkhead concurrency limiter
├── shedx/          # Load shedding
├── adaptx/         # Adaptive concurrency (AIMD, Vegas, Gradient)
├── hedgex/         # Hedged requests
├── toutx/          # Timeout enforcement
├── fallx/          # Fallback strategies
├── ratex/          # Token-bucket rate limiter
├── quotax/         # Per-key rate limiting
├── warmupx/        # Gradual capacity ramp-up
├── panix/          # Panic → error conversion
├── signalx/        # OS signal handling
├── healthx/        # Health probes
├── syncx/          # Lazy[T], concurrent map
├── poolx/          # Worker/object pools
├── cfgx/           # Config file loader
├── envx/           # Typed env binding
├── clix/           # CLI flag parser
├── lrux/           # LRU cache (generic, sharded)
├── internal/testx/ # Test infrastructure
├── .github/        # CI workflows
├── .golangci.yml   # Linter config
├── go.mod
└── go.sum
```

## License

MIT — see [LICENSE](LICENSE) for details.