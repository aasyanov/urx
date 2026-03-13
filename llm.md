# URX — LLM Reference Guide

> Unified Resilience eXtensions for Go.
> Module: `github.com/aasyanov/urx` | Go 1.24+ | 31 packages

## CRITICAL RULES

1. ALL execution wrappers are **package-level generic functions**, not methods:
   `retryx.Do[T](...)`, `bulkx.Execute[T](...)`, NOT `r.Do(...)`, `b.Execute(...)`
2. ALL public API errors are `*errx.Error` with Domain/Code — prefer `errx.New`/`errx.Wrap` over `fmt.Errorf`
3. ALL `Execute`/`Do` callbacks are wrapped with `panix.Safe` — panics become `*errx.Error`
4. Seven packages pass **controllers** to callbacks — always include them in fn signature
5. Return type is always `(T, error)` — never just `error`

---

## PACKAGE QUICK REFERENCE

### errx — Structured errors (ALWAYS use this)

```go
import "github.com/aasyanov/urx/pkg/errx"

// Create
err := errx.New("DOMAIN", "CODE", "message", opts...)
err := errx.Wrap(cause, "DOMAIN", "CODE", "message", opts...)

// Options
errx.WithMeta("key1", val1, "key2", val2)
errx.WithRetry(errx.RetrySafe)       // retryable
errx.WithSeverity(errx.SeverityWarn)
errx.WithOp("pkg.Function")
errx.WithCategory(errx.CategoryBusiness)
errx.WithTrace(traceID, spanID)

// Inspect
xe, ok := errx.As(err)  // type-assert any error
xe.Retryable()           // bool
xe.IsPanic()             // bool
xe.Domain                // "BULK", "CIRCUIT", etc.
xe.Code                  // "TIMEOUT", "OPEN", etc.

// Multi-error
me := errx.NewMulti()           // or errx.NewMulti(err1, err2)
me.Add(err3)
if err := me.Err(); err != nil { ... }
```

### panix — Panic recovery

```go
import "github.com/aasyanov/urx/pkg/panix"

// Wrap function with panic recovery → returns *errx.Error on panic
val, err := panix.Safe[T](ctx, "op.name", func(ctx context.Context) (T, error) { ... })

// Fire-and-forget goroutine with recovery
panix.SafeGo(ctx, "op.name", func(ctx context.Context) { ... })

// Wrap a function for reuse
wrapped := panix.Wrap[T](fn, "op.name")
```

---

## RESILIENCE PACKAGES (with controllers)

### retryx — Retry with backoff

```go
import "github.com/aasyanov/urx/pkg/retryx"

resp, err := retryx.Do(ctx, func(rc retryx.RetryController) (*Response, error) {
    // rc.Number() — current attempt (1-based)
    // rc.Abort()  — stop retrying immediately
    resp, err := callAPI(ctx)
    if isNonRetryable(err) {
        rc.Abort()
    }
    return resp, err
},
    retryx.WithMaxAttempts(5),
    retryx.WithBackoff(100*time.Millisecond),
    retryx.WithMaxBackoff(5*time.Second),
    retryx.WithJitter(true),
    retryx.WithRetryIf(func(err error) bool { ... }),
    retryx.WithOnRetry(func(attempt int, err error) { ... }),
)
// Errors: RETRY.EXHAUSTED, RETRY.CANCELLED, RETRY.ABORTED
// Note: auto-detects errx.RetrySafe/RetryUnsafe if fn returns *errx.Error
```

### circuitx — Circuit breaker

```go
import "github.com/aasyanov/urx/pkg/circuitx"

cb := circuitx.New(
    circuitx.WithMaxFailures(5),
    circuitx.WithResetTimeout(30*time.Second),
)

resp, err := circuitx.Execute(cb, ctx, func(ctx context.Context, cc circuitx.CircuitController) (*Response, error) {
    // cc.State()       — Closed, Open, HalfOpen at admission
    // cc.Failures()    — current failure count
    // cc.SkipFailure() — don't count this error as a circuit failure
    resp, err := callAPI(ctx)
    if isBusinessError(err) {
        cc.SkipFailure()
    }
    return resp, err
})
// Errors: CIRCUIT.OPEN
// States: circuitx.Closed, circuitx.Open, circuitx.HalfOpen
```

### bulkx — Concurrency limiter (bulkhead)

