# panix — Structured Panic Recovery for Go

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/panix)
[License: MIT](../LICENSE)

Deterministic panic-to-error conversion with captured stack traces. Go 1.24+. Zero external dependencies (testify in tests only).

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> **Core primitive:** `panic → *PanicError{Op, Value, Stack}` — a safety layer, not an error-handling framework. `panix` does **not** replace proper error returns. It catches unexpected panics from third-party code, plugin boundaries, and defensive wrappers. If you're using `panix.Safe` around your own code that panics by design, you're doing it wrong — return an `error` instead.

## The Problem

Go's `panic`/`recover` mechanism is a raw primitive. In production, a bare `defer recover()` in every goroutine leads to:

1. **Lost context** — the recovered value is `any`; no indication of *where* or *what operation* failed
2. **No stack trace** — `recover()` discards the stack; post-mortem debugging requires reproducing the crash
3. **Boilerplate everywhere** — every goroutine, every plugin call, every boundary function repeats the same `defer func() { if r := recover() { ... } }()` block
4. **Broken error chains** — when the panic value is an `error`, `errors.Is`/`errors.As` stop working because the value is boxed in `any`
5. **Silent goroutine death** — a panicking goroutine vanishes; the parent never learns

Without a structured recovery layer, teams write fragile wrappers:

```go
defer func() {
    if r := recover(); r != nil {
        log.Printf("panic: %v", r)  // no stack, no op name, no errors.Is
    }
}()
```

`panix` addresses **five production requirements**:

1. **Structured error** — every recovered panic becomes `*PanicError` with `Op`, `Value`, `Stack` fields
2. **Stack capture** — `runtime.Stack` with auto-growing buffer (4 KB → 64 KB)
3. **Error chain preservation** — `Unwrap()` exposes the original error for `errors.Is`/`errors.As`
4. **Goroutine safety** — `SafeGo` launches panic-safe goroutines with a callback
5. **Zero overhead on happy path** — no allocations when no panic occurs

## Architectural Position

```text
✅ A panic-to-error converter for synchronous calls (Safe, SafeVoid)
✅ A panic-safe goroutine launcher (SafeGo)
✅ A reusable wrapper factory (Wrap, WrapVoid)
✅ A structured error type with stack traces (PanicError)

❌ NOT an error-handling framework
❌ NOT a logging system (it returns errors; you decide what to log)
❌ NOT a crash reporter (it captures stacks; you decide where to send them)
❌ NOT a replacement for proper error returns
❌ NOT a supervisor (SafeGo does NOT restart goroutines)
```

### Position in the urx Stack

```text
┌─────────────────────────────────────────────────────┐
│  Higher-level urx packages                          │
│  retryx, poolx, bulkx, shedx, ...                   │
│     use panix.Safe / SafeGo internally              │
└──────────────────────┬──────────────────────────────┘
                       │ panic → *PanicError
┌──────────────────────▼──────────────────────────────┐
│  panix  (this package)                              │
│  Safe · SafeVoid · SafeGo · Wrap · WrapVoid         │
│  PanicError{Op, Value, Stack}                       │
└──────────────────────┬──────────────────────────────┘
                       │ defer recover() + runtime.Stack
┌──────────────────────▼──────────────────────────────┐
│  Go runtime                                         │
│  panic() / recover() / runtime.Stack()              │
└─────────────────────────────────────────────────────┘
```

`panix` is a **leaf dependency** — it imports only the standard library and is used by other urx packages as a foundational safety layer.

## Architecture

```text
                     ┌─────────────────────┐
     caller code     │   Safe[T](op, fn)            │
    ─────────────►   │                     │
                     │  defer func() {     │
                     │    r := recover()   │
                     │    if r != nil {    │
                     │      captureStack() │
                     │      → PanicError   │
                     │    }                │
                     │  }()                │
                     │  return fn()        │
                     └─────┬───────────────┘
                           │
              ┌────────────┼────────────────┐
              ▼            ▼                ▼
         no panic      fn returns      panic(v)
         (T, nil)     (T, error)       (*PanicError)
                                       {Op, Value, Stack}
```

All five public functions are thin wrappers around the same core: `Safe[T]`.

