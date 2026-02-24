# busx

Thread-safe, synchronous, in-process event bus for industrial Go services.

## Philosophy

**One job: deliver events.** `busx` dispatches events to subscribed handlers
synchronously in the caller's goroutine, recovers panics via `panix`, and
reports problems as structured `errx.Error` values. It does not manage
goroutines, serialize payloads, or route across processes. Those are
responsibilities of your application code or a message broker.

## Quick start

```go
b := busx.New()

id, _ := b.Subscribe("user.created", func(ctx context.Context, event string, payload any) {
    u := payload.(*User)
    sendWelcomeEmail(ctx, u)
})

if err := b.Publish(ctx, "user.created", newUser); err != nil {
    log.Error("publish failed", "error", err)
}

b.Unsubscribe(id)
b.Close()
```

## API

| Function / Method | Description |
|---|---|
| `New() *Bus` | Create an empty bus |
| `b.Subscribe(event, fn) (SubscriptionID, error)` | Register a handler; returns unique ID |
| `b.Unsubscribe(id) bool` | Remove subscription by ID |
| `b.Publish(ctx, event, payload) error` | Invoke all handlers synchronously; aggregate panics |
| `b.Subscribers(event) int` | Count of handlers for event |
| `b.Events() []string` | All events with subscribers |
| `b.Close()` | Shut down, clear subscriptions (idempotent) |
| `b.IsClosed() bool` | Whether the bus is closed |

## Handler signature

```go
type HandlerFunc func(ctx context.Context, event string, payload any)
```

The payload is a single `any` value. Callers wrap structured data in a struct
or slice as needed. No variadic `...any` -- cleaner API, explicit types.

## Behavior details

- **Synchronous dispatch**: handlers run in the caller's goroutine, in
  subscription order. If you need async, wrap the call in `go`. This makes
  event delivery predictable and testable without `sync.WaitGroup` hacks.

- **Panic recovery**: each handler is wrapped with `panix.Safe`. If a handler
  panics, the panic is recovered into a structured `*errx.Error`, and the
  remaining handlers still execute. All panic errors are aggregated via
  `errx.MultiError` and returned from `Publish`.

- **Subscription IDs**: `Subscribe` returns a `SubscriptionID` (monotonic
  `uint64`). Use it with `Unsubscribe` -- no `reflect.ValueOf(fn).Pointer()`
  hacks, no fragile function pointer comparison.

- **O(1) unsubscribe**: an internal `map[SubscriptionID]string` index maps each
  ID to its event name. `Unsubscribe` locates the event in constant time, then
  removes the handler via swap-delete on the per-event slice.

- **Snapshot isolation**: `Publish` takes a snapshot of the handler slice under
  a read lock. Handlers that subscribe or unsubscribe during dispatch do not
  affect the current publish cycle.

- **Close**: marks the bus as closed via `atomic.Bool`, clears all subscriptions
  and the ID index. After `Close`, `Subscribe` and `Publish` return `CodeClosed`
  errors. `Close` is idempotent.

## Error diagnostics

All errors are `*errx.Error` with `Domain = "BUS"` and structured metadata.

### Codes

| Code | When |
|---|---|
| `CLOSED` | `Subscribe` or `Publish` called on a closed bus |
| `NIL_HANDLER` | `Subscribe` called with a nil handler |
| `PUBLISH_FAILED` | A handler panicked; wraps the `panix` error with event name in `Meta` |

### Example

```text
BUS.PUBLISH_FAILED: handler panicked | cause: INTERNAL.PANIC: panic: boom
  meta: event=user.created
```

Multiple panics produce an `errx.MultiError` with one `PUBLISH_FAILED` entry per panicked handler.

## Thread safety

- `Subscribe` / `Unsubscribe` / `Close` take a write lock (both `subs` map and `index` map are mutated together)
- `Publish` takes a read lock (snapshot copy), then releases before invoking handlers
- `Subscribers` / `Events` take a read lock
- `IsClosed` is lock-free (`atomic.Bool`)
- Concurrent `Publish` calls are fully parallel after the snapshot copy

## Tests

