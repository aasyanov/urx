# shedx

Priority-based load shedding for industrial Go services.

## Philosophy

**One job: shed excess load.** `shedx` tracks in-flight operations and rejects
low-priority requests when load exceeds a configurable threshold. Higher
priorities survive longer under pressure. It does not retry, queue, rate-limit,
or log. Those are responsibilities of `retryx`, `poolx`, `ratex`, and the
caller.

## Quick start

```go
s := shedx.New(
    shedx.WithCapacity(1000),
    shedx.WithThreshold(0.8),
)
defer s.Close()

resp, err := shedx.Execute(s, ctx, shedx.PriorityNormal, func(ctx context.Context, sc shedx.ShedController) (*Response, error) {
    if sc.Load() > 0.9 {
        return quickResponse(ctx)
    }
    return handleRequest(ctx)
})
if err != nil {
    // request shed or function error
}
```

## API

| Function / Method | Description |
|---|---|
| `New(opts ...Option) *Shedder` | Create a shedder (1000 capacity, 0.8 threshold) |
| `s.Allow(priority) bool` | Check if request would be admitted (no tracking) |
| `Execute[T](s, ctx, priority, fn) (T, error)` | Run fn if admitted; fn receives [ShedController]; returns `CodeRejected` if shed |
| `s.Load() float64` | Current load ratio (inflight / capacity) |
| `s.InFlight() int64` | Number of in-flight operations |
| `s.Stats() Stats` | Point-in-time counters snapshot |
| `s.ResetStats()` | Zero admitted and shed counters |
| `s.Close()` | Shut down shedder (idempotent) |
| `s.IsClosed() bool` | Whether shedder is shut down |

### ShedController

The callback passed to `Execute` receives a `ShedController`:

| Method | Description |
|---|---|
| `Priority() Priority` | The priority this request was admitted with |
| `Load() float64` | Load ratio (inflight/capacity) at admission time |
| `InFlight() int64` | In-flight count at admission time |

### Options

| Option | Default | Description |
|---|---|---|
| `WithCapacity(n)` | `1000` | Maximum in-flight operations |
| `WithThreshold(t)` | `0.8` | Load fraction at which shedding starts |

### Priority levels

| Priority | Value | Shed cutoff (overload) | Description |
|---|---|---|---|
| `PriorityLow` | 0 | `< 0.25` | Shed first |
| `PriorityNormal` | 1 | `< 0.60` | Default level |
| `PriorityHigh` | 2 | `< 0.90` | Shed only under heavy load |
| `PriorityCritical` | 3 | never | Never shed |

## Behavior details

- **Admission logic**: when `Load() >= Threshold`, the shedder computes
  `overload = (load - threshold) / (1 - threshold)` and rejects requests
  whose priority cutoff is exceeded. Below threshold, all requests pass.
  `PriorityCritical` (and any value >= `PriorityCritical`) is always admitted
  regardless of load. This is a deliberate fail-open design — unknown priority
  values are treated as critical rather than rejected.

- **In-flight tracking**: `Execute` increments an `atomic.Int64` on entry and
  decrements on exit via `defer` (including panic paths). `Load()` returns
  `inflight / capacity`.

- **Panic recovery**: the function passed to `Execute` is wrapped with
  `panix.Safe`. If it panics, the panic is converted to an `*errx.Error`
  and the in-flight counter is still decremented.

- **Close**: marks the shedder as closed via `atomic.Bool`. After `Close`,
  `Execute` returns `CodeClosed` and `Allow` returns `false`.

## Error diagnostics

All errors are `*errx.Error` with `Domain = "SHED"`.

### Codes

| Code | When |
|---|---|
| `REJECTED` | Request shed due to overload (severity: warn) |
| `CLOSED` | Shedder has been shut down |

### Example

```text
SHED.REJECTED: request shed | meta: priority=low
SHED.CLOSED: shedder closed
```

## Thread safety

- `Allow` / `Execute` use `atomic.Int64` for in-flight counter — lock-free
- `Load` / `InFlight` read atomics — lock-free
- `Close` / `IsClosed` use `atomic.Bool` — lock-free
- `Stats` / `ResetStats` use atomic counters — lock-free
- Fully concurrent — no mutexes

## Tests

**35 tests, 98.3% statement coverage.**

```text
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/shedx  coverage: 98.3% of statements
```

Coverage includes:
- Allow: under threshold, at threshold, over threshold by priority level
- Execute: admitted, rejected, closed, function error, panic recovery
- Priority ordering: Low shed before Normal, Critical never shed
- Load / InFlight: accurate under concurrent operations
- Stats: counters increment, reset
- Lifecycle: Close, Close idempotent, IsClosed
- New: defense-in-depth fallbacks for invalid capacity and threshold
- ShedController: Priority, Load, InFlight snapshotting

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz.
Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkExecute_Admitted    ~48 ns/op       0 B/op     0 allocs/op
BenchmarkExecute_Rejected   ~519 ns/op     544 B/op     4 allocs/op
BenchmarkAllow                ~3 ns/op       0 B/op     0 allocs/op
```

### Analysis

**Execute (admitted):** ~48 ns, 0 allocs. Atomic increment + `fn()` call + atomic decrement. No lock, no allocation. The function body (no-op in benchmark) adds negligible cost.

**Execute (rejected):** ~519 ns, 4 allocs. The 4 allocations are the `errx.Error` struct and its metadata. This is the rejection path — expected to be less frequent than admitted requests.

**Allow:** ~3 ns, 0 allocs. Single atomic load + comparison. Effectively free.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| Admitted | < 50 ns, 0 allocs | Invisible overhead |
| Rejected | < 600 ns, 4 allocs | Acceptable for rejection path |
| Allow | < 3 ns, 0 allocs | Free |

## What shedx does NOT do

| Concern | Owner |
|---------|-------|
| Retry rejected requests | `retryx` |
| Rate limiting | `ratex` |
| Circuit breaking | `circuitx` |
| Request queuing | `poolx` / caller |
| Logging | caller / `slog` |
| Dynamic priority assignment | caller |
| HTTP middleware | caller |

## File structure

```text
pkg/shedx/
    shedx.go       -- Shedder, ShedController, Priority, New(), Allow(), Execute()
    errors.go      -- DomainShed, Code constants, error constructors
    shedx_test.go  -- 35 tests, 98.3% coverage
    bench_test.go  -- 3 benchmarks
    README.md
```
