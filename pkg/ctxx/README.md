# ctxx

Lightweight trace and span propagation via `context.Context`.

## Philosophy

**One job: propagate trace IDs.** `ctxx` stores W3C-compatible identifiers in `context.Context`: `trace_id` (32 hex), `span_id` (16 hex), and `trace_flags` (2 hex). `WithTrace` creates or reuses a trace; `WithSpan` forks a new span. `TraceFromContext` extracts IDs. It does not export spans or include an OTEL SDK.

## Quick start

```go
ctx := ctxx.WithTrace(ctx)

// Later, in a handler or middleware:
traceID, spanID := ctxx.TraceFromContext(ctx)

// Fork a new span for a sub-operation:
ctx = ctxx.WithSpan(ctx)

// Must-have variant (generates if missing):
traceID, spanID, ctx = ctxx.MustTraceFromContext(ctx)
```

## API

| Function | Description |
|---|---|
| `WithTrace(ctx) context.Context` | Set trace and span; preserves existing IDs |
| `WithSpan(ctx) context.Context` | New span; creates trace if missing |
| `TraceFromContext(ctx) (traceID, spanID string)` | Extract trace; empty if missing |
| `TraceFlagsFromContext(ctx) string` | Extract W3C trace-flags |
| `MustTraceFromContext(ctx) (traceID, spanID string, newCtx context.Context)` | Extract or generate trace |
| `ParseTraceparent(v) (traceID, spanID, traceFlags string, ok bool)` | Parse W3C `traceparent` |
| `FormatTraceparent(traceID, spanID, traceFlags) string` | Build W3C `traceparent` |
| `WithTraceparent(ctx, v) context.Context` | Apply `traceparent` or generate a new trace |
| `InjectTraceparent(ctx, h)` | Write `traceparent` to `http.Header` |
| `ExtractTraceparent(ctx, h) context.Context` | Read `traceparent` from `http.Header` |
| `HeaderTraceparent` (const) | `"traceparent"` header name |
| `TraceContext` (struct) | Carries `TraceID`, `SpanID`, `TraceFlags` |

## Behavior details

- **ID generation**: if the context has no trace, `WithTrace` generates cryptographically random W3C-shaped IDs: trace ID is 16 random bytes (32 lowercase hex chars), span ID is 8 random bytes (16 lowercase hex chars), trace flags default to `01` (sampled).

- **Span forking**: `WithSpan` replaces only the `spanID`, preserving the existing `traceID`. If no trace exists, it creates both.

- **W3C propagation**: `InjectTraceparent` / `ExtractTraceparent` support `traceparent` without OTEL dependency, useful for HTTP boundaries.

- **Context key isolation**: keys are of an unexported `ctxKey` type, preventing collisions with other packages that use `context.WithValue`.

- **Zero allocations on read**: `TraceFromContext` performs a single `context.Value` lookup (primary path) — no heap allocation.

## Thread safety

- All functions create new `context.Context` values — immutable, safe for concurrent use
- `TraceFromContext` / `MustTraceFromContext` are pure reads — safe for concurrent use
- No shared mutable state

## Tests

**38 tests, 99.1% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/ctxx  coverage: 99.1% of statements
```

Coverage includes:
- WithTrace: new trace, existing trace, empty IDs (auto-generate), nil context
- WithSpan: with existing trace, without trace, nil context, multiple spans
- TraceFromContext: present, missing, nil context, empty context, wrong value type
- MustTraceFromContext: present (reuse), missing (generate), nil context, partial trace
- ID uniqueness: generated IDs are distinct
- traceparent: parse/format roundtrip, invalid inputs, inject/extract HTTP headers
- TraceFlagsFromContext: nil, empty
- InjectTraceparent: nil header, no trace
- ExtractTraceparent: nil header
- Legacy fallback: old-style separate context keys, invalid legacy IDs
- TraceContext normalization: uppercase hex, invalid flags

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz. Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkWithTrace_New                   ~1024 ns/op     256 B/op     8 allocs/op
BenchmarkWithTrace_Existing                ~33 ns/op       0 B/op     0 allocs/op
BenchmarkWithSpan                         ~570 ns/op     128 B/op     4 allocs/op
BenchmarkWithSpan_NoTrace                ~1085 ns/op     256 B/op     8 allocs/op
BenchmarkTraceFromContext                  ~29 ns/op       0 B/op     0 allocs/op
BenchmarkTraceFromContext_Nil               ~3 ns/op       0 B/op     0 allocs/op
BenchmarkTraceFromContext_Empty             ~9 ns/op       0 B/op     0 allocs/op
BenchmarkMustTraceFromContext_Existing     ~33 ns/op       0 B/op     0 allocs/op
BenchmarkWithTrace_Nil                    ~934 ns/op     216 B/op     8 allocs/op
BenchmarkMustTraceFromContext_Generate   ~1272 ns/op     256 B/op     8 allocs/op
```

### Analysis

**WithTrace (new):** ~1.0 us. Dominated by random ID generation (`crypto/rand`) and context wrapping. One-time cost per request.

**WithTrace (existing):** ~33 ns, 0 allocs. Single `context.Value` lookup + `context.WithValue`. No generation needed.

**TraceFromContext:** ~29 ns, 0 allocs. Single `context.Value` lookup. Hot-path friendly.

**TraceFromContext (nil):** ~3 ns. Early nil check. Free.

**MustTraceFromContext (existing):** ~33 ns. Same as `TraceFromContext` + nil check.

**MustTraceFromContext (generate):** ~1.3 us. Falls back to `WithTrace` — same cost as new trace creation.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| New trace | < 1.1 us, 8 allocs | One-time per request |
| Reuse trace | < 35 ns, 0 allocs | Hot-path friendly |
| WithSpan | < 600 ns, 4 allocs | One per sub-operation |
| Read trace | < 30 ns, 0 allocs | Free |

## What ctxx does NOT do

| Concern | Owner |
|---------|-------|
| Span export (Jaeger, Zipkin) | OpenTelemetry SDK |
| Full tracing pipeline (sampler/exporter/spans) | OpenTelemetry SDK |
| Log injection | `logx.Handler` |
| Metrics correlation | Prometheus / caller |
| `tracestate` / baggage management | caller |

## File structure

```text
pkg/ctxx/
    ctxx.go          -- WithTrace(), WithSpan(), TraceFromContext(), MustTraceFromContext()
    ctxx_test.go     -- 37 tests, 99.1% coverage
    bench_test.go    -- 10 benchmarks
    example_test.go  -- runnable examples
    README.md
```
