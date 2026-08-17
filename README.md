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
> URX is a **greenfield** rewrite (1.4.0). It optimizes for correctness and consistent conventions, not backward compatibility with ≤1.3.0. There are no deprecated shims and no migration guides. See [CHANGELOG.md](CHANGELOG.md) for the breaking changes.

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
| Backward-compatible with URX ≤1.3.0 | Treat 1.4.0 as a new library surface; see [CHANGELOG.md](CHANGELOG.md) |

---

## Shared Conventions

Every public package follows the same patterns unless the README states an exception.

| Convention | In practice |
| ---------- | ----------- |
| `context.Context` | Passed into callbacks and blocking APIs; checked at admission time where relevant |
| Sentinel errors | `"<pkg>: …"` prefix; documented **when** each error is returned |
| `Execute` / `TryExecute` / `Do` | Generic entry points for “run fn under this policy” |
| `Allow` | Non-blocking admission hint where applicable — 0-allocation fast path |
| Functional options | `WithXxx` on private `config` structs; **nil options skipped**; defaults documented in README |
| Execution controllers | Callback receives a small interface to read state and influence the wrapper; do not retain after return |
| Lifecycle | `Close` / `Stop` idempotent (`nil`); `ErrClosed` is for **work after** close. Types that own no resource have no `Close` |
| User hooks (`On*`) | Synchronous on the driving goroutine; panics recovered; must not block |
| Classification | Injectable `WithRetryIf` / `WithFailureIf` / `WithFallbackIf` — never HTTP status tables |
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
| `ShedController` | shedx | `Priority()`, `Load()`, `InFlight()`, `Capacity()` | `Shed()`, `SkipSlot()` |
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
| [retryx](retryx/) | Backoff + jitter; **retries every error** until `WithRetryIf` / `Abort` | Transient downstream failures |
| [circuitx](circuitx/) | **Consecutive**-failure Closed→Open→HalfOpen (not a rate window) | Fail fast; probe recovery |
| [bulkx](bulkx/) | Semaphore bulkhead; `TryExecute` does not barge waiters | Cap concurrent calls to one dependency |
| [shedx](shedx/) | Priority shedder; **Critical is never shed**; optional hysteresis | Protect service under overload |
| [adaptx](adaptx/) | **Windowed** AIMD / Vegas / Gradient concurrency servo | Auto-tune concurrency from latency |
| [hedgex](hedgex/) | Hedged copies; first success wins; `Cancel` is that copy's ctx | Cut tail latency on idempotent reads |
| [toutx](toutx/) | Deadline wrapper; Go does not kill `fn`; inner timeouts propagate | Per-attempt or call timeout |
| [fallx](fallx/) | Static / func / cached fallback; **primary always hits origin** | Degrade when primary fails |
| [ratex](ratex/) | Process-local token bucket; `WaitN(n > burst)` fails immediately | Process-wide or handler rate cap |
| [quotax](quotax/) | Per-key buckets; Wait/Execute pins so eviction cannot split a key | Tenant/user/API-key limits |
| [warmupx](warmupx/) | Process-local Bernoulli ramp; `Close` rejects, `Stop` freeze-and-admits | Roll out new instances gradually |

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
| [cfgx](cfgx/) | Load/save YAML/JSON/TOML + nested `Validator` walk | File-backed config |
| [envx](envx/) | Typed `Bind`; opt-in `Walk` / `BindField` | 12-factor overrides at boot |
| [clix](clix/) | Flags + subcommands; `WithHelpLabels` for help chrome | CLI entrypoints and dev overrides |

### Boot vs runtime

URX covers **loading, parsing, and saving** config. **When** to reload subsystems after a change is your composition root — URX does not ship a lifecycle graph or hot-reload orchestrator.

**At process start**, wire precedence explicitly (lowest → highest):

```text
defaults in Go
    ↓
cfgx.Load("config.yaml")          — fatal unless ErrValidationFailed
    ↓
envx.BindTo / Walk+BindField
    ↓
clix flags                        — handle ErrHelp / ErrVersion first
    ↓
env.Validate()                    — required (env parse / required keys)
cfgx.Validate(&cfg, false)        — required (nested walk; not a substitute)
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
cfg := DefaultConfig() // compiled-in defaults

if err := cfgx.Load("config.yaml", &cfg, cfgx.WithCreateIfMissing()); err != nil {
    if !errors.Is(err, cfgx.ErrValidationFailed) {
        return err // I/O / parse / format — do not continue on partial decode
    }
    // optional: still continue so env/flags can repair file-only validation
}

env := envx.New(envx.WithPrefix("APP"))
envx.BindTo(env, "HTTP_PORT", &cfg.HTTP.Port) // absent keeps file
envx.BindTo(env, "LOG_LEVEL", &cfg.Log.Level)

p := clix.New(os.Args[1:], "myservice", "API server",
    clix.AddFlag(&cfg.HTTP.Port, "http-port", "p", cfg.HTTP.Port, "listen port"), // def MUST be live field
    clix.SubCommand("serve", "run server", clix.Run(runServer)),
)
if errors.Is(p.Err(), clix.ErrHelp) {
    fmt.Print(p.Help())
    return nil
}
if errors.Is(p.Err(), clix.ErrVersion) {
    fmt.Println(p.Version())
    return nil
}
if err := errors.Join(env.Validate(), p.Err()); err != nil {
    return err
}
if err := cfgx.Validate(&cfg, false); err != nil {
    return err
}
return p.Run()
```

