# healthx

Health-check registry for Kubernetes-style liveness and readiness probes.

## Philosophy

**One job: health reporting.** `healthx` maintains a registry of named
health-check functions and exposes Kubernetes-compatible `/livez` and `/readyz`
endpoints. Liveness is a manual up/down toggle; readiness runs all registered
checks with per-check timeouts. It does not restart services, log, or alert.
Those are responsibilities of the orchestrator and the caller.

## Quick start

```go
hc := healthx.New(healthx.WithTimeout(3 * time.Second))

hc.Register("postgres", func(ctx context.Context) error {
    return db.PingContext(ctx)
})
hc.Register("redis", func(ctx context.Context) error {
    return rdb.Ping(ctx).Err()
})

mux := http.NewServeMux()
hc.RegisterHandlers(mux) // /healthz, /livez, /readyz
```

## API

| Function / Method | Description |
|---|---|
| `New(opts ...Option) *Checker` | Create a health checker (5 s timeout) |
| `c.Register(name, fn)` | Add a named health-check function |
| `c.MarkDown()` | Mark system as down (liveness fails) |
| `c.MarkUp()` | Clear MarkDown |
| `c.IsDown() bool` | Whether system is manually marked down |
| `c.Liveness(ctx) Report` | Report from manual up/down state |
| `c.Readiness(ctx) Report` | Run all checks, return aggregate Report. Nil ctx → `context.Background()` |
| `c.LiveHandler() http.Handler` | HTTP handler for `/livez` (200/503 + JSON) |
| `c.ReadyHandler() http.Handler` | HTTP handler for `/readyz` (200/503 + JSON) |
| `c.RegisterHandlers(mux)` | Register `/healthz`, `/livez`, `/readyz` on ServeMux |

### Options

| Option | Default | Description |
|---|---|---|
| `WithTimeout(d)` | `5s` | Per-check execution timeout |

### Types

| Type | Description |
|---|---|
| `Status` | `"up"` or `"down"` |
| `ComponentStatus` | Result of a single check (Status, Error, Duration) |
| `Report` | Aggregate result (Status, Components map, Duration) |

## Behavior details

- **Liveness**: returns `StatusUp` unless `MarkDown()` was called. No checks
  are executed — liveness is a simple toggle for orchestrator drain.

- **Readiness**: runs all registered checks concurrently, each with its own
  `context.WithTimeout`. If any check fails or times out, the overall status is
  `StatusDown`. Returns a detailed `Report` with per-component results.

- **HTTP handlers**: `LiveHandler` and `ReadyHandler` serialize the `Report` as
  JSON and return HTTP 200 (up) or 503 (down).

- **Endpoint aliases**: `/healthz` and `/livez` are both liveness endpoints.
  Use `RegisterHandlers` to mount standard probe paths in one call.

- **MarkDown / MarkUp**: controlled via `atomic.Bool`. Use `MarkDown` before
  graceful shutdown to drain traffic.

## Error diagnostics

All errors are `*errx.Error` with `Domain = "HEALTH"`.

### Codes

| Code | When |
|---|---|
| `UNHEALTHY` | A health-check function returned an error |
| `TIMEOUT` | A health-check function exceeded its timeout |

### Example

```text
HEALTH.UNHEALTHY: health check failed | meta: component=postgres
HEALTH.TIMEOUT: health check timed out | meta: component=redis
```

## Thread safety

- `Register` appends under `sync.RWMutex` write lock — safe for concurrent registration. Panics if check is nil
- `MarkDown` / `MarkUp` / `IsDown` use `atomic.Bool` — lock-free
- `Liveness` reads `atomic.Bool` — lock-free
- `Readiness` takes a snapshot of checks under mutex, then runs concurrently
- `LiveHandler` / `ReadyHandler` are stateless HTTP handlers — safe for concurrent use

## Kubernetes probes

```yaml
livenessProbe:
  httpGet:
    path: /livez
    port: 8080
  initialDelaySeconds: 5
  periodSeconds: 10
  timeoutSeconds: 1
  failureThreshold: 3

readinessProbe:
  httpGet:
    path: /readyz
    port: 8080
  initialDelaySeconds: 2
  periodSeconds: 5
  timeoutSeconds: 2
  failureThreshold: 2
```

Use `/healthz` as an alias for liveness when your platform expects that name.
Add `startupProbe` only when the service has a long cold start (for example,
heavy migrations or model loading).

## Tests

**21 tests, 98.6% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/healthx  coverage: 98.6% of statements
```

Coverage includes:
- Liveness: up, after MarkDown, after MarkUp
- Readiness: all healthy, one unhealthy, check timeout
- HTTP handlers: status codes, JSON body, content type
- RegisterHandlers: standard Kubernetes paths `/healthz`, `/livez`, `/readyz`
- Register: multiple checks, concurrent registration, nil check panic
- Readiness: nil context normalization
- Report structure: component names, status values

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz.
Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkLiveness              ~4 ns/op         0 B/op     0 allocs/op
BenchmarkReadiness_3Checks  ~9170 ns/op      2160 B/op    30 allocs/op
BenchmarkLiveHandler        ~4525 ns/op      6189 B/op    20 allocs/op
BenchmarkReadyHandler      ~11816 ns/op      7535 B/op    37 allocs/op
```

### Analysis

**Liveness:** ~4 ns, 0 allocs. Single `atomic.Bool` load. Effectively free.

**Readiness (3 checks):** ~9.2 us, 30 allocs. Dominated by goroutine spawning (one per check) and result aggregation. Each check is a no-op in benchmarks — real checks (DB ping, etc.) will dominate.

**LiveHandler:** ~4.5 us, 20 allocs. HTTP response writing + JSON serialization. The JSON overhead is ~4 us per call.

**ReadyHandler:** ~11.8 us, 37 allocs. Readiness checks + HTTP + JSON. In production, check latency dominates.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| Liveness | < 5 ns, 0 allocs | Free |
| Readiness (3 checks) | < 10 us | Dominated by real checks |
| LiveHandler | < 5 us | HTTP overhead only |
| ReadyHandler | < 12 us | HTTP + check overhead |

## What healthx does NOT do

| Concern | Owner |
|---------|-------|
| Service restart | Kubernetes / orchestrator |
| Alerting | Prometheus / Grafana |
| Logging | caller / `slog` |
| Retry on check failure | caller |
| Custom status codes | caller wraps handler |
| Authentication | HTTP middleware |

## File structure

```text
pkg/healthx/
    healthx.go      -- Checker, New(), Register(), Liveness(), Readiness(), handlers
    errors.go       -- DomainHealth, Code constants, error constructors
    healthx_test.go -- 21 tests, 98.6% coverage
    bench_test.go   -- 4 benchmarks
    README.md
```
