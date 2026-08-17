# envx — Typed environment-variable binding

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/envx)
[Changelog](../CHANGELOG.md)
[License: MIT](../LICENSE)

Read environment variables into typed values with generics. Bind is the canonical overlay — compile-time types, no struct tags. `Walk` + `BindField` is an opt-in reflect helper for tagged trees. Optional prefix and fallback prefixes, injectable lookup, deferred validation. Zero dependencies, Go 1.24+.

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
- a secrets manager — it reads strings, it does not fetch or decrypt them;
- a `ProcessEnv(cfg, prefix, merge)` mega-wrapper — [Walk] yields fields, the caller filters, [BindField] feeds the same [Var] path as [Bind].

**Bind is canon.** [Walk] is opt-in reflection with an allowlist (`env` tags by default). Do not treat Walk as the default overlay.

### Position in the urx Stack

```text
┌──────────────────────────────────────────────────────────┐
│ main(): assemble config from defaults + file + env + CLI │
└────────────────────────┬─────────────────────────────────┘
                         │ overlays typed fields
┌────────────────────────▼─────────────────────────────────┐
│  envx   Env · Bind[T] · BindTo · Walk · Validate         │
└──────────────┬────────────────────────┬──────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼──────────────────┐
│  cfgx (file defaults)  │   │  clix (flag overrides)      │
│  Load → struct         │   │  parse → struct fields      │
└────────────────────────┘   └─────────────────────────────┘
```



## Architecture

```
   New(opts...) ──► Env{ cfg{ prefix, fallbacks, lookup }, vars[] }
                          │
   Bind[T](env, name, default)                BindTo[T](env, name, &field)
                          │                              │
              lookupName = prefix_NAME then fallbacks    │
                          │                              │
              raw, found = lookup(first hit)             │
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

Binding is a two-phase design — **bind eagerly, validate lazily**. The whole pipeline is a **cold** startup path (one goroutine, once per process). `Parse_Int` / `Parse_Time` are internal microbenches only — they are not a request hot path.

1. **Resolve the key (cold).** The variable name is upper-cased. [WithPrefix] is tried first, then each [WithFallbackPrefix] until one is set (first-fill-wins). A found primary is never overwritten by a fallback.
2. **Look up (cold).** The injectable lookup (default [os.LookupEnv]) returns `(raw, found)`. Empty string is Found.
3. **Parse on presence (cold).** When found, the raw string is parsed into `T`. A parse failure is *recorded on the Var* (not returned), and the resolved value stays at the default — so a bad value never silently becomes a zero. [Walk]/[BindField] uses the same Duration and int64 rules as [Bind] (ParseDuration requires a unit; raw `int64` and named int64 do not accept `"1s"` / `"5s"`).
4. **Register (cold).** The [`Var`] is appended to the Env's list. For [`BindTo`]/[`BindRequiredTo`], the Var also stores a pointer to the caller's field so [`Var.Ptr`] aliases that field. [`BindField`] registers a type-erased var (no typed Ptr for clix).
5. **Validate once (cold).** [`Env.Validate`] walks every bound Var and joins all problems: required-but-absent → [`ErrMissing`], present-but-unparseable → [`ErrInvalid`].

This is why a service can surface all configuration errors at once. [`BindTo`] writes the resolved value into the target and links [`Var.Ptr`] to the same memory — the seam that lets env overlay a `cfgx`-loaded struct and hand off cleanly to `clix`.

## Normative Contracts


| Invariant               | Guarantee                                                                                      |
| ----------------------- | ---------------------------------------------------------------------------------------------- |
| Bind never fails inline | A bad value is recorded, surfaced only by [`Env.Validate`].                                    |
| Invalid keeps default   | A parse failure leaves the resolved value at the default.                                      |
| Absent keeps default    | An unset variable leaves the default (or target) unchanged.                                    |
| Validate is total       | One call reports every missing/invalid binding via [`errors.Join`].                            |
| Empty ≠ unset           | `FOO=` is `Found()`. For `string` and `[]string` it overrides the default (empty string / empty slice). For int/bool/[`time.Duration`]/[`time.Time`]/etc, empty is Found + [`ErrInvalid`] and the **default is kept**. |
| BindTo aliases target   | [`Var.Ptr`] after [`BindTo`]/[`BindRequiredTo`] is the same pointer as the caller's field.     |
| First-fill-wins         | Primary prefix is tried first; a found value is never overwritten by a fallback.               |
| Bind is canon           | Explicit [`Bind`]/[`BindTo`] is the documented overlay; [`Walk`] is opt-in.                    |
| KeysFromEnvTag default  | [`Walk`] yields only `env` tags unless [`KeysFromYAML`] / JSON / TOML is passed.               |
| Walk does not mutate    | Nil pointer fields are skipped, not allocated. Caller/`cfgx.Load` must have filled the tree.   |
| Walk parse = Bind       | Exact [`time.Duration`] is ParseDuration-only (unit required). Builtin and named `int64` reject duration strings (`"5s"`). |
| Walk interface values   | An interface holding a **non-pointer** struct value yields no leaves (not addressable; Walk does not allocate a copy). |
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
cfg := DefaultConfig() // compiled-in defaults

if err := cfgx.Load("config.yaml", &cfg, cfgx.WithCreateIfMissing()); err != nil {
	if !errors.Is(err, cfgx.ErrValidationFailed) {
		log.Fatal(err) // I/O / parse / format — do not continue on partial decode
	}
	// optional: still continue so env/flags can repair file-only validation
}

env := envx.New(envx.WithPrefix("APP"))
port := envx.BindTo(env, "PORT", &cfg.Port) // absent keeps file
envx.BindTo(env, "HOST", &cfg.Host)

p := clix.New(os.Args[1:], "app", "my service",
	clix.AddFlag(port.Ptr(), "port", "p", cfg.Port, "listen port"), // def MUST be live field
	clix.AddFlag(&cfg.Host, "host", "", cfg.Host, "bind host"),
)

if errors.Is(p.Err(), clix.ErrHelp) {
	fmt.Print(p.Help())
	return
}
if errors.Is(p.Err(), clix.ErrVersion) {
	fmt.Println(p.Version())
	return
}
if err := errors.Join(env.Validate(), p.Err()); err != nil {
	log.Fatal(err)
}
if err := cfgx.Validate(&cfg, false); err != nil {
	log.Fatal(err)
}
```