`Required()` is CLI-presence only. Share only `string`, `int`, `bool`, `float64`, `time.Duration`, and `time.Time` with `AddFlag`. YAML duration `30s` works; JSON needs nanoseconds or a string you parse. Do not `cfgx.Validate(..., true)` after flags. `envx` distinguishes unset from empty string (`os.LookupEnv` semantics). See [envx](envx/) for list/time types.

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

CI snapshot from **1.5.0** (`-benchmem -count=3`, medians; Linux **Intel Xeon 6973P-C**, Windows **AMD EPYC 7763**). **0 allocs/op** on these check/reject paths is the contract; nanosecond figures are hardware-specific and were not re-run for 1.5.2:

| Hot path | Linux | Windows | allocs/op |
| -------- | ----- | ------- | --------- |
| `ratex.Allow` | 62.2 ns | **14.7 ns** | 0 |
| `quotax.Allow` (hit) | 115.9 ns | **44.8 ns** | 0 |
| `bulkx.Allow` | **0.7 ns** | 1.8 ns | 0 |
| `shedx.TryExecute` (shed) | **7.5 ns** | 8.7 ns | 0 |
| `circuitx.Execute` (open reject) | 47.5 ns | **27.2 ns** | 0 |
| `lrux` cache hit | 65.9 ns | **38.3 ns** | 0 |
| `panix.Safe` (no panic) | **5.2 ns** | 8.0 ns | 0 |
| `adaptx.Allow` | 11.4 ns | **5.4 ns** | 0 |
| `warmupx.Allow` | 20.9 ns | **18.4 ns** | 0 |


Run benchmarks locally:

```powershell
go test -race -count=1 ./... && golangci-lint run ./...   # local Gate 2–5 commands
# CI: .github/workflows/ci.yml (lint + matrix + fuzz + bench)
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
- **No shared `errx` type** since 1.4.0 — map to your service error model at the boundary if needed.

---

## Development and Quality

URX uses a **gate pipeline** — craft gates 0–5 plus Gate M (mission proof). Production-ready means Gate M ✅ and Gate 5 ✅.

| Gate | Checks |
| ---- | ------ |
| M | Mission proof tables (`pkg-*.mdc`) with `file:TestName` evidence; `go test -race` |
| 0 | `go build ./...` |
| 1 | `go test -race -count=1 ./...` |
| 2 | `go vet` + `golangci-lint run ./...` |
| 3 | Benchmarks (`-benchmem`); pprof on hot paths when claiming alloc floors |
| 4 | GoDoc + Examples + package READMEs honest vs exports |
| 5 | Fuzz (≥30s per target in CI), coverage ≥90%, CI green |

**Repository quality bar:**

| Metric | Value |
| ------ | ----- |
| Public packages | 20 |
| Statement coverage | ≥90% ship bar (CI); last published total **98.5%** (`-race`, 1.5.0) |
| `golangci-lint` | 0 issues |
| Fuzz targets | 52 across packages |
| CI | Lint + OS×Go 1.24–1.26 test matrix + fuzz discover + bench on `main` ([ci.yml](.github/workflows/ci.yml)) |

Contributing workflow: bring one package to Gate M+5 per focused change — audit, fix structure and implementation, write tests (unit, concurrent, fuzz), benchmarks, GoDoc, and package README.

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

- **1.5.2** — resilience 8+: windowed adaptx (first Gradient window holds; `Close()` does not wait), fail-fast `WaitN`, pin-safe quotax, consecutive circuitx generation (heal + every `Reset`), hysteresis that sheds in-band, fallx/retryx/hedgex/bulkx/toutx/warmupx contract fixes. Same release: `clix` `WithHelpLabels`, nested `cfgx.Validate`, `envx` `Walk`/`BindField`. Breaking details in [CHANGELOG.md](CHANGELOG.md).
- **1.5.1** — `circuitx.WithSuccessThreshold` (consecutive HalfOpen successes to heal).
- **1.5.0** — ship-kit / CI / English docs; `ratex`/`quotax` honor fractional rates below 1.0 req/s.
- **1.4.0** — greenfield rewrite: flat imports, sentinel errors, expanded controllers, removed packages from ≤1.3.0 (`errx`, `dicx`, `logx`, …). Details in [CHANGELOG.md](CHANGELOG.md).
- Pin a version in `go.mod`. Releases through 1.3.0 are a different library surface; upgrading to 1.4.0+ requires code changes.

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
