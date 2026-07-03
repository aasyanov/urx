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
│ main(): assemble config from defaults + file + env + CLI │
└────────────────────────┬─────────────────────────────────┘
                         │ overlays typed fields
┌────────────────────────▼─────────────────────────────────┐
│  envx   Env · Bind[T] · BindTo · Validate                │
└──────────────┬────────────────────────┬──────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼──────────────────┐
│  cfgx (file defaults)  │   │  clix (flag overrides)      │
│  Load → struct         │   │  parse → struct fields      │
└────────────────────────┘   └─────────────────────────────┘
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


| Option       | Default           | Effect                                                                  |
| ------------ | ----------------- | ----------------------------------------------------------------------- |
| `WithPrefix` | empty (no prefix) | Upper-cases and prepends `PREFIX_` to every name. Trailing `_` trimmed. |
| `WithLookup` | `os.LookupEnv`    | Injectable lookup; nil is ignored.                                      |




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

Three environments, two hardware classes, two operating systems. All values are **medians**. `B/op` and `allocs/op` are deterministic — they depend on code, not hardware.

### Environments


|            | Laptop                      | CI Server (Linux)     | CI Server (Windows)   |
| ---------- | --------------------------- | --------------------- | --------------------- |
| CPU        | Intel Core i7-10510U, 4C/8T | AMD EPYC 7763, 4 vCPU | AMD EPYC 9V74, 4 vCPU |
| TDP        | 15W (mobile, throttles)     | 280W (server, stable) | server, stable        |
| OS         | Windows 10                  | Ubuntu                | Windows Server 2022   |
| Go         | 1.26.2                      | 1.26                  | 1.26                  |
| GOMAXPROCS | 8                           | 4                     | 4                     |
| Runs       | 3 (`-count=3`)              | 3 (`-count=3`)        | 3 (`-count=3`)        |


No `_Parallel` benchmarks — binding is a cold startup path on a single goroutine, not a concurrent hot path.

### Bind / Validate


| Benchmark     | What it measures            | Laptop     | Linux      | Windows | B/op | allocs/op |
| ------------- | --------------------------- | ---------- | ---------- | ------- | ---- | --------- |
| Bind_Int      | Parse + store `Var[int]`    | 134 ns     | **129 ns** | 130 ns  | 160  | 1         |
| Bind_String   | Parse + store `Var[string]` | **120 ns** | 142 ns     | 128 ns  | 178  | 1         |
| Bind_Duration | `time.ParseDuration` path   | 177 ns     | **170 ns** | 161 ns  | 174  | 1         |
| Bind_List     | Split + parse slice         | 341 ns     | **353 ns** | 344 ns  | 348  | 3         |
| Bind_Absent   | Lookup miss, no parse       | **112 ns** | 112 ns     | 119 ns  | 168  | 1         |
| Bind_Time     | RFC3339 parse               | **157 ns** | 170 ns     | 184 ns  | 195  | 1         |
| Validate      | Cross-field check           | 356 ns     | **316 ns** | 400 ns  | 184  | 6         |
| Parse_Int     | Internal int parse only     | **9.4 ns** | 9.8 ns     | 11.3 ns | 0    | 0         |
| Parse_Time    | Internal time parse only    | **40 ns**  | 38 ns      | 47 ns   | 0    | 0         |




### Analysis

**Linux and Windows CI agree within ~15% on bind paths.** Scalar binds (`Bind_Int`, `Bind_String`, `Bind_Duration`) cluster at 130–170 ns on CI — one heap-allocated `Var[T]` per bind plus the parse. The laptop matches CI on serial binds; differences are noise, not OS effects.

**One allocation per scalar Bind — architectural floor.** Deferred validation requires retaining each `Var[T]` until `Validate` runs. The parse itself is alloc-free: `Parse_Int` is ~10 ns / 0 allocs; `Parse_Time` is ~40 ns / 0 allocs on CI.

**Bind_List — 3 allocs.** The `Var`, the backing array for the result slice, and its header — proportional to element count, not a surprise.

**Bind_Absent skips parsing on lookup miss.** ~112 ns on Linux — only the `Var` is allocated; no string parsing work.

**Validate scales with binding count — runs once at startup.** ~316 ns (Linux) / 400 ns (Windows) with six allocs for the joined error slice. Off any request hot path by design.

**No parallel benchmarks by intent.** Concurrent use applies only after startup to the resolved values; the `Env` itself is single-goroutine during bind.

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