# testx

Deterministic failure simulation for testing resilience patterns in industrial Go services.

## Philosophy

**One job: simulate failures.** `testx` provides a deterministic `Simulator` that produces `*errx.Error` values on a configurable schedule. No randomness, no `time.Sleep`, no struct tags, no reflection. Use it to verify that `retryx`, `circuitx`, `bulkx`, and other resilience wrappers behave correctly under predictable failure conditions.

## Quick start

```go
// Fail first 2 calls, then succeed — test retryx recovery
sim := testx.FailUntil(2)
err := retryx.Do(ctx, func(rc retryx.RetryController) error {
    return sim.Call()
}, retryx.WithMaxAttempts(5))
// err == nil (succeeded on 3rd attempt)

// Pattern-based failure for precise control
sim := testx.Pattern("SSFS")
// call 1: success, call 2: success, call 3: fail, call 4: success, ...

// Custom errx.Error for testing non-retryable paths
sim := testx.New(
    testx.WithFailAlways(),
    testx.WithErrorFunc(func() *errx.Error {
        return errx.New(errx.DomainAuth, errx.CodeForbidden, "denied",
            errx.WithRetry(errx.RetryNone),
        )
    }),
)
```

## API

| Function / Method | Description |
|---|---|
| `New(opts ...Option) *Simulator` | Create a simulator (default: never fail) |
| `s.Call() error` | Execute one simulated call |
| `s.Stats() Stats` | Snapshot of calls/failures counters |
| `s.Reset()` | Zero counters and rewind pattern |

### Options

| Option | Default | Description |
|---|---|---|
| `WithFailAlways()` | — | Fail on every call |
| `WithFailPattern(p)` | — | Follow repeating pattern (S=success, F=fail) |
| `WithFailAfterN(n)` | — | Succeed n times, then fail forever |
| `WithFailUntilN(n)` | — | Fail n times, then succeed forever |
| `WithFailEveryN(n)` | — | Fail every nth call |
| `WithMessage(msg)` | `"simulated failure"` | Error message for failures |
| `WithErrorFunc(fn)` | — | Custom error factory for full control |

### Convenience constructors

| Function | Equivalent |
|---|---|
| `AlwaysFail()` | `New(WithFailAlways())` |
| `NeverFail()` | `New()` |
| `FailAfter(n)` | `New(WithFailAfterN(n))` |
| `FailUntil(n)` | `New(WithFailUntilN(n))` |
| `FailEvery(n)` | `New(WithFailEveryN(n))` |
| `Pattern(p)` | `New(WithFailPattern(p))` |

## Behavior details

- **Deterministic**: no randomness. Every failure schedule is fully predictable and reproducible.

- **`errx` integration**: all errors are `*errx.Error` with `Domain = "TEST"`, `Code = "SIMULATED"`, and `RetryClass = RetrySafe` by default. Use `WithErrorFunc` to produce custom domains, codes, and retryability.

- **`WithErrorFunc` nil safety**: if the custom error factory returns nil, the simulator falls back to the default `TEST.SIMULATED` error. A failure is never silently swallowed.

- **Pattern wrapping**: patterns repeat cyclically. `"SF"` produces success, fail, success, fail, ... indefinitely. Case-insensitive.

- **No latency simulation**: `testx` does not `time.Sleep`. Tests run at full speed. For latency testing, use `toutx` or `context.WithTimeout`.

- **No panic simulation**: `testx` does not panic. For panic testing, use `panix.Safe` directly with a function that panics.

## Error diagnostics

All errors are `*errx.Error` with `Domain = "TEST"`.

### Codes

| Code | When |
|---|---|
| `SIMULATED` | Simulator produced a failure |

### Example

```text
TEST.SIMULATED: simulated failure
TEST.SIMULATED: db timeout
```

## Thread safety

- `Call` uses `atomic.Int64` for counters and `sync.Mutex` for pattern index — safe for concurrent use
- `Stats` reads atomics — lock-free
- `Reset` zeroes atomics and resets pattern under lock — safe for concurrent use
- `Simulator` is immutable after `New` except for counters — fully concurrent

## Tests

**29 tests, 98.6% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/testx  coverage: 98.6% of statements
```

Coverage includes:
- FailNever: 100 calls, all succeed
- FailAlways: error structure, domain, code, retryability
- FailPattern: SSFS cycle, empty pattern, all-fail, case insensitivity
- FailAfterN: transition from success to failure
- FailUntilN: transition from failure to success
- FailEveryN: periodic failure, n=0 edge case
- WithMessage: custom and empty message
- WithErrorFunc: custom domain/retryability, nil return fallback
- Stats/Reset: counter accuracy, pattern rewind
- Concurrent safety: 100 goroutines with failure count verification

The uncovered 1.4% is the `default` branch in `shouldFail` (unreachable for valid `FailMode` values).

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz. Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkCall_NeverFail       ~15 ns/op       0 B/op     0 allocs/op
BenchmarkCall_AlwaysFail     ~203 ns/op     192 B/op     1 allocs/op
BenchmarkCall_Pattern         ~93 ns/op      48 B/op     0 allocs/op
BenchmarkCall_FailEveryN     ~206 ns/op      38 B/op     0 allocs/op
BenchmarkCall_WithErrorFunc  ~593 ns/op     192 B/op     1 allocs/op
```

### Analysis

**NeverFail:** ~15 ns, 0 allocs. Atomic increment + switch. Free.

**AlwaysFail:** ~203 ns, 1 alloc. Atomic increment + `errx.New`. The single allocation is the `errx.Error` struct.

**Pattern:** ~93 ns, 0 allocs amortized. Mutex lock for pattern index + atomic increment. The 48 B/op is amortized from failure-path allocations.

**FailEveryN:** ~206 ns amortized. Similar to pattern but with modulo check.

**WithErrorFunc:** ~593 ns, 1 alloc. Custom error factory call adds overhead.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| Success path | < 20 ns, 0 allocs | Free |
| Failure path | < 250 ns, 1 alloc | errx.Error construction |
| Pattern | < 100 ns, 0 allocs | Mutex + atomic |
| Custom error | < 600 ns, 1 alloc | Factory call overhead |

## What testx does NOT do

| Concern | Owner |
|---------|-------|
| Latency simulation | `toutx` / `time.Sleep` |
| Panic simulation | `panix` / caller |
| Random failures | caller (`math/rand/v2`) |
| HTTP mocking | `net/http/httptest` |
| Context cancellation | `context.WithCancel` |
| Virtual time | third-party libraries |

## File structure

```text
pkg/testx/
    testx.go       -- Simulator, FailMode, New(), Call(), convenience constructors
    errors.go      -- DomainTest, Code constants, error constructors
    testx_test.go  -- 29 tests, 98.6% coverage
    bench_test.go  -- 5 benchmarks
    README.md
```
