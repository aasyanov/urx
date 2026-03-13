# cfgx

Configuration file loader for industrial Go services. Supports YAML, JSON, and TOML.

## Philosophy

`cfgx` handles one step in the config pipeline: **file to struct** and **struct to file**. It does not parse env vars (that's `envx`), does not parse flags (that's `clix`), and does not orchestrate the pipeline (that's your `main()`). The three packages compose through simple pointer sharing.

## Quick start

```go
cfg := NewDefaultConfig()
if err := cfgx.Load("config.yaml", &cfg); err != nil {
    log.Fatal(err)
}
```

### Full pipeline

```go
func main() {
    // 1. Struct with defaults
    cfg := ServerConfig{Host: "0.0.0.0", Port: 8080, Timeout: 30}

    // 2. Load from file (overrides defaults)
    cfgx.Load("config.yaml", &cfg, cfgx.WithAutoFix(), cfgx.WithCreateIfMissing())

    // 3. Save after autofix
    cfgx.Save("config.yaml", &cfg)

    // 4. Env overrides (via envx pointer bridge)
    env := envx.New(envx.WithPrefix("APP"))
    envx.BindTo(env, "PORT", &cfg.Port)
    envx.BindTo(env, "HOST", &cfg.Host)
    if err := env.Validate(); err != nil {
        log.Fatal(err)
    }

    // 5. CLI flag overrides (via clix pointer bridge)
    p := clix.New(os.Args[1:], "myapp", "my service",
        clix.AddFlag(&cfg.Port, "port", "p", cfg.Port, "listen port"),
    )
    if err := p.Err(); err != nil { ... }
    if err := p.Run(); err != nil { ... }

    // cfg is ready — file < env < flags
}
```

## API

| Function | Description |
|---|---|
| `Load(path, &dst, opts...)` | Read file into struct |
| `Save(path, &src, opts...)` | Write struct to file |

### Options

| Option | Default | Description |
|---|---|---|
| `WithFormat(f)` | auto-detect | Force YAML / JSON / TOML |
| `WithAutoFix()` | false | Call `Validator.Validate(true)` after load |
| `WithCreateIfMissing()` | false | Write defaults to disk if file absent |
| `WithReader(fn)` | `os.ReadFile` | Injectable reader for testing |
| `WithWriter(fn)` | `os.WriteFile` | Injectable writer for testing |

### Validator interface

```go
type Validator interface {
    Validate(fix bool) []error
}
```

If `dst` implements `Validator`, `Load` calls it after unmarshal. With `WithAutoFix()`, `fix=true` — the struct may self-correct and should return only validation errors that remain after fixing. Without it, `fix=false` — validation errors are returned without mutating the struct. `Load` returns validation failures as `*errx.MultiError`.

## Behavior details

- **Format detection**: The file extension determines the codec (`.yaml`/`.yml` → YAML, `.json` → JSON, `.toml` → TOML). Use `WithFormat` to override when the extension is ambiguous.

- **CreateIfMissing**: When the file does not exist and `WithCreateIfMissing()` is set, `Load` runs the `Validator` (if implemented) before writing to disk. With `WithAutoFix()`, defaults are corrected first, then the corrected struct is saved. Without autofix, validation errors are returned and nothing is written.

- **Input validation**: `Load` rejects `nil` or non-pointer `dst` with `INVALID_INPUT`. `Save` rejects `nil` `src` and nil-pointed `src`.

- **Validation flow**: After successful unmarshal, if `dst` implements `Validator`, `Load` calls `Validate(fix)`. Any non-nil errors returned are wrapped into `*errx.MultiError` with code `VALIDATION_FAILED`.

- **Testability**: Inject `WithReader` / `WithWriter` to test without touching the filesystem.

## Error diagnostics

All errors use domain **CONFIG** with [errx] structured metadata:

| Code | Meta | Meaning |
|---|---|---|
| `NOT_FOUND` | `path` | File does not exist |
| `READ_FAILED` | `path` | I/O error |
| `PARSE_FAILED` | `path` | Unmarshal error |
| `WRITE_FAILED` | `path` | Could not save |
| `UNSUPPORTED_FORMAT` | `path`, `ext` | Unknown extension |
| `INVALID_INPUT` | `param`, `reason` | `Load`/`Save` received invalid input |
| `VALIDATION_FAILED` | `path` | Validator reported invalid config |

## Thread safety

`Load` and `Save` are stateless functions with no shared mutable state. They are safe for concurrent use as long as callers do not concurrently mutate the same `dst`/`src` struct.

## Tests

**33 tests, 93.7% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/cfgx  coverage: 93.7% of statements
```

Coverage includes: Load (YAML, JSON, TOML success and parse errors), Save (YAML, JSON, TOML success and write errors, non-pointer src, nil pointer src, unsupported format), AutoFix (Validator interface invocation), validation propagation (Validate errors returned from Load), input guardrails (nil/non-pointer destination, nil source, nil pointer source), CreateIfMissing (file creation with defaults, write failure, Validator+AutoFix before write, Validator without fix returns error), custom reader/writer injection, error constructors and domain/code constants.

## Benchmarks

```text
BenchmarkLoad_YAML     ~125 us/op    8456 B/op    75 allocs/op
BenchmarkLoad_JSON      ~11 us/op     320 B/op     8 allocs/op
BenchmarkLoad_TOML      ~71 us/op    4207 B/op    67 allocs/op
BenchmarkSave_YAML      ~53 us/op    7024 B/op    39 allocs/op
BenchmarkSave_JSON       ~6 us/op     216 B/op     4 allocs/op
BenchmarkSave_TOML      ~56 us/op    5010 B/op    47 allocs/op
```

### Analysis

**JSON** is fastest: ~11 us load, ~6 us save — the Go stdlib JSON encoder/decoder is highly optimized. **YAML** is the slowest: ~125 us load — reflection-heavy parsing. **TOML** sits in between. All paths are I/O-bound in production; the marshal/unmarshal overhead is negligible.

## What cfgx does NOT do

| Concern | Owner |
|---------|-------|
| Environment variables | `envx` / `env2x` |
| CLI flags | `clix` |
| Pipeline orchestration | your `main()` |
| Remote config (etcd, Consul) | caller |
| Hot-reload / watching | caller / `fsnotify` |

## File structure

```text
pkg/cfgx/
    cfgx.go        -- Load(), Save(), Format, Validator, options
    errors.go      -- DomainConfig, Code constants, error constructors
    cfgx_test.go   -- 33 tests, 93.7% coverage
    bench_test.go  -- 6 benchmarks
    README.md
```