`Required()` is CLI-presence only. Share only `string`, `int`, `bool`, `float64`, `time.Duration`, and `time.Time` with `AddFlag`. YAML duration `30s` works; JSON needs nanoseconds or a string you parse. Do not `cfgx.Validate(..., true)` after flags.



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



### Dual prefix (first-fill-wins)

```go
env := envx.New(
	envx.WithPrefix("SMCORE"),
	envx.WithFallbackPrefix("SMP"),
)
port := envx.Bind(env, "PORT", 8080)
// SMCORE_PORT if set, else SMP_PORT. Never merges two found values.
```



### Named types and TextUnmarshaler

```go
type LogLevel string
level := envx.Bind(env, "LOG_LEVEL", LogLevel("info"))
```

Defined types whose underlying kind is a supported builtin parse without tags (`type Port int`, `type Level string`). Named [`time.Time`] (`type Stamp time.Time`) parses RFC3339 via [Bind]/[BindTo]. Named int64 — including `type MyDur time.Duration` — parses as an integer only; `"5s"` is [`ErrInvalid`]. Custom duration types should implement `encoding.TextUnmarshaler` or bind as [`time.Duration`]. Types whose pointer implements `encoding.TextUnmarshaler` use `UnmarshalText`.

Struct, map, and non-string slice types record [`ErrInvalid`] **when the key is present**. An absent unsupported type does not error — parse runs only on found keys.

[Walk] does not yield named [`time.Time`] as a leaf (`*Stamp` does not implement `TextUnmarshaler`; Walk sees a struct with no exported fields). Use [Bind]/[BindTo] for those fields. An interface holding a non-pointer struct value yields no leaves (the boxed value is not addressable; Walk does not allocate a copy, because [BindField] would write the copy).

### Opt-in Walk (filter, then BindField)

Walk does not read env and does not write. The caller skips secrets and feeds the same `Validate` path. Default keys are `env` tags, not YAML:

```go
env := envx.New(
	envx.WithPrefix("SMCORE"),
	envx.WithFallbackPrefix("SMP"),
)
for f := range envx.Walk(&cfg, envx.KeysFromYAML()) {
	if f.Path == "SQL.Password" {
		continue
	}
	envx.BindField(env, f)
}
if err := env.Validate(); err != nil { ... }
```

`BindField` does not return a typed [`Var`]; clix flags stay on explicit [`BindTo`] / [`Var.Ptr`]. A nil nested pointer is not allocated — that env key will not apply until the caller (or `cfgx.Load`) has filled the struct. An interface field holding a non-pointer struct value is skipped (not addressable).

After overlay, re-validate nested file validators with `cfgx.Validate(&cfg, false)`.

## API


| Symbol               | Signature                                                         | Description                                              |
| -------------------- | ----------------------------------------------------------------- | -------------------------------------------------------- |
| `New`                | `New(opts ...Option) *Env`                                        | Create an Env. Nil options are ignored.                  |
| `Option`             | functional option for [New]                                       | Nil values are ignored.                                  |
| `WalkOption`         | functional option for [Walk]                                      | Nil values are ignored.                                  |
| `Bind`               | `Bind[T any](env *Env, name string, defaultVal T) *Var[T]`        | Read a variable, fall back to default.                   |
| `BindRequired`       | `BindRequired[T any](env *Env, name string) *Var[T]`              | Read a required variable (zero default).                 |
| `BindTo`             | `BindTo[T any](env *Env, name string, target *T) *Var[T]`         | Overlay onto an existing field.                          |
| `BindRequiredTo`     | `BindRequiredTo[T any](env *Env, name string, target *T) *Var[T]` | Required overlay onto a field.                           |
| `BindField`          | `BindField(env *Env, f Field)`                                    | Type-erased overlay of one [Walk] field.                 |
| `Walk`               | `Walk(dst any, opts ...WalkOption) iter.Seq[Field]`               | Yield bindable leaves; does not read env or mutate.      |
| `Field`              | `Key`, `Path`, `Ptr`                                              | One Walk leaf (`Ptr` is `*T`, never nil).                |
| `KeysFromEnvTag`     | `KeysFromEnvTag() WalkOption`                                     | Allowlist `env` tags (default).                          |
| `KeysFromYAML`       | `KeysFromYAML() WalkOption`                                       | YAML-tag keys, kebab→`_`, UPPER, inline flatten.         |
| `KeysFromJSON`       | `KeysFromJSON() WalkOption`                                       | JSON-tag keys.                                           |
| `KeysFromTOML`       | `KeysFromTOML() WalkOption`                                       | TOML-tag keys.                                           |
| `Env.Validate`       | `Validate() error`                                                | Joined error of all missing/invalid bindings.            |
| `Env.Vars`           | `Vars() []string`                                                 | Full names of all bound variables, in order (found key). |
| `Var.Value`          | `Value() T`                                                       | Resolved value (env or default).                         |
| `Var.Ptr`            | `Ptr() *T`                                                        | Pointer to the resolved value.                           |
| `Var.Found`          | `Found() bool`                                                    | Whether the variable was present.                        |
| `Var.Key`            | `Key() string`                                                    | Found key, or primary if unset.                          |
| `Var.Raw`            | `Raw() string`                                                    | Unparsed string read from the environment.               |
| `WithPrefix`         | `WithPrefix(prefix string) Option`                                | Primary `PREFIX_` on all names.                          |
| `WithFallbackPrefix` | `WithFallbackPrefix(prefix string) Option`                        | Tried only when the primary key is unset.                |
| `WithLookup`         | `WithLookup(fn func(string) (string, bool)) Option`               | Inject the lookup source.                                |
| `MapLookup`          | `MapLookup(m map[string]string) func(string) (string, bool)`      | Static-map lookup for tests.                             |




## Configuration


| Option               | Default           | Effect                                                                  |
| -------------------- | ----------------- | ----------------------------------------------------------------------- |
| `WithPrefix`         | empty (no prefix) | Upper-cases and prepends `PREFIX_` to every name. Trailing `_` trimmed. |
| `WithFallbackPrefix` | none              | Extra prefixes after primary; first hit wins; never overwrites found.   |
| `WithLookup`         | `os.LookupEnv`    | Injectable lookup; nil is ignored.                                      |
| nil `Option`         | skipped           | [New] ignores nil options, matching [Walk].                             |




## Supported Types

