# URX — Unified Runtime eXtensions

[![CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml/badge.svg)](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/aasyanov/urx.svg)](https://pkg.go.dev/github.com/aasyanov/urx)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Changelog](https://img.shields.io/badge/Changelog-CHANGELOG-blue)](CHANGELOG.md)

Twenty focused Go packages for production service runtime concerns — retry, circuit breaking, rate limiting, concurrency control, health probes, configuration loading, and related primitives. Each package does one thing, composes through `context.Context` and plain interfaces, and ships with tests, benchmarks, fuzz targets, and a standalone README. No framework runtime, no central dependency, no code generation.

```
go get github.com/aasyanov/urx
```

Requires **Go 1.24+** (generics). Import paths: `github.com/aasyanov/urx/<pkg>`.

> [!IMPORTANT]
> URX is a **greenfield** module (2.0). It optimizes for correctness and consistent conventions, not backward compatibility. There are no deprecated shims and no migration guides from 1.x. See [CHANGELOG.md](CHANGELOG.md) for the 2.0 breaking changes.

---

## The Problem

Production Go services repeat the same runtime concerns in every repository: retries with backoff, circuit breakers, bulkheads, rate limits, graceful shutdown, health endpoints, config loading. Each team implements them differently — one service retries with ad-hoc loops, another wraps errors inconsistently, a third shares no common semantics for `TryExecute` vs blocking admission.

That inconsistency shows up in production as duplicated bugs (retry storms, circuits that never half-open, limiters that allocate on every check) and in code review as endless debate about local conventions.

URX extracts these patterns into **single-purpose packages** with **shared conventions** so you can compose only what you need and still get predictable behavior across services.

---

## What URX Is

| Property | Meaning |
| -------- | ------- |
| **Composable primitives** | Import `retryx` alone, or stack `bulkx` → `circuitx` → `retryx` around one call. No init, no global registry. |
| **Generic-first** | Execution wrappers are package-level functions: `Execute[T]`, `Do[T]`. Type-safe `(T, error)` without reflection. |
| **Context-native** | Blocking operations honour cancellation and deadlines. Callbacks receive `context.Context` as the first argument. |
| **Sentinel errors** | Each package defines its own `errors.New` values — comparable with `==` and `errors.Is`. |
| **Panic-safe callbacks** | Resilience paths run user code under `panix.Safe`; panics become `*panix.PanicError`. |
| **Allocation-aware** | Admission checks (`Allow`, open-circuit rejects, cache hits) benchmark at 0 allocs/op; hot paths are documented per package. |
| **Specification-grade docs** | Root README (this file) + one exhaustive README per package + GoDoc on every export. |

## What URX Is Not

| URX is **not** | Use instead |
| -------------- | ----------- |
| A web framework or HTTP router | `net/http`, `chi`, `echo`, etc. |
| A DI container or lifecycle orchestrator | Your composition root; optional external tools if you need them |
| A logging or metrics SDK | `log/slog`, OpenTelemetry, Prometheus client |
| A unified error taxonomy across packages | Per-package sentinels; wrap at your service boundary if you need one model |
| A mega-wrapper that configures everything | Explicit nesting of the packages you need |
| Backward-compatible with URX 1.x | Treat 2.0 as a new module; see [CHANGELOG.md](CHANGELOG.md) |

---

## Shared Conventions

Every public package follows the same patterns unless the README states an exception.

| Convention | In practice |
| ---------- | ----------- |
| `context.Context` | Passed into callbacks and blocking APIs; checked at admission time where relevant |
| Sentinel errors | `"<pkg>: …"` prefix; documented **when** each error is returned |
| `Execute` / `TryExecute` / `Do` | Generic entry points for “run fn under this policy” |
| `Allow` | Non-blocking admission hint where applicable — 0-allocation fast path |
| Functional options | `WithXxx` on private `config` structs; defaults documented in README |
| Execution controllers | Callback receives a small interface to read state and influence the wrapper |
| Lifecycle | `Close`, `IsClosed`, `Stats`, `ResetStats` on long-lived instances where applicable |
| Tests | Table-driven cases, `-race`, fuzz on input paths, benchmarks on hot paths |
| File layout | `<pkg>.go`, `errors.go`, `options.go`, `types.go`, tests, `README.md` — see [Project layout](#project-layout) |

---

## Design Principles

| # | Principle | In practice |
| - | --------- | ----------- |
| 1 | **Single responsibility** | `retryx` retries. `circuitx` breaks circuits. They compose; they do not merge. |
| 2 | **Generic-first API** | Package-level generic functions; no `interface{}` in public callbacks. |
| 3 | **Panic safety** | Library code returns errors; `panix` converts panics in user callbacks. |
| 4 | **Allocation-conscious** | Separate cheap checks from `Execute` wrappers that allocate a controller. |
| 5 | **Execution controllers** | Callbacks can abort retry, skip a circuit failure, refund a rate token, etc. |
| 6 | **Testable by design** | Injectable clocks/readers/writers where needed; `internal/testx` for stress simulation. |
| 7 | **Minimal dependencies** | Stdlib + `panix` for most packages; `yaml`/`toml` only in `cfgx`; `x/sync` in `lrux`. |

---

## Architecture

URX is a **flat module**: twenty public packages at the repository root plus `internal/testx`. Packages do not import each other unless structurally necessary (for example `quotax` builds on `ratex`, resilience packages use `panix`).

```text
                         your service / composition root
                                    │
          ┌─────────────────────────┼─────────────────────────┐
          │                         │                         │
    configuration              resilience                 infrastructure
    cfgx · envx · clix     retryx · circuitx · bulkx      panix · signalx
                           shedx · adaptx · hedgex         healthx · syncx
                           toutx · fallx · ratex          poolx
                           quotax · warmupx
                                    │
                              data: lrux
```

**Dependency rule:** prefer stdlib and local code. A new cross-package dependency needs a concrete reason (shared token bucket, panic recovery).

---

## How Composition Works

Resilience packages wrap **a single unit of work** — one HTTP call, one query, one publish. Nest wrappers from the outside in: outer limits concurrency or shedding; inner retries or breaks circuits.

Recommended mental model for an outbound dependency call:

```text
  caller
    │
    ▼
  bulkx / shedx / warmupx / ratex / quotax   ← admission (capacity, load, rate)
    │
    ▼
  circuitx                                    ← fail fast when dependency is down
    │
    ▼
  retryx                                      ← retry transient failures
    │
    ▼
  toutx (optional, per attempt)               ← hard deadline on one attempt
    │
    ▼
  fallx (optional)                            ← degrade when primary fails
    │
    ▼
  your client.Call(ctx, req)
```

Not every layer belongs on every call. A read-only health probe might need only `circuitx` + `toutx`. A background worker might use `bulkx` + `retryx` without rate limiting.

**Order matters** for semantics: an outer `bulkx` slot is held for the entire inner stack including retries. Put **retry inside bulkhead** if each retry attempt should consume concurrency; put **bulkhead outside retry** if the whole retry sequence should count as one slot (unusual).

---

## Controller Pattern

Eleven packages pass an **execution controller** into the callback — a public interface backed by a private struct. Controllers expose read-only state at admission time and, where documented, let the callback influence wrapper behavior.

```text
Execute/Do  ──creates──▶  private struct
                               │
                          satisfies
                               │
                          public interface  ──passed to──▶  user fn(ctx, ctrl)
                               │                              │
                          read methods ◀──────────────────────┘
                          write methods ◀─────────────────────┘
```

| Controller | Package | Read (examples) | Write |
| ---------- | ------- | --------------- | ----- |
| `RetryController` | retryx | `Number()`, `LastErr()`, `Elapsed()` | `Abort()` |
| `TimeoutController` | toutx | `Op()`, `Timeout()`, `Deadline()`, `Elapsed()`, `Remaining()` | — |
| `CircuitController` | circuitx | `State()`, `Failures()`, `MaxFailures()` | `SkipFailure()`, `Trip()` |
| `BulkController` | bulkx | `Active()`, `MaxConcurrent()`, `Load()`, `WaitedSlot()` | — |
| `ShedController` | shedx | `Priority()`, `Load()`, `InFlight()`, `Capacity()` | `Shed()` |
| `AdaptController` | adaptx | `Limit()`, `InFlight()`, `Algorithm()` | `SkipSample()` |
| `HedgeController` | hedgex | `Attempt()`, `IsHedge()`, `Backends()`, `Elapsed()` | `Cancel()` |
| `FallController` | fallx | `Strategy()`, `Key()`, `OnFallback()`, `Error()` | — |
| `RateController` | ratex | `Tokens()`, `Rate()`, `Burst()`, `Waited()` | `SkipToken()` |
| `QuotaController` | quotax | `Key()`, `Tokens()`, `Rate()`, `Burst()`, `Waited()` | `SkipToken()` |
| `WarmupController` | warmupx | `Capacity()`, `Progress()`, `Strategy()` | `Reject()` |

Rules:

- One controller per `Execute`/`Do` invocation; do not retain it after the callback returns.
- Compile-time check: `var _ XxxController = (*execution)(nil)` in `types.go`.
- Full method docs live in package READMEs and GoDoc.

**`TryExecute`:** when work is rejected without running the callback, returns `(false, zero, nil)` unless a sentinel applies (`ErrOpen`, `ErrClosed`, `ErrCancelled`, …). Use it for non-blocking call sites; use `Allow()` for cheapest yes/no probes.

---

## Packages

Detailed API tables, benchmarks, pitfalls, and file trees are in each package README.

### Resilience

| Package | One-line | Typical use |
| ------- | -------- | ----------- |
| [retryx](retryx/) | Exponential backoff + jitter + `WithRetryIf` | Transient downstream failures |
| [circuitx](circuitx/) | Closed → open → half-open breaker | Fail fast; probe recovery |
| [bulkx](bulkx/) | Semaphore bulkhead | Cap concurrent calls to one dependency |
| [shedx](shedx/) | Priority-based load shedding | Protect service under overload |
| [adaptx](adaptx/) | AIMD / Vegas / Gradient adaptive limit | Auto-tune concurrency from latency |
| [hedgex](hedgex/) | Hedged speculative requests | Cut tail latency on idempotent reads |
| [toutx](toutx/) | Deadline wrapper (goroutine + timer) | Per-attempt or call timeout |
| [fallx](fallx/) | Static, func, or cached fallback | Degrade when primary fails |
| [ratex](ratex/) | Global token-bucket limiter | Process-wide or handler rate cap |
| [quotax](quotax/) | Per-key token buckets + eviction | Tenant/user/API-key limits |
| [warmupx](warmupx/) | Slow-start capacity ramp | Roll out new instances gradually |

### Infrastructure

| Package | One-line | Typical use |
| ------- | -------- | ----------- |
| [panix](panix/) | Panic → `*PanicError` | Used internally; safe user callbacks in goroutines |
| [signalx](signalx/) | Signal trap + ordered shutdown hooks | SIGINT/SIGTERM graceful stop |
| [healthx](healthx/) | Liveness/readiness + HTTP handlers | Kubernetes/load-balancer probes |
| [syncx](syncx/) | `Lazy[T]`, bounded `Group`, typed `Map` | Init-once, worker groups, concurrent maps |
| [poolx](poolx/) | Worker pool, object pool, batch flush | Background work, reuse, bulk I/O |

### Configuration

| Package | One-line | Typical use |
| ------- | -------- | ----------- |
| [cfgx](cfgx/) | Load/save struct ↔ YAML/JSON/TOML | File-backed config |
| [envx](envx/) | Typed env binding (generics) | 12-factor overrides at boot |
| [clix](clix/) | Flags + subcommands | CLI entrypoints and dev overrides |

### Boot vs runtime

URX covers **loading, parsing, and saving** config. **When** to reload subsystems after a change is your composition root — URX does not ship a lifecycle graph or hot-reload orchestrator.

**At process start**, wire precedence explicitly (lowest → highest):

```text
defaults in Go
    ↓
cfgx.Load("config.yaml")
    ↓
envx.BindTo(env, "KEY", &cfg.Field)
    ↓
clix flags
    ↓
cfg.Validate() / env.Validate()
    ↓
start servers, pools, clients …
```

**After boot**, treat env and CLI as fixed deployment overrides. A runtime change (admin API, config watch, control plane) should **not** re-run `envx`/`clix`; typical flow:

```text
incoming config (JSON/YAML/merge)
    ↓
cfg.Validate(fix=true)     — before persistence
    ↓
cfgx.Save(path, &cfg)     — canonical file state
    ↓
your ApplyConfig(ctx, cfg) — restart/reload only what changed
```

| Source | When it applies | Notes |
| ------ | --------------- | ----- |
| Defaults in Go | Always (base) | Document in struct comments |
| File (`cfgx`) | Boot and after each successful save | Canonical on-disk state |
| Env (`envx`) | Boot only | Infra/orchestrator override; document which fields |
| CLI (`clix`) | Boot / dev | Highest at start; not for runtime reload |

Keep an in-process snapshot (`sync.RWMutex` + struct, or immutable copy per version) for handlers that read config. Validate **before** `cfgx.Save` so a bad write never becomes canonical. If `ApplyConfig` fails after save, choose an explicit policy (fail loud, rollback file, or track desired vs applied drift) in your service — URX does not pick one for you.

See [cfgx](cfgx/), [envx](envx/), and [clix](clix/) READMEs for format support, `Validator` hooks, and flag/subcommand details.

### Data

| Package | One-line | Typical use |
| ------- | -------- | ----------- |
| [lrux](lrux/) | Generic LRU + TTL + sharded variant | In-memory cache, `GetOrCompute` + singleflight |

### Internal

| Package | Note |
| ------- | ---- |
| [internal/testx](internal/testx/) | Hammer tests, latency simulators, footprint helpers — not a public API |

---

## Usage Scenarios

### 1. Outbound call with bulkhead, breaker, and retry

```go
bh := bulkx.New(bulkx.WithMaxConcurrent(20))
cb := circuitx.New(circuitx.WithMaxFailures(5), circuitx.WithResetTimeout(30*time.Second))
defer bh.Close()
defer cb.Close()

resp, err := bulkx.Execute(bh, ctx,
    func(ctx context.Context, bc bulkx.BulkController) (*Response, error) {
        return circuitx.Execute(cb, ctx,
            func(ctx context.Context, cc circuitx.CircuitController) (*Response, error) {
                return retryx.Do(ctx,
                    func(ctx context.Context, rc retryx.RetryController) (*Response, error) {
                        resp, err := client.Call(ctx, req)
                        if errors.Is(err, ErrBadRequest) {
                            cc.SkipFailure()
                            rc.Abort()
                        }
                        return resp, err
                    },
                    retryx.WithMaxAttempts(4),
                    retryx.WithRetryIf(isTransient),
                )
            })
    })
if err != nil {
    return nil, err
}
return resp, nil
```

### 2. HTTP middleware — rate limit before handler work

Use `ratex.Allow` or `ratex.TryExecute` on the hot path; avoid wrapping every request in `Execute` if you only need a token check.

```go
lim := ratex.New(ratex.WithRate(100), ratex.WithBurst(20))

http.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
    if !lim.Allow() {
        http.Error(w, "rate limited", http.StatusTooManyRequests)
        return
    }
    handler.ServeHTTP(w, r)
})
```

Per-tenant limits: [quotax](quotax/) with a stable key (`user ID`, API key hash).

### 3. Process boot — config file, env, flags

```go
cfg := DefaultConfig()

if err := cfgx.Load("config.yaml", &cfg, cfgx.WithCreateIfMissing()); err != nil {
    return err
}

env := envx.New(envx.WithPrefix("APP"))
envx.BindTo(env, "HTTP_PORT", &cfg.HTTP.Port)
envx.BindTo(env, "LOG_LEVEL", &cfg.Log.Level)

p := clix.New(os.Args[1:], "myservice", "API server",
    clix.AddFlag(&cfg.HTTP.Port, "http-port", "p", cfg.HTTP.Port, "listen port"),
    clix.SubCommand("serve", "run server", clix.Run(runServer)),
)
if err := errors.Join(env.Validate(), p.Err()); err != nil {
    return err
}
if errs := cfg.Validate(false); len(errs) > 0 {
    return errors.Join(errs...)
}
return p.Run()
```

`envx` distinguishes unset from empty string (`os.LookupEnv` semantics). See [envx](envx/) for list/time types.

### 4. Graceful shutdown and probes

```go
checker := healthx.New(healthx.WithTimeout(2 * time.Second))
checker.Register("db", db.PingContext)

http.Handle("/live", healthx.LiveHandler(checker))
http.Handle("/ready", healthx.ReadyHandler(checker))

signalx.OnShutdown(func(ctx context.Context) error {
    return server.Shutdown(ctx)
})

ctx := signalx.Trap(syscall.SIGINT, syscall.SIGTERM)
signalx.Wait(ctx) // runs hooks in registration order
```

Liveness is a cheap atomic check; readiness runs registered probes concurrently with per-check timeouts.

---

## Performance and Allocations

URX separates **cheap checks** from **Execute wrappers**.

| Tier | Examples | Typical allocs |
| ---- | -------- | -------------- |
| Check / reject | `bulkx.Allow`, `circuitx` open reject, `ratex.Allow`, `lrux` cache hit | **0** |
| Admit + callback | Most `Execute` success paths | **1** (controller escapes to heap) |
| Adaptive / manual release | `adaptx.Execute`, `adaptx.Acquire` | **2–3** (release closure + controller) |
| Orchestration | `toutx.Execute`, `hedgex` hedged path, `healthx.Readiness` | **many** (goroutines, timers — by design) |

Reject paths are optimized: e.g. `shedx.TryExecute` when shedding returns **0 allocs**; `bulkx.TryExecute` on a full bulkhead likewise.

Run benchmarks locally:

```powershell
./quality.ps1          # Windows — full pipeline, writes quality.result
./quality.sh           # Linux/macOS
```

Or per package:

```bash
go test -bench=. -benchmem -count=3 -run='^$' ./retryx/
```

Each package README documents its allocation floor and bottleneck in a **Benchmarks → Analysis** section.

---

## Errors and Panics

- **Sentinel errors** per package in `errors.go` — prefixed with the package name (`retryx: …`), documented with when they are returned, comparable via `errors.Is`; wrapping with `%w` stays in unexported helpers.
- **Wrapping** with `%w` happens in unexported helpers; public API returns sentinels testable via `errors.Is`.
- **Panics in callbacks** become `*panix.PanicError` (`errors.As`); they do not crash the process on resilience paths.
- **No shared `errx` type** in 2.0 — map to your service error model at the boundary if needed.

---

## Development and Quality

URX uses a **gate pipeline** — sequential quality gates, each including checks from all previous gates:

| Gate | Checks |
| ---- | ------ |
| 0 | `go build ./...` |
| 1 | + `go vet`, `golangci-lint` |
| 2 | + `go test -race` |
| 3 | + benchmarks |
| 4 | + GoDoc + README complete |
| 5 | + fuzz, coverage ≥95%, CI green |

**Repository quality bar (2.0 release):**

| Metric | Value |
| ------ | ----- |
| Public packages | 20 |
| Statement coverage | **98.6%** (with `-race`) |
| `golangci-lint` | 0 issues |
| Fuzz targets | 52 across packages |
| CI | Go 1.26 on Ubuntu (see [.github/workflows/ci.yml](.github/workflows/ci.yml)) |

Contributing workflow: bring one package to Gate 5 per focused change — audit, fix structure and implementation, write tests (unit, concurrent, fuzz), benchmarks, GoDoc, and package README.

---

## When to Use URX

**Good fit:**

- Long-running services with outbound dependencies, overload protection, or config-from-file + env + flags
- Teams that want **consistent** resilience semantics without adopting a full framework
- Incremental adoption — one import at a time

**Poor fit:**

- Single-file CLI tools with no concurrency or network resilience needs
- Projects already standardized on a framework that covers the same concerns
- Cases requiring a single shared application error type out of the box

---

## Versioning

- **2.0.0** — greenfield rewrite: flat imports, sentinel errors, expanded controllers, removed 1.x packages (`errx`, `dicx`, `logx`, …). Details in [CHANGELOG.md](CHANGELOG.md).
- SemVer applies from 2.0 onward; pre-1.0 compatibility promises do not extend into 2.x unless explicitly documented.

---

## Project Layout

```text
urx/
├── adaptx/ … warmupx/     # resilience (11)
├── panix/ … poolx/        # infrastructure (5)
├── cfgx/ … clix/          # configuration (3)
├── lrux/                    # data (1)
├── internal/testx/          # private test helpers
├── .github/workflows/       # CI
├── .golangci.yml
├── quality.ps1 · quality.sh # full local quality run
├── CHANGELOG.md
├── go.mod · go.sum
└── README.md                # this file — module overview
```

Standard package layout:

```text
<pkg>/
├── <pkg>.go           # primary API
├── errors.go          # sentinels
├── options.go         # WithXxx options
├── types.go           # controllers, types
├── *_test.go · bench_test.go · fuzz_test.go · example_test.go · footprint_test.go
└── README.md          # full package specification
```

---

## License

MIT — see [LICENSE](LICENSE).
