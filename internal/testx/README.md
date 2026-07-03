# testx — Deterministic Testing Toolkit for Resilience Patterns

Internal test infrastructure for the `urx` module. Go 1.24+. Single external dependency: `testify`.

```go
import "github.com/aasyanov/urx/internal/testx"
```

> [!IMPORTANT]
> **This is an `internal` package** — it is not importable by code outside the `urx` module. It provides deterministic failure simulation, latency injection, footprint verification, context factories, and concurrency stress helpers. Every `urx` package's test suite should use `testx` instead of hand-rolling these primitives.

## The Problem

Resilience packages (retryx, circuitx, bulkx, shedx, hedgex, ratex, toutx, poolx, warmupx, quotax, fallx, adaptx) share common testing needs:

1. **Deterministic failures** — "fail the first 3 calls, then succeed" for retry testing; "fail every 5th call" for circuit breaker thresholds; "SSFS" patterns for complex scenarios
2. **Latency injection** — "sleep 500ms per call" for timeout testing; "sleep then fail" for hedge testing; all respecting context cancellation
3. **Struct size regression** — catch accidental struct bloat across releases (footprint tests)
4. **Pre-configured contexts** — already-cancelled, already-expired, timed contexts — every test file needs these
5. **Concurrency stress** — "run 100 goroutines × 1000 iterations under -race" is repeated in every concurrent package

Without `testx`, every package re-implements:

```go
// repeated in every package's test file
sim := make([]bool, 10)
sim[2] = true  // manual failure schedule — fragile, unclear
// ...
ctx, cancel := context.WithCancel(context.Background())
cancel()  // pre-cancelled ctx — 3 lines for 1 concept
// ...
var wg sync.WaitGroup  // same hammer pattern in every _test.go
wg.Add(100)
for i := 0; i < 100; i++ { ... }
```

`testx` addresses **five testing requirements**:

1. **Failure simulation** — `Simulator` with 6 deterministic modes, custom error factories, counters
2. **Latency simulation** — `LatencySim` with context-aware delays and error injection
3. **Footprint verification** — `AssertFootprint`, `AssertSize`, generic `Sizeof[T]()` helpers
4. **Context factories** — `CancelledCtx`, `ExpiredCtx`, `TimedCtx`, `DeadlineCtx`
5. **Concurrency stress** — `Hammer`, `HammerNoError`, `HammerVoid`, `HammerIndexed`

## Architectural Position: What `testx` Actually Does

```text
✅ Deterministic failure simulation (Simulator, LatencySim)
✅ Test-only helpers (footprint, context, concurrency)
✅ Shared across all urx packages via internal/

❌ NOT a test framework (use testify for assertions)
❌ NOT a mock generator (write specific mocks per package)
❌ NOT a property-based testing engine (use go test -fuzz)
❌ NOT importable outside urx (internal package)
```

### Position in the urx Stack

```text
┌────────────────────────────────────────────────────────┐
│  urx packages (retryx, circuitx, bulkx, ...)           │
│  *_test.go files import internal/testx                 │
└────────────────────────┬───────────────────────────────┘
                         │
┌────────────────────────▼───────────────────────────────┐
│  internal/testx  (this package)                        │
│  Simulator · LatencySim · Hammer · AssertFootprint     │
│  CancelledCtx · TimedCtx · Sizeof[T]                   │
└────────────────────────┬───────────────────────────────┘
                         │
┌────────────────────────▼───────────────────────────────┐
│  testing · testify · unsafe                            │
└────────────────────────────────────────────────────────┘
```

## Architecture

```text
                              testx
    ┌──────────────┬──────────────┬──────────────┬──────────────┬──────────────┬──────────────┐
    │              │              │              │              │              │              │
 Simulator    LatencySim      Hammer       Footprint      Context       Eventually    Lifecycle
 (simulator)  (latency)      (hammer)     (footprint)    (context)     (eventually)  (lifecycle)
    │              │              │              │              │              │              │
 6 FailModes  delay+errFn    N×iters      AssertSize   CancelledCtx  Eventually    CloseIdemp
 pattern idx  ctx cancel     race safe    Sizeof[T]    ExpiredCtx    Never         OpAfterClose
 atomics      atomics        indexed      table-driven TimedCtx
 custom err                                            DeadlineCtx
    │
 Panics (panics.go)
 RequirePanicError
 AssertNotPanicError
```

## How It Works

### Simulator: Failure Schedule Engine

The `Simulator` maintains an atomic call counter and applies the configured `FailMode` on each `Call()`:

