# panix

Panic recovery primitives that convert panics into structured `*errx.Error` values with trace propagation from `ctxx`.

## Philosophy

**One job: recover panics.** `panix` provides `Safe` (run a closure, catch panics), `SafeGo` (same but in a new goroutine), and `Wrap` (returns a panic-safe version of a function). Every recovered panic is converted into `*errx.Error` via `errx.NewPanicError`, enriched with `TraceID`/`SpanID` from context. It does not log, retry, or report. Those are responsibilities of the caller.

## Quick start

```go
// Inline recovery
user, err := panix.Safe(ctx, "UserRepo.Find", func(ctx context.Context) (*User, error) {
    return repo.Find(ctx, id)
})
// err is *errx.Error with Domain="INTERNAL", Code="PANIC", TraceID/SpanID attached

// Background goroutine with error callback
panix.SafeGo(ctx, "worker", func(ctx context.Context) {
    process(ctx)
}, panix.WithOnError(func(ctx context.Context, err error) {
    slog.Error("background panic", logx.Err(err))
}))

// Wrap a function for reuse
safeFn := panix.Wrap(riskyHandler, "RiskyHandler")
user, err = safeFn(ctx)
```

## API

| Function | Description |
|---|---|
| `Safe[T](ctx, op, fn) (T, error)` | Run fn, recover panics as `*errx.Error` with trace IDs |
| `SafeGo(ctx, op, fn, opts...)` | Run fn in a goroutine with panic recovery |
| `Wrap[T](fn, op) func(ctx) (T, error)` | Return a panic-safe wrapper of fn |

### Options (SafeGo)

| Option | Description |
|---|---|
| `WithOnError(func(ctx, error))` | Called when fn panics; receives ctx and `*errx.Error` |

## Behavior details

- **Panic conversion**: when a panic is recovered, `panix` calls `errx.NewPanicError(op, recovered)` which produces an `*errx.Error` with `Domain = "INTERNAL"`, `Code = "PANIC"`, `Severity = Critical`, `Category = System`, `IsPanic = true`, and the original panic value in the message. If `errx.EnableStackTrace(true)` was called, a full call stack is captured inside the error.

- **Trace propagation**: after recovery, `Safe` extracts `TraceID` and `SpanID` from ctx via `ctxx.MustTraceFromContext` and attaches them to the panic error. If ctx has no trace, `MustTraceFromContext` generates new IDs, ensuring every panic error is traceable.

- **Error passthrough**: if fn returns a non-nil error without panicking, `Safe` and `Wrap` return it unmodified — no wrapping, no allocation.

- **SafeGo lifecycle**: launches a goroutine. The fn signature is `func(ctx context.Context)` (void). Internally, `SafeGo` wraps fn in `Safe`. If fn panics, the `WithOnError` callback (if provided) is invoked with the original context and the `*errx.Error`. Without a callback, the panic is recovered silently (fire-and-forget).

- **Nil context**: if ctx is nil, `Safe` substitutes `context.Background()` before executing fn and before trace extraction.

## Thread safety

- `Safe` and `Wrap` are stateless — safe for concurrent use
- `SafeGo` launches a goroutine — safe for concurrent calls
- No shared mutable state

## Tests

**20 tests, 100.0% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/panix  coverage: 100.0% of statements
```

Coverage includes:
- Safe: no panic, fn returns error, string panic, error panic, int panic
- Safe: trace propagation (with trace, without trace — auto-generated)
- Safe: nil ctx (no panic, panic)
- SafeGo: no panic, panic with callback, panic without callback (fire-and-forget)
- SafeGo: fn success with callback (verifies callback NOT called)
- SafeGo: nil ctx
- Wrap: no panic, panic recovery, error passthrough
- WithOnError: option sets callback
- Error structure: Domain, Code, Severity, IsPanic, Op, TraceID, SpanID

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz. Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkSafe_NoPanic           ~15 ns/op       0 B/op     0 allocs/op
BenchmarkSafe_Panic            ~934 ns/op     224 B/op     4 allocs/op
BenchmarkSafe_NilCtx            ~17 ns/op       0 B/op     0 allocs/op
BenchmarkSafeGo_NoPanic       ~1059 ns/op      72 B/op     2 allocs/op
BenchmarkSafeGo_WithOnError   ~1120 ns/op      72 B/op     2 allocs/op
BenchmarkWrap_NoPanic           ~25 ns/op       0 B/op     0 allocs/op
BenchmarkWrap_Panic           ~1114 ns/op     224 B/op     4 allocs/op
```

### Analysis

**Safe (no panic):** ~15 ns, 0 allocs. `defer recover()` + function call. The `defer` cost is ~5 ns. Effectively free.

**Safe (panic):** ~934 ns, 4 allocs. `recover()` + `errx.NewPanicError` + trace extraction + string conversion. The 4 allocations are: error struct, message string, cause wrapping, and stack frame (if enabled). Acceptable because panics are exceptional.

**SafeGo (no panic):** ~1.1 us, 2 allocs. Goroutine spawn + closure. The goroutine allocation dominates.

**Wrap (no panic):** ~25 ns, 0 allocs. Closure call + `defer recover()`. Marginally more than `Safe` due to the extra closure indirection.

**Wrap (panic):** ~1.1 us, 4 allocs. Same as `Safe` panic path + closure overhead.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| Safe (no panic) | < 16 ns, 0 allocs | Free |
| Safe (panic) | < 1 us, 4 allocs | Exceptional path |
| SafeGo | < 1.2 us, 2 allocs | Goroutine cost |
| Wrap (no panic) | < 26 ns, 0 allocs | Free |

The no-panic path adds < 16 ns overhead. This is why every `Execute` across the ecosystem uses `panix.Safe` without concern.

## What panix does NOT do

| Concern | Owner |
|---------|-------|
| Logging panics | caller / `logx` |
| Alerting | external monitoring |
| Retry after panic | `retryx` |
| Process exit | caller |
| Metrics / counting panics | caller / Prometheus |

## File structure

```text
pkg/panix/
    panix.go          -- Safe(), SafeGo(), Wrap(), WithOnError()
    panix_test.go     -- 20 tests, 100% coverage
    bench_test.go     -- 7 benchmarks
    example_test.go   -- runnable examples
    README.md
```