```text
Safe[T]     ← core generic implementation
SafeVoid    ← delegates to Safe[struct{}]
SafeGo      ← go func() { SafeVoid(...); onError(...) }
Wrap[T]     ← returns func() that calls Safe[T]
WrapVoid    ← returns func() that calls SafeVoid
```

## How It Works

### Recovery Pipeline

When `Safe` is called, it executes the following deterministic pipeline:

```text
1. Set up deferred recover()
2. Call fn()
    ├── fn returns (val, nil)     → return (val, nil)
    ├── fn returns (val, err)     → return (val, err)
    └── fn panics with value v    → recover() captures v
         ├── captureStack()       → runtime.Stack (4KB buffer)
         │   └── if truncated     → grow buffer (×2, up to 64KB)
         └── return (zero, &PanicError{Op: op, Value: v, Stack: stack})
```

### Stack Capture Algorithm

`captureStack` uses an auto-growing buffer strategy:

```text
buf = make([]byte, 4096)          ← initial 4KB allocation
loop:
    n = runtime.Stack(buf, false) ← capture current goroutine only
    if n < len(buf) → return buf[:n]    ← stack fits
    if len(buf) ≥ 64KB → return buf     ← hard cap reached (trace may truncate)
    buf = make([]byte, min(len(buf)*2, 64KB))
    goto loop
```

The 4 KB default covers 95%+ of production stacks. The 64 KB cap prevents unbounded allocation in pathological recursion cases. Only the **current goroutine** is captured (`false` to `runtime.Stack`) — capturing all goroutines would be prohibitively expensive.

### Error Chain Semantics

`PanicError` implements the `error` interface and provides `Unwrap() error`:

```text
panic(errors.New("root"))
    │
    ▼
*PanicError{Value: error("root")}
    │
    └── Unwrap() → error("root")
            │
            └── errors.Is(err, rootErr) ✅

panic("string value")
    │
    ▼
*PanicError{Value: "string value"}
    │
    └── Unwrap() → nil (not an error type)
            │
            └── errors.Is(err, sentinel) ❌
```

### SafeGo Goroutine Lifecycle

```text
SafeGo(ctx, op, fn, onError)
    │
    └── go func() {
            err := SafeVoid(op, func() error {
                fn(ctx)         ← user function runs in new goroutine
                return nil
            })
            if err != nil && onError != nil {
                SafeVoid(op+".onError", func() error {   ← onError also panic-safe
                    onError(ctx, err)
                    return nil
                })
            }
        }()
```

> [!WARNING]
> `SafeGo` is fire-and-forget. There is no built-in mechanism to wait for the goroutine to finish. If you need synchronization, pass a `sync.WaitGroup`, channel, or similar primitive through the closure.

## Normative Contracts


| Contract                                        | Guarantee                                                          |
| ----------------------------------------------- | ------------------------------------------------------------------ |
| `Safe`/`SafeVoid` never panic                   | Even if `fn` panics, the caller receives a `*PanicError`           |
| `SafeGo` never crashes the process              | Panics in the goroutine are recovered; the goroutine exits cleanly |
| `PanicError.Stack` is never nil on recovery     | Every recovered panic includes a stack trace (may be truncated at 64 KB) |
| Zero allocations on happy path                  | `Safe` with no panic: 0 allocs/op                                  |
| `Unwrap` preserves error chains                 | If `panic(err)`, then `errors.Is(result, err)` is true             |
| `SafeGo` with nil ctx uses `context.Background` | Never passes a nil context to `fn`                                 |
| `SafeGo` with nil `onError` silently recovers   | Panics in fn are caught but not reported                           |
| `SafeGo` onError panics are recovered           | Handler panics never crash the process; incomplete delivery is lost |
| Stack buffer ≤ 64 KB                            | `captureStack` never allocates more than `maxStackSize`            |


## Quick Start

```go
package main

import (
    "errors"
    "fmt"
    "log"

    "github.com/aasyanov/urx/panix"
)

func main() {
    // Guard a function that might panic
    val, err := panix.Safe("myapp.riskyOp", func() (string, error) {
        // Simulate third-party code that panics
        panic("unexpected nil pointer")
    })
    if err != nil {
        var pe *panix.PanicError
        if errors.As(err, &pe) {
            log.Printf("panic recovered in %s: %v", pe.Op, pe.Value)
            log.Printf("stack trace:\n%s", pe.Stack)
        }
        return
    }
    fmt.Println(val)
}
```