```go
import "github.com/aasyanov/urx/pkg/bulkx"

bh := bulkx.New(
    bulkx.WithMaxConcurrent(10),
    bulkx.WithTimeout(5*time.Second),
)
defer bh.Close()

resp, err := bulkx.Execute(bh, ctx, func(ctx context.Context, bc bulkx.BulkController) (*Response, error) {
    // bc.Active()        — in-flight ops at admission
    // bc.MaxConcurrent() — configured slot count
    // bc.WaitedSlot()    — true if waited (slow path)
    if bc.Active() > 8 {
        return lightPath(ctx)
    }
    return fullPath(ctx)
})

// Non-blocking variant:
ok, val, err := bulkx.TryExecute(bh, ctx, func(ctx context.Context, bc bulkx.BulkController) (*Response, error) { ... })
// Errors: BULK.TIMEOUT, BULK.CANCELLED, BULK.CLOSED
```

### shedx — Load shedding

```go
import "github.com/aasyanov/urx/pkg/shedx"

s := shedx.New(
    shedx.WithCapacity(1000),
    shedx.WithThreshold(0.8),
)
defer s.Close()

resp, err := shedx.Execute(s, ctx, shedx.PriorityNormal, func(ctx context.Context, sc shedx.ShedController) (*Response, error) {
    // sc.Priority() — PriorityLow/Normal/High/Critical
    // sc.Load()     — inflight/capacity ratio at admission
    // sc.InFlight() — in-flight count at admission
    if sc.Load() > 0.9 {
        return quickResponse(ctx)
    }
    return fullResponse(ctx)
})
// Priorities: PriorityLow(0), PriorityNormal(1), PriorityHigh(2), PriorityCritical(3)
// PriorityCritical is NEVER shed
// Errors: SHED.REJECTED, SHED.CLOSED
```

### adaptx — Adaptive concurrency

```go
import "github.com/aasyanov/urx/pkg/adaptx"

l := adaptx.New(
    adaptx.WithAlgorithm(adaptx.AIMD),  // or Vegas, Gradient
    adaptx.WithInitialLimit(10),
    adaptx.WithMinLimit(1),
    adaptx.WithMaxLimit(1000),
)
defer l.Close()           // calls CloseWithTimeout(30s); use l.CloseWithTimeout(d) for custom drain

rows, err := adaptx.Do(l, ctx, func(ctx context.Context, ac adaptx.AdaptController) (*sql.Rows, error) {
    // ac.Limit()      — concurrency limit at admission
    // ac.InFlight()   — in-flight count at admission
    // ac.Algorithm()  — AIMD, Vegas, or Gradient
    // ac.SkipSample() — don't feed this result into the algorithm
    rows, err := db.QueryContext(ctx, sql)
    if isCacheMiss(err) {
        ac.SkipSample() // abnormal latency, don't shrink limit
    }
    return rows, err
})
// Algorithms: adaptx.AIMD, adaptx.Vegas, adaptx.Gradient
// Errors: ADAPT.LIMIT_EXCEEDED(retryable), ADAPT.TIMEOUT, ADAPT.CANCELLED, ADAPT.CLOSED

// Manual acquire/release:
release, err := l.Acquire(ctx)
// ... do work ...
release(success, latency)
```

### hedgex — Hedged requests

```go
import "github.com/aasyanov/urx/pkg/hedgex"

h := hedgex.New[string](
    hedgex.WithDelay(100*time.Millisecond),
    hedgex.WithMaxParallel(3),
)

val, err := h.Do(ctx, func(ctx context.Context, hc hedgex.HedgeController) (string, error) {
    // hc.Attempt() — 1=original, 2=first hedge, 3=second hedge
    // hc.IsHedge() — true if Attempt() > 1
    if hc.IsHedge() {
        return fetchFromReplica(ctx)
    }
    return fetchFromPrimary(ctx)
})

// Multiple backends:
val, err := h.DoMulti(ctx, []func(context.Context, hedgex.HedgeController) (string, error){
    func(ctx context.Context, hc hedgex.HedgeController) (string, error) { return callBackend1(ctx) },
    func(ctx context.Context, hc hedgex.HedgeController) (string, error) { return callBackend2(ctx) },
})
// Errors: HEDGE.ALL_FAILED, HEDGE.NO_FUNCTIONS, HEDGE.CANCELLED
```

### cronx — Job scheduler

