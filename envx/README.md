# envx — Typed environment-variable binding

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/envx)
[License: MIT](../LICENSE)

Read environment variables into typed values with generics — no reflection, no struct tags. Optional prefix, injectable lookup, deferred validation, and direct overlay onto an existing config struct. Zero dependencies, Go 1.24+.

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> envx does not panic or exit on a missing or malformed variable. Binding records the problem; you decide what to do with it by calling [`Env.Validate`] once, after all binds. This lets a service report *every* configuration problem at startup in a single error, instead of failing on the first one.

## The Problem

Reading configuration from the environment by hand produces the same boilerplate in every service:

1. **Manual parsing.** `strconv.Atoi(os.Getenv("PORT"))` repeated for every variable, each with its own error handling.
2. **Scattered failure.** A typo in `PORT` crashes immediately, hiding the fact that `SECRET` is also missing — operators fix one problem, redeploy, and hit the next.
3. **Prefix repetition.** Every key is written `"APP_" + name`, easy to get inconsistent.
4. **Untestable.** Code that calls `os.Getenv` directly cannot be tested without mutating the process environment.
5. **No layering.** Env values need to override file defaults and be overridden by flags, but a raw `os.Getenv` call has no notion of "default" or "was it set".

envx removes the boilerplate (generic [`Bind`]), collects all failures (deferred [`Env.Validate`]), centralises the prefix ([`WithPrefix`]), is fully injectable ([`WithLookup`]), and overlays cleanly onto a config struct ([`BindTo`]).

## Architectural Position

envx **is** the environment layer of a configuration pipeline: string env → typed value, with validation deferred to one call. It **is not**:

- a config-file loader — use `cfgx`;
- a CLI flag parser — use `clix`;
- a reflection-based struct populator — bindings are explicit and type-checked at compile time;
- a secrets manager — it reads strings, it does not fetch or decrypt them.

### Position in the urx Stack

```text
┌──────────────────────────────────────────────────────────┐
│  main(): assemble config from defaults + file + env + CLI │
└────────────────────────┬─────────────────────────────────┘
                         │ overlays typed fields
┌────────────────────────▼─────────────────────────────────┐
│  envx   Env · Bind[T] · BindTo · Validate                │
└──────────────┬───────────────────────┬───────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼───────────────────┐
│  cfgx (file defaults)  │   │  clix (flag overrides)       │
│  Load → struct         │   │  parse → struct fields       │
└────────────────────────┘   └──────────────────────────────┘
```

## Architecture

```
   New(opts...) ──► Env{ cfg{ prefix, lookup }, vars[] }
                          │
   Bind[T](env, name, default)                BindTo[T](env, name, &field)
                          │                              │
              fullKey = prefix + "_" + NAME              │
                          │                              │
              raw, found = lookup(key)                   │
                          │                              │
              found?  ── yes ─► parse[T](raw) ─► value / parseErr
                  │                                      │
                  no ─► value = default          Var.target = &field
                          │                      *field = value
              append &Var[T] to env.vars         Ptr() → &field (alias)
                          ▼
              env.Validate() ── walks vars ──► errors.Join(
                                                 ErrMissing  (required & !found)
                                                 ErrInvalid  (parseErr != "")
                                               )
```

## How It Works

Binding is a two-phase design — **bind eagerly, validate lazily**:

1. **Resolve the key.** The variable name is upper-cased and, if a prefix was set, joined with `_`: `WithPrefix("APP")` + `PORT` → `APP_PORT`.
2. **Look up.** The injectable lookup (default [os.LookupEnv]) returns `(raw, found)`.
3. **Parse on presence.** When found, the raw string is parsed into `T`. A parse failure is *recorded on the Var* (not returned), and the resolved value stays at the default — so a bad value never silently becomes a zero.
4. **Register.** The [`Var`] is appended to the Env's list. For [`BindTo`]/[`BindRequiredTo`], the Var also stores a pointer to the caller's field so [`Var.Ptr`] aliases that field.
5. **Validate once.** [`Env.Validate`] walks every bound Var and joins all problems: required-but-absent → [`ErrMissing`], present-but-unparseable → [`ErrInvalid`].

This is why a service can surface all configuration errors at once. [`BindTo`] writes the resolved value into the target and links [`Var.Ptr`] to the same memory — the seam that lets env overlay a `cfgx`-loaded struct and hand off cleanly to `clix`.

## Normative Contracts


| Invariant               | Guarantee                                                                                      |
| ----------------------- | ---------------------------------------------------------------------------------------------- |
| Bind never fails inline | A bad value is recorded, surfaced only by [`Env.Validate`].                                    |
| Invalid keeps default   | A parse failure leaves the resolved value at the default.                                      |
| Absent keeps default    | An unset variable leaves the default (or target) unchanged.                                    |
| Validate is total       | One call reports every missing/invalid binding via [`errors.Join`].                            |
| Empty ≠ unset           | An env var set to "" is `Found()` and overrides the default; use [`Var.Found`] to distinguish. |
| BindTo aliases target   | [`Var.Ptr`] after [`BindTo`]/[`BindRequiredTo`] is the same pointer as the caller's field.     |
| No urx imports          | envx composes with cfgx/clix through pointers, not imports.                                    |


