# dicx

Reflection-based dependency injection container with lifecycle management.

## Philosophy

**One job: wire dependencies.** `dicx` registers constructors, resolves them lazily via reflection, and manages singleton/transient lifetimes. `Start` eagerly resolves singletons and runs lifecycle hooks; `Stop` tears them down in reverse dependency order. It does not provide scoped lifetimes, HTTP middleware, or config loading.

## Quick start

```go
c := dicx.New()

c.Provide(func() (*sql.DB, error) {
    return sql.Open("postgres", dsn)
})

c.Provide(func(db *sql.DB) (*UserRepo, error) {
    return &UserRepo{db: db}, nil
})

if err := c.Start(ctx); err != nil {
    log.Fatal(err)
}
defer c.Stop(ctx)

repo := dicx.MustResolve[*UserRepo](c)
```

## API

| Function / Method | Description |
|---|---|
| `New() *Container` | Create an empty container |
| `c.Provide(ctor, opts...) error` | Register a constructor |
| `Resolve[T](c) (T, error)` | Resolve a dependency by type |
| `MustResolve[T](c) T` | Resolve or panic |
| `c.Start(ctx) error` | Instantiate all singletons in dependency order. Nil ctx → `context.Background()` |
| `c.Stop(ctx) error` | Tear down singletons in reverse order. Nil ctx → `context.Background()` |
| `c.Stats() Stats` | Container statistics |
| `c.IsClosed() bool` | Whether container is stopped |

### Options

| Option | Default | Description |
|---|---|---|
| `WithLifetime(lt)` | `Singleton` | `Singleton` or `Transient` |

### Constructor rules

```go
func() T                     // no deps, no error
func() (T, error)            // no deps, may fail
func(A, B) T                 // deps resolved by type
func(A, B) (T, error)        // deps resolved by type, may fail
```

All parameters are resolved recursively from the container.

## Behavior details

- **Lazy resolution**: `Resolve` instantiates the dependency (and its transitive deps) on first call. Singletons are cached; transients are created fresh each time.

- **Three-step resolution**: dependency resolution captures provider/cache state under lock, executes constructors outside lock, and commits singleton cache under lock. This avoids global-lock deadlocks around user code.

- **Start / Stop**: `Start(ctx)` freezes the container (`Provide` is rejected), resolves all singletons, then calls `Start(ctx)` on each component that implements `Starter` in dependency order. `Stop(ctx)` calls `Stop(ctx)` on singleton `Stopper`s in reverse dependency order and aggregates errors.

- **Cycle detection**: `Resolve` tracks the resolution chain. If a cycle is detected (`A → B → A`), it returns `CodeCyclicDep` with the cycle path.

- **Panic recovery**: constructor calls are wrapped by `panix.Safe`. A panicking constructor is wrapped as `CodeConstructorFailed`.

## Error diagnostics

All errors are `*errx.Error` with `Domain = "DI"`.

### Codes

| Code | When |
|---|---|
| `BAD_CONSTRUCTOR` | Constructor signature doesn't match expected patterns |
| `ALREADY_PROVIDED` | Type already registered |
| `FROZEN` | `Provide` after `Start` |
| `MISSING_DEP` | Required dependency not registered |
| `CYCLIC_DEP` | Circular dependency detected |
| `CONSTRUCTOR_FAILED` | Constructor returned an error or panicked |
| `LIFECYCLE_FAILED` | Start or Stop hook returned an error |

### Example

```text
DI.MISSING_DEP: *sql.DB not registered | meta: type=*sql.DB, trace=*UserRepo -> *sql.DB
DI.CYCLIC_DEP: cyclic dependency detected | meta: cycle=A -> B -> A
```

## Thread safety

- `Provide` is guarded by `sync.RWMutex` and rejected after `Start` begins
- `Resolve` / `MustResolve` are concurrent-safe; singleton instantiation is deduplicated with in-flight tracking
- Constructor execution and lifecycle hooks run outside the global container lock to avoid lock contention and deadlocks
- `Start` / `Stop` serialize container state transitions

## Tests

Comprehensive unit tests cover constructor validation, resolve paths, cycle detection, lifecycle orchestration, panic handling, and concurrent resolve.

```bash
go test -race -count=1 ./...
```

Test coverage includes:
- Provide: valid constructors, invalid signatures, duplicate registration, after freeze
- Resolve: singleton, transient, with deps, missing dep, cyclic dep
- Start: topological order, constructor failure, already started
- Stop: reverse order, io.Closer, already stopped
- Generic API: Resolve[T], MustResolve[T]
- Panic recovery: constructor panic
- Stats / IsClosed: snapshot correctness
- dependsOnAnyLocked: direct, transitive, empty chain
- Thread safety: concurrent resolve, concurrent singleton dedup

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz. The table below reflects a recent run (`-count=1`).

```text
BenchmarkProvide                          ~538 ns/op      513 B/op      7 allocs/op
BenchmarkResolve_Singleton_Cached          ~67 ns/op        0 B/op      0 allocs/op
BenchmarkResolve_Singleton_Cold          ~3058 ns/op     1682 B/op     25 allocs/op
BenchmarkResolve_Transient                ~456 ns/op       56 B/op      3 allocs/op
BenchmarkResolve_DeepChain               ~8677 ns/op     2989 B/op     52 allocs/op
BenchmarkStart                           ~7403 ns/op     2611 B/op     44 allocs/op
BenchmarkConcurrentResolve                ~144 ns/op        0 B/op      0 allocs/op
BenchmarkResolve_WithDeps_Cached           ~67 ns/op        0 B/op      0 allocs/op
BenchmarkMustResolve_Cached                ~62 ns/op        0 B/op      0 allocs/op
BenchmarkConcurrentResolve_Contention     ~119 ns/op        0 B/op      0 allocs/op
```

### Analysis

**Resolve (singleton, cached):** ~67 ns, 0 allocs. Fast map/cache path suitable for hot execution paths.

**Resolve (singleton, cold):** ~3.1 us, 25 allocs. First-time resolution pays reflection + recursive construction cost.

**Resolve (transient):** ~456 ns, 3 allocs. Constructor call on each resolve.

**Deep chain (5 deps):** ~8.7 us, 52 allocs. Recursive resolution cost grows with graph depth.

**Start:** ~7.4 us, 44 allocs. Resolves singletons and runs lifecycle hooks once.

**Concurrent resolve (cached):** ~119-144 ns under `RunParallel`. Stable under contention with zero allocations.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| Singleton (cached) | ~67 ns, 0 allocs | Hot-path friendly |
| Singleton (cold) | ~3.1 us, 25 allocs | One-time per type |
| Transient | ~456 ns, 3 allocs | Constructor cost dominates |
| Concurrent | ~119-144 ns, 0 allocs | Scales under contention |
| Start | ~7.4 us | One-time startup |

## What dicx does NOT do

| Concern | Owner |
|---------|-------|
| Scoped lifetime (per-request) | caller |
| Config file loading | `viper` / caller |
| HTTP middleware | caller |
| Named dependencies | caller uses wrapper types |
| Interface binding | caller registers concrete type |

## File structure

```text
pkg/dicx/
    dicx.go       -- Container, New(), Provide(), Start(), Stop()
    generic.go    -- Resolve[T](), MustResolve[T]()
    provider.go   -- provider struct, constructor validation
    resolve.go    -- three-step resolve, in-flight dedup, safeCall()
    lifecycle.go  -- Starter and Stopper interfaces
    errors.go     -- DomainDI, Code constants, error constructors
    dicx_test.go  -- 67 tests, 94.1% coverage
    bench_test.go -- 10 benchmarks
    README.md
```
