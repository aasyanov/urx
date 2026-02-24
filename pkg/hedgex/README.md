# hedgex

Hedging (speculative execution) for reducing tail latency in industrial Go
services.

## Philosophy

**One job: first-success-wins.** A [Hedger] launches the same request with
staggered delays against one or more backends. The first successful result
is returned; remaining in-flight requests are cancelled. No randomness, no
struct tags, no framework coupling.

Use cases: P99 latency reduction, multi-region failover, read replicas.

## Quick start

```go
h := hedgex.New[string](
    hedgex.WithDelay(100 * time.Millisecond),
    hedgex.WithMaxParallel(3),
)

val, err := h.Do(ctx, func(ctx context.Context, hc hedgex.HedgeController) (string, error) {
    if hc.IsHedge() {
        return queryReadReplica(ctx, "SELECT ...")
    }
    return queryPrimary(ctx, "SELECT ...")
})
```

If the first call doesn't complete within 100ms, a second copy is launched.
If that also hangs, a third copy fires 100ms later. The first to succeed wins.
The callback receives a [HedgeController] so it can distinguish original
requests from hedge copies.

## API

| Method | Description |
|---|---|
| `New[T](opts ...Option) *Hedger[T]` | Create a hedger |
| `h.Do(ctx, fn) (T, error)` | Execute fn with hedging; fn receives `HedgeController` |
| `h.DoMulti(ctx, fns) (T, error)` | Execute against multiple backends; each fn receives `HedgeController` |
| `h.Stats() Stats` | Snapshot of counters |
| `h.ResetStats()` | Zero all counters |

### HedgeController

| Method | Description |
|---|---|
| `Attempt() int` | 1-based attempt number (1 = original, 2+ = hedge) |
| `IsHedge() bool` | True if this is a speculative hedge copy |

### Options

| Option | Default | Description |
|---|---|---|
| `WithMaxParallel(n)` | `3` | Max concurrent requests |
| `WithDelay(d)` | `100ms` | Wait before launching next hedge |
| `WithMaxDelay(d)` | `1s` | Cap on total stagger window |
| `WithOnHedge(fn)` | — | Async callback on hedge launch |

## Behavior details

- **Staggered launches**: request 1 fires at t=0, request 2 at t=Delay,
  request 3 at t=2*Delay, etc.

- **MaxDelay cap**: when cumulative delay exceeds MaxDelay, remaining
  requests are spread evenly using Delay/4 intervals to avoid bursts.

- **Auto-cancel**: when one request succeeds, `context.WithCancel` cancels
  all remaining in-flight requests.

- **DoMulti**: each function runs as a separate backend. Functions beyond
  MaxParallel are silently dropped.

- **Single function**: when `len(fns) == 1`, the function runs directly
  without goroutines or timers.

- **OnHedge callback**: invoked asynchronously in a separate goroutine to
  avoid blocking hedge launches. Panics in the callback are recovered silently
  to prevent crashing the hedge machinery.

## Error diagnostics

All errors are `*errx.Error` with `Domain = "HEDGE"`.

### Codes

| Code | When |
|---|---|
| `ALL_FAILED` | Every hedged attempt returned an error |
| `NO_FUNCTIONS` | Empty function list provided to DoMulti |
| `CANCELLED` | Parent context was cancelled or timed out |

### Examples

```text
HEDGE.ALL_FAILED: all hedged requests failed | cause: db timeout
HEDGE.CANCELLED: hedging cancelled | cause: context deadline exceeded
```

## Thread safety

- `Do` / `DoMulti` use per-call goroutines and channels — no shared mutable state
- `Stats` reads atomics — lock-free
- `ResetStats` writes atomics — lock-free

## Tests

**32 tests, 98.4% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/hedgex  coverage: 98.4% of statements
```

Coverage includes:
- All options: valid and invalid values, clamping
- Do: success, all-fail with errx validation
- DoMulti: fast-wins, empty fns, single fn, single fn fails, caps at MaxParallel
- Context cancellation with error code
- OnHedge async callback
- Stats / ResetStats
- delays(): single, linear, MaxDelay spread, no-burst
- Error constructors: AllFailed, NoFunctions, Cancelled
- HedgeController: Attempt, IsHedge for original and hedge copies, DoMulti attempt numbers, single-function path

## File structure

```text
pkg/hedgex/
    hedgex.go       -- Hedger[T], HedgeController, New(), Do(), DoMulti(), options
    errors.go       -- DomainHedge, Code constants, error constructors
    hedgex_test.go  -- 32 tests + 1 benchmark, 98.4% coverage
    README.md
```
