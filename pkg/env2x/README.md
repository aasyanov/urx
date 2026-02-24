# env2x

Reflection-based environment variable overlay for Go structs.
An **experimental** companion to [`envx`](../envx/) that trades compile-time
type safety for zero-boilerplate binding.

## Philosophy

`envx` requires one explicit `BindTo` call per field — ideal for small configs
where every binding is visible at a glance. `env2x` walks the struct via
reflection and discovers bindings automatically — better for large configs
with many fields.

Both can be combined: `env2x.Overlay` for bulk fields, then `envx.BindRequired`
for secrets that need explicit required-validation.

## Quick start

```go
type Config struct {
    Host     string `yaml:"host"`
    Port     int    `yaml:"port"`
    LogLevel string `yaml:"log_level"`
    Secret   string `yaml:"-"`          // never bound
}

cfg := Config{Host: "localhost", Port: 8080, LogLevel: "info"}

env := env2x.New(env2x.WithPrefix("APP"))
result := env2x.Overlay(env, &cfg)
// reads APP_HOST, APP_PORT, APP_LOG_LEVEL from environment
// Secret is skipped (yaml:"-")

if err := result.Err(); err != nil {
    log.Fatal(err)
}

for _, line := range result.Applied {
    log.Println(line) // "APP_PORT=3000 -> Config.Port"
}
```

## API

| Function / Type | Purpose |
|---|---|
| `New(opts...)` | Create an `Env` with prefix, lookup, and tag options |
| `Overlay(env, &cfg)` | Walk struct, read env vars, write into fields, return `Result` |
| `MapLookup(map)` | Create a test-friendly lookup function from a static map |

### Options

| Option | Default | Description |
|---|---|---|
| `WithPrefix(s)` | `""` | Prefix prepended to all variable names (`APP` -> `APP_HOST`) |
| `WithLookup(fn)` | `os.LookupEnv` | Custom lookup function for testing |
| `WithTag(s)` | `"yaml"` | Struct tag to read field names from |

### Result

| Field | Type | Description |
|---|---|---|
| `Available` | `[]string` | All env var names the struct could consume |
| `Found` | `[]string` | Subset that are currently set |
| `Applied` | `[]string` | Human-readable log of successful writes |
| `Errors` | `[]error` | Parse/assignment errors (`*errx.Error`) |

`Result.Err()` returns nil if no errors, or `*errx.MultiError` if any.

## Behavior details

- **Tag resolution**: The default tag is `yaml`, so existing config structs work
  without extra annotation. Use `WithTag("env")` or `WithTag("json")` to switch.
  Tag values are converted to SCREAMING_SNAKE_CASE: `log_level` → `LOG_LEVEL`.
  Hyphens become underscores: `log-level` → `LOG_LEVEL`. Fields tagged `-` are
  skipped entirely. The `,inline` option flattens the path.

- **Nested structs**: Path segments are joined with `_`. With prefix `APP` and
  nested struct `DB.Host`, the env var is `APP_DB_HOST`. Nil pointers are
  allocated lazily only when matching env vars exist.

- **Supported types**: `string`, `int`, `int8`..`int64`, `uint`..`uint64`,
  `float32`, `float64`, `bool`, `time.Duration`. Numeric overflow is checked
  before assignment. Unsupported types (slices, maps, interfaces) are silently
  skipped.

- **Input validation**: `Overlay` rejects `nil` or non-pointer targets with an
  `INVALID_INPUT` error.

- **Testability**: Inject `WithLookup(MapLookup(map))` to test without touching
  the real environment.

### envx vs env2x

| Aspect | envx | env2x |
|---|---|---|
| Binding | Explicit `BindTo` per field | Automatic via reflection |
| Type safety | Compile-time (generics) | Runtime (reflection) |
| Boilerplate | N lines for N fields | 1 line for any struct |
| Required vars | `BindRequired` | Not supported (use envx) |
| Testability | `WithLookup(func(string) string)` | `WithLookup(func(string) (string, bool))` |
| Dependencies | errx | errx |

## Error diagnostics

Every error is an `*errx.Error` with domain `ENV2`:

| Code | Meaning |
|---|---|
| `PARSE_FAILED` | Value could not be converted to the target type |
| `NOT_SETTABLE` | Field is unexported or not addressable |
| `UNSUPPORTED_TYPE` | Field type is not a supported scalar |
| `INVALID_INPUT` | `Overlay` received an invalid target value |

## Thread safety

`Overlay` is a pure function with no shared mutable state. It is safe for
concurrent use as long as callers do not concurrently mutate the same target
struct. `Env` is immutable after construction.

## Tests

**34 tests, 94.8% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/env2x  coverage: 94.8% of statements
```

Coverage includes:
- Overlay: all supported scalar types (string, int, float, bool, duration)
- Nested structs with path joining
- Nil pointer allocation
- Tag resolution: yaml, json, custom tags, hyphens, inline
- Skipped fields (yaml:"-")
- Parse errors for invalid values
- Numeric overflow detection
- Not-settable fields (unexported)
- Unsupported field type diagnostics
- Internal helper branches: pointer scalar assignment, pointer struct probing
- Custom prefix and lookup injection
- MapLookup helper
- Input validation: nil/non-pointer target returns `*errx.Error`

The remaining uncovered branches are mostly defensive reflection paths
(rare recursive traversal shapes and helper edge-cases).

## What env2x does NOT do

| Concern | Owner |
|---------|-------|
| Required variables | `envx.BindRequired` |
| File loading | `cfgx` |
| CLI flags | `clix` |
| Secrets management | caller / vault |

## File structure

```text
pkg/env2x/
    env2x.go       -- Env, Overlay(), Result, tag resolution, type conversion
    errors.go      -- DomainEnv2, Code constants, error constructors
    env2x_test.go  -- 34 tests, 94.8% coverage
    README.md
```
