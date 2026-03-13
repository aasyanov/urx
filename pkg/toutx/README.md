# toutx

Thread-safe timeout execution wrapper for industrial Go services.

## Philosophy

**One job: enforce deadlines.** `toutx` wraps a function call with a
`context.WithTimeout` and converts deadline/cancellation errors into structured
`*errx.Error` values. It provides both a reusable `Timer` and a one-shot
`Execute` function. It does not retry, circuit-break, or log.

## Quick start

```go
// One-shot
resp, err := toutx.Execute(ctx, 5*time.Second, func(ctx context.Context) (*Response, error) {
    return client.Call(ctx, req)
})

// Reusable timer
t := toutx.New(toutx.WithTimeout(5*time.Second), toutx.WithOp("db.query"))
rows, err := toutx.Execute(ctx, 0, func(ctx context.Context) (*sql.Rows, error) {
    return db.QueryContext(ctx, sql)
}, toutx.WithTimer(t))
```

## API

| Function / Method | Description |
|---|---|
| `New(opts ...Option) *Timer` | Create a reusable timer (30 s default) |
| `Execute[T](ctx, timeout, fn, opts...) (T, error)` | Run fn with timeout; use WithTimer for reusable config |

### Options

| Option | Default | Description |
|---|---|---|
| `WithTimeout(d)` | `30s` | Maximum execution duration |
| `WithOp(op)` | `""` (falls back to `"toutx.Execute"`) | Logical operation name for errors |

## Behavior details

- **Timeout enforcement**: `Execute` creates a derived context with
  `context.WithTimeout`. The function runs in a goroutine; the caller blocks
  until `fn` completes or the deadline fires — whichever comes first.

- **Error mapping**: if the deadline fires, `Execute` returns
  `CodeDeadlineExceeded` with the operation name on `errx.Error.Op`. If the
  parent context is cancelled, `Execute` returns `CodeCancelled` wrapping
  `ctx.Err()`.

- **Goroutine leak safety**: if `fn` is still running when the timeout fires,
  the derived context is cancelled. `fn` must respect `ctx.Done()` to avoid
  leaking.

- **Timer vs Execute**: `Timer` pre-configures timeout and operation name.
  Package-level `Execute` accepts them inline.

## Error diagnostics

All errors are `*errx.Error` with `Domain = "TIMEOUT"`.

### Codes

| Code | When |
|---|---|
| `DEADLINE_EXCEEDED` | Function exceeded the configured timeout |
| `CANCELLED` | Parent context was cancelled |

### Example

```text
TIMEOUT.DEADLINE_EXCEEDED: timeout exceeded | op: db.query
TIMEOUT.CANCELLED: cancelled | op: db.query | cause: context canceled
```

## Thread safety

- `Timer` is immutable after `New` — safe for concurrent use
- `Execute` creates a new context per call — no shared state
- Package-level `Execute` is stateless — safe for concurrent use

## Tests

**17 tests, 100% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/toutx  coverage: 100% of statements
```

Coverage includes:
- Execute: success, function error, timeout, context cancellation
- Timer: success, timeout, cancellation, custom op name
- Options: WithTimeout, WithOp, defaults
- Error structure: domain, code, metadata fields

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz.
Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkExecute_Success      ~2584 ns/op     584 B/op     9 allocs/op
BenchmarkExecute_FuncError    ~2638 ns/op     608 B/op    10 allocs/op
BenchmarkTimer_Execute        ~2514 ns/op     560 B/op     8 allocs/op
```

### Analysis

**Execute (success):** ~2.6 us, 9 allocs. `context.WithTimeout` + goroutine spawn + channel send/receive. The goroutine and channel dominate the cost.

**Execute (function error):** ~2.6 us, 10 allocs. Same as success + error wrapping. The extra allocation is the error struct.

**Timer Execute:** ~2.5 us, 8 allocs. One fewer alloc than package-level `Execute` because options are pre-configured.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| Execute (success) | < 3 us, 9 allocs | Negligible vs real I/O |
| Execute (error) | < 3 us, 10 allocs | Negligible vs real I/O |
| Timer Execute | < 3 us, 8 allocs | Negligible vs real I/O |

For context: any real operation wrapped by `toutx` (HTTP call, DB query) takes 1-100 ms. The 2.5 us wrapper overhead is noise.

## What toutx does NOT do

| Concern | Owner |
|---------|-------|
| Retry on timeout | `retryx` |
| Circuit breaking | `circuitx` |
| Rate limiting | `ratex` |
| Logging | caller / `slog` |
| Dynamic timeout adjustment | caller |

## File structure

```text
pkg/toutx/
    toutx.go       -- Timer, New(), Execute() (both Timer and package-level)
    errors.go      -- DomainTimeout, Code constants, error constructors
    toutx_test.go  -- 17 tests, 100% coverage
    bench_test.go  -- 3 benchmarks
    README.md
```
