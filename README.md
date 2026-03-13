# URX — Unified Resilience eXtensions

[![CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml/badge.svg)](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/aasyanov/urx.svg)](https://pkg.go.dev/github.com/aasyanov/urx)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

Composable infrastructure primitives for Go — 31 packages with no framework runtime or central dependency.

## Motivation

In larger systems, concerns such as retry logic, circuit breaking, concurrency limiting, structured errors, and graceful shutdown are often implemented multiple times across services. Over time this leads to duplicated code, inconsistent behavior, and reduced observability.

URX extracts these patterns into focused, single-purpose packages with shared conventions:

- `context.Context` for cancellation and control flow
- `*errx.Error` for structured, inspectable errors
- Generic `Execute` / `Do` wrappers for type-safe execution control
- Panic-to-error conversion via `panix`

Each package addresses one concern and composes with others through plain Go interfaces — never through a central framework, struct tags, or code generation. Packages can be adopted incrementally in existing codebases.

### Design principles

1. **Single responsibility** — one package, one concern. `retryx` retries. `circuitx` breaks circuits. `bulkx` limits concurrency. They compose; they don't merge.

2. **Generic-first API** — all execution wrappers (`Execute`, `Do`) are package-level generic functions returning `(T, error)`. Type safety without reflection.

3. **Structured errors** — all public APIs return `*errx.Error` with Domain, Code, and metadata. No `fmt.Errorf`, no string matching.

4. **Panic safety** — every `Execute`/`Do` path is wrapped with `panix.Safe`. Panics are converted into structured errors instead of propagating to the caller.

5. **Allocation-conscious hot paths** — admission checks, rate limiting, and circuit state reads avoid heap allocations.

6. **Execution controllers** — callbacks in `retryx`, `circuitx`, `bulkx`, `shedx`, `adaptx`, `hedgex`, and `cronx` receive a controller interface that exposes execution context (attempt number, load, limit) and lets the function influence wrapper behavior (abort retry, skip failure, reschedule) from the inside.

7. **Testable by design** — injectable lookup functions, injectable readers, `testx` failure simulators. No `os.Setenv` in tests, no global state.

8. **Minimal dependencies** — the entire toolkit depends on 4 external modules: `sync`, `yaml`, `toml`, `crypto`. All other dependencies are from the Go standard library.

## Quick start

```go
import (
    "github.com/aasyanov/urx/pkg/retryx"
    "github.com/aasyanov/urx/pkg/circuitx"
    "github.com/aasyanov/urx/pkg/bulkx"
    "github.com/aasyanov/urx/pkg/errx"
)

// Compose: bulkhead → circuit breaker → retry
resp, err := bulkx.Execute(bh, ctx, func(ctx context.Context, bc bulkx.BulkController) (*Response, error) {
    return circuitx.Execute(cb, ctx, func(ctx context.Context, cc circuitx.CircuitController) (*Response, error) {
        return retryx.Do(ctx, func(rc retryx.RetryController) (*Response, error) {
            resp, err := client.Call(ctx, req)
            if isBusinessError(err) {
                cc.SkipFailure()  // don't trip the circuit
                rc.Abort()        // don't retry
            }
            return resp, err
        })
    })
})
```

See [Getting Started](docs/getting-started.md) for a step-by-step tutorial and [examples/](examples/) for runnable programs.

## Packages

### Resilience

| Package | Description |
|---------|-------------|
| [**retryx**](pkg/retryx/) | Retry with backoff, jitter, and errx-aware retryability |
| [**circuitx**](pkg/circuitx/) | Circuit breaker (closed → open → half-open) |
| [**bulkx**](pkg/bulkx/) | Concurrency limiter (bulkhead isolation) |
| [**shedx**](pkg/shedx/) | Priority-based load shedding |
| [**adaptx**](pkg/adaptx/) | Adaptive concurrency limiting (AIMD, Vegas, Gradient) |
| [**hedgex**](pkg/hedgex/) | Hedged requests (speculative execution) |
| [**toutx**](pkg/toutx/) | Timeout enforcement with structured errors |
| [**fallx**](pkg/fallx/) | Fallback strategies (static, func, cached) |
| [**ratex**](pkg/ratex/) | Token-bucket rate limiter |
| [**quotax**](pkg/quotax/) | Per-key rate limiting with auto-eviction |
| [**warmupx**](pkg/warmupx/) | Gradual capacity ramp-up (slow start) |
| [**cronx**](pkg/cronx/) | Minimal job scheduler (interval + one-off) with JobController |

### Infrastructure

| Package | Description |
|---------|-------------|
| [**errx**](pkg/errx/) | Structured errors with Domain, Code, metadata, severity, retryability |
| [**panix**](pkg/panix/) | Panic recovery → `*errx.Error` conversion |
| [**logx**](pkg/logx/) | `slog.Handler` with `ctxx` trace injection and `errx` field extraction |
| [**ctxx**](pkg/ctxx/) | Trace ID and span ID propagation via `context.Context` |
| [**signalx**](pkg/signalx/) | OS signal trapping and graceful shutdown hooks |
| [**healthx**](pkg/healthx/) | Liveness and readiness probes with HTTP handlers |
| [**syncx**](pkg/syncx/) | Generic `Lazy[T]`, error group, typed concurrent map |
| [**poolx**](pkg/poolx/) | Worker pool, object pool, batch collector |
| [**busx**](pkg/busx/) | In-process event bus with topic routing |
| [**testx**](pkg/testx/) | Failure simulator for deterministic testing |

### Configuration

| Package | Description |
|---------|-------------|
| [**cfgx**](pkg/cfgx/) | File → struct loader (YAML, JSON, TOML) |
| [**envx**](pkg/envx/) | Typed environment variable binding (generics, no reflection) |
| [**env2x**](pkg/env2x/) | Reflection-based env overlay for large structs |
| [**clix**](pkg/clix/) | CLI flag parser with subcommands, aliases, and version handling |
| [**validx**](pkg/validx/) | Functional validators and fixers |

### Data

| Package | Description |
|---------|-------------|
| [**lrux**](pkg/lrux/) | Generic LRU cache with sharded variant for concurrent access |
| [**hashx**](pkg/hashx/) | Password hashing (Argon2id, scrypt, bcrypt) |
| [**i18n**](pkg/i18n/) | Translation engine with anchor-based lookup |
| [**dicx**](pkg/dicx/) | Dependency injection container with lifecycle management |

## Quality

| Metric | Value |
|--------|-------|
| Packages | 31 |
| Tests | 1323 |
| Benchmarks | 207 |
| Coverage | 91.6% – 100% per package |
| Race detector | All tests pass with `-race` |
| Go version | 1.24 |
| External deps | 4 (`sync`, `yaml`, `toml`, `crypto`) |

### Coverage by package

| Package | Tests | Coverage | Benchmarks |
|---------|------:|:--------:|-----------:|
| adaptx | 56 | 91.6% | 2 |
| bulkx | 37 | 98.3% | 4 |
| busx | 39 | 94.6% | 9 |
| cfgx | 33 | 93.7% | 6 |
| circuitx | 33 | 94.4% | 4 |
| clix | 83 | 96.0% | 4 |
| cronx | 29 | 97.5% | 3 |
| ctxx | 19 | 99.1% | 10 |
| dicx | 67 | 94.1% | 10 |
| env2x | 34 | 94.7% | — |
| envx | 37 | 97.5% | 7 |
| errx | 83 | 100.0% | 23 |
| fallx | 33 | 96.2% | 3 |
| hashx | 54 | 95.9% | 3 |
| healthx | 21 | 98.6% | 4 |
| hedgex | 33 | 98.4% | 1 |
| i18n | 74 | 97.5% | 14 |
| logx | 14 | 94.7% | 4 |
| lrux | 139 | 98.8% | 29 |
| panix | 19 | 100.0% | 7 |
| poolx | 24 | 96.9% | 3 |
| quotax | 33 | 95.4% | 3 |
| ratex | 28 | 94.6% | 5 |
| retryx | 40 | 100.0% | 13 |
| shedx | 34 | 98.3% | 3 |
| signalx | 11 | 94.6% | 4 |
| syncx | 18 | 100.0% | 3 |
| testx | 28 | 98.6% | 5 |
| toutx | 16 | 100.0% | 3 |
| validx | 67 | 100.0% | 11 |
| warmupx | 56 | 97.6% | 7 |

## Controller pattern

Seven packages pass an **execution controller** into the callback — an interface that exposes execution state and, where applicable, lets the function influence the wrapper's behavior.

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

Read methods return execution state (attempt number, failure count, current limit). Write methods change wrapper behavior (abort retry, skip failure recording, exclude sample).

| Controller | Package | Read | Write |
|------------|---------|------|-------|
| `RetryController` | retryx | `Number()` | `Abort()` |
| `CircuitController` | circuitx | `State()`, `Failures()` | `SkipFailure()` |
| `BulkController` | bulkx | `Active()`, `MaxConcurrent()`, `WaitedSlot()` | — |
| `ShedController` | shedx | `Priority()`, `Load()`, `InFlight()` | — |
| `AdaptController` | adaptx | `Limit()`, `InFlight()`, `Algorithm()` | `SkipSample()` |
| `HedgeController` | hedgex | `Attempt()`, `IsHedge()` | — |
| `JobController` | cronx | `RunNumber()`, `LastRunTime()` | `Abort()`, `Reschedule()`, `SkipError()` |

The wrapper passes a private implementation as a public interface. The callback only sees the interface surface — no access to internal state.

## Error model

All packages return `*errx.Error`, providing consistent error inspection and propagation:

```go
type Error struct {
    Domain   string       // "BULK", "CIRCUIT", "RETRY", ...
    Code     string       // "TIMEOUT", "OPEN", "EXHAUSTED", ...
    Message  string       // Human-readable description
    Cause    error        // Wrapped underlying error
    Meta     map[string]string  // Structured metadata
    Severity Severity     // Debug, Info, Warn, Error
    Retry    RetryHint    // Safe, Unsafe, Unknown
}
```

Errors are inspectable via `errors.As`, serializable to JSON, and integrate with `slog` via the `LogValue()` method.

## Roadmap

Packages under development, following URX conventions (generic API, `errx.Error`, `context.Context`, minimal dependencies):

| Package | Description | Status |
|---------|-------------|--------|
| **metricx** | Generic metrics collector (Counter, Gauge, Histogram, Timer, Summary, Rate, Statistics) with pluggable exporters (Prometheus, StatsD, InfluxDB) | Planned |
| **tracex** | Lightweight span builder on top of `ctxx` with duration tracking and structured export | Planned |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines

## License

MIT — see [LICENSE](LICENSE) for details.