**39 tests, 94.6% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/busx  coverage: 94.6% of statements
```

Coverage includes:
- Subscribe: basic, multiple handlers, nil handler, closed bus, unique IDs
- Unsubscribe: basic, non-existent ID, removes event when empty, keeps others, double unsubscribe
- Publish: payload delivery, event name, context propagation, no subscribers, closed bus, multiple events
- Panic recovery: single panic, panic does not stop others, multiple panics aggregated, error-value panic
- Introspection: Subscribers count, Subscribers on closed, Events, Events on closed
- Lifecycle: Close, Close idempotent, Close clears subscriptions
- Thread safety: concurrent subscribe+publish, concurrent subscribe+unsubscribe, publish with close
- Error structure: CodeClosed fields, CodePublishFailed meta
- Snapshot isolation: subscribe-during-publish

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz.
Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkSubscribe                   ~436 ns/op     464 B/op     4 allocs/op
BenchmarkPublish_NoSubscribers        ~29 ns/op       0 B/op     0 allocs/op
BenchmarkPublish_1Handler            ~145 ns/op      48 B/op     2 allocs/op
BenchmarkPublish_10Handlers          ~389 ns/op     192 B/op     2 allocs/op
BenchmarkPublish_100Handlers        ~3507 ns/op    1824 B/op     2 allocs/op
BenchmarkUnsubscribe                 ~594 ns/op     464 B/op     4 allocs/op
BenchmarkPublish_WithPanic          ~2874 ns/op    1101 B/op    20 allocs/op
BenchmarkConcurrentPublish           ~147 ns/op      48 B/op     2 allocs/op
BenchmarkConcurrentPublish_Contention ~751 ns/op   216 B/op     3 allocs/op
```

### Analysis

**No subscribers:** ~29 ns, 0 allocs. The fast path checks `closed` (atomic) + read lock + empty map lookup. Effectively free.

**Single handler (hot path):** ~145 ns, 2 allocs. The 2 allocations are the snapshot slice and the `panix.Safe` closure. This is the steady-state cost per publish in most applications.

**10 handlers:** ~389 ns, still only 2 allocs. The snapshot `copy` and `panix.Safe` closures dominate. Linear scaling at ~24 ns per additional handler.

**100 handlers:** ~3.5 us, 2 allocs. Linear scaling holds. The single snapshot allocation grows but remains one alloc.

**Panic path:** ~2.9 us. Dominated by `recover()` + `errx.NewPanicError` + `errx.Wrap` + `MultiError.Add`. The 20 allocs come from panic recovery machinery. Acceptable because panics are exceptional.

**Concurrent publish:** ~147 ns under `RunParallel`. The `RWMutex` read path scales well. Under high goroutine contention (~751 ns), cost is dominated by goroutine scheduling.

**Subscribe/Unsubscribe:** ~436/594 ns. One-time costs during setup. The allocs include the `Bus` struct, maps (`subs` + `index`), and slice. `Unsubscribe` uses an internal index for O(1) event lookup, avoiding a full scan of all events.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| No subscribers | < 30 ns, 0 allocs | Free |
| Single handler | < 150 ns, 2 allocs | Invisible in any real workload |
| 10 handlers | < 400 ns | Well under 1 us |
| Panic recovery | < 3 us | Acceptable for exceptional path |
| Concurrent | < 150 ns | Scales across cores |

For context: a typical HTTP handler takes 50-500 us, a database query takes 1-50 ms. Event dispatch at 145 ns is noise.

## What busx does NOT do

| Concern | Owner |
|---------|-------|
| Async dispatch | Caller wraps in `go` |
| Typed payloads | Caller uses concrete struct via type assertion |
| Cross-process messaging | Message broker (NATS, Kafka, RabbitMQ) |
| Event persistence | Event store / database |
| Retry on handler failure | `retryx` or caller logic |
| Payload serialization | `encoding/json` or caller |
| Wildcard / pattern matching | Application-level routing |
| Priority ordering | Application-level sorting |

## File structure

```text
pkg/busx/
    busx.go         -- Bus struct, New(), Subscribe(), Unsubscribe(), Publish(), Close()
    errors.go       -- DomainBus, Code constants, error constructors
    busx_test.go    -- 39 tests, 94.6% coverage
    bench_test.go   -- 9 benchmarks
    README.md
```