```go
import "github.com/aasyanov/urx/pkg/cronx"

s := cronx.New(cronx.WithName("billing"))

_ = cronx.AddJob(s, "invoice-sync", 30*time.Second,
    func(ctx context.Context, jc cronx.JobController) (int, error) {
        // jc.RunNumber() / jc.LastRunTime()
        // jc.Abort()      — stop this job permanently
        // jc.Reschedule() — switch to a new interval
        // jc.SkipError()  — do not count current run as failure
        return syncInvoices(ctx)
    },
)

// interval <= 0 runs once at Start (one-off)

_ = s.Start(ctx)
defer s.Stop(30 * time.Second)

stats := s.Stats()
_ = stats
// Errors: CRON.ALREADY_STARTED, CRON.NOT_STARTED, CRON.INVALID_INPUT, CRON.NIL_FUNC,
// CRON.JOB_FAILED, CRON.SHUTDOWN_TIMEOUT, CRON.CLOSED
```

---

## RESILIENCE PACKAGES (without controllers)

### toutx — Timeout enforcement

```go
import "github.com/aasyanov/urx/pkg/toutx"

// One-shot:
resp, err := toutx.Execute(ctx, 5*time.Second, func(ctx context.Context) (*Response, error) {
    return callAPI(ctx) // ctx has 5s deadline
})

// Reusable timer:
t := toutx.New(toutx.WithTimeout(5*time.Second), toutx.WithOp("api.call"))
resp, err := toutx.Execute(ctx, 0, fn, toutx.WithTimer(t))
// Errors: TIMEOUT.DEADLINE_EXCEEDED, TIMEOUT.CANCELLED
```

### fallx — Fallback strategies

```go
import "github.com/aasyanov/urx/pkg/fallx"

// Static fallback:
fb := fallx.New(fallx.WithStatic[string]("default value"))

// Function fallback:
fb := fallx.New(fallx.WithFunc(func(ctx context.Context, err error) (string, error) {
    return fetchBackup(ctx)
}))

// Cached fallback (replays last success):
fb := fallx.New(
    fallx.WithCached[Response](5*time.Minute, 1000),
    fallx.WithKeyFunc[Response](func(ctx context.Context) string { return userID(ctx) }),
)
defer fb.Close()

val, err := fb.Do(ctx, primaryFn)
// Errors: FALLBACK.NO_FUNC, FALLBACK.FUNC_FAILED, FALLBACK.NO_CACHED, FALLBACK.CLOSED
```

### ratex — Token-bucket rate limiter

```go
import "github.com/aasyanov/urx/pkg/ratex"

rl := ratex.New(ratex.WithRate(100), ratex.WithBurst(200))

if rl.Allow() { handle() }           // non-blocking
if rl.AllowN(5) { handleBatch() }    // n tokens (panics if n < 1)
err := rl.Wait(ctx)                  // blocking
err := rl.WaitN(ctx, 5)              // blocking, n tokens (panics if n < 1)
// Errors: RATE.LIMITED, RATE.CANCELLED
```

### quotax — Per-key rate limiting

```go
import "github.com/aasyanov/urx/pkg/quotax"

ql := quotax.New(
    quotax.WithRate(10),
    quotax.WithBurst(20),
    quotax.WithMaxKeys(100000),
    quotax.WithEvictionTTL(15*time.Minute),
)
defer ql.Close()

if ql.Allow("user:123") { handle() }
err := ql.AllowOrError("user:123")  // returns *errx.Error
err := ql.Wait(ctx, "user:123")     // blocking
// Errors: QUOTA.LIMITED, QUOTA.MAX_KEYS, QUOTA.CANCELLED, QUOTA.CLOSED
```

### warmupx — Gradual capacity ramp-up

```go
import "github.com/aasyanov/urx/pkg/warmupx"

w := warmupx.New(
    warmupx.WithDuration(30*time.Second),
    warmupx.WithStrategy(warmupx.Exponential),  // Linear, Exponential, Logarithmic, Step
    warmupx.WithMinCapacity(0.1),
    warmupx.WithMaxCapacity(1.0),
)
w.Start()
defer w.Stop()

if w.Allow() { handle() }  // probabilistic admission
w.Capacity()                // current [0,1]
w.Progress()                // warmup progress [0,1]
w.MaxRequests(1000)         // scale base limit by capacity (returns 0 for base <= 0)
// Errors: WARMUP.REJECTED
```

