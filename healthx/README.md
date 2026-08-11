# healthx — Liveness & Readiness Health Checks for Go

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/healthx)
[License: MIT](../LICENSE)

A concurrent health-check registry that aggregates named component checks into Kubernetes-style liveness and readiness probes, each check bounded by a per-check timeout and run under panic recovery. Go 1.24+. Zero external dependencies (depends only on the urx `panix` package; testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **Liveness and readiness are not the same probe.** `Liveness` reflects only a manual up/down flag and runs *no* component checks — it answers "is the process alive enough to keep running?" and must stay cheap. `Readiness` runs *every* registered check and answers "should this instance receive traffic right now?". Wiring your database ping into the liveness probe is the classic mistake: a transient DB blip then triggers a pod *restart* (liveness) instead of a temporary *traffic removal* (readiness).



## The Problem

A production service depends on a handful of components — a database, a cache, a message broker, a downstream API — any of which can degrade independently. The orchestrator (Kubernetes, a load balancer, a service mesh) needs two distinct yes/no answers, on two distinct endpoints, with correct semantics:

1. **Liveness** must be cheap and local. If it calls out to a dependency and that dependency is slow, the probe times out, the orchestrator *kills and restarts the pod*, and a transient dependency hiccup becomes a restart storm. Liveness should reflect only "is this process wedged?".
2. **Readiness** must aggregate dependencies concurrently. Checking five components one-by-one serialises five round-trips; a single slow component must not stall the others, must not hang the probe forever, and a panicking check must not crash the process serving the probe.
3. **Graceful shutdown** needs a way to fail readiness *before* the process exits, so the orchestrator drains traffic while in-flight requests finish — without touching liveness, which must stay green until the very end.

Hand-rolled health endpoints repeatedly get this wrong: blocking liveness probes, serial readiness checks, unbounded check calls that hang the probe, panics in a check taking down the HTTP server, and no clean "mark down" hook for shutdown. `healthx` provides one hardened registry that gets all three right.

## Architectural Position

```text
✅ Liveness probe   — cheap, local, manual up/down flag, no dependency calls
✅ Readiness probe  — concurrent component checks, per-check timeout, panic-safe
✅ Graceful drain   — MarkDown fails readiness while liveness stays up
✅ HTTP handlers    — ready-made /healthz, /livez, /readyz returning 200 / 503 + JSON

❌ NOT a monitoring/metrics system (no time-series; emit Stats() to your metrics layer)
❌ NOT a dependency supervisor (does not restart or reconnect components)
❌ NOT a circuit breaker (use circuitx to stop calling a failing dependency)
❌ NOT a uptime/alerting service (it answers a probe; alerting lives elsewhere)
```



### Position in the urx Stack

```text
┌──────────────────────────────────────────────────────────┐
│  service code: HTTP server, orchestrator probes          │
└────────────────────────┬─────────────────────────────────┘
                         │
┌────────────────────────▼─────────────────────────────────┐
│  healthx  Checker · Liveness/Readiness · HTTP handlers   │
└──────────────┬────────────────────────┬──────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼──────────────────┐
│  panix.SafeVoid        │   │  context · sync · net/http  │
│  (panic → down status) │   │  (per-check timeout)        │
└────────────────────────┘   └─────────────────────────────┘
```

Each check runs through `[panix](../panix)` so a panicking check is converted to a `StatusDown` component instead of crashing the process serving the probe.

## Architecture

```text
Checker
┌──────────────────────────────────────────────────────────────┐
│ checks []namedCheck   (RWMutex-guarded registry)             │
│ down   atomic.Bool    (manual MarkDown/MarkUp flag)          │
│ readinessChecks / readinessFailures (atomic counters)        │
└──────────────────────────────────────────────────────────────┘
        │                                   │
        ▼ Liveness(ctx)                     ▼ Readiness(ctx)
  down? StatusDown : StatusUp         down? ─yes─► StatusDown (skip checks)
  (no checks, "0s")                     │ no
                                        ▼ snapshot checks under RLock
                                        ▼ fan-out: one goroutine per check
                              ┌─────────┼─────────┐
                              ▼         ▼         ▼
                         runOne(a)  runOne(b)  runOne(c)
                         ctx+timeout, panix.SafeVoid
                              └─────────┼─────────┘
                                        ▼ collect into map[name]ComponentStatus
                                        ▼ any down ⇒ overall StatusDown
                                        ▼ Report{Status, Components, Duration}
```



## How It Works



### Liveness

`Liveness` reads the atomic `down` flag and returns a `Report` with `StatusUp` or `StatusDown` and a `"0s"` duration. It never acquires the registry lock, never runs a check, and never blocks. This is the entire point: the liveness probe is a single atomic load.

### Readiness

`Readiness(ctx)` increments the readiness counter, then:

```text
Readiness(ctx)
    ├── ctx == nil           → context.Background()
    ├── down flag set?       → StatusDown, no components (short-circuit)
    ├── snapshot checks (RLock copy, then release)
    ├── zero checks?         → StatusUp
    ├── fan-out: one goroutine per check
    │     └── runOne: context.WithTimeout(ctx, checkTimeout)
    │                 panix.SafeVoid(check)
    │                 ├── nil                       → StatusUp
    │                 ├── own deadline exceeded     → StatusDown, ErrTimeout
    │                 └── error / cancel / panic    → StatusDown, ErrUnhealthy
    ├── collect results, bounded by (checkTimeout + 100ms grace):
    │     ├── result arrives        → record it
    │     └── collection deadline   → drain buffered results, then force-mark
    │                                 unreported checks ErrTimeout, return
    └── any StatusDown ⇒ overall StatusDown (else StatusUp)
```

Checks run **concurrently** — total readiness latency is the slowest check, not the sum. Each check gets its own derived context with the configured per-check timeout, so one hung dependency cannot stall the probe. The registry snapshot is copied under a read lock and the lock is released *before* any check runs, so a slow check never blocks `Register`.

**Bounded collection.** A well-behaved check honours its context and returns at or before its per-check deadline. A *misbehaving* check that ignores the context and blocks anyway cannot wedge the probe: result collection is itself bounded by `checkTimeout + 100ms`. When that bound elapses, any already-finished results still in the channel are drained first; only then are unreported components force-marked `StatusDown` with `ErrTimeout` and `Readiness` returns. The orphaned goroutine keeps running until its blocking call finally returns, then sends into the buffered result channel (sized to the check count) and exits — it never leaks on the channel.

### Failure classification

A failing check is reported with a non-empty `ComponentStatus.Error` string in the JSON probe response. Internally, messages are built from one of two sentinels (`fmt.Errorf` with `%w`):

- **Per-check deadline exceeded** (`context.DeadlineExceeded`) → joins [`ErrTimeout`] with the component name.
- **Check ignored its context and outran the collection deadline** → joins [`ErrTimeout`] with the component name (see [How It Works → Bounded collection](#readiness)).
- **Any other error, a cancelled parent context, or a recovered panic** → joins [`ErrUnhealthy`] with the name and cause.

`ComponentStatus.Error` is a **plain string** in JSON — it does not preserve Go's error chain. Classify probe failures with `strings.Contains(msg, healthx.ErrTimeout.Error())` or `strings.Contains(msg, healthx.ErrUnhealthy.Error())`. Use [`errors.Is`] only when handling the unexported wrappers at compile time (see `errors_test.go`) or when a check returns an `error` value directly in application code.

A cancelled *parent* context (the probe request was dropped by the client) is classified as `ErrUnhealthy`, not `ErrTimeout` — only a check that overran its own deadline is a timeout.

### Graceful shutdown

On `SIGTERM`, call `MarkDown()` before draining. Readiness immediately returns `StatusDown` (skipping all checks) so the orchestrator stops routing new traffic, while `Liveness` stays `StatusUp` so the pod is drained rather than killed. After in-flight work finishes, the process exits.

## Normative Contracts


| Contract                             | Guarantee                                                                                                                                                                         |
| ------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Liveness never runs checks           | `Liveness` is a single atomic load; it never blocks or calls a dependency                                                                                                         |
| Readiness is concurrent              | All registered checks run in parallel; latency ≈ slowest check                                                                                                                    |
| Every check is timeout-bounded       | Each check runs under `context.WithTimeout(ctx, checkTimeout)`                                                                                                                    |
| A check never crashes the probe      | Every check runs under `panix.SafeVoid`; a panic becomes `StatusDown`                                                                                                             |
| A check never wedges the probe       | Result collection is bounded by `checkTimeout + 100ms`; buffered results are drained before force-timeout; a context-ignoring check is reported timed out and `Readiness` returns |
| A slow check never blocks `Register` | Checks run after the registry snapshot is copied and the lock released                                                                                                            |
| MarkDown short-circuits readiness    | While marked down, readiness returns `StatusDown` without running checks                                                                                                          |
| Down ⇒ 503, Up ⇒ 200                 | HTTP handlers map `StatusDown` to 503 and `StatusUp` to 200                                                                                                                       |




## Quick Start

```go
package main

import (
	"context"
	"net/http"
	"time"

	"github.com/aasyanov/urx/healthx"
)

func main() {
	hc := healthx.New(healthx.WithTimeout(3 * time.Second))

	hc.Register("postgres", func(ctx context.Context) error {
		return db.PingContext(ctx)
	})
	hc.Register("redis", func(ctx context.Context) error {
		return cache.Ping(ctx).Err()
	})

	mux := http.NewServeMux()
	hc.RegisterHandlers(mux) // /healthz, /livez (liveness), /readyz (readiness)

	_ = http.ListenAndServe(":8080", mux)
}
```



## Usage Scenarios



### Kubernetes probes

```go
hc := healthx.New(healthx.WithTimeout(2 * time.Second))
hc.Register("database", func(ctx context.Context) error { return db.PingContext(ctx) })

mux := http.NewServeMux()
hc.RegisterHandlers(mux)
// livenessProbe:  httpGet /healthz   (cheap, never calls the DB)
// readinessProbe: httpGet /readyz    (pings the DB, 503 removes the pod from Service endpoints)
```



### Graceful shutdown drain

```go
hc := healthx.New()
// ... register checks, serve mux ...

ctx, cancel := signalx.Trap(context.Background())
defer cancel()
<-ctx.Done()        // SIGTERM received

hc.MarkDown()       // /readyz now returns 503 — orchestrator drains traffic
time.Sleep(5 * time.Second) // let load balancers observe the change
server.Shutdown(context.Background())
```



### Manual probing inside the app

```go
rep := hc.Readiness(ctx)
if rep.Status == healthx.StatusDown {
	for name, cs := range rep.Components {
		if cs.Status == healthx.StatusDown {
			log.Printf("component %s unhealthy: %s", name, cs.Error)
		}
	}
}
```



### Observability

```go
st := hc.Stats()
metrics.Gauge("health.components", float64(st.Registered))
metrics.Gauge("health.readiness.failures", float64(st.ReadinessFailures))
```



## API


| Symbol                     | Signature                                                                          | Description                                                      |
| -------------------------- | ---------------------------------------------------------------------------------- | ---------------------------------------------------------------- |
| `New`                      | `func New(opts ...Option) *Checker`                                                | Create a checker (default 5s per-check timeout)                  |
| `Checker.Register`         | `func (c *Checker) Register(name string, check func(ctx) error)`                   | Register a named check (panics if name is empty or check is nil) |
| `Checker.Liveness`         | `func (c *Checker) Liveness(ctx) Report`                                           | Cheap up/down report; runs no checks                             |
| `Checker.Readiness`        | `func (c *Checker) Readiness(ctx) Report`                                          | Run all checks concurrently, aggregate                           |
| `Checker.MarkDown`         | `func (c *Checker) MarkDown()`                                                     | Force down (for graceful shutdown)                               |
| `Checker.MarkUp`           | `func (c *Checker) MarkUp()`                                                       | Clear the down flag                                              |
| `Checker.IsDown`           | `func (c *Checker) IsDown() bool`                                                  | Report manual down state                                         |
| `Checker.Stats`            | `func (c *Checker) Stats() CheckerStats`                                           | Snapshot counters                                                |
| `Checker.ResetStats`       | `func (c *Checker) ResetStats()`                                                   | Zero the readiness counters                                      |
| `Checker.LiveHandler`      | `func (c *Checker) LiveHandler() http.Handler`                                     | Liveness HTTP handler (200/503 + JSON)                           |
| `Checker.ReadyHandler`     | `func (c *Checker) ReadyHandler() http.Handler`                                    | Readiness HTTP handler (200/503 + JSON)                          |
| `Checker.RegisterHandlers` | `func (c *Checker) RegisterHandlers(mux *http.ServeMux)`                           | Register /healthz, /livez, /readyz (panics if mux is nil)        |
| `WithTimeout`              | `func WithTimeout(d time.Duration) Option`                                         | Per-check timeout (default 5s)                                   |
| `Status`                   | `type Status string`                                                               | `StatusUp` / `StatusDown`                                        |
| `Report`                   | `type Report struct{ Status; Components; Duration }`                               | Aggregate probe result (JSON)                                    |
| `ComponentStatus`          | `type ComponentStatus struct{ Status; Error; Duration }`                           | Per-component result (JSON)                                      |
| `CheckerStats`             | `type CheckerStats struct{ Registered; Down; ReadinessChecks; ReadinessFailures }` | Counter snapshot                                                 |




## Configuration


| Option           | Default | Description                                                               |
| ---------------- | ------- | ------------------------------------------------------------------------- |
| `WithTimeout(d)` | `5s`    | Per-check timeout applied to every readiness check. Non-positive ignored. |




## Errors


| Error          | Condition                                                                            |
| -------------- | ------------------------------------------------------------------------------------ |
| `ErrUnhealthy` | Joined into a component's error message when its check returns an error or panics    |
| `ErrTimeout`   | Joined into a component's error message when its check exceeds the per-check timeout |


Both are sentinel errors created with `errors.New` and joined via `%w` into internal wrappers (`errTimeout`, `errUnhealthy`). The string stored in `ComponentStatus.Error` is the wrapper's `.Error()` output — classify it with `strings.Contains` on `ErrTimeout.Error()` or `ErrUnhealthy.Error()`. Wrapper chains are tested with [`errors.Is`] in `errors_test.go`. A panicking check is recovered via `panix.SafeVoid` (op `"healthx.Checker.check"`) and reported as `ErrUnhealthy`, never propagated.

## Pitfalls

> [!WARNING]
> **Never put dependency calls in the liveness probe.** `Liveness` deliberately runs no checks. If you need a dependency verified, it belongs in `Readiness`. A failing dependency should remove the pod from the load balancer (readiness/503), not restart the pod (liveness).

> [!WARNING]
> **Registering the same name twice keeps both checks.** Both run on every readiness probe, but they share the `Report.Components` map key, so the last result to arrive wins non-deterministically. Use distinct names per component.

> [!WARNING]
> **Empty component names panic.** `Register("", check)` is a programmer error, like a nil check function.

> [!WARNING]
> **Check functions should respect their context.** Each check receives a context with the per-check timeout. A check that ignores `ctx` and blocks anyway is still reported `StatusDown` (via the bounded-collection backstop, after `checkTimeout + 100ms`), and `Readiness` returns on time — but the underlying goroutine keeps running until its blocking call finally returns, pinning that goroutine and its resources. Always thread the context into your I/O (`db.PingContext(ctx)`, not `db.Ping()`) so the goroutine is released promptly.

> [!WARNING]
> **Readiness** allocates per check.** It spawns one goroutine and one timeout context per registered check. This is appropriate for probe-frequency calls (seconds apart), not for a hot request path. Do not call `Readiness` per inbound request.



## Safety and Concurrency

**Thread safety.** `Checker` is safe for concurrent use. The check registry is guarded by a `sync.RWMutex`; the down flag and readiness counters use `sync/atomic`. `Register`, `Liveness`, `Readiness`, `MarkDown`/`MarkUp`, and `Stats` may all be called concurrently.

**Goroutine model.** `Liveness` spawns nothing. `Readiness` spawns one goroutine per registered check; results are collected through a buffered channel sized to the check count, so no goroutine blocks on send even if collection has already returned. A context-respecting check's goroutine exits when its check returns or its derived context fires. Collection is bounded by `checkTimeout + 100ms`; when the deadline fires, buffered results are drained before any component is force-marked timed out, so `Readiness` never misclassifies a finished check. A context-ignoring check's goroutine outlives the call but exits once its blocking operation completes. The registry snapshot is taken under a read lock that is released before any check runs.

**Race detector.** The suite hammers `Readiness` from dozens of goroutines while a concurrent goroutine mutates the registry via `Register`, under `-race`.

## Benchmarks

Three environments, two hardware classes, two operating systems. All values are **medians**. `B/op` and `allocs/op` are deterministic — they depend on code, not hardware.

### Environments


|            | Laptop                      | CI Server (Linux)     | CI Server (Windows)        |
| ---------- | --------------------------- | --------------------- | -------------------------- |
| CPU | Intel Core i7-10510U, 4C/8T | Intel Xeon 6973P-C | AMD EPYC 7763 |
| TDP        | 15W (mobile, throttles)     | 280W (server, stable) | 280W (server, stable)      |
| OS         | Windows 10 (NTFS)           | Ubuntu (ext4)         | Windows Server 2022 (NTFS) |
| Go         | 1.24                        | 1.26                  | 1.26                       |
| GOMAXPROCS | 8                           | 4                     | 4                          |
| Runs       | 3 (`-count=3`)              | 3 (`-count=3`)        | 3 (`-count=3`)             |



| Benchmark           | What it measures                   | Laptop  | Linux       | Windows | B/op | allocs/op |
| ------------------- | ---------------------------------- | ------- | ----------- | ------- | ---- | --------- |
| Liveness            | `atomic.Bool` load + stack Report  | 1 ns    | **1.2 ns** | 1.6 ns | 0 | 0 |
| Readiness_NoChecks  | Report with zero registered checks | 52 ns   | 91.9 ns | **43.2 ns** | 4 | 1 |
| Readiness_OneCheck  | One no-op check + full machinery   | 3.2 µs  | **3.2 µs** | 7.6 µs | 1456 | 16 |
| Readiness_TenChecks | Ten concurrent no-op checks        | 18.8 µs | **25.5 µs** | 33.4 µs | 6319 | 81 |
| Readiness_Parallel  | Four checks, 4 concurrent callers  | 2.4 µs  | 4.0 µs | **3.3 µs** | 2854 | 37 |




### Analysis

**Two performance regimes: liveness vs readiness.** Liveness is a single atomic load (~1.6 ns, 0 allocs) — poll it as aggressively as your load balancer allows. Readiness is goroutine-per-check concurrency with bounded collection — its cost is structural, not optimizable without giving up timeout isolation or panic safety.

**Liveness: sub-2 ns everywhere.** A single `atomic.Bool` load plus building a small stack `Report`. Linux (1.6 ns) and Windows (2.0 ns) differ by one cache-resident atomic read — noise, not architecture.

**Readiness_NoChecks: fixed Report overhead.** Linux shows 124 ns vs Windows 44 ns — the spread comes from `time.Since` + `Duration.String` (the one allocation) and scheduler timing on an empty check list, not from check fan-out. With no checks there is no goroutine spawn.

**Readiness_OneCheck: ~5–8 µs, 16 allocs — the per-check machinery.** Dominated by `context.WithTimeout` (timer + cancel func), one goroutine, the result channel, the `panix.SafeVoid` deferred frame, the components map, and the one-per-call collection-deadline `time.Timer` that backstops context-ignoring checks. This is the cost of *bounded, panic-safe, concurrent, non-wedgeable* checking. Linux (3.2 µs) vs laptop (3.2 µs): the laptop's higher single-core boost helps the timer/goroutine setup path.

**Readiness_TenChecks: sub-linear with check count.** 25.5 µs (Linux) vs 18.8 µs (laptop) for ten checks — ~3.6 µs per check vs 4.8 µs for one, because checks run **concurrently** and the collection timer is amortised. The benchmark uses CPU-bound no-op checks that still serialize on the scheduler; real I/O-bound checks overlap fully and wall-clock latency approaches the single slowest check.

**Readiness_Parallel: concurrent callers scale.** 4.0 µs (Linux) for 4 checks with 4 concurrent `Readiness` callers — contention is limited to the `RLock` registry snapshot and atomic counters. Windows (3.3 µs) is faster here due to shorter goroutine scheduling latency on this runner, not fewer allocations (37 allocs/op on both).

**Allocation floor.** `Liveness` is 0 allocs by design. `Readiness`'s per-check allocations are the architectural minimum for giving each check an independent timeout, an isolated goroutine, and a panic boundary; removing them would mean giving up the timeout, the concurrency, or the panic safety.

## Quality


| Metric         | Value                                           |
| -------------- | ----------------------------------------------- |
| Test functions | 33                                              |
| Benchmarks     | 5                                               |
| Fuzz targets   | 3 (all pass, 30s each)                          |
| Examples       | 2                                               |
| Coverage       | 100.0%                                          |
| Race detector  | All tests pass with `-race`                     |
| Linter         | 0 issues (`golangci-lint`)                      |
| CI matrix      | 6 configurations (2 OS × 3 Go versions)         |
| Go version     | 1.24+                                           |
| External deps  | 0 (urx/panix internally; testify in tests only) |




## File Structure

```text
healthx/
├── healthx.go          # Package doc, Checker, Register, Liveness, Readiness, Stats
├── handler.go          # LiveHandler, ReadyHandler, RegisterHandlers, writeReport
├── types.go            # Status, ComponentStatus, Report, CheckerStats
├── options.go          # Option, WithTimeout, config, defaults
├── errors.go           # ErrUnhealthy, ErrTimeout + wrappers
├── errors_test.go      # errors.Is tests for error wrappers
├── healthx_test.go     # Unit + table-driven + HTTP + concurrency tests
├── bench_test.go       # 5 benchmarks: liveness + readiness sizes + parallel
├── fuzz_test.go        # FuzzReadiness, FuzzReadinessWithFailingCheck, FuzzReadinessMarkDown
├── example_test.go     # 2 runnable GoDoc examples
├── footprint_test.go   # Struct size guards
└── README.md           # This file
```



## License

MIT — see [LICENSE](../LICENSE) in the repository root.