```text
Call() → calls.Add(1)
    │
    ├── FailNever   → always nil
    ├── FailAlways  → always error
    ├── FailPattern → "SSFS"[idx % len] → 'F'/'f' = error
    ├── FailAfterN  → callNum > N = error
    ├── FailUntilN  → callNum ≤ N = error
    └── FailEveryN  → callNum % N == 0 = error
```

Pattern mode uses a mutex-protected index that wraps around the pattern string. All other modes use lock-free atomic comparisons.

### LatencySim: Context-Aware Delay

```text
Call(ctx) → calls.Add(1)
    │
    ├── delay ≤ 0       → return errFn() or nil
    └── delay > 0       → select {
                              case <-time.After(delay): return errFn() or nil
                              case <-ctx.Done():        return ctx.Err()
                          }
```

### Hammer: Concurrency Stress

```text
Hammer(n=100, iters=1000, fn)
    │
    ├── WaitGroup.Add(100)
    ├── 100 goroutines × 1000 iterations
    │       └── fn() → if err != nil → mutex-protected append
    └── WaitGroup.Wait() → return []error
```

Variants: `HammerNoError` (assert empty), `HammerVoid` (no error capture), `HammerIndexed` (goroutine ID passed to fn).

## Quick Start

```go
package retryx

import (
    "testing"
    "github.com/aasyanov/urx/internal/testx"
    "github.com/stretchr/testify/require"
)

func TestDo_RetriesUntilSuccess(t *testing.T) {
    sim := testx.FailUntil(3)  // fail 3 times, then succeed

    var attempts int
    err := Do(ctx, func() error {
        attempts++
        return sim.Call()
    }, WithMaxAttempts(5))

    require.NoError(t, err)
    assert.Equal(t, 4, attempts)  // 3 failures + 1 success
}
```

## Usage Scenarios

### Testing Retry Logic

```go
func TestRetry_ExhaustsAttempts(t *testing.T) {
    sim := testx.AlwaysFail()

    err := retryx.Do(ctx, func(ctx context.Context, rc retryx.RetryController) error {
        return sim.Call()
    }, retryx.WithMaxAttempts(3))

    require.ErrorIs(t, err, retryx.ErrExhausted)
    assert.Equal(t, int64(3), sim.Calls())
}
```

### Testing Circuit Breaker Threshold

```go
func TestCircuit_OpensAfterFailures(t *testing.T) {
    sim := testx.AlwaysFail()
    cb := circuitx.New(circuitx.WithMaxFailures(5))

    for range 5 {
        circuitx.Execute(cb, ctx, func(_ context.Context, _ circuitx.CircuitController) (int, error) {
            return 0, sim.Call()
        })
    }

    _, err := circuitx.Execute(cb, ctx, func(_ context.Context, _ circuitx.CircuitController) (int, error) {
        return 0, nil
    })
    require.ErrorIs(t, err, circuitx.ErrOpen)
}
```

### Testing Timeout Behavior

```go
func TestTimeout_Expires(t *testing.T) {
    slow := testx.SlowCall(5 * time.Second)
    ctx, cancel := testx.TimedCtx(50 * time.Millisecond)
    defer cancel()

    err := toutx.Do(ctx, func(ctx context.Context) error {
        return slow.Call(ctx)
    })

    require.ErrorIs(t, err, context.DeadlineExceeded)
}
```

### Struct Footprint Regression

```go
func TestFootprint(t *testing.T) {
    testx.AssertFootprint(t, []testx.SizeEntry{
        {"Breaker", testx.Sizeof[Breaker](), 120},
        {"config", testx.Sizeof[config](), 64},
    })
}
```

### Concurrency Stress Test

```go
func TestLimiter_ConcurrentAccess(t *testing.T) {
    lim := NewLimiter(WithMaxConcurrent(10))
    defer lim.Close()

    testx.HammerNoError(t, 100, 1000, func() error {
        tok, err := lim.Acquire(context.Background())
        if err != nil {
            return err
        }
        tok.Release()
        return nil
    })
}
```

### Async State Transitions

```go
func TestCircuit_OpensAfterFailures(t *testing.T) {
    cb := circuitx.New(circuitx.WithMaxFailures(5))
    sim := testx.AlwaysFail()

    for range 5 {
        circuitx.Execute(cb, ctx, func(_ context.Context, _ circuitx.CircuitController) (int, error) {
            return 0, sim.Call()
        })
    }

    testx.Eventually(t, func() bool {
        return cb.State() == circuitx.Open
    }, 2*time.Second)
}
```

### Panic Recovery Assertions

```go
func TestExecute_PanickingHandler(t *testing.T) {
    cb := circuitx.New()
    _, err := circuitx.Execute(cb, ctx, func(_ context.Context, _ circuitx.CircuitController) (int, error) {
        panic("handler crashed")
    })
    pe := testx.RequirePanicError(t, err, "circuitx.Execute")
    assert.Equal(t, "handler crashed", pe.Value)
}
```

### Lifecycle Idempotency

```go
func TestBulkhead_DoubleClose(t *testing.T) {
    bh := bulkx.New(bulkx.WithMaxConcurrent(10))
    testx.AssertCloseIdempotent(t, bh)
    testx.AssertOpAfterClose(t, func() error {
        _, err := bulkx.Execute(bh, ctx, fn)
        return err
    }, bulkx.ErrClosed, "Execute")
}
```

## API


| Symbol                  | Signature                                                                                   | Description                         |
| ----------------------- | ------------------------------------------------------------------------------------------- | ----------------------------------- |
| **Simulator**           |                                                                                             |                                     |
| `NewSimulator`          | `func NewSimulator(opts ...SimOption) *Simulator`                                           | Create failure simulator            |
| `Simulator.Call`        | `func (s *Simulator) Call() error`                                                          | Execute one simulated call          |
| `Simulator.Calls`       | `func (s *Simulator) Calls() int64`                                                         | Total call count                    |
| `Simulator.Failures`    | `func (s *Simulator) Failures() int64`                                                      | Total failure count                 |
| `Simulator.Reset`       | `func (s *Simulator) Reset()`                                                               | Zero counters, rewind pattern       |
| `AlwaysFail`            | `func AlwaysFail() *Simulator`                                                              | Shorthand: fail every call          |
| `NeverFail`             | `func NeverFail() *Simulator`                                                               | Shorthand: never fail               |
| `FailAfter`             | `func FailAfter(n int) *Simulator`                                                          | Succeed n times, then fail          |
| `FailUntil`             | `func FailUntil(n int) *Simulator`                                                          | Fail n times, then succeed          |
| `FailEvery`             | `func FailEvery(n int) *Simulator`                                                          | Fail every nth call                 |
| `Pattern`               | `func Pattern(p string) *Simulator`                                                         | Follow S/F pattern                  |
| **LatencySim**          |                                                                                             |                                     |
| `NewLatencySim`         | `func NewLatencySim(opts ...LatencyOption) *LatencySim`                                     | Create latency simulator            |
| `LatencySim.Call`       | `func (l *LatencySim) Call(ctx context.Context) error`                                      | Sleep then return                   |
| `LatencySim.Calls`      | `func (l *LatencySim) Calls() int64`                                                        | Total call count                    |
| `SlowCall`              | `func SlowCall(d time.Duration) *LatencySim`                                                | Sleep d, return nil                 |
| `SlowThenFail`          | `func SlowThenFail(d time.Duration) *LatencySim`                                            | Sleep d, return error               |
| **Hammer**              |                                                                                             |                                     |
| `Hammer`                | `func Hammer(n, iters int, fn func() error) []error`                                        | n goroutines × iters calls          |
| `HammerNoError`         | `func HammerNoError(t *testing.T, n, iters int, fn func() error)`                           | Hammer + assert empty               |
| `HammerVoid`            | `func HammerVoid(n, iters int, fn func())`                                                  | Hammer without error capture        |
| `HammerIndexed`         | `func HammerIndexed(n, iters int, fn func(idx int) error) []error`                          | Hammer with goroutine index         |
| **Footprint**           |                                                                                             |                                     |
| `AssertFootprint`       | `func AssertFootprint(t *testing.T, entries []SizeEntry)`                                   | Verify struct sizes ≤ limits        |
| `AssertSize`            | `func AssertSize(t *testing.T, name string, got, want uintptr)`                             | Verify exact struct size            |
| `Sizeof[T]`             | `func Sizeof[T any]() uintptr`                                                              | Generic unsafe.Sizeof wrapper       |
| **Context**             |                                                                                             |                                     |
| `CancelledCtx`          | `func CancelledCtx() context.Context`                                                       | Already-cancelled context           |
| `ExpiredCtx`            | `func ExpiredCtx() context.Context`                                                         | Already-expired deadline context    |
| `TimedCtx`              | `func TimedCtx(d time.Duration) (context.Context, context.CancelFunc)`                      | Context with timeout                |
| `DeadlineCtx`           | `func DeadlineCtx(d time.Duration) (context.Context, context.CancelFunc)`                   | Context with deadline               |
| **Eventually**          |                                                                                             |                                     |
| `Eventually`            | `func Eventually(t *testing.T, cond func() bool, timeout time.Duration, msgAndArgs ...any)` | Poll until true or timeout          |
| `Never`                 | `func Never(t *testing.T, cond func() bool, duration time.Duration, msgAndArgs ...any)`     | Assert condition stays false        |
| **Panics**              |                                                                                             |                                     |
| `RequirePanicError`     | `func RequirePanicError(t *testing.T, err error, wantOp string) *panix.PanicError`          | Assert *PanicError with Op match    |
| `AssertNotPanicError`   | `func AssertNotPanicError(t *testing.T, err error)`                                         | Assert err is NOT a PanicError      |
| **Lifecycle**           |                                                                                             |                                     |
| `AssertCloseIdempotent` | `func AssertCloseIdempotent(t *testing.T, c Closer)`                                        | Verify double-close is safe         |
| `AssertOpAfterClose`    | `func AssertOpAfterClose(t *testing.T, op func() error, wantErr error, opName string)`      | Verify closed resource rejects work |