---

## INFRASTRUCTURE PACKAGES

### ctxx — Trace propagation

```go
import "github.com/aasyanov/urx/pkg/ctxx"

ctx = ctxx.WithTrace(ctx)                        // generate trace ID
ctx = ctxx.WithSpan(ctx)                         // generate span ID within trace
traceID, spanID := ctxx.TraceFromContext(ctx)     // read (may be empty)
traceID, spanID, ctx := ctxx.MustTraceFromContext(ctx) // read or generate
```

### logx — Structured logging

```go
import "github.com/aasyanov/urx/pkg/logx"

// Wrap slog handler to auto-inject trace/span from context:
handler := logx.NewHandler(slog.NewJSONHandler(os.Stdout, nil))
logger := slog.New(handler)

ctx = logx.WithLogger(ctx, logger)
log := logx.FromContext(ctx)         // retrieve logger
log.Info("msg", logx.Err(someErr))   // errx-aware error attr
```

### signalx — Graceful shutdown

```go
import "github.com/aasyanov/urx/pkg/signalx"

ctx, cancel := signalx.Context(context.Background(), syscall.SIGINT, syscall.SIGTERM)
defer cancel()

signalx.OnShutdown(func(ctx context.Context) { server.Shutdown(ctx) })
signalx.OnShutdown(func(ctx context.Context) { db.Close() })

err := signalx.Wait(ctx, 30*time.Second) // blocks until signal, runs hooks
```

### healthx — Health probes

```go
import "github.com/aasyanov/urx/pkg/healthx"

hc := healthx.New(healthx.WithTimeout(5*time.Second))
hc.Register("postgres", func(ctx context.Context) error { return db.PingContext(ctx) })
hc.Register("redis", func(ctx context.Context) error { return redis.Ping(ctx).Err() })

mux.Handle("/healthz", hc.LiveHandler())
mux.Handle("/readyz", hc.ReadyHandler())
// Errors: HEALTH.UNHEALTHY, HEALTH.TIMEOUT
```

### syncx — Concurrency primitives

```go
import "github.com/aasyanov/urx/pkg/syncx"

// Lazy initialization (thread-safe with atomic+mutex; supports Reset):
lazy := syncx.NewLazy(func() (*DB, error) { return connectDB() })
db, err := lazy.Get() // computed once, cached
lazy.Reset()          // allows re-initialization on next Get()

// Error group with concurrency limit:
g, ctx := syncx.NewGroup(ctx, syncx.WithLimit(10))
g.Go(func(ctx context.Context) error { ... })
g.Go(func(ctx context.Context) error { ... })
err := g.Wait()

// Typed concurrent map:
m := syncx.NewMap[string, *User]()
m.Store("id", user)
user, ok := m.Load("id")
```

### poolx — Worker pool, object pool, batch collector

```go
import "github.com/aasyanov/urx/pkg/poolx"

// Worker pool:
wp := poolx.NewWorkerPool(poolx.WithWorkers(8), poolx.WithQueueSize(1000))
defer wp.Close()
err := wp.Submit(ctx, func(ctx context.Context) error { ... }) // respects ctx cancellation while waiting for slot

// Object pool:
op := poolx.NewObjectPool(func() *Buffer { return new(Buffer) })
buf := op.Get()
defer op.Put(buf)

// Batch collector:
b := poolx.NewBatch(flushFn, poolx.WithBatchSize(100), poolx.WithFlushInterval(time.Second))
defer b.Close()
b.Add(item)
// Errors: POOL.CLOSED, POOL.QUEUE_FULL, POOL.CANCELLED, POOL.FLUSH_FAILED
```

### busx — Event bus

```go
import "github.com/aasyanov/urx/pkg/busx"

bus := busx.New()
defer bus.Close()

id, _ := bus.Subscribe("user.created", func(ctx context.Context, event string, payload any) {
    user := payload.(*User)
    sendWelcome(user)
})
bus.Publish(ctx, "user.created", newUser)
bus.Unsubscribe(id)
// Errors: BUS.CLOSED, BUS.PUBLISH_FAILED, BUS.NIL_HANDLER
```

### testx — Failure simulator

```go
import "github.com/aasyanov/urx/pkg/testx"

sim := testx.FailUntil(3)       // fail first 3 calls, then succeed
sim := testx.FailEvery(5)       // fail every 5th call
sim := testx.Pattern("FFSFFFS") // F=fail, S=success
sim := testx.AlwaysFail()

err := sim.Call() // returns *errx.Error or nil
// Errors: TEST.SIMULATED
```