## Usage Scenarios

### Plugin / Third-Party Code Boundary

When calling code you don't control (plugins, generated code, user callbacks), wrap every call in `Safe` to prevent panics from crashing the host process:

```go
func executePlugin(name string, plugin Plugin) error {
    return panix.SafeVoid("plugin."+name, func() error {
        return plugin.Execute()
    })
}
```

### Worker Pool with Panic Safety

Each worker goroutine should not crash the entire pool:

```go
for i, job := range jobs {
    panix.SafeGo(ctx, fmt.Sprintf("worker.%d", i), func(ctx context.Context) {
        process(ctx, job)
    }, func(ctx context.Context, err error) {
        logger.Error("worker panicked", "error", err)
        metrics.IncrCounter("worker.panics", 1)
    })
}
```

### Reusable Wrapper for Hot Path

When the same function is called repeatedly (e.g., in a retry loop), create the wrapper once:

```go
safeFetch := panix.Wrap("http.fetch", func() (*Response, error) {
    return client.Do(req)
})

for attempt := range maxRetries {
    resp, err := safeFetch()
    if err == nil {
        return resp, nil
    }
    // err might be *PanicError or a regular error — handle both
}
```

### Structured Error Inspection

Use `errors.As` to extract panic details and `errors.Is` to match through the chain:

```go
val, err := panix.Safe("db.query", func() ([]Row, error) {
    return db.Query(ctx, sql)
})
if err != nil {
    var pe *panix.PanicError
    if errors.As(err, &pe) {
        // Panic — log stack trace, alert ops
        alerting.Send("db.query panicked", pe.Stack)
    } else {
        // Regular error — handle normally
        return nil, fmt.Errorf("query failed: %w", err)
    }
}
```

## API


| Symbol              | Signature                                                                                                                 | Description                                                     |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| `Safe[T]`           | `func Safe[T any](op string, fn func() (T, error)) (T, error)`                                                            | Execute `fn` with panic recovery; return `*PanicError` on panic |
| `SafeVoid`          | `func SafeVoid(op string, fn func() error) error`                                                                         | `Safe` for functions returning only `error`                     |
| `SafeGo`            | `func SafeGo(ctx context.Context, op string, fn func(ctx context.Context), onError func(ctx context.Context, err error))` | Launch a panic-safe goroutine; call `onError` on panic          |
| `Wrap[T]`           | `func Wrap[T any](op string, fn func() (T, error)) func() (T, error)`                                                     | Return a reusable panic-safe wrapper                            |
| `WrapVoid`          | `func WrapVoid(op string, fn func() error) func() error`                                                                  | `Wrap` for functions returning only `error`                     |
| `PanicError`        | `type PanicError struct { Op string; Value any; Stack []byte }`                                                           | Structured panic error with stack trace                         |
| `PanicError.Error`  | `func (e *PanicError) Error() string`                                                                                     | `"panix: panic in <Op>: <Value>"`                               |
| `PanicError.Unwrap` | `func (e *PanicError) Unwrap() error`                                                                                     | Returns `Value` if it implements `error`; nil otherwise         |


### Internal Constants


| Constant           | Value   | Purpose                             |
| ------------------ | ------- | ----------------------------------- |
| `defaultStackSize` | `4096`  | Initial buffer for `runtime.Stack`  |
| `maxStackSize`     | `65536` | Hard cap on stack buffer allocation |


## Errors


| Error         | Type     | Condition                                                                      |
| ------------- | -------- | ------------------------------------------------------------------------------ |
| `*PanicError` | `struct` | Any recovered `panic()` — wraps the original value with `Op`, `Value`, `Stack` |


`panix` does not define sentinel errors. All errors are `*PanicError` instances, always returned as the `error` interface.

## Pitfalls

> [!WARNING]
> **panic(nil)** in Go 1.21+ — since Go 1.21, `panic(nil)` wraps the value in a `runtime.PanicNilError`. The recovered value is NOT nil; it is the wrapper. `PanicError.Value` will be that error with message `panic called with nil argument`.

