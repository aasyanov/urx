# retryx

Configurable retry engine with exponential backoff, jitter, and `errx`-aware
retryability for industrial Go services.

## Philosophy

**One job: retry operations.** `retryx` wraps a function and re-invokes it on
failure with exponential backoff and optional jitter. It inspects `errx` errors
for `Retryable()` and respects `RetryController.Abort()` for early exit. It does
not log, circuit-break, or rate-limit. Those are responsibilities of the caller,
`circuitx`, and `ratex`.

## Quick start

```go
resp, err := retryx.Do(ctx, func(rc retryx.RetryController) (*http.Response, error) {
    resp, err := http.Get(url)
    if err != nil {
        return nil, err
    }
    if resp.StatusCode == 429 {
        rc.Abort()
        return nil, fmt.Errorf("rate limited")
    }
    return resp, nil
},
    retryx.WithMaxAttempts(5),
    retryx.WithBackoff(200*time.Millisecond),
    retryx.WithJitter(true),
)
```

## API

| Function / Method | Description |
|---|---|
| `Do[T](ctx, fn, opts...) (T, error)` | Execute fn with retries |
| `RetryController.Number() int` | Current attempt number (1-based) |
| `RetryController.Abort()` | Stop retrying after this attempt |

### Options

| Option | Default | Description |
|---|---|---|
| `WithMaxAttempts(n)` | `3` | Maximum number of attempts |
| `WithBackoff(d)` | `100ms` | Initial backoff duration |
| `WithMaxBackoff(d)` | `10s` | Backoff cap |
| `WithJitter(on)` | `true` | Add random jitter to backoff |
| `WithRetryIf(fn)` | retry all | Custom retryability predicate |
| `WithOnRetry(fn)` | none | Callback invoked before each retry sleep |

## Behavior details

- **Exponential backoff**: sleep duration doubles each attempt:
  `backoff * 2^attempt`, capped at `MaxBackoff`.

- **Jitter**: when enabled, the sleep duration is multiplied by a random factor
  in `[0.5, 1.5)`. Prevents thundering herd on shared resources.

- **Retryability**: by default, all errors are retried. If the error is
  `*errx.Error` with `Retryable() == false` (i.e. `RetryClass == RetryNone`),
  the loop stops immediately. `WithRetryIf` overrides this logic.

- **Abort**: calling `RetryController.Abort()` terminates the retry loop after
  the current attempt. The error is wrapped as `CodeAborted`.

- **Context cancellation**: if `ctx` is cancelled between retries, `Do` returns
  `CodeCancelled` wrapping the context error.

- **Error wrapping**: after exhausting all attempts, `Do` returns
  `CodeExhausted` wrapping the last error.

- **Panic recovery**: each attempt is wrapped in `panix.Safe`. If fn panics,
  the panic is converted to an `*errx.Error` and treated as a failed attempt.

## Error diagnostics

All errors are `*errx.Error` with `Domain = "RETRY"`.

### Codes

| Code | When |
|---|---|
| `EXHAUSTED` | All attempts used; wraps last error |
| `CANCELLED` | Context cancelled during backoff |
| `ABORTED` | Caller called `RetryController.Abort()` |

### Example

```text
RETRY.EXHAUSTED: all retry attempts exhausted | cause: connection refused
RETRY.CANCELLED: retry cancelled by context | cause: context deadline exceeded
RETRY.ABORTED: retry aborted by caller | cause: rate limited
```

## Thread safety

- `Do` is stateless — safe for concurrent use
- `RetryController` is scoped to a single attempt — no shared state
- Options are applied during `Do` initialization — no mutation after start

## Tests

**42 tests, 100% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/retryx  coverage: 100% of statements
```

Coverage includes:
- Do: success first try, success on retry, all attempts fail
- Backoff: exponential growth, max cap, jitter range
- Retryability: plain error, errx retryable, errx non-retryable, custom predicate
- Abort: early exit, error wrapping
- Context: cancellation during sleep, pre-cancelled context
- OnRetry: callback invocation, attempt number
- Error structure: domain, code, cause chain, attempt metadata

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz.
Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkDo_SuccessFirst          ~227 ns/op      64 B/op     2 allocs/op
BenchmarkDo_SuccessSecond       ~307685 ns/op     352 B/op     8 allocs/op
BenchmarkDo_Exhausted           ~615223 ns/op    1144 B/op    16 allocs/op
BenchmarkDo_ErrxRetryable       ~646049 ns/op    1720 B/op    19 allocs/op
BenchmarkDo_ErrxNonRetryable      ~1045 ns/op     792 B/op     7 allocs/op
BenchmarkDo_Abort                  ~659 ns/op     608 B/op     6 allocs/op
BenchmarkDo_WithOnRetry         ~774817 ns/op    1144 B/op    16 allocs/op
BenchmarkDo_WithRetryIf         ~780124 ns/op    1120 B/op    13 allocs/op
BenchmarkBackoff_NoJitter           ~27 ns/op       0 B/op     0 allocs/op
BenchmarkBackoff_WithJitter         ~37 ns/op       0 B/op     0 allocs/op
BenchmarkIsRetryable_Plain         ~139 ns/op       8 B/op     1 allocs/op
BenchmarkIsRetryable_Errx          ~140 ns/op       8 B/op     1 allocs/op
BenchmarkDefaultConfig               ~4 ns/op       0 B/op     0 allocs/op
```

### Analysis

**Success first try:** ~228 ns, 2 allocs. Config initialization + single function call. Negligible overhead.

**Success second try:** ~308 ms. Dominated entirely by `time.Sleep` during the 100 ms backoff. The retry machinery adds < 1 us.

**Exhausted (3 attempts):** ~615 ms. Two backoff sleeps (100 ms + 200 ms). The error wrapping adds ~500 ns at the end.

**Non-retryable errx:** ~1 us, 7 allocs. Detects `Retryable() == false` immediately — no sleep.

**Abort:** ~659 ns, 6 allocs. First attempt + abort detection + error wrapping.

**Backoff (no jitter):** ~27 ns. Pure arithmetic (`base * 2^attempt`, capped). Free.

**Backoff (with jitter):** ~37 ns. Adds `rand.Float64()`. ~10 ns overhead for randomization.

## What retryx does NOT do

| Concern | Owner |
|---------|-------|
| Circuit breaking | `circuitx` |
| Rate limiting | `ratex` |
| Timeout enforcement | `toutx` |
| Logging | caller / `slog` |
| Metrics | Prometheus / caller |
| Per-error backoff | caller adjusts via `WithOnRetry` |

## File structure

```text
pkg/retryx/
    retryx.go      -- Do(), RetryController, backoff(), isRetryable()
    errors.go      -- DomainRetry, Code constants, error constructors
    retryx_test.go -- 42 tests, 100% coverage
    bench_test.go  -- 13 benchmarks
    README.md
```