## Configuration (SimOption)


| Option               | Default               | Description           |
| -------------------- | --------------------- | --------------------- |
| `WithFailAlways()`   | —                     | Fail every call       |
| `WithFailPattern(p)` | —                     | S/F repeating pattern |
| `WithFailAfterN(n)`  | —                     | Succeed n, then fail  |
| `WithFailUntilN(n)`  | —                     | Fail n, then succeed  |
| `WithFailEveryN(n)`  | —                     | Fail every nth        |
| `WithMessage(msg)`   | `"simulated failure"` | Error message text    |
| `WithErrorFunc(fn)`  | wraps `ErrSimulated`  | Custom error factory  |


## Errors


| Error          | Condition                                                 |
| -------------- | --------------------------------------------------------- |
| `ErrSimulated` | Default sentinel wrapped by `Simulator.Call()` on failure |


## Pitfalls

> [!WARNING]
> **Pattern mode uses a mutex** — the pattern index is protected by `sync.Mutex`. Under extreme concurrency (>10k goroutines), this can become a bottleneck. For pure throughput stress tests, prefer `FailAlways` or `FailEveryN` which are lock-free.

> [!WARNING]
> `**LatencySim` uses `time.After`** — each call allocates a timer. For benchmarks, prefer `Simulator` (zero-delay) and test latency behavior separately.

> [!NOTE]
> `**ExpiredCtx` releases its cancel function** — the internal cancel func is invoked before returning, so no timer resource leaks. The returned context still reports `context.DeadlineExceeded` (the deadline-exceeded status is latched and not overwritten by the cancel).

## Safety and Concurrency

All types (`Simulator`, `LatencySim`) are safe for concurrent use. Counters use `sync/atomic`. Pattern index uses `sync.Mutex`. `Hammer` and `HammerVoid` synchronize via `sync.WaitGroup`. Error collection in `Hammer` uses a mutex-protected slice.

## Quality


| Metric                | Value          |
| --------------------- | -------------- |
| Test functions        | 56             |
| Table-driven subtests | 23             |
| Coverage              | 100.0%         |
| Race detector         | All pass       |
| External deps         | testify, panix |


## File Structure

```text
internal/testx/
├── simulator.go         # Simulator, FailMode, SimOption, convenience constructors
├── latency.go           # LatencySim, LatencyOption, SlowCall, SlowThenFail
├── footprint.go         # AssertFootprint, AssertSize, Sizeof[T]
├── context.go           # CancelledCtx, ExpiredCtx, TimedCtx, DeadlineCtx
├── hammer.go            # Hammer, HammerNoError, HammerVoid, HammerIndexed
├── eventually.go        # Eventually, Never — async state polling
├── panics.go            # RequirePanicError, AssertNotPanicError
├── lifecycle.go         # AssertCloseIdempotent, AssertOpAfterClose
├── errors.go            # ErrSimulated sentinel
├── simulator_test.go    # Simulator tests — all modes, edge cases, concurrency
├── latency_test.go      # LatencySim tests — delay, cancel, concurrent
├── footprint_test.go    # Footprint helper tests
├── context_test.go      # Context factory tests
├── hammer_test.go       # Hammer tests — errors, void, indexed
├── eventually_test.go   # Eventually/Never tests
├── panics_test.go       # PanicError assertion tests
├── lifecycle_test.go    # Close idempotency / op-after-close tests
└── README.md            # This file
```

## License

MIT — see [LICENSE](../../LICENSE) in the repository root.