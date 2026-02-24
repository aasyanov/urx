# signalx

Graceful shutdown primitives for industrial Go services.

## Philosophy

**One job: graceful shutdown.** `signalx` converts OS signals into context
cancellation and runs shutdown hooks in order with a configurable timeout. It
does not manage goroutine lifecycles, log, or retry. Those are responsibilities
of the caller, `syncx.Group`, and `retryx`.

## Quick start

```go
ctx, cancel := signalx.Context(context.Background())
defer cancel()

signalx.OnShutdown(func(ctx context.Context) {
    db.Close()
})
signalx.OnShutdown(func(ctx context.Context) {
    server.Shutdown(ctx)
})

if err := signalx.Wait(ctx, 10*time.Second); err != nil {
    log.Error("shutdown error", "error", err)
}
```

## API

| Function / Method | Description |
|---|---|
| `Context(parent, signals...) (context.Context, context.CancelFunc)` | Context cancelled on SIGINT/SIGTERM (or custom signals). Nil parent → `context.Background()` |
| `OnShutdown(fn)` | Register a global shutdown hook |
| `ResetHooks()` | Clear all global hooks (for tests) |
| `Wait(ctx, timeout, hooks...) error` | Wait for ctx.Done, run hooks in order with timeout |

## Behavior details

- **Signal context**: `Context` calls `signal.Notify` on a buffered channel
  with the provided signals (defaults to `SIGINT`, `SIGTERM`). A goroutine
  waits for the first signal and calls `cancel()`. When either the signal
  arrives or the parent context is cancelled, `signal.Stop(ch)` is called
  to release signal notification resources.

- **Global hooks**: `OnShutdown` appends hooks to a global registry protected
  by `sync.Mutex`. Hooks run in FIFO (registration) order during `Wait`.

- **Wait**: blocks until `ctx.Done()`, then creates a timeout context via
  `context.WithTimeout` and executes all hooks (global first, then inline)
  sequentially. Each hook is wrapped in `panix.Safe` for panic recovery —
  a panicking hook does not abort subsequent hooks, and the panic error is
  collected into an `*errx.MultiError`. The timeout is checked *between*
  hooks — a running hook is not interrupted mid-execution. If the timeout
  expires before all hooks complete, `Wait` returns `context.DeadlineExceeded`.
  If any hooks panicked, `Wait` returns the aggregated errors.

- **ResetHooks**: clears the global registry. Intended for test isolation.

## Thread safety

- `OnShutdown` / `ResetHooks` acquire `sync.Mutex` — safe for concurrent use
- `Context` sets up signal notification — safe to call once per process
- `Wait` reads the hook slice under lock, then executes without lock

## Tests

**11 tests, 97.3% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/signalx  coverage: 97.3% of statements
```

Coverage includes:
- Context: cancel function, parent cancellation, nil parent normalization
- OnShutdown: single hook, multiple hooks, global-before-local ordering
- Wait: no hooks, with hooks, timeout returns `context.DeadlineExceeded`
- Wait: hook receives a non-cancelled shutdown context
- Wait: panicking hook propagates `*errx.Error` and does not abort other hooks
- ResetHooks: clears registry

The uncovered 3.1% is the `case <-ch:` branch in `Context` (actual OS signal
reception), which requires sending real signals to the process.

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz.
Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkContext         ~2622 ns/op     289 B/op     6 allocs/op
BenchmarkWait_NoHooks     ~985 ns/op     368 B/op     6 allocs/op
BenchmarkWait_3Hooks     ~1388 ns/op     392 B/op     7 allocs/op
BenchmarkOnShutdown        ~73 ns/op       8 B/op     1 allocs/op
```

### Analysis

**Context:** ~2.6 us, 6 allocs. Sets up `signal.Notify` + channel + goroutine. One-time startup cost per application.

**Wait (no hooks):** ~985 ns, 6 allocs. Creates timeout context + checks for hooks. Dominated by `context.WithTimeout`.

**Wait (3 hooks):** ~1.4 us, 7 allocs. Three sequential hook invocations add ~400 ns. In production, hook bodies (DB close, server shutdown) dominate.

**OnShutdown:** ~73 ns, 1 alloc. Mutex lock + slice append. One-time registration cost.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| Context | < 3 us | One-time startup |
| Wait (no hooks) | < 1 us | Minimal overhead |
| Wait (3 hooks) | < 1.5 us | Hook bodies dominate |
| OnShutdown | < 100 ns | One-time registration |

Shutdown is a once-per-process event. Performance is irrelevant in practice.

## What signalx does NOT do

| Concern | Owner |
|---------|-------|
| Goroutine lifecycle | `syncx.Group` / caller |
| Retry on hook failure | `retryx` / caller |
| Logging | caller / `slog` |
| Health status toggle | `healthx.MarkDown()` |
| Process management | systemd / Kubernetes |

## File structure

```text
pkg/signalx/
    signalx.go      -- Context(), OnShutdown(), Wait(), ResetHooks()
    signalx_test.go -- 11 tests, 97.3% coverage
    bench_test.go   -- 4 benchmarks
    README.md
```