`string`, `bool`, `int`, `int32`, `int64`, `uint` (platform width), `float64`, exact [`time.Duration`] (ParseDuration, unit required — matching clix), [`time.Time`] and named types convertible to it (RFC3339, matching clix), and `[]string` (comma-separated, whitespace-trimmed, empties dropped). The reflect path used by named types and [Walk]/[BindField] also accepts other integer and float kinds (`int8`…`int64`, `uint8`…`uint64`, `float32`). Defined types whose underlying kind is a supported builtin parse without tags. Types whose pointer implements `encoding.TextUnmarshaler` use `UnmarshalText`. Binding any other type (including struct, map, and non-string slices) records an [`ErrInvalid`] ("unsupported type") on [`Env.Validate`] **when the key is present**. An absent unsupported type does not error — parse runs only on found keys.

Exact [`time.Duration`] never parses as a raw int64: `"90"` is invalid; `"90ns"` / `"1m30s"` are valid. Named int64 — including `type MyDur time.Duration` — parses as an integer only: `"90"` is 90 nanoseconds, `"5s"` is [`ErrInvalid`]. Custom duration types should implement `encoding.TextUnmarshaler` or bind as [`time.Duration`].

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
> An empty string is a *set* value (`Found()` is true). For `string` and `[]string`, `FOO=` overrides the default with `""` / an empty slice. For int, bool, [`time.Duration`], [`time.Time`], and other non-string types, empty is Found + [`ErrInvalid`] and the default is kept.

> [!WARNING]
> `BindTo`/`BindRequiredTo`/`BindField`/`Walk` panic on a nil destination. A nil pointer is a programming error caught immediately, not a runtime condition to handle.

> [!WARNING]
> Fallback prefixes are first-fill-wins, not a merge of two found values. An empty primary (`SMCORE_HOST=`) is Found and blocks `SMP_HOST`.

> [!WARNING]
> `Walk` does not allocate nil nested structs. `CHILD_PORT` is ignored while `Child == nil`. Fill the tree in Go defaults or via `cfgx.Load` first. Slice/map of structs are not descended. Two fields that alias the same pointer are walked once — the second path is skipped. An interface holding a non-pointer struct value yields no leaves (not addressable). Named [`time.Time`] is not a Walk leaf — use [Bind]/[BindTo].

> [!WARNING]
> `BindField` does not return `*Var[T]`. Operator flags stay 3–5 manual `clix.AddFlag` calls on [`BindTo`] pointers.

> [!WARNING]
> Exact [`time.Duration`] requires a unit (`90s`, `90ns`). A unitless `90` is [`ErrInvalid`] on both [Bind] and [Walk] — it is never 90 nanoseconds. That matches clix. Named int64 (including `type MyDur time.Duration`) is the opposite: `"90"` is 90 nanoseconds and `"5s"` is [`ErrInvalid`].



## Safety and Concurrency

An `Env` is **not** safe for concurrent `Bind` calls — bindings mutate an internal slice. The intended pattern is to bind everything on one goroutine at startup, call `Validate` once, then read the resulting `Var` values (or the overlaid struct) concurrently, which is safe since binding has stopped. The lookup function may be called concurrently only if it is itself safe; the defaults ([os.LookupEnv], [`MapLookup`]) are. No `_Parallel` benchmarks apply: binding is a cold startup path, not a concurrent hot path.

## Benchmarks

Three environments, two hardware classes, two operating systems. All values are **medians**. `B/op` and `allocs/op` are deterministic — they depend on code, not hardware.

### Environments


|            | Laptop                      | CI Server (Linux)     | CI Server (Windows) |
| ---------- | --------------------------- | --------------------- | ------------------- |
| CPU        | Intel Core i7-10510U, 4C/8T | Intel Xeon 6973P-C    | AMD EPYC 7763       |
| TDP        | 15W (mobile, throttles)     | 280W (server, stable) | server, stable      |
| OS         | Windows 10                  | Ubuntu                | Windows Server 2022 |
| Go         | 1.26.2                      | 1.26                  | 1.26                |
| GOMAXPROCS | 8                           | 4                     | 4                   |
| Runs       | 3 (`-count=3`)              | 3 (`-count=3`)        | 3 (`-count=3`)      |


No `_Parallel` benchmarks — binding is a cold startup path on a single goroutine, not a concurrent hot path.

### Bind / Validate

Bind benches reset the Env's var list each op so ns/op is a single bind, not an accumulating slice. `B/op` and `allocs/op` are from this module (2026-08-17). CI ns/op are last published matrix medians.

