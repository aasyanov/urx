# errx

Structured error type with domain, code, severity, retry classification,
metadata, slog integration, and JSON serialization.

Part of the **urx** infrastructure ecosystem alongside `bulkx`, `busx`, `circuitx`, `clix`, `ctxx`, `dicx`, `healthx`, `i18n`, `logx`, `lrux`, `panix`, `poolx`, `ratex`, `retryx`, `shedx`, `signalx`, `syncx`, `toutx`, and `validx`.

## Philosophy

**One job: structured errors.** `errx` defines a rich `Error` type that every
package in the ecosystem uses. Each error carries `Domain`, `Code`, `Message`,
`Severity`, `Category`, `RetryClass`, `Meta`, `Cause`, `TraceID`, `SpanID`,
and optional stack traces. Errors are immutable after construction, implement
`slog.LogValuer` and `json.Marshaler`, and integrate with `errors.As`.

## Quick start

```go
// Create
err := errx.New("DB", "CONN_FAILED", "connection refused",
    errx.WithSeverity(errx.SeverityCritical),
    errx.WithRetry(errx.RetrySafe),
    errx.WithMeta("host", "db.prod", "port", "5432"),
)

// Wrap
wrapped := errx.Wrap(originalErr, "DB", "QUERY_FAILED", "select failed")

// Inspect
var ee *errx.Error
if errors.As(err, &ee) {
    fmt.Println(ee.Domain, ee.Code, ee.Retryable())
}

// slog integration
slog.Error("operation failed", logx.Err(err))

// JSON serialization
data, _ := json.Marshal(err)
```

## API

| Function / Method | Description |
|---|---|
| `New(domain, code, msg, opts...) *Error` | Create a new structured error |
| `Wrap(err, domain, code, msg, opts...) *Error` | Wrap an existing error |
| `NewPanicError(op, recovered) *Error` | Convert a recovered panic value |
| `As(err) (*Error, bool)` | Extract `*Error` from chain (shortcut for `errors.As`) |
| `EnableStackTrace(on)` | Enable/disable stack capture globally |
| `NewMulti(errs...) *MultiError` | Create a multi-error from multiple errors |

### Error fields

| Field | Type | Description |
|---|---|---|
| `Domain` | `string` | Error domain (e.g. `"DB"`, `"CLI"`) |
| `Code` | `string` | Error code (e.g. `"CONN_FAILED"`) |
| `Message` | `string` | Human-readable message |
| `Cause` | `error` | Wrapped cause |
| `Severity` | `Severity` | `info`, `warn`, `error`, `critical` |
| `Category` | `Category` | `business`, `system`, `security` |
| `Retry` | `RetryClass` | `none`, `safe`, `unsafe` |
| `Meta` | `map[string]any` | Arbitrary key-value metadata |
| `TraceID` | `string` | Trace identifier |
| `SpanID` | `string` | Span identifier |
| `Op` | `string` | Logical operation name |
| `IsPanic()` | `bool` | Whether error originated from a panic |

### Options

| Option | Description |
|---|---|
| `WithOp(op)` | Set operation name |
| `WithSeverity(s)` | Set severity level |
| `WithCategory(c)` | Set error category |
| `WithRetry(r)` | Set retry classification |
| `WithTrace(traceID, spanID)` | Set trace context |
| `WithMeta(k, v, ...)` | Set key-value metadata pairs |
| `WithMetaMap(m)` | Set metadata from map |

### MultiError

| Function / Method | Description |
|---|---|
| `NewMulti(errs...) *MultiError` | Create from multiple errors (nils filtered) |
| `me.Add(err)` | Append an error |
| `me.Len() int` | Number of contained errors |
| `me.Err() error` | Returns self if non-empty, nil otherwise |
| `me.Unwrap() []error` | Contained errors |
| `me.Severity() Severity` | Highest severity among contained errors |
| `me.Retryable() bool` | Whether any contained error permits retry |
| `me.IsPanic() bool` | Whether any contained error originated from a panic |

## Behavior details

- **Immutable after construction**: all `With*` options are applied during
  `New`/`Wrap`. The resulting `*Error` is never mutated.

- **Error chain**: `Wrap` sets `Cause` and participates in `errors.Is` /
  `errors.As` via `Unwrap()`.

- **slog integration**: `LogValue()` returns a `slog.GroupValue` with all
  fields. `SlogLevel()` maps `Severity` to `slog.Level`.

- **JSON**: `MarshalJSON()` produces a flat JSON object with all fields
  including cause chain.

- **Stack traces**: disabled by default. `EnableStackTrace(true)` captures
  call stacks on every `New`/`Wrap`. `StackTrace()` formats the stack.

- **MultiError**: aggregates multiple errors. Computes overall `Severity`,
  `Retryable`, and `IsPanic` from contained errors.

## Thread safety

- `*Error` is immutable after construction — safe for concurrent use
- `MultiError.Add` is NOT concurrent-safe (aggregate before sharing)
- `EnableStackTrace` sets a global `atomic.Bool` — safe for concurrent use
- `As` is a pure function — safe for concurrent use

