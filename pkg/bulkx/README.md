# bulkx

Thread-safe concurrency limiter for industrial Go services.

Part of the **urx** infrastructure ecosystem alongside `busx`, `circuitx`, `clix`,
`ctxx`, `dicx`, `errx`, `healthx`, `i18n`, `logx`, `lrux`, `panix`, `poolx`,
`ratex`, `retryx`, `shedx`, `signalx`, `syncx`, `toutx`, and `validx`.

## Philosophy

**One job: concurrency isolation.** `bulkx` limits the number of operations
that may execute simultaneously. When all slots are occupied, `Execute` blocks
until a slot frees up, the context is cancelled, or the configured timeout
fires. `TryExecute` is the non-blocking variant. It does not retry,
circuit-break, rate-limit, or log. Those are responsibilities of `retryx`,
`circuitx`, `ratex`, and the caller.

## Quick start

```go
bh := bulkx.New(
    bulkx.WithMaxConcurrent(10),
    bulkx.WithTimeout(5*time.Second),
)
defer bh.Close()

resp, err := bulkx.Execute(bh, ctx, func(ctx context.Context, bc bulkx.BulkController) (*Response, error) {
    if bc.Active() > 8 {
        return lightweightResponse(ctx)
    }
    return client.Call(ctx, req)
})
```

## API

| Function / Method | Description |
|---|---|
| `New(opts ...Option) *Bulkhead` | Create a bulkhead with defaults (10 slots, 30 s timeout) |
| `Execute[T](b, ctx, fn) (T, error)` | Run fn (receives BulkController), blocking until a slot is available |
| `TryExecute[T](b, ctx, fn) (bool, T, error)` | Run fn (receives BulkController) if a slot is immediately available |
| `b.Active() int` | Number of in-flight operations |
| `b.Stats() Stats` | Point-in-time counters snapshot |
| `b.ResetStats()` | Zero all counters |
| `b.Close()` | Shut down the bulkhead (idempotent) |
| `b.IsClosed() bool` | Whether the bulkhead has been shut down |

### Options

| Option | Default | Description |
|---|---|---|
| `WithMaxConcurrent(n)` | `10` | Maximum concurrent operations |
| `WithTimeout(d)` | `30s` | Max wait time to acquire a slot |

### BulkController

The callback passed to `Execute` and `TryExecute` receives a `BulkController`:

| Method | Description |
|---|---|
| `Active() int` | In-flight operations at admission time |
| `MaxConcurrent() int` | Configured slot count |
| `WaitedSlot() bool` | True if call went through the slow (timer) path |

## Behavior details

- **Three-phase slot acquisition** (`Execute`):
  1. **Fast reject** — if the context is already cancelled, return immediately.
  2. **Optimistic path** — non-blocking `select` tries to grab a slot without
     allocating a timer. Under normal load this is the common path: zero
     allocations, no timer overhead.
  3. **Slow path** — all slots are busy; a `time.Timer` is created and the call
     blocks until a slot frees up, the context is cancelled, or the timeout fires.
     Context cancellation returns `*errx.Error` with `Code = "CANCELLED"` wrapping
     the underlying context error, consistent with the structured error pattern
     used throughout urx.

- **Non-blocking execution**: `TryExecute` attempts to acquire a slot
  immediately. Returns `(false, nil)` if no slot is available, without blocking.

- **Shared execution logic**: both `Execute` and `TryExecute` delegate to a
  private `run` function that manages the semaphore release, active counter, and
  `panix.Safe` invocation.

- **Panic recovery**: every call is wrapped with `panix.Safe`. Panics inside the
  function produce a structured `*errx.Error`.

- **Close**: marks the bulkhead as closed via `atomic.Bool`. After `Close`,
  both `Execute` and `TryExecute` return `CodeClosed` errors. `Close` is
  idempotent. Note that `Close` does not drain in-flight operations — it only
  prevents new submissions.

## Error diagnostics

All errors are `*errx.Error` with `Domain = "BULK"`.

### Codes

| Code | When |
|---|---|
| `TIMEOUT` | Timed out waiting for a concurrency slot |
| `CLOSED` | Bulkhead has been shut down |
| `CANCELLED` | Context was cancelled while waiting for a slot |

### Example

```text
BULK.TIMEOUT: bulkhead timeout
BULK.CLOSED: bulkhead closed
BULK.CANCELLED: bulkhead wait cancelled | cause: context canceled
```

## Thread safety

- `Execute` / `TryExecute` use a channel-based semaphore — fully concurrent
- `Active` reads an `atomic.Int32` — lock-free
- `Close` / `IsClosed` use `atomic.Bool` — lock-free
- `Stats` / `ResetStats` use atomic counters — lock-free

## Tests

**33 tests, 94.9% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/bulkx  coverage: 94.9% of statements
```

Coverage includes:
- Execute: success, pre-cancelled context (fast reject), optimistic path, slow-path slot acquisition, context cancellation, timeout, closed bulkhead, panic recovery
- TryExecute: success, no slot, closed bulkhead, panic recovery
- Active: during execution, after completion
- Stats: counters increment, reset
- Lifecycle: Close, Close idempotent, IsClosed
- BulkController: Active, MaxConcurrent, WaitedSlot (optimistic and slow paths), TryExecute

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz.
Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkExecute_Success        ~199 ns/op       0 B/op     0 allocs/op
BenchmarkTryExecute_Success      ~96 ns/op       0 B/op     0 allocs/op
BenchmarkTryExecute_NoSlot        ~15 ns/op       0 B/op     0 allocs/op
BenchmarkActive                    ~2 ns/op       0 B/op     0 allocs/op
```

### Analysis

**Execute (happy path):** ~199 ns, 0 allocs. The optimistic non-blocking `select`
grabs a slot without allocating a timer. A 4.5x improvement over the previous
implementation (~889 ns, 3 allocs) that created a `time.Timer` on every call.

**TryExecute (happy path):** ~96 ns, 0 allocs. Non-blocking `select` on the channel. Fast enough for hot-path admission checks.

**TryExecute (no slot):** ~15 ns, 0 allocs. Immediate return on the `default` branch. Effectively free.

**Active:** ~2 ns. Atomic load — no locking, no allocation.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| Execute | < 200 ns, 0 allocs | Zero-alloc hot path |
| TryExecute | < 100 ns, 0 allocs | Hot-path friendly |
| No slot | < 16 ns, 0 allocs | Free |
| Active | < 3 ns, 0 allocs | Free |

## What bulkx does NOT do

| Concern | Owner |
|---------|-------|
| Retry logic | `retryx` |
| Circuit breaking | `circuitx` |
| Rate limiting | `ratex` |
| Logging | caller / `slog` |
| Tracing | `ctxx` |
| Error construction | `errx` |
| HTTP middleware | caller |

## File structure

```text
pkg/bulkx/
    bulkx.go        -- Bulkhead, BulkController, New(), Execute(), TryExecute(), Close()
    errors.go       -- DomainBulkhead, Code constants, error constructors
    bulkx_test.go   -- 33 tests, 94.9% coverage
    bench_test.go   -- 4 benchmarks
    README.md
```