> [!WARNING]
> **SafeGo** is fire-and-forget — there is no return value and no built-in wait mechanism. If the goroutine panics and `onError` is nil, the panic is silently recovered. Always provide an `onError` callback in production code.

> [!WARNING]
> **onError** runs in the same goroutine as fn — the callback executes after fn panics, still inside the SafeGo goroutine. If onError itself panics, that panic is recovered by an internal `SafeVoid(op+".onError", ...)` wrapper: the process does not crash, but any work onError did not finish (for example a channel send or log flush) is silently lost. Keep onError simple: log, increment a counter, non-blocking channel send.

> [!WARNING]
> **nil fn** — passing a nil function to `Safe`, `SafeVoid`, or `SafeGo` triggers a runtime nil-call panic that is recovered into `*PanicError`. Higher-level urx packages return `ErrNilFunc` instead; at the panix layer the panic is the signal.

> [!WARNING]
> **Stack traces are captured AFTER recovery** — the stack trace includes frames from `captureStack` → `recover` → `defer`, not the exact panic site. The panic origin is still visible in the trace but may be several frames deep.

> [!WARNING]
> **Wrap** captures the function pointer, not a closure snapshot — if the wrapped function closes over mutable state, that state may change between calls. This is by design (reusable wrapper), but can be surprising if you expect snapshot semantics.

> [!WARNING]
> **Stack cap at 64 KB** — pathological recursion produces a truncated trace when the buffer fills. The returned `Stack` slice is exactly 64 KB in that case; bytes beyond the cap are omitted.

## Safety and Concurrency

**Thread safety:** All functions in `panix` are safe for concurrent use. `Safe`, `SafeVoid`, `Wrap`, `WrapVoid` are pure functions with no shared mutable state. `SafeGo` launches an independent goroutine — the caller and the goroutine share no state except what is passed through the closure.

**Goroutine model:** `SafeGo` creates exactly one goroutine per call. The goroutine runs `fn` under `SafeVoid`, then (on panic) calls `onError` under a nested `SafeVoid(op+".onError", ...)`, then exits. No goroutines are leaked.

**Race detector:** All tests pass under `-race`. The concurrent test suite exercises 100 simultaneous `Safe` calls and 50 simultaneous `SafeGo` calls.

## Benchmarks

Three environments, two hardware classes, two operating systems. All values are **medians**. `B/op` and `allocs/op` are deterministic — they depend on code, not hardware.

### Environments

| | Laptop | CI Server (Linux) | CI Server (Windows) |
|---|---|---|---|
| CPU | Intel Core i7-10510U, 4C/8T | Intel Xeon 6973P-C | AMD EPYC 7763 |
| TDP | 15W (mobile, throttles) | 280W (server, stable) | 280W (server, stable) |
| OS | Windows 10 (NTFS) | Ubuntu (ext4) | Windows Server 2022 (NTFS) |
| Go | 1.24 | 1.26 | 1.26 |
| GOMAXPROCS | 8 | 4 | 4 |
| Runs | 3 (`-count=3`) | 3 (`-count=3`) | 3 (`-count=3`) |

### Happy Path (no panic)

| Benchmark | Laptop | Linux | Windows | B/op | allocs/op |
|---|---|---|---|---|---|
| Safe_NoPanic | 26 ns | **5.2 ns** | 8.0 ns | 0 | 0 |
| Safe_NoPanic_Error | 27 ns | **4.6 ns** | 8.4 ns | 0 | 0 |
| SafeVoid_NoPanic | 27 ns | **5.2 ns** | 10.3 ns | 0 | 0 |
| Wrap_NoPanic | 32 ns | **5.1 ns** | 8.3 ns | 0 | 0 |
| WrapVoid_NoPanic | 50 ns | **5.1 ns** | 10.3 ns | 0 | 0 |
| Safe_NoPanic_Parallel | 8 ns | **2.5 ns** | 4.1 ns | 0 | 0 |
| SafeVoid_NoPanic_Parallel | 8 ns | **3.1 ns** | 4.9 ns | 0 | 0 |

### Panic Path