## Tests

**83 tests, 100% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/errx  coverage: 100% of statements
```

Coverage includes:
- New: minimal, all options, with stack trace
- Wrap: basic, nil cause, chained wraps
- As: found, not found, deep chain
- Error.String: with/without cause
- MarshalJSON: minimal, full, with cause
- LogValue: minimal, full, severity mapping
- MultiError: Add, Len, Err, Error, Unwrap, severity/retry aggregation
- NewPanicError: string value, error value
- WithMeta / WithMetaMap: merge behavior
- StackTrace: capture and format
- Enums: Severity, Category, RetryClass String/MarshalText

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz.
Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkNew_Minimal                 ~174 ns/op     192 B/op      1 allocs/op
BenchmarkNew_AllOptions             ~1012 ns/op     744 B/op     10 allocs/op
BenchmarkWrap                        ~238 ns/op     216 B/op      2 allocs/op
BenchmarkWrap_Nil                      ~7 ns/op       0 B/op      0 allocs/op
BenchmarkNewPanicError_String        ~667 ns/op     248 B/op      4 allocs/op
BenchmarkNewPanicError_Error         ~469 ns/op     208 B/op      2 allocs/op
BenchmarkNew_WithStack              ~1178 ns/op     704 B/op      2 allocs/op
BenchmarkError_String_NoCause        ~121 ns/op      32 B/op      1 allocs/op
BenchmarkError_String_WithCause      ~219 ns/op      80 B/op      2 allocs/op
BenchmarkMarshalJSON_Minimal        ~4885 ns/op     568 B/op      7 allocs/op
BenchmarkMarshalJSON_Full           ~6306 ns/op     808 B/op     10 allocs/op
BenchmarkLogValue_Minimal            ~432 ns/op     480 B/op      2 allocs/op
BenchmarkLogValue_Full               ~951 ns/op    1184 B/op      3 allocs/op
BenchmarkAs_Found                    ~147 ns/op       8 B/op      1 allocs/op
BenchmarkAs_NotFound                 ~132 ns/op       8 B/op      1 allocs/op
BenchmarkAs_DeepChain                ~132 ns/op       8 B/op      1 allocs/op
BenchmarkStackTrace_Format          ~2727 ns/op     864 B/op     16 allocs/op
BenchmarkNewMulti_3                  ~568 ns/op     104 B/op      5 allocs/op
BenchmarkNewMulti_WithNils           ~213 ns/op     104 B/op      3 allocs/op
BenchmarkMultiError_Add              ~294 ns/op      56 B/op      3 allocs/op
BenchmarkMultiError_Error_10        ~3505 ns/op    1424 B/op     26 allocs/op
BenchmarkWithMeta_2Pairs             ~502 ns/op     624 B/op      5 allocs/op
BenchmarkWithMetaMap                 ~593 ns/op     544 B/op      4 allocs/op
```

### Analysis

**New (minimal):** ~174 ns, 1 alloc. Single struct allocation. The cheapest error type that still carries domain+code+message.

**New (all options):** ~1.0 us, 10 allocs. Includes severity, category, retry, meta map, trace IDs. Still under 2 us.

**Wrap:** ~238 ns, 2 allocs. Struct + cause pointer. Cheaper than most error wrapping libraries.

**Wrap (nil):** ~7 ns, 0 allocs. Early nil check. Free.

**Error.String:** ~121-219 ns. `fmt.Sprintf` with domain.code + message + optional cause chain.

**MarshalJSON:** ~4.9-6.3 us. JSON encoding with reflection. Acceptable for error serialization (error path only).

**LogValue:** ~432-951 ns. Builds `slog.Group` with all fields. Called once per log record.

**As (found):** ~147 ns. `errors.As` with one level of unwrapping. Efficient for error inspection.

**Stack trace:** ~2.7 us for format. Disabled by default — zero cost in production.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| New (minimal) | < 200 ns, 1 alloc | Cheap error creation |
| Wrap | < 250 ns, 2 allocs | Cheap wrapping |
| Wrap (nil) | < 8 ns, 0 allocs | Free nil path |
| String | < 250 ns | Fast formatting |
| JSON | < 7 us | Error path only |
| LogValue | < 1 us | Once per log record |
| As | < 150 ns | Fast inspection |

## What errx does NOT do

| Concern | Owner |
|---------|-------|
| Logging | `logx` / `slog` |
| Error translation | `i18n.TranslateError` |
| HTTP status mapping | caller |
| gRPC status mapping | caller |
| Error recovery | `panix` |
| Retry logic | `retryx` |

## File structure

```text
pkg/errx/
    errx.go       -- Error, New(), Wrap(), As(), enums, options, JSON, slog
    multi.go      -- MultiError, NewMulti(), Add(), Err()
    error_test.go   -- 58 tests
    multi_test.go   -- 25 tests
    example_test.go -- runnable examples
    bench_test.go   -- 23 benchmarks
    README.md
```