---

## CONFIGURATION PACKAGES

### cfgx — File loader

```go
import "github.com/aasyanov/urx/pkg/cfgx"

// Load (auto-detects YAML/JSON/TOML from extension):
err := cfgx.Load("config.yaml", &cfg)
err := cfgx.Load("config.yaml", &cfg, cfgx.WithAutoFix(), cfgx.WithCreateIfMissing())

// Save:
err := cfgx.Save("config.yaml", &cfg)

// If cfg implements cfgx.Validator, Load calls cfg.Validate(fix) after unmarshal
// Errors: CONFIG.NOT_FOUND, CONFIG.READ_FAILED, CONFIG.PARSE_FAILED, CONFIG.WRITE_FAILED, CONFIG.UNSUPPORTED_FORMAT, CONFIG.INVALID_INPUT, CONFIG.VALIDATION_FAILED
```

### envx — Typed env binding (no reflection)

```go
import "github.com/aasyanov/urx/pkg/envx"

env := envx.New(envx.WithPrefix("APP"))

port   := envx.Bind(env, "PORT", 8080)             // optional with default
host   := envx.Bind(env, "HOST", "localhost")
secret := envx.BindRequired[string](env, "SECRET")  // required
envx.BindTo(env, "DEBUG", &cfg.Debug)               // write into existing pointer

if err := env.Validate(); err != nil { log.Fatal(err) }

fmt.Println(port.Value())   // 8080 or from APP_PORT
fmt.Println(secret.Found()) // true — variable was set (even if empty string)

// Key API:
// New(opts...) *Env                         — create env reader
// Bind[T](env, name, default) *Var[T]       — optional with fallback
// BindRequired[T](env, name) *Var[T]        — required (Validate reports missing)
// BindTo[T](env, name, &target) *Var[T]     — write into pointer, panics on nil
// env.Validate() error                      — check all bindings (*errx.MultiError)
// env.Vars() []string                       — list bound variable names
// var.Value() T                             — resolved value
// var.Ptr() *T                              — pointer (for CLI flag binding)
// var.Found() bool                          — true if variable was set in env
// var.Key() string                          — full name with prefix

// Options:
// WithPrefix(p)    — prepend P_ to all names
// WithLookup(fn)   — custom func(string)(string,bool), default os.LookupEnv
// MapLookup(m)     — helper: map[string]string -> lookup function

// Supported types: string, int, int64, float64, bool, time.Duration
// Errors: ENV.MISSING, ENV.INVALID
```

### env2x — Reflection-based env overlay

```go
import "github.com/aasyanov/urx/pkg/env2x"

env := env2x.New(env2x.WithPrefix("APP"), env2x.WithTag("yaml"))
result := env2x.Overlay(env, &cfg) // reads APP_HOST, APP_PORT, etc. from struct tags
if err := result.Err(); err != nil { log.Fatal(err) }
// Errors: ENV2.PARSE_FAILED, ENV2.NOT_SETTABLE, ENV2.UNSUPPORTED_TYPE, ENV2.INVALID_INPUT
```

### clix — CLI parser

