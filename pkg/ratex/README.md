# ratex

Thread-safe token-bucket rate limiter for industrial Go services.

## Philosophy

**One job: rate limiting.** `ratex` controls how many operations per unit of
time are allowed through using a token-bucket algorithm. `Allow` is the
non-blocking check; `Wait` blocks until a token is available or the context is
cancelled. It does not retry, queue, circuit-break, or log. Those are
responsibilities of `retryx`, `bulkx`, `circuitx`, and the caller.

## Quick start

```go
rl := ratex.New(
    ratex.WithRate(100),
    ratex.WithBurst(20),
)

if !rl.Allow() {
    // rejected
}

// or block until allowed:
if err := rl.Wait(ctx); err != nil {
    // context cancelled or rate limited
}
```

## API

| Function / Method | Description |
|---|---|
| `New(opts ...Option) *Limiter` | Create a rate limiter (10 req/s, burst 20) |
| `rl.Allow() bool` | Check if one request is allowed (non-blocking) |
| `rl.AllowN(n) bool` | Check if n requests are allowed; panics if n < 1 |
| `rl.Wait(ctx) error` | Block until one token is available |
| `rl.WaitN(ctx, n) error` | Block until n tokens are available; panics if n < 1 |
| `rl.Tokens() float64` | Current available tokens |
| `rl.Reset()` | Restore bucket to full capacity |
| `rl.Stats() Stats` | Point-in-time counters snapshot |
| `rl.ResetStats()` | Zero all counters |

### Options

| Option | Default | Description |
|---|---|---|
| `WithRate(r)` | `10` | Sustained rate in requests per second |
| `WithBurst(n)` | `20` | Maximum burst size (bucket capacity) |

## Behavior details

- **Token bucket**: tokens refill at `Rate` per second up to `Burst` capacity.
  Each `Allow` / `AllowN` call consumes tokens. When the bucket is empty,
  `Allow` returns `false` immediately; `Wait` blocks until refill.

- **Atomic refill**: `refill()` is called on every access. It computes elapsed
  time since last refill and adds the proportional number of tokens, capped at
  `Burst`. The operation uses `sync.Mutex` — no floating-point races.

- **Wait semantics**: `WaitN` calculates the delay until enough tokens are
  available and sleeps using `time.NewTimer`. Returns early if the context is
  cancelled. The error wraps the context cancellation as `*errx.Error` with
  `Code = "CANCELLED"`.

- **Input validation**: `AllowN` and `WaitN` panic if `n < 1`. Passing zero or
  negative values would otherwise corrupt token accounting (negative
  consumption adds tokens). This is a programming error, not a runtime
  condition, hence a panic rather than an error return.

- **Stats**: `Allowed` and `Limited` counters are protected by `sync.Mutex`
  (same lock as token arithmetic).

## Error diagnostics

All errors are `*errx.Error` with `Domain = "RATE"`.

### Codes

| Code | When |
|---|---|
| `LIMITED` | Defined for caller use (rate limit exceeded) |
| `CANCELLED` | Wait cancelled via context (deadline or cancel) |

### Example

```text
RATE.CANCELLED: rate limiter wait cancelled | cause: context canceled
```

## Thread safety

- `Allow` / `AllowN` / `Wait` / `WaitN` acquire `sync.Mutex` for token arithmetic
- `Tokens` acquires the same mutex — consistent snapshot
- `Reset` acquires the mutex — safe for concurrent use
- `Stats` / `ResetStats` acquire `sync.Mutex` — consistent snapshot

## Tests

**23 tests, 94.3% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/ratex  coverage: 94.3% of statements
```

Coverage includes:
- Allow: under limit, over limit, refill after time
- AllowN: batch allow, insufficient tokens
- Wait: immediate, delayed, context cancelled
- WaitN: multi-token wait, context deadline
- Tokens: accurate count after partial consumption
- Reset: restores full capacity
- Stats: counters increment, reset

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz.
Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkAllow              ~97 ns/op       0 B/op     0 allocs/op
BenchmarkAllowN            ~135 ns/op       0 B/op     0 allocs/op
BenchmarkWait              ~860 ns/op       0 B/op     0 allocs/op
BenchmarkTokens             ~74 ns/op       0 B/op     0 allocs/op
BenchmarkAllow_Parallel    ~226 ns/op       0 B/op     0 allocs/op
```

### Analysis

**Allow:** ~97 ns, 0 allocs. Mutex lock + refill calculation + token decrement. Zero allocations on every call.

**AllowN:** ~135 ns, 0 allocs. Same path as `Allow` but with multi-token arithmetic. Marginal overhead.

**Wait:** ~860 ns, 0 allocs. Includes `time.NewTimer` setup. The actual sleep is not measured — benchmark uses a high-rate limiter. In production, the cost is dominated by the sleep duration.

**Tokens:** ~74 ns. Read-only mutex acquire + refill.

**Parallel Allow:** ~226 ns under `RunParallel`. Mutex contention adds ~130 ns. Still well under 1 us.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| Allow | < 100 ns, 0 allocs | Hot-path friendly |
| AllowN | < 140 ns, 0 allocs | Hot-path friendly |
| Wait | < 1 us, 0 allocs | Dominated by actual sleep |
| Parallel | < 230 ns, 0 allocs | Scales under contention |

## What ratex does NOT do

| Concern | Owner |
|---------|-------|
| Retry logic | `retryx` |
| Resource isolation | `bulkx` |
| Circuit breaking | `circuitx` |
| Logging | caller / `slog` |
| Tracing | `ctxx` |
| Error construction | `errx` |
| HTTP middleware | caller |

## File structure

```text
pkg/ratex/
    ratex.go       -- Limiter, New(), Allow(), Wait(), Tokens(), Reset()
    errors.go      -- DomainRate, Code constants, error constructors
    ratex_test.go  -- 23 tests, 94.3% coverage
    bench_test.go  -- 5 benchmarks
    README.md
```
