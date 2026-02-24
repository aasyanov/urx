# Getting Started with URX

This guide walks you through adding resilience to a Go service in 10
minutes.

## Install

```bash
go get github.com/aasyanov/urx@latest
```

## Step 1: Add retry to an HTTP call

The simplest use case — retry a flaky API call with exponential backoff:

```go
import (
    "context"
    "net/http"
    "time"

    "github.com/aasyanov/urx/pkg/retryx"
)

resp, err := retryx.Do(ctx, func(rc retryx.RetryController) (*http.Response, error) {
    return http.Get("https://api.example.com/data")
}, retryx.WithMaxAttempts(3), retryx.WithBackoff(500*time.Millisecond))
```

`rc.Number()` tells you which attempt this is (1-based). Call `rc.Abort()`
to stop retrying early — useful when you know the error is permanent.

## Step 2: Add a circuit breaker

Wrap the retry in a circuit breaker so you stop hammering a dead service:

```go
import "github.com/aasyanov/urx/pkg/circuitx"

cb := circuitx.New(
    circuitx.WithMaxFailures(5),
    circuitx.WithResetTimeout(10*time.Second),
)

resp, err := circuitx.Execute(cb, ctx, func(ctx context.Context, cc circuitx.CircuitController) (*http.Response, error) {
    return retryx.Do(ctx, func(rc retryx.RetryController) (*http.Response, error) {
        resp, err := http.Get("https://api.example.com/data")
        if isBizError(err) {
            cc.SkipFailure() // don't count business errors
            rc.Abort()       // don't retry them either
        }
        return resp, err
    }, retryx.WithMaxAttempts(3))
})
```

Notice how `cc.SkipFailure()` and `rc.Abort()` work together — the
function decides what counts as a real failure, not the wrapper.

## Step 3: Add concurrency limiting

Protect your service from overloading the downstream:

```go
import "github.com/aasyanov/urx/pkg/bulkx"

bh := bulkx.New(bulkx.WithMaxConcurrent(10))

resp, err := bulkx.Execute(bh, ctx, func(ctx context.Context, bc bulkx.BulkController) (*http.Response, error) {
    // bc.Active() shows how many calls are running right now
    return circuitx.Execute(cb, ctx, func(ctx context.Context, cc circuitx.CircuitController) (*http.Response, error) {
        return retryx.Do(ctx, func(rc retryx.RetryController) (*http.Response, error) {
            return http.Get("https://api.example.com/data")
        })
    })
})
```

## Step 4: Add structured logging with trace IDs

```go
import (
    "log/slog"
    "os"

    "github.com/aasyanov/urx/pkg/ctxx"
    "github.com/aasyanov/urx/pkg/logx"
)

logger := slog.New(logx.NewHandler(slog.NewJSONHandler(os.Stdout, nil)))

ctx = ctxx.WithTrace(ctx)           // generate trace ID
ctx = logx.WithLogger(ctx, logger)  // store logger in context

// Every log line now includes trace_id automatically
logx.FromContext(ctx).Info("request started")

// Errors log as structured groups with Domain, Code, etc.
logx.FromContext(ctx).Error("call failed", logx.Err(err))
```

## Step 5: Add health probes

```go
import "github.com/aasyanov/urx/pkg/healthx"

hc := healthx.New()
hc.Register("database", func(ctx context.Context) error {
    return db.PingContext(ctx)
})

http.Handle("/healthz", hc.LiveHandler())
http.Handle("/readyz", hc.ReadyHandler())
```

## Step 6: Add background jobs

```go
import "github.com/aasyanov/urx/pkg/cronx"

s := cronx.New(cronx.WithName("worker"))

_ = cronx.AddJob(s, "cleanup", 5*time.Minute,
    func(ctx context.Context, jc cronx.JobController) (int, error) {
        if jc.RunNumber() > 100 {
            jc.Reschedule(30 * time.Minute)
        }
        return runCleanup(ctx)
    },
)

_ = s.Start(ctx)
defer s.Stop(30 * time.Second)
```

## Composition pattern

URX packages are designed to nest:

```text
bulkx.Execute          ← limits concurrency
  └─ circuitx.Execute  ← stops calling dead services
       └─ retryx.Do    ← retries transient failures
            └─ toutx.Execute  ← enforces deadline
                 └─ your function
```

Each layer adds protection. Controllers let inner functions talk back to
outer wrappers.

## Error handling

All URX packages return `*errx.Error`:

```go
import "github.com/aasyanov/urx/pkg/errx"

if ex, ok := errx.As(err); ok {
    switch ex.Code {
    case "OPEN":
        // circuit is open, back off
    case "TIMEOUT":
        // bulkhead full or deadline exceeded
    case "REJECTED":
        // load shedder dropped the request
    }
}
```

## Next steps

- Browse [examples/](../examples/) for runnable programs
- Read package READMEs for detailed API reference
- See [llm.md](../llm.md) for exact function signatures