## Quick Start

```go
env := envx.New(envx.WithPrefix("APP"))

port := envx.Bind(env, "PORT", 8080)              // APP_PORT, default 8080
host := envx.Bind(env, "HOST", "localhost")       // APP_HOST
secret := envx.BindRequired[string](env, "SECRET") // APP_SECRET, must be set

if err := env.Validate(); err != nil {
	log.Fatal(err) // e.g. "envx: required environment variable not set: APP_SECRET"
}

fmt.Printf("%s:%d (secret loaded)\n", host.Value(), port.Value())
_ = secret
```

## Usage Scenarios

### The Precedence Pipeline (cfgx → envx → clix)

envx is the middle layer. Each layer writes through pointers into the same struct, so env overrides the file and is in turn overridden by flags:

```go
cfg := Config{Port: 8080, Host: "localhost"} // 1. defaults
_ = cfgx.Load("config.yaml", &cfg)           // 2. file

env := envx.New(envx.WithPrefix("APP"))      // 3. environment
port := envx.BindTo(env, "PORT", &cfg.Port)  //    APP_PORT > file
envx.BindTo(env, "HOST", &cfg.Host)

p := clix.New(os.Args[1:], "app", "service", // 4. flags (highest)
	clix.AddFlag(port.Ptr(), "port", "p", cfg.Port, "listen port"),
)

if err := errors.Join(env.Validate(), p.Err()); err != nil {
	log.Fatal(err)
}
```

### Required variables, reported together

```go
env := envx.New()
envx.BindRequired[string](env, "DATABASE_URL")
envx.BindRequired[string](env, "API_KEY")
if err := env.Validate(); err != nil {
	// Both missing variables appear in one joined error.
	log.Fatal(err)
}
```

### List values

```go
env := envx.New()
origins := envx.Bind(env, "CORS_ORIGINS", []string{"localhost"})
// CORS_ORIGINS="a.com, b.com ,c.com" → []string{"a.com", "b.com", "c.com"}
```

### Timestamps (RFC3339, matching clix)

```go
env := envx.New()
started := envx.Bind(env, "STARTED_AT", time.Time{})
// STARTED_AT="2025-01-02T15:04:05Z" → parsed time.Time
```

### Deterministic tests

```go
env := envx.New(
	envx.WithPrefix("APP"),
	envx.WithLookup(envx.MapLookup(map[string]string{
		"APP_PORT": "9090",
	})),
)
port := envx.Bind(env, "PORT", 8080)
```

## API


| Symbol           | Signature                                                         | Description                                   |
| ---------------- | ----------------------------------------------------------------- | --------------------------------------------- |
| `New`            | `New(opts ...Option) *Env`                                        | Create an Env.                                |
| `Bind`           | `Bind[T any](env *Env, name string, defaultVal T) *Var[T]`        | Read a variable, fall back to default.        |
| `BindRequired`   | `BindRequired[T any](env *Env, name string) *Var[T]`              | Read a required variable (zero default).      |
| `BindTo`         | `BindTo[T any](env *Env, name string, target *T) *Var[T]`         | Overlay onto an existing field.               |
| `BindRequiredTo` | `BindRequiredTo[T any](env *Env, name string, target *T) *Var[T]` | Required overlay onto a field.                |
| `Env.Validate`   | `Validate() error`                                                | Joined error of all missing/invalid bindings. |
| `Env.Vars`       | `Vars() []string`                                                 | Full names of all bound variables, in order.  |
| `Var.Value`      | `Value() T`                                                       | Resolved value (env or default).              |
| `Var.Ptr`        | `Ptr() *T`                                                        | Pointer to the resolved value.                |
| `Var.Found`      | `Found() bool`                                                    | Whether the variable was present.             |
| `Var.Key`        | `Key() string`                                                    | Full variable name (with prefix).             |
| `Var.Raw`        | `Raw() string`                                                    | Unparsed string read from the environment.    |
| `WithPrefix`     | `WithPrefix(prefix string) Option`                                | Prepend `PREFIX_` to all names.               |
| `WithLookup`     | `WithLookup(fn func(string) (string, bool)) Option`               | Inject the lookup source.                     |
| `MapLookup`      | `MapLookup(m map[string]string) func(string) (string, bool)`      | Static-map lookup for tests.                  |


## Configuration


