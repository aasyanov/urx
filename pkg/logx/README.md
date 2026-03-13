# logx

Structured logging bridge between `log/slog`, `ctxx` trace propagation, and `errx` structured errors.

## Philosophy

**One job: enrich slog records.** `logx` provides a `slog.Handler` wrapper that automatically injects `trace_id` and `span_id` from `ctxx` context values into every log record. `Err()` converts `*errx.Error` into a structured `slog.Attr` group. It does not define log levels, configure outputs, or manage rotation. Those are responsibilities of `slog` and the caller.

## Quick start

```go
inner := slog.NewJSONHandler(os.Stdout, nil)
handler := logx.NewHandler(inner)
logger := slog.New(handler)

ctx := ctxx.WithTrace(ctx)
ctx = logx.WithLogger(ctx, logger)

log := logx.FromContext(ctx)
log.InfoContext(ctx, "request processed",
    logx.Err(someErr),
)
// Output includes trace_id, span_id, and structured error fields
```

## API

| Function / Method | Description |
|---|---|
| `NewHandler(inner) *Handler` | Wrap a `slog.Handler` with trace injection |
| `WithLogger(ctx, logger) context.Context` | Store a `*slog.Logger` in context. Nil ctx → `context.Background()` |
| `FromContext(ctx) *slog.Logger` | Retrieve logger from context (or `slog.Default`) |
| `Err(err) slog.Attr` | Convert error to `slog.Attr` (`errx.Error` → group) |
| `(h *Handler) Enabled(ctx, level) bool` | Delegates to inner handler |
| `(h *Handler) Handle(ctx, record) error` | Injects trace_id/span_id, delegates |
| `(h *Handler) WithAttrs(attrs) slog.Handler` | Returns Handler with pre-applied attrs |
| `(h *Handler) WithGroup(name) slog.Handler` | Returns Handler with group name |

## Behavior details

- **Trace injection**: `Handle` extracts `trace_id` and `span_id` from the context (via `ctxx.TraceFromContext`) and adds them as `slog.Attr` fields to the log record. If no trace is present, the fields are omitted.

- **errx integration**: `Err()` checks whether the error is `*errx.Error` (via `errx.As`). If so, it returns a `slog.Group("error", ...)` with these fields: `domain`, `code`, `message`, `severity`, and conditionally `op`, `trace_id`, `span_id`, `panic`, `meta`, `cause`. Plain errors produce a simple `slog.String("error", err.Error())`. Nil returns an empty attr.

- **Context logger**: `WithLogger` stores a `*slog.Logger` in the context. `FromContext` retrieves it, falling back to `slog.Default()`. Nil context also returns `slog.Default()`.

- **Duplicate trace_id**: `Handler` injects `trace_id` from context, and `Err` may include `trace_id` from `errx.Error`. These can differ when an error originated in another service. `slog` outputs both, which is correct — they carry different semantics (current request vs error origin).

## Thread safety

- `Handler` is stateless (wraps inner handler) — safe for concurrent use
- `WithLogger` / `FromContext` use `context.Value` — immutable, safe
- `Err` is a pure function — safe for concurrent use

## Tests

**14 tests, 100.0% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/logx  coverage: 100.0% of statements
```

Coverage includes:
- Handler: trace injection, no-trace passthrough, WithAttrs, WithGroup
- WithLogger: nil context normalization
- FromContext: with logger, without logger (default), nil context
- Err: errx.Error (all fields), errx with trace_id/span_id, errx panic error, errx with cause, plain error, nil error
- Enabled: level delegation

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz. Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkHandler_WithTrace    ~1068 ns/op       0 B/op     0 allocs/op
BenchmarkErr_ErrxError         ~957 ns/op     488 B/op     8 allocs/op
BenchmarkErr_PlainError        ~117 ns/op       8 B/op     1 allocs/op
BenchmarkFromContext             ~9 ns/op       0 B/op     0 allocs/op
```

### Analysis

**Handler with trace:** ~1.1 us, 0 allocs. The handler extracts trace from context and adds two `slog.Attr` fields. Zero allocations because slog uses pre-allocated buffers.

**Err (errx.Error):** ~957 ns, 8 allocs. Decomposing the structured error into a `slog.Group` with up to 10 fields. The allocations are `slog.Attr` slices and string conversions.

**Err (plain error):** ~117 ns, 1 alloc. Simple `err.Error()` call → `slog.String`.

**FromContext:** ~9 ns, 0 allocs. Single `context.Value` lookup. Effectively free.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| Handler (trace) | < 1.2 us, 0 allocs | Invisible in I/O-bound handlers |
| Err (errx) | < 1 us, 8 allocs | Acceptable for error path |
| Err (plain) | < 120 ns, 1 alloc | Hot-path friendly |
| FromContext | < 10 ns, 0 allocs | Free |

## What logx does NOT do

| Concern | Owner |
|---------|-------|
| Log level configuration | `slog` / caller |
| Output formatting | `slog.Handler` (JSON, text) |
| Log rotation | `lumberjack` / caller |
| Alerting | external systems |
| Metrics | `prometheus` / caller |

## File structure

```text
pkg/logx/
    logx.go       -- Handler, NewHandler(), WithLogger(), FromContext(), Err()
    logx_test.go  -- 14 tests, 100.0% coverage
    bench_test.go -- 4 benchmarks
    README.md
```
