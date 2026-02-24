# cronx

Minimal, reliable job scheduler for Go services in URX style.

## Philosophy

**One job: reliable scheduling.** `cronx` runs background jobs at a fixed
`time.Duration` interval with graceful shutdown. One-off jobs (interval <= 0)
execute once at `Start`. It does not implement cron syntax, distributed
locking, overlap strategies, retries, or logging. Those remain caller
concerns (or other URX packages).

## Quick start

```go
s := cronx.New(cronx.WithName("billing"))

_ = cronx.AddJob(s, "invoice-sync", time.Minute,
    func(ctx context.Context, jc cronx.JobController) (int, error) {
        if jc.RunNumber() > 100 {
            jc.Reschedule(5 * time.Minute)
        }
        return syncInvoices(ctx)
    },
)

_ = s.Start(ctx)
defer s.Stop(30 * time.Second)
```

## API

| Function / Method | Description |
|---|---|
| `New(opts ...Option) *Scheduler` | Create scheduler |
| `AddJob[T](s, name, interval, fn) error` | Register typed job |
| `s.Start(ctx) error` | Start all jobs |
| `s.Stop(timeout) error` | Graceful shutdown |
| `s.Stats() Stats` | Scheduler snapshot |
| `s.ResetStats()` | Zero all counters |
| `s.HealthCheck(ctx) error` | Failure-rate health check |
| `s.IsClosed() bool` | Whether stopped |

## JobController

| Method | Description |
|---|---|
| `RunNumber() int64` | Current run count (1-based) |
| `LastRunTime() time.Time` | Start time of previous run |
| `Abort()` | Stop this job permanently |
| `Reschedule(d)` | Change interval for future runs |
| `SkipError()` | Do not count current error as failure |

## Error codes

All errors are `*errx.Error` with `Domain = "CRON"`.

| Code | Meaning |
|---|---|
| `ALREADY_STARTED` | Start called twice |
| `NOT_STARTED` | Stop called before Start |
| `CLOSED` | Scheduler stopped |
| `INVALID_INPUT` | invalid function input (e.g. nil scheduler) |
| `NIL_FUNC` | nil job function |
| `JOB_FAILED` | Failure rate threshold exceeded |
| `SHUTDOWN_TIMEOUT` | Stop timeout exceeded |