| Option       | Default          | Effect                                              |
| ------------ | ---------------- | --------------------------------------------------- |
| `WithPrefix` | empty (no prefix) | Upper-cases and prepends `PREFIX_` to every name. Trailing `_` trimmed. |
| `WithLookup` | `os.LookupEnv`   | Injectable lookup; nil is ignored.                  |


## Supported Types

`string`, `bool`, `int`, `int32`, `int64`, `uint`, `float64`, [`time.Duration`], [`time.Time`] (RFC3339, matching clix), and `[]string` (comma-separated, whitespace-trimmed, empties dropped). Binding any other type records an [`ErrInvalid`] ("unsupported type") on [`Env.Validate`].

## Errors


| Error        | Condition                                                                                   |
| ------------ | ------------------------------------------------------------------------------------------- |
| `ErrMissing` | A required variable ([`BindRequired`]/[`BindRequiredTo`]) was not set.                      |
| `ErrInvalid` | A present variable could not be parsed into the requested type, or the type is unsupported. |


Both are reported only by [`Env.Validate`], joined via [`errors.Join`]; use [`errors.Is`] to test for either.

## Pitfalls

> [!WARNING]
> Binding does not return an error. `Bind`/`BindRequired` always return a `*Var`; a missing or malformed value surfaces only when you call `Env.Validate`. Forgetting `Validate` means silently running on defaults.

> [!WARNING]
> An empty string is a *set* value. `FOO=` makes `Found()` true and overrides the default with `""`. If you want "empty means use default", check `Var.Found()` explicitly.

> [!WARNING]
> `BindTo`/`BindRequiredTo` panic on a nil target. A nil destination pointer is a programming error caught immediately, not a runtime condition to handle. Pass the address of a real field.

## Safety and Concurrency

An `Env` is **not** safe for concurrent `Bind` calls — bindings mutate an internal slice. The intended pattern is to bind everything on one goroutine at startup, call `Validate` once, then read the resulting `Var` values (or the overlaid struct) concurrently, which is safe since binding has stopped. The lookup function may be called concurrently only if it is itself safe; the defaults ([os.LookupEnv], [`MapLookup`]) are. No `_Parallel` benchmarks apply: binding is a cold startup path, not a concurrent hot path.

## Benchmarks

> CPU: Intel i7-10510U · OS: Windows 10 · Go 1.24 · `-benchmem -count=1`


| Benchmark            | ns/op | B/op | allocs/op |
| -------------------- | ----- | ---- | --------- |
| Bind_Int             | 520   | 166  | 1         |
| Bind_String          | 364   | 165  | 1         |
| Bind_Duration        | 523   | 179  | 1         |
| Bind_Time            | 1122  | 184  | 1         |
| Bind_List            | 941   | 338  | 3         |
| Bind_Absent          | 541   | 160  | 1         |
| Validate             | 1429  | 184  | 6         |
| Parse_Int (internal) | 13    | 0    | 0         |
| Parse_Time (internal)| 71    | 0    | 0         |


### Analysis

- **One allocation per Bind** for scalar types — the heap-allocated `Var[T]` that the Env retains for later validation. This is the architectural floor: deferred validation requires keeping each binding alive. The value parse itself is allocation-free (`Parse_Int`: 0 allocs, ~13 ns; `Parse_Time`: 0 allocs, ~71 ns).
- **Bind_Time** parses RFC3339 (~1.1 µs) — dominated by `time.Parse`; still one Var allocation.
- **Bind_List allocates 3** — the `Var`, the backing array for the result slice, and its header. Proportional to element count.
- **Bind_Absent** skips parsing when the lookup misses; only the `Var` is allocated.
- **Validate scales with binding count** and allocates the joined error slice; it runs once at startup, off any hot path.
- **No parallel benchmarks** — `Env` is intentionally single-goroutine during bind; concurrent use applies only after startup to the resolved values.

## Quality


| Metric         | Value                   |
| -------------- | ----------------------- |
| Test functions | 26                      |
| Benchmarks     | 9                       |
| Fuzz targets   | 2                       |
| Examples       | 5                       |
| Coverage       | 100.0%                  |
| Race detector  | All pass                |
| External deps  | 0 (testify in dev only) |


## File Structure

```
envx/
├── envx.go            # package doc, New, Env.Validate, Env.Vars
├── types.go           # Env, Var[T], validator, accessors
├── options.go         # config, defaults, WithPrefix/WithLookup, MapLookup
├── bind.go            # Bind, BindRequired, BindTo, BindRequiredTo
├── parse.go           # generic value parsing per supported type
├── errors.go          # sentinel errors + internal wrappers
├── envx_test.go       # unit + table-driven tests
├── bench_test.go      # benchmarks per type
├── fuzz_test.go       # never-panic fuzz targets
├── footprint_test.go  # testx.AssertFootprint bounds for core types
├── example_test.go    # runnable GoDoc examples (incl. the pipeline)
└── README.md          # this file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.