```go
import "github.com/aasyanov/urx/pkg/clix"

p := clix.New(os.Args[1:], "myapp", "description",
    clix.AddFlag(&port, "port", "p", 8080, "listen port"),
    clix.AddFlag(&host, "host", "H", "0.0.0.0", "bind address"),
    clix.AddFlag(&level, "level", "l", "info", "log level", clix.Enum("debug","info","warn","error")),
    clix.AddFlag(&name, "name", "n", "", "user name", clix.Required()),
    clix.Version("1.0.0"),
    clix.SubCommand("migrate", "run migrations",
        clix.Alias("m"),
        clix.Run(func(c *clix.Context) error { return runMigrations() }),
    ),
)

// Handle sentinels first
if errors.Is(p.Err(), clix.ErrHelp) {
    fmt.Println(p.Help())  // help for whichever command was matched
    os.Exit(0)
}
if errors.Is(p.Err(), clix.ErrVersion) {
    fmt.Println(p.Version())
    os.Exit(0)
}
if err := p.Err(); err != nil {
    fmt.Fprintln(os.Stderr, err) // structured *errx.Error
    os.Exit(1)
}
if err := p.Run(); err != nil { // execute the matched action
    fmt.Fprintln(os.Stderr, err)
    os.Exit(1)
}

// Key API:
// New(osArgs, name, desc, opts...) *Parser — parse only, does NOT execute action
// p.Err() error          — parse error, ErrHelp, or ErrVersion
// p.Help() string        — help for matched command (works at any nesting level)
// p.Version() string     — version string set via Version()
// p.Run() error          — execute matched action (separate from parsing)
// AddFlag[T]             — string, int, float64, bool, time.Duration, time.Time
// Required()             — mark flag as mandatory
// Enum(vals...)          — restrict flag to allowed values
// SubCommand(name, desc) — nested subcommand
// Alias(names...)        — alternative names for a subcommand
// Version(v)             — enable --version / -V
// Run(fn Action)         — set command action (one per command, duplicate panics)
// Context.Args()         — positional args (including after --)
// Context.Command()      — matched command node
// Context.Parser()       — parser (for help access from within action)
//
// Flag syntax: --port 8080, -p 8080, --port=8080, -p=8080
// POSIX grouped short flags: -vdq, -vp 3000, -vp3000
// Bool negation: --no-verbose
// Flag inheritance: parent flags visible in subcommands
// -h inside POSIX groups triggers help: -vh → ErrHelp
//
// Errors: CLI.UNKNOWN_FLAG, CLI.UNKNOWN_COMMAND, CLI.MISSING_VALUE, CLI.INVALID_VALUE, CLI.REQUIRED, CLI.ENUM_VIOLATED
// Sentinels: ErrHelp (--help/-h), ErrVersion (--version/-V when Version() set)
```

### validx — Validators and fixers

```go
import "github.com/aasyanov/urx/pkg/validx"

// Validators (return *errx.Error or nil):
err := validx.Required("name", name)
err := validx.MinLen("name", name, 3)
err := validx.MaxLen("name", name, 100)
err := validx.Between("age", age, 0, 150)
err := validx.Email("email", email)
err := validx.URL("website", url)
err := validx.Match("code", code, `^[A-Z]{3}$`)
err := validx.OneOf("status", status, []string{"active","inactive"})

// Fixers (mutate value, return *errx.Error describing fix or nil):
err := validx.Clamp("age", &age, 0, 150)
err := validx.Default("name", &name, "unnamed")
err := validx.DefaultStr("host", &host, "localhost")

// Collect into single error:
err := validx.Collect(
    validx.Required("name", name),
    validx.Email("email", email),
    validx.Between("age", age, 0, 150),
)
// Errors: VALIDATION.REQUIRED, VALIDATION.TOO_SHORT, VALIDATION.TOO_LONG, etc.
```

---

## DATA PACKAGES

### lrux — LRU cache

```go
import "github.com/aasyanov/urx/pkg/lrux"

// Single-threaded (caller locks):
cache := lrux.New[string, *User](lrux.WithCapacity[string, *User](1000), lrux.WithTTL[string, *User](5*time.Minute))
defer cache.Close()
cache.Set("key", user)
user, ok := cache.Get("key")
user := cache.GetOrCompute("key", func() *User { return fetchUser() })

// Concurrent (sharded):
sc := lrux.NewSharded[string, *User](
    lrux.WithShardCount[string, *User](16),
    lrux.WithShardCapacity[string, *User](1000),
    lrux.WithShardTTL[string, *User](5*time.Minute),
)
defer sc.Close()
sc.Set("key", user) // safe from any goroutine
```

### hashx — Password hashing

```go
import "github.com/aasyanov/urx/pkg/hashx"

// Quick (Argon2id, TierDefault):
hash, err := hashx.Generate(ctx, "password")
err := hashx.Compare(ctx, hash, "password")

// Custom:
h := hashx.New(hashx.WithAlgorithm(hashx.Bcrypt), hashx.WithTier(hashx.TierMax))
hash, err := h.Generate(ctx, "password")
// Auto-detects algorithm from hash format on Compare
// Errors: HASH.EMPTY_PASSWORD, HASH.MISMATCH, HASH.INVALID_HASH, HASH.INTERNAL, HASH.CANCELLED
```

### dicx — Dependency injection

