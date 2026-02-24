# warmupx

Gradual capacity ramp-up (slow start) for industrial Go services.

## Philosophy

**One job: gradual capacity ramp-up.** A [Warmer] increases its admission
probability from a minimum to a maximum over a configurable duration using
one of four strategies. No HTTP, no framework coupling, no struct tags.

Use cases: cold start, post-deploy rollout, circuit-breaker recovery,
auto-scaling new instances.

## Quick start

```go
w := warmupx.New(
    warmupx.WithDuration(30 * time.Second),
    warmupx.WithStrategy(warmupx.Exponential),
)
w.Start()
defer w.Stop()

if w.Allow() {
    handleRequest(ctx)
} else {
    // 503 or queue
}
```

## API

| Method | Description |
|---|---|
| `New(opts ...Option) *Warmer` | Create a warmer (default: Linear, 10% → 100%, 1 min) |
| `w.Start()` | Begin warmup from MinCapacity |
| `w.StartAt(cap)` | Begin warmup at a specific capacity |
| `w.Stop()` | Halt warmup; capacity stays at current value |
| `w.Reset()` | Stop + Start from MinCapacity |
| `w.Capacity() float64` | Current capacity in [0, 1] |
| `w.IsWarming() bool` | True if warmup is in progress |
| `w.IsComplete() bool` | True if warmup has finished |
| `w.Progress() float64` | Warmup progress in [0, 1] |
| `w.Allow() bool` | Probabilistic admission based on current capacity |
| `w.AllowOrError() error` | nil if allowed, `*errx.Error` if rejected |
| `w.MaxRequests(base) int` | Scale a base limit by current capacity (ceil); returns 0 if base <= 0 |
| `w.WaitForCompletion(ctx) error` | Block until warmup finishes or ctx cancelled |
| `w.Stats() Stats` | Snapshot of state and counters |
| `w.ResetStats()` | Zero allowed/rejected counters |

### Options

| Option | Default | Description |
|---|---|---|
| `WithStrategy(s)` | `Linear` | Ramp-up curve |
| `WithMinCapacity(v)` | `0.1` | Starting capacity [0, 1] |
| `WithMaxCapacity(v)` | `1.0` | Target capacity (0, 1] |
| `WithDuration(d)` | `1m` | Total warmup duration |
| `WithInterval(d)` | `Duration/100` | Capacity update interval [10ms, 1s] |
| `WithStepCount(n)` | `10` | Steps for Step strategy |
| `WithExpFactor(f)` | `3.0` | Factor for Exponential strategy |
| `WithOnCapacityChange(fn)` | — | Async callback on > 1% change |
| `WithOnComplete(fn)` | — | Async callback on completion |

## Strategies

| Strategy | Formula | Best for |
|---|---|---|
| `Linear` | `min + delta * t` | Predictable load |
| `Exponential` | `min + delta * (1 - e^(-k*t*5))` | Fast initial ramp, then flatten |
| `Logarithmic` | `min + delta * log(1+t*e) / log(1+e)` | Fast start, cautious finish |
| `Step` | `min + (delta/steps) * floor(t*steps)` | Discrete levels for monitoring |

```text
Capacity
    ^
100%│                    ┌─────────────
    │                   /
    │                  /   Linear
    │                 /    Exponential
    │                /     Logarithmic
    │               /      Step
 10%│──────────────/
    └──────────────────────────────────▶ Time
                   Duration
```

## Behavior details

- **Probabilistic admission**: `Allow()` returns true with probability equal
  to current capacity. At 50% capacity, roughly half of calls are admitted.

- **Allow before Start**: calling `Allow()` before `Start()` uses the initial
  `MinCapacity` value (default 0.1). This means ~10% of calls are admitted
  even before warmup begins, providing a safe default.

- **Tick-based updates**: a background goroutine updates capacity at
  `WithInterval` frequency. Default is `Duration/100` (100 updates),
  clamped to [10ms, 1s].

- **Completion**: when elapsed time reaches Duration, capacity is set to
  MaxCapacity, `IsComplete()` returns true, `WaitForCompletion` unblocks,
  and callbacks fire asynchronously.

- **Restart safety**: calling `StartAt` while warming safely closes the
  previous goroutine before starting a new one. No double-close panics.

- **Callbacks are async**: `OnCapacityChange` and `OnComplete` run in
  separate goroutines. They may be delivered out-of-order under high
  contention.

## Error diagnostics

All errors are `*errx.Error` with `Domain = "WARMUP"`, `Severity = Warn`,
`Retry = RetrySafe`.

### Codes

| Code | When |
|---|---|
| `REJECTED` | Request rejected during warmup |

### Example

```text
WARMUP.REJECTED: request rejected during warmup | meta: capacity=0.35, progress=0.42
```

## Thread safety

- `Capacity`, `IsWarming`, `IsComplete`, `Progress`, `Stats` use `sync.RWMutex` — readers never block each other
- `Allow` reads capacity under RLock, increments atomic counters — lock-free on hot path
- `Start`, `Stop`, `Reset`, `StartAt` use exclusive lock — safe for concurrent calls
- `ResetStats` is lock-free (atomic stores)
- Background ticker goroutine updates capacity under exclusive lock

## Tests

**55 tests, 97.6% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/warmupx  coverage: 97.6% of statements
```

Coverage includes:
- All four strategies reach MaxCapacity
- Direct `calculate()` validation for each curve
- All options: valid and invalid values, clamping
- Start/Stop/Reset lifecycle, multiple restarts
- Completion with callbacks
- Progress before/during/after warmup
- Allow at 0%, 50%, 100% capacity
- AllowOrError with errx.Error validation
- MaxRequests with rounding
- WaitForCompletion: success, timeout, already-complete
- Stats before start, during warmup
- ResetStats
- Concurrent read operations (100 goroutines)
- Concurrent Start/Stop/Reset (150 goroutines)
- tick() early returns (not warming, already complete)

The uncovered 2.4% is defensive code: `remaining < 0` guard in Stats and
`default` branch in calculate (unreachable for valid Strategy values).

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz.

```text
BenchmarkAllow                    parallel    0 allocs/op
BenchmarkAllowOrError_Allowed     parallel    0 allocs/op
BenchmarkAllowOrError_Rejected    parallel    1 alloc/op (errx.Error)
BenchmarkCapacity                 parallel    0 allocs/op
BenchmarkProgress                 parallel    0 allocs/op
BenchmarkStats                    parallel    0 allocs/op
BenchmarkMaxRequests              sequential  0 allocs/op
```

### Performance summary

| Path | Allocs | Notes |
|------|--------|-------|
| Allow (success) | 0 | RLock + atomic + rand |
| Allow (rejected) | 0 | RLock + atomic + rand |
| AllowOrError (success) | 0 | Same as Allow |
| AllowOrError (rejected) | 1 | errx.Error construction |

## File structure

```text
pkg/warmupx/
    warmupx.go       -- Warmer, Strategy, New(), Start/Stop/Allow, options
    errors.go        -- DomainWarmup, Code constants, error constructors
    warmupx_test.go  -- 55 tests, 97.6% coverage
    bench_test.go    -- 7 benchmarks
    README.md
```