| Benchmark     | Class | What it measures            | Laptop     | Linux        | Windows  | B/op | allocs/op |
| ------------- | ----- | --------------------------- | ---------- | ------------ | -------- | ---- | --------- |
| Bind_Int      | cold  | Parse + store `Var[int]`    | 134 ns     | **98.8 ns**  | 155.6 ns | 104  | 2         |
| Bind_String   | cold  | Parse + store `Var[string]` | **120 ns** | 88.9 ns      | 145.9 ns | 112  | 2         |
| Bind_Duration | cold  | `time.ParseDuration` path   | 177 ns     | **126.5 ns** | 193.4 ns | 104  | 2         |
| Bind_List     | cold  | Split + parse slice         | 341 ns     | **229.9 ns** | 384.6 ns | 296  | 4         |
| Bind_Absent   | cold  | Lookup miss, no parse       | **112 ns** | 86.4 ns      | 120.9 ns | 96   | 1         |
| Bind_Time     | cold  | RFC3339 parse               | **157 ns** | 114.5 ns     | 180 ns   | 136  | 2         |
| Validate      | cold  | Cross-field check           | 356 ns     | **214.1 ns** | 417 ns   | 184  | 6         |
| Parse_Int     | micro | Internal int parse only     | **9.4 ns** | 7.3 ns       | 9.1 ns   | 8    | 1         |
| Parse_Time    | micro | Internal time parse only    | **40 ns**  | 28.4 ns      | 40.9 ns  | 24   | 1         |


### Analysis

**Linux CI is faster than Windows CI on every Bind row; they do not agree within 15%.** `Bind_Int` is 98.8 ns (Linux) vs 155.6 ns (Windows) in the table — about 1.6×. `Validate` is 214.1 ns vs 417 ns. That is a server-CPU difference, not noise. Laptop ns/op on the 15 W i7 throttles; treat the laptop column as a cooler-run snapshot, not a cross-OS comparison.

**Two allocations per scalar Bind when the variable is set.** One is the heap `Var[T]` retained until `Validate` (architectural floor for deferred validation). The second is the generic type-switch box inside `parse` (`any(v).(T)`). `Bind_Absent` skips parse and is 1 alloc / 96 B — only the `Var`. With no fallback prefixes, lookup does not allocate a candidate map.

**Parse_Int / Parse_Time are not 0-alloc.** They pay the same type-switch box (8 B / 24 B). They are internal microbenches, not a request hot path. Do not read them as a user-API budget.

**Bind_List — 4 allocs.** The `Var`, the parse box, the backing array for the result slice, and its header — proportional to element count.

**Validate scales with binding count — runs once at startup.** 214.1 ns (Linux) / 417 ns (Windows) with six allocs for the joined error slice. Off any request hot path by design.

**No parallel benchmarks by intent.** Concurrent use applies only after startup to the resolved values; the `Env` itself is single-goroutine during bind.

## Quality


| Metric         | Value                   |
| -------------- | ----------------------- |
| Test functions | 79                      |
| Benchmarks     | 9                       |
| Fuzz targets   | 2                       |
| Examples       | 7                       |
| Coverage       | 100.0%                  |
| Race detector  | All pass                |
| External deps  | 0 (testify in tests only) |
| Gate           | M evidenced; craft 0–2 green locally |

```
go test -race -count=1 ./envx/
golangci-lint run ./envx/
go test ./envx/ -coverprofile=cover.out
go test ./envx/ -run='^$' -bench=. -benchmem -count=3
go test ./envx/ -fuzz=FuzzParse -fuzztime=30s
go test ./envx/ -fuzz=FuzzBindValidate -fuzztime=30s
```




## File Structure

```
envx/
├── envx.go            # package doc, New, Env.Validate, Env.Vars
├── types.go           # Env, Var[T], validator, accessors, lookupName
├── options.go         # config, WithPrefix/WithFallbackPrefix/WithLookup, MapLookup
├── bind.go            # Bind, BindTo, BindField
├── parse.go           # exact type-switch + named/TextUnmarshaler default
├── walk.go            # Walk, Field, KeysFrom*
├── errors.go          # sentinel errors + internal wrappers
├── envx_test.go       # unit + table-driven tests
├── parse_named_test.go
├── parse_kind_test.go # named kinds + extra integer/float widths
├── walk_test.go
├── bench_test.go      # benchmarks per type (Bind resets vars each op)
├── fuzz_test.go       # never-panic fuzz targets
├── footprint_test.go  # testx.AssertFootprint bounds for core types
├── example_test.go    # runnable GoDoc examples
└── README.md          # this file
```



## License

MIT — see [LICENSE](../LICENSE) in the repository root.