```go
import "github.com/aasyanov/urx/pkg/dicx"

c := dicx.New()
c.Provide(NewDatabase)                                           // singleton (default)
c.Provide(NewUserRepo, dicx.WithLifetime(dicx.Transient))       // new instance each resolve
c.Provide(NewUserService)                                        // auto-resolves dependencies

if err := c.Start(ctx); err != nil { ... }  // calls Starter.Start on all singletons
defer c.Stop(ctx)                            // calls Stopper.Stop in reverse order

svc, err := dicx.Resolve[*UserService](c)
svc := dicx.MustResolve[*UserService](c) // panics on error
// Errors: DI.CYCLIC_DEP, DI.MISSING_DEP, DI.BAD_CONSTRUCTOR, DI.CONSTRUCTOR_FAILED, DI.ALREADY_PROVIDED, DI.FROZEN, DI.LIFECYCLE_FAILED
```

### i18n — Translations

```go
import "github.com/aasyanov/urx/pkg/i18n"

i18n.MustInit(i18n.WithFolder("./lang"), i18n.WithLanguage("ru"))
msg := i18n.T("welcome.message", userName)
msg := i18n.T2("en", "welcome.message", userName)
translated := i18n.TranslateError("ru", err) // translates errx.Error
```

---

## COMPOSITION PATTERNS

### Pattern 1: Retry inside Circuit Breaker

```go
resp, err := circuitx.Execute(cb, ctx, func(ctx context.Context, cc circuitx.CircuitController) (*Resp, error) {
    return retryx.Do(ctx, func(rc retryx.RetryController) (*Resp, error) {
        return callAPI(ctx)
    }, retryx.WithMaxAttempts(3))
})
```

### Pattern 2: Bulkhead → Circuit → Retry (full stack)

```go
resp, err := bulkx.Execute(bh, ctx, func(ctx context.Context, bc bulkx.BulkController) (*Resp, error) {
    return circuitx.Execute(cb, ctx, func(ctx context.Context, cc circuitx.CircuitController) (*Resp, error) {
        return retryx.Do(ctx, func(rc retryx.RetryController) (*Resp, error) {
            resp, err := callAPI(ctx)
            if isBizErr(err) { cc.SkipFailure(); rc.Abort() }
            return resp, err
        })
    })
})
```

### Pattern 3: Timeout + Fallback

```go
resp, err := fb.Do(ctx, func(ctx context.Context) (*Resp, error) {
    return toutx.Execute(ctx, 5*time.Second, func(ctx context.Context) (*Resp, error) {
        return callAPI(ctx)
    })
})
```

### Pattern 4: Adaptive + Hedge for DB reads

```go
val, err := adaptx.Do(limiter, ctx, func(ctx context.Context, ac adaptx.AdaptController) (string, error) {
    return hedger.Do(ctx, func(ctx context.Context, hc hedgex.HedgeController) (string, error) {
        if hc.IsHedge() { return readReplica(ctx) }
        return readPrimary(ctx)
    })
})
```

### Pattern 5: Load shedding + Rate limiting

```go
if !rateLimiter.Allow() { return errx.New("API", "RATE_LIMITED", "too many requests") }

resp, err := shedx.Execute(shedder, ctx, shedx.PriorityNormal, func(ctx context.Context, sc shedx.ShedController) (*Resp, error) {
    return handleRequest(ctx)
})
```

### Pattern 6: Full service init

```go
func main() {
    // Config
    cfg := DefaultConfig()
    cfgx.Load("config.yaml", &cfg, cfgx.WithAutoFix(), cfgx.WithCreateIfMissing())
    env := envx.New(envx.WithPrefix("APP"))
    envx.BindTo(env, "PORT", &cfg.Port)
    env.Validate()

    // Tracing + Logging
    ctx := ctxx.WithTrace(context.Background())
    handler := logx.NewHandler(slog.NewJSONHandler(os.Stdout, nil))
    logger := slog.New(handler)
    ctx = logx.WithLogger(ctx, logger)

    // Health
    health := healthx.New()
    health.Register("db", func(ctx context.Context) error { return db.Ping() })

    // Signal
    ctx, cancel := signalx.Context(ctx, syscall.SIGINT, syscall.SIGTERM)
    defer cancel()

    // Resilience
    cb := circuitx.New(circuitx.WithMaxFailures(5))
    bh := bulkx.New(bulkx.WithMaxConcurrent(100))
    defer bh.Close()

    // Server
    mux := http.NewServeMux()
    mux.Handle("/healthz", health.LiveHandler())
    mux.Handle("/readyz", health.ReadyHandler())

    signalx.OnShutdown(func(ctx context.Context) { server.Shutdown(ctx) })
    signalx.Wait(ctx, 30*time.Second)
}
```

---

## DOMAIN/CODE REFERENCE

| Package | Domain | Codes |
|---------|--------|-------|
| adaptx | ADAPT | LIMIT_EXCEEDED, TIMEOUT, CANCELLED, CLOSED |
| bulkx | BULK | TIMEOUT, CANCELLED, CLOSED |
| busx | BUS | CLOSED, PUBLISH_FAILED, NIL_HANDLER |
| cfgx | CONFIG | NOT_FOUND, READ_FAILED, PARSE_FAILED, WRITE_FAILED, UNSUPPORTED_FORMAT, INVALID_INPUT, VALIDATION_FAILED |
| circuitx | CIRCUIT | OPEN |
| clix | CLI | UNKNOWN_FLAG, UNKNOWN_COMMAND, MISSING_VALUE, INVALID_VALUE, REQUIRED, ENUM_VIOLATED |
| cronx | CRON | ALREADY_STARTED, NOT_STARTED, CLOSED, INVALID_INPUT, NIL_FUNC, JOB_FAILED, SHUTDOWN_TIMEOUT |
| dicx | DI | CYCLIC_DEP, MISSING_DEP, BAD_CONSTRUCTOR, CONSTRUCTOR_FAILED, ALREADY_PROVIDED, FROZEN, LIFECYCLE_FAILED |
| env2x | ENV2 | PARSE_FAILED, NOT_SETTABLE, UNSUPPORTED_TYPE, INVALID_INPUT |
| envx | ENV | MISSING, INVALID |
| fallx | FALLBACK | NO_FUNC, FUNC_FAILED, NO_CACHED, CLOSED |
| hashx | HASH | EMPTY_PASSWORD, MISMATCH, INVALID_HASH, INTERNAL, CANCELLED |
| healthx | HEALTH | UNHEALTHY, TIMEOUT |
| hedgex | HEDGE | ALL_FAILED, NO_FUNCTIONS, CANCELLED |
| poolx | POOL | CLOSED, QUEUE_FULL, CANCELLED, FLUSH_FAILED |
| quotax | QUOTA | LIMITED, MAX_KEYS, CANCELLED, CLOSED |
| ratex | RATE | LIMITED, CANCELLED |
| retryx | RETRY | EXHAUSTED, CANCELLED, ABORTED |
| shedx | SHED | REJECTED, CLOSED |
| syncx | SYNC | INIT_FAILED |
| testx | TEST | SIMULATED |
| toutx | TIMEOUT | DEADLINE_EXCEEDED, CANCELLED |
| validx | VALIDATION | REQUIRED, TOO_SHORT, TOO_LONG, OUT_OF_RANGE, INVALID_FORMAT, INVALID_VALUE, FIXED |
| warmupx | WARMUP | REJECTED |

---

## COMMON MISTAKES TO AVOID

1. **Method-style call** — WRONG: `bh.Execute(ctx, fn)` → RIGHT: `bulkx.Execute(bh, ctx, fn)`
2. **Missing controller in fn** — WRONG: `func(ctx) (T, error)` → RIGHT: `func(ctx, bc BulkController) (T, error)`
3. **Using fmt.Errorf in public APIs** — prefer `errx.New` or `errx.Wrap` for domain errors; `fmt.Errorf` is acceptable for internal wraps and sentinels
4. **Returning just error** — ALL generic Execute/Do return `(T, error)`, not just `error`
5. **Forgetting defer Close/Stop()** — `bulkx`, `shedx`, `adaptx`, `quotax`, `lrux`, `poolx`, `fallx`, `busx` need `Close()`; `cronx` needs `Stop(timeout)`
6. **retryx.Do signature** — fn takes `RetryController` only (no ctx): `func(rc RetryController) (T, error)`
7. **fallx.Do signature** — primaryFn does NOT receive a controller: `func(ctx context.Context) (T, error)`
8. **hedgex is a method** — `h.Do(ctx, fn)` is a method, not package-level (Hedger has type parameter)
9. **ratex.AllowN(0) or AllowN(-1)** — panics; `n` must be `>= 1`
10. **Logging from packages** — URX packages never write to `slog`; logging is the caller's responsibility
