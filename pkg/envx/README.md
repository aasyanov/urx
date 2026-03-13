# envx

Typed environment variable binding with injectable lookup for industrial Go services.

## Philosophy

`envx` follows the **urx** principle: no reflection, no struct tags, no magic. Variables are bound explicitly with compile-time type safety via generics. The lookup function is injectable (`WithLookup`) so tests never touch the real environment — the same pattern used across the ecosystem.

## Quick Start

```go
env := envx.New(envx.WithPrefix("APP"))

port   := envx.Bind(env, "PORT", 8080)
host   := envx.Bind(env, "DB_HOST", "localhost")
debug  := envx.Bind(env, "DEBUG", false)
secret := envx.BindRequired[string](env, "SECRET")

if err := env.Validate(); err != nil {
    log.Fatal(err) // ENV.MISSING: APP_SECRET
}

fmt.Println(port.Value())   // 8080 or from APP_PORT
fmt.Println(secret.Value()) // from APP_SECRET
```

## Testability

```go
env := envx.New(
    envx.WithPrefix("APP"),
    envx.WithLookup(envx.MapLookup(map[string]string{
        "APP_PORT":   "9090",
        "APP_SECRET": "test-key",
    })),
)
```

No `os.Setenv`, no cleanup, no race conditions in parallel tests.

## API

| Function | Description |
|---|---|
| `New(opts...)` | Create an Env instance |
| `Bind[T](env, name, default)` | Bind optional variable with default |
| `BindTo[T](env, name, &target)` | Bind and write directly into target pointer (panics on nil target) |
| `BindRequired[T](env, name)` | Bind required variable |
| `env.Validate()` | Check all bindings, returns `*errx.MultiError` |
| `env.Vars()` | List all bound variable names |
| `var.Value()` | Get resolved value |
| `var.Ptr()` | Pointer to value (for CLI flag binding) |
| `var.Found()` | Whether variable was set in environment |
| `var.Key()` | Full variable name with prefix |

## Options

| Option | Default | Description |
|---|---|---|
| `WithPrefix(p)` | none | Prepend `P_` to all variable names |
| `WithLookup(fn)` | `os.LookupEnv` | Custom lookup `func(string) (string, bool)` |
| `MapLookup(m)` | — | Helper: lookup from `map[string]string` |

## Supported Types

`string`, `int`, `int64`, `float64`, `bool`, `time.Duration`

## Error Diagnostics

All errors use domain **ENV** with [errx] structured metadata:

| Code | Meta | Meaning |
|---|---|---|
| `MISSING` | `var` | Required variable not set |
| `INVALID` | `var`, `reason` | Value could not be parsed to target type |

Multiple errors are collected into `*errx.MultiError` by `Validate()`.

## Thread safety

`Env` is designed for **initialization phase** — bind all variables at startup, call `Validate()`, then read `Var.Value()` concurrently. No locking needed because values are immutable after `Validate()`.

## Tests

**37 tests, 97.5% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/envx  coverage: 97.5% of statements
```

Coverage includes: Bind for all supported types (string, int, int64, float64, bool, duration), BindTo (pointer writing for all types), BindRequired (missing variable error), Validate (collects multiple errors into MultiError), prefix (correct prepending), MapLookup (static map with found/not-found semantics), WithLookup (custom lookup function), empty-string vs unset distinction, Var accessors (Value(), Ptr(), Found(), Key()), error constructors and domain/code constants.

## Benchmarks

```text
BenchmarkBind_String           ~186 ns/op    128 B/op    3 allocs/op
BenchmarkBind_Int              ~190 ns/op    112 B/op    3 allocs/op
BenchmarkBindRequired          ~190 ns/op    128 B/op    3 allocs/op
BenchmarkValidate_AllPresent   ~55 ns/op      0 B/op    0 allocs/op
BenchmarkValidate_Missing      ~780 ns/op    584 B/op    7 allocs/op
BenchmarkValue                   ~3 ns/op      0 B/op    0 allocs/op
BenchmarkVars                    ~8 ns/op      0 B/op    0 allocs/op
```

### Analysis

**Bind** is a one-time startup cost (~190 ns per variable). **Value()** is ~3 ns — a simple field read, free on the hot path. **Validate** with all present ~55 ns, 0 allocs. The only allocation path is error construction on missing/invalid vars.

## File structure

```text
pkg/envx/
    envx.go        -- Env, Var[T], New(), Bind(), BindTo(), BindRequired(), Validate()
    errors.go      -- DomainEnv, Code constants, error constructors
    envx_test.go   -- 37 tests, 97.5% coverage
    bench_test.go  -- 7 benchmarks
    README.md
```
