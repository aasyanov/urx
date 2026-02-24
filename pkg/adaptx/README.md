# adaptx

Adaptive concurrency limiting for industrial Go services.

## Philosophy

**One job: self-tuning concurrency.** A [Limiter] adjusts its concurrency
limit based on latency and error feedback. Unlike static `bulkx`
(fixed concurrency), `adaptx` learns the right limit at runtime. No struct
tags, no framework coupling.

Three algorithms:
- **AIMD** (TCP-style) — increase on success, halve on failure
- **Vegas** — estimates queue build-up from RTT
- **Gradient** — reacts to latency trend direction

## Quick start

```go
l := adaptx.New(
    adaptx.WithAlgorithm(adaptx.AIMD),
    adaptx.WithInitialLimit(10),
)
defer l.Close()

rows, err := adaptx.Do(l, ctx, func(ctx context.Context, ac adaptx.AdaptController) (*sql.Rows, error) {
    return db.QueryContext(ctx, "SELECT ...")
})
```

## API

| Method | Description |
|---|---|
| `New(opts ...Option) *Limiter` | Create a limiter |
| `l.Acquire(ctx) (release, error)` | Block until a slot is available |
| `l.TryAcquire() (release, bool)` | Non-blocking acquire |
| `Do[T](l, ctx, fn) (T, error)` | Acquire + execute with panix.Safe + release; fn receives AdaptController |
| `l.Limit() int` | Current concurrency limit |
| `l.InFlight() int` | Number of in-flight requests |
| `l.Stats() Stats` | Snapshot with P50/P99 latency percentiles |
| `l.ResetStats()` | Zero all counters, reset adaptive state |
| `l.Close() error` | Graceful shutdown (waits up to 30s); see also `CloseWithTimeout` |
| `l.CloseWithTimeout(d) error` | Graceful shutdown with custom drain timeout |

### AdaptController

| Method | Description |
|---|---|
| `Limit() int` | Concurrency limit at admission time |
| `InFlight() int` | In-flight count at admission time |
| `Algorithm() Algorithm` | Active adaptation algorithm |
| `SkipSample()` | Don't feed this call's result into the adaptive algorithm |

### Release function

`Acquire` and `TryAcquire` return a release function:

```go
release(success bool, latency time.Duration)
```

- **Must** be called exactly once after the operation completes
- Double-call safe (atomic CAS guard)
- Feeds success/latency back to the adaptive algorithm

### Options

| Option | Default | Description |
|---|---|---|
| `WithAlgorithm(a)` | `AIMD` | Adaptation algorithm |
| `WithInitialLimit(n)` | `10` | Starting limit |
| `WithMinLimit(n)` | `1` | Floor (prevents starvation) |
| `WithMaxLimit(n)` | `1000` | Ceiling (prevents runaway) |
| `WithSmoothing(f)` | `0.2` | EMA smoothing for latency (0, 1] |
| `WithIncreaseRate(r)` | `1.0` | Additive increase per success (AIMD) |
| `WithDecreaseRatio(r)` | `0.5` | Multiplicative decrease factor (0, 1) |
| `WithTargetLatency(d)` | `100ms` | Target latency for Vegas |
| `WithTolerance(f)` | `0.1` | Acceptable latency deviation |
| `WithSampleWindow(d)` | `1s` | Window for percentile calculation |
| `WithWarmupSamples(n)` | `10` | Samples before adaptation starts |
| `WithMinLatencyDecay(f)` | `0.001` | RTT_min decay towards avg |
| `WithJitter(f)` | `0.1` | Jitter on limit increases |
| `WithOnLimitChange(fn)` | — | Async callback on limit change |

## Algorithms

### AIMD

Classic TCP congestion control:
- Success: `limit += IncreaseRate`
- Failure: `limit *= DecreaseRatio`

Best for: unknown backends, when latency is not informative.

### Vegas

Proactive, estimates queue build-up:
- `queue = limit * (RTT_actual - RTT_min) / RTT_min`
- Increases if queue is small, decreases if growing

Best for: when latency correlates with load.

### Gradient

Reacts to latency trend:
- Latency decreasing: increase by 2
- Latency stable: increase by 1
- Latency increasing: decrease proportionally

Best for: when fast reaction to changes is important.

## Behavior details

- **Semaphore-based**: uses a buffered channel for slot management
- **Ring buffer**: samples are stored in a pre-allocated ring buffer
  (100–10000 entries) to prevent memory growth
- **Warmup period**: during warmup, the limit stays at InitialLimit
  to gather baseline statistics
- **MinLatency decay**: RTT_min slowly drifts towards avg to prevent
  sticking on anomalous one-time low values
- **Jitter**: randomizes limit increases to prevent thundering herd
  when multiple limiters recover simultaneously
- **panix.Safe**: `Do()` wraps the function with panic recovery;
  panics produce structured `errx.Error` values
- **Double release protection**: release functions use atomic CAS
  to safely ignore duplicate calls
- **Graceful shutdown**: `Close()` waits up to 30 seconds for
  in-flight requests to complete. Use `CloseWithTimeout(d)` for a custom
  drain timeout (zero means no wait)

## Error diagnostics

All errors are `*errx.Error` with `Domain = "ADAPT"`.

### Codes

| Code | When |
|---|---|
| `LIMIT_EXCEEDED` | Concurrency limit exceeded (retryable) |
| `TIMEOUT` | Context deadline exceeded while waiting |
| `CANCELLED` | Context cancelled while waiting |
| `CLOSED` | Limiter has been closed |

## Thread safety

- `Do`, `Acquire`, `TryAcquire` — safe for concurrent use
- `Limit` uses `sync.Mutex`; `InFlight` uses lock-free atomic reads
- `Stats` — copies samples under short lock, sorts without lock
- `ResetStats` — holds lock briefly to reset state
- `Close` — atomic CAS prevents double-close

## Tests

**56 tests, 91.6% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/adaptx  coverage: 91.6% of statements
```

Coverage includes:
- All options: valid and invalid values, clamping
- Acquire/Release, double-release, context cancellation/timeout
- TryAcquire: success, full, closed
- Do: success, failure, panic recovery
- All three algorithms: AIMD increase/decrease, Vegas, Gradient
  increase/decrease, Vegas/Gradient edge cases
- Stats, empty samples, ResetStats
- Close, double-close
- Warmup period (no adjustment)
- Concurrent operations (100 goroutines)
- Error constructors
- AdaptController: Limit, InFlight, Algorithm, SkipSample

The uncovered 8.4% is defensive race-condition code in Acquire/TryAcquire
(closed-while-waiting, limit-decreased-while-waiting).

## Benchmarks

```text
BenchmarkDo             parallel    report allocs
BenchmarkTryAcquire     sequential  report allocs
```

## File structure

```text
pkg/adaptx/
    adaptx.go       -- Limiter, AdaptController, Algorithm, New(), Acquire/TryAcquire/Do, options
    errors.go       -- DomainAdapt, Code constants, error constructors
    adaptx_test.go  -- 56 tests + 2 benchmarks, 91.6% coverage
    README.md
```