| Benchmark | Laptop | Linux | Windows | B/op | allocs/op |
|---|---|---|---|---|---|
| Safe_Panic | 18 µs | **5.0 µs** | 8.0 µs | 4160 | 2 |
| SafeVoid_Panic | 27 µs | **6.0 µs** | 9.3 µs | 4160 | 2 |
| Wrap_Panic | 130 µs | **5.4 µs** | 8.9 µs | 4160 | 2 |
| WrapVoid_Panic | 43 µs | **6.2 µs** | 10.3 µs | 4160 | 2 |
| Safe_Panic_Parallel | 38 µs | **9.0 µs** | 13.4 µs | 4160 | 2 |
| CaptureStack | 36 µs | **2.9 µs** | 4.4 µs | 4096 | 1 |

### Goroutine Path

| Benchmark | Laptop | Linux | Windows | B/op | allocs/op |
|---|---|---|---|---|---|
| SafeGo_NoPanic | 2.2 µs | **321.6 ns** | 1.4 µs | 64 | 1 |
| SafeGo_Panic | 48 µs | **11.8 µs** | 22.3 µs | 4240 | 4 |

### Analysis

**Happy path: ~5–8 ns, 0 allocs — the deferred `recover()` tax.** `Safe_NoPanic` at 5.2 ns (Linux) is only a few nanoseconds over a raw function call. The 0-allocation guarantee means `Safe` can be used on hot paths without GC pressure. Linux CI is ~5× faster than the laptop (26 ns) because the Xeon 6973P-C server runs at stable clocks without mobile throttling.

**Parallel scaling: ~2.5–4 ns/op under 4 goroutines.** `Safe_NoPanic_Parallel` at 2.5 ns (Linux) vs 5.2 ns serial — each goroutine has its own deferred closure with no shared mutable state. The sub-serial number is expected from `b.RunParallel` work distribution across P's.

**Panic path: ~5–13 µs, 2 allocs (4160 B) — dominated by `runtime.Stack`.** ~700× slower than the happy path. The 4096 B buffer allocation + stack walk is the irreducible floor for any stack-capturing recovery mechanism. Linux (5.0 µs) vs laptop (18 µs): server hardware walks stacks faster; the allocation count is identical.

**CaptureStack: 2.9 µs, 1 alloc (4096 B).** Pure `runtime.Stack` cost without the `recover()` overhead. OS-independent within 5%.

**SafeGo_NoPanic: 321.6 ns (Linux) vs 1.4 µs (Windows) — goroutine launch dominates.** One 64 B allocation for the goroutine descriptor. Suitable for background tasks; not appropriate for per-request hot loops. The Windows CI VM adds ~3× scheduling overhead vs Linux on this micro-benchmark.

**SafeGo_Panic: ~16 µs, 4 allocs.** Two `SafeVoid` layers (fn + onError path) plus goroutine scheduling on the panic path.

**Wrap/WrapVoid: thin closures, no meaningful overhead.** `Wrap_NoPanic` at 5.1 ns vs `Safe_NoPanic` at 5.2 ns — one indirection, negligible in practice.

**Allocation floor on panic path.** 2 allocations (4160 B) is the architectural minimum: 4096 B for the stack buffer + 64 B for the `PanicError` struct. This cannot be reduced without sacrificing stack trace capture.

## Quality

| Metric | Value |
|---|---|
| Test functions | 39 |
| Table-driven subtests | 11 |
| Benchmarks | 15 |
| Fuzz targets | 4 (all pass, 30s each) |
| Examples | 7 |
| Coverage | 100.0% |
| Race detector | All tests pass with `-race` |
| Linter | 0 issues (`golangci-lint`) |
| CI matrix | 6 configurations (2 OS × 3 Go versions) |
| Go version | 1.24+ |
| External deps | 0 (testify in tests only) |


## File Structure

```text
panix/
├── panix.go            # Safe, SafeVoid, SafeGo, Wrap, WrapVoid, captureStack
├── errors.go           # PanicError struct, Error(), Unwrap()
├── panix_test.go       # Unit tests — white-box, testify, 38 test functions
├── helpers_test.go     # Local footprint/concurrency helpers (no testx import cycle)
├── bench_test.go       # 15 benchmarks: happy path, panic path, parallel
├── fuzz_test.go        # 4 fuzz targets: Safe, SafeVoid, Wrap, WrapVoid
├── example_test.go     # 7 runnable examples for GoDoc
├── footprint_test.go   # struct size guard (PanicError ≤ 56 bytes)
└── README.md           # This file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.