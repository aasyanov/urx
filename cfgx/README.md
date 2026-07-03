# cfgx — Config file loader for YAML, JSON, and TOML

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/cfgx)
[License: MIT](../LICENSE)

Decode a config file (or byte slice) into a struct and encode it back, with format auto-detection, an optional self-fix validation seam, and injectable I/O for tests. Go 1.24+.

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> cfgx owns **one step** of the configuration pipeline: the file ↔ struct boundary. It does not read environment variables or parse CLI flags. Those are the jobs of `envx` and `clix`. The three compose through plain pointer sharing — cfgx imports neither of them — so you assemble the precedence chain yourself in `main()` (see [The Precedence Pipeline](#the-precedence-pipeline)).

## The Problem

A production service reads its configuration from several sources at once: compiled-in defaults, a config file shipped with the deployment, environment variables injected by the orchestrator, and command-line flags for ad-hoc overrides. The hard parts are rarely the file format itself:

1. **Format sprawl.** YAML in dev, JSON from an API, TOML from a sidecar — the loader must handle all three without the caller branching on extension.
2. **Partial files.** A config file that sets only `port` must not wipe out the defaults for every other field.
3. **Validation and repair.** A loaded config is often *almost* valid; the service wants to clamp `port` to a sane value rather than crash, and still know that a fix was needed.
4. **Layering.** File values must be overridable by env, which must be overridable by flags — without the loader knowing about env or flags.
5. **Testability.** Config loading must be exercisable without writing temp files to disk.

cfgx solves 1–3 and 5 directly, and is designed so that 4 falls out of pointer sharing with `envx` and `clix`.

## Architectural Position

cfgx **is** a codec boundary: `[]byte` ↔ struct, plus file I/O and a validation hook. It **is not**:

- a configuration *framework* (no global registry, no live reload, no watch);
- an environment reader — use `envx`;
- a flag parser — use `clix`;
- a schema/validation library — implement [`Validator`] on your config struct.

It is the deliberately small piece the other layers build on.

### Position in the urx Stack

```text
┌──────────────────────────────────────────────────────────┐
│  main(): defaults → file → env → CLI → validated struct  │
└────────────────────────┬─────────────────────────────────┘
                         │ file ↔ struct
┌────────────────────────▼─────────────────────────────────┐
│  cfgx   Load/Parse/Save · YAML/JSON/TOML · Validator      │
└──────────────┬───────────────────────┬───────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼───────────────────┐
│  envx (env overlay)    │   │  clix (flag overlay)         │
│  BindTo shared fields  │   │  flags → shared fields       │
└────────────────────────┘   └──────────────────────────────┘
```

## Architecture

```
            Load(path)                 Parse(data)
                │                           │
        reader(path) ──► data               data
                │                           │
        resolveFormat(ext) ◄── path   WithFormat(f) required
                │                           │
                ▼                           ▼
            ┌────────────── unmarshal(format) ──────────────────────┐
            │   yaml.v3   │   encoding/json   │   BurntSushi/toml   │
            └───────────────────────┬───────────────────────────────┘
                                    │
                       dst implements Validator?
                                    │  yes
                       Validate(fix) ─► errors.Join under ErrValidationFailed
                                    │
                                    ▼
                              caller's struct

    Save(path)/Marshal(data):  struct ──► marshal(format) ──► writer/bytes
```

## How It Works

`Load` runs a fixed sequence:

1. **Validate the destination** — it must be a non-nil pointer, else [`ErrInvalidInput`].
2. **Resolve the format** — explicit [`WithFormat`] wins; otherwise the file extension is mapped (`.yaml`/`.yml` → YAML, `.json` → JSON, `.toml` → TOML). An unknown extension yields [`ErrUnsupportedFormat`].
3. **Read** — the injectable reader (default [os.ReadFile]) returns the bytes. A missing file becomes [`ErrNotFound`], or — with [`WithCreateIfMissing`] — triggers a write of the current defaults. Other I/O errors become [`ErrReadFailed`].
4. **Decode** — the resolved codec unmarshals into the struct. Fields absent from the file keep their pre-load values, so defaults survive partial files. A decode failure becomes [`ErrParseFailed`].
5. **Validate** — if the struct implements [`Validator`], it is called with `fix` equal to [`WithAutoFix`]. Remaining errors are joined under [`ErrValidationFailed`].

`Parse` is the same pipeline minus steps 3–3a: it decodes a byte slice directly and requires an explicit format. `Save`/`Marshal` are the reverse direction.

## Normative Contracts


| Invariant           | Guarantee                                                                                  |
| ------------------- | ------------------------------------------------------------------------------------------ |
| Partial-file safety | Fields not present in the file retain their prior values.                                  |
| Validator timing    | `Validate` runs after a successful decode, or before writing when creating a missing file. |
| AutoFix reporting   | Even when `fix=true` repairs a field, the original violation is still reported.            |
| Sentinel errors     | Every failure wraps a package sentinel; use [`errors.Is`].                                 |
| Injectable I/O      | `Load`/`Save` never touch disk when [`WithReader`]/[`WithWriter`] are supplied.            |
| No urx imports      | cfgx imports no other urx subpackage; layering is via pointer sharing.                     |


## Quick Start

```go
type Config struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

func (c *Config) Validate(fix bool) []error {
	if c.Port <= 0 {
		if fix {
			c.Port = 8080
		}
		return []error{fmt.Errorf("port must be > 0")}
	}
	return nil
}

func main() {
	cfg := Config{Port: 8080, Host: "localhost"} // defaults
	if err := cfgx.Load("config.yaml", &cfg, cfgx.WithAutoFix()); err != nil {
		if !errors.Is(err, cfgx.ErrValidationFailed) {
			log.Fatal(err)
		}
		log.Printf("config repaired: %v", err)
	}
	fmt.Printf("listening on %s:%d\n", cfg.Host, cfg.Port)
}
```

## Usage Scenarios

### The Precedence Pipeline

The canonical 12-factor order — **defaults < file < env < flags** — is four blocks in `main()`, because every layer writes through pointers into the same struct:

```go
cfg := Config{Port: 8080, Host: "localhost"}     // 1. defaults

_ = cfgx.Load("config.yaml", &cfg,               // 2. file
	cfgx.WithCreateIfMissing())

env := envx.New(envx.WithPrefix("APP"))          // 3. environment
envx.BindTo(env, "PORT", &cfg.Port)              //    APP_PORT > file
envx.BindTo(env, "HOST", &cfg.Host)

p := clix.New(os.Args[1:], "app", "my service",  // 4. flags (highest)
	clix.AddFlag(&cfg.Port, "port", "p", cfg.Port, "listen port"),
	clix.AddFlag(&cfg.Host, "host", "", cfg.Host, "bind host"),
)

if err := errors.Join(env.Validate(), p.Err()); err != nil {
	log.Fatal(err)
}
if errs := cfg.Validate(false); len(errs) > 0 { // validate once, at the end
	log.Fatal(errors.Join(errs...))
}
```

Each layer is independent and unit-testable in isolation. cfgx provides only the file step and the [`Validator`] seam they share.

### In-memory config (no filesystem)

```go
var cfg Config
data := []byte(`{"port": 9090}`)
if err := cfgx.Parse(data, &cfg, cfgx.WithFormat(cfgx.FormatJSON)); err != nil {
	log.Fatal(err)
}
```

### First-run config generation

```go
// Writes defaults to disk if the file is absent, then loads normally next time.
err := cfgx.Load("config.toml", &cfg,
	cfgx.WithCreateIfMissing(),
	cfgx.WithFileMode(0o600),
)
```

### Persist after auto-fix

```go
cfgx.Load("config.yaml", &cfg, cfgx.WithAutoFix())
cfgx.Save("config.yaml", &cfg) // write the corrected values back
```

## API


| Symbol                | Signature                                                       | Description                                             |
| --------------------- | --------------------------------------------------------------- | ------------------------------------------------------- |
| `Load`                | `Load(path string, dst any, opts ...Option) error`              | Read a file into dst, then validate.                    |
| `Parse`               | `Parse(data []byte, dst any, opts ...Option) error`             | Decode bytes into dst (format required), then validate. |
| `Save`                | `Save(path string, src any, opts ...Option) error`              | Encode src and write it to path.                        |
| `Marshal`             | `Marshal(src any, format Format) ([]byte, error)`               | Encode src to bytes in an explicit format.              |
| `Format`              | `type Format uint8`                                             | Encoding selector.                                      |
| `Format.String`       | `String() string`                                               | "auto" / "yaml" / "json" / "toml".                      |
| `Validator`           | `interface { Validate(fix bool) []error }`                      | Optional self-check/self-repair hook.                   |
| `WithFormat`          | `WithFormat(f Format) Option`                                   | Force a format instead of extension detection.          |
| `WithAutoFix`         | `WithAutoFix() Option`                                          | Call `Validate(true)` so the struct can repair itself.  |
| `WithCreateIfMissing` | `WithCreateIfMissing() Option`                                  | Write defaults when the file is absent.                 |
| `WithFileMode`        | `WithFileMode(mode os.FileMode) Option`                         | Permission bits for created files (default 0o644).      |
| `WithReader`          | `WithReader(fn func(string) ([]byte, error)) Option`            | Inject the file reader.                                 |
| `WithWriter`          | `WithWriter(fn func(string, []byte, os.FileMode) error) Option` | Inject the file writer.                                 |


## Configuration


| Option                | Default        | Effect                                      |
| --------------------- | -------------- | ------------------------------------------- |
| `WithFormat`          | `FormatAuto`   | Detects from extension when auto.           |
| `WithAutoFix`         | disabled       | `fix=false` (report only) when disabled.    |
| `WithCreateIfMissing` | disabled       | Missing file → `ErrNotFound` when disabled. |
| `WithFileMode`        | `0o644`        | Applied by `Save` and create-if-missing.    |
| `WithReader`          | `os.ReadFile`  | nil is ignored.                             |
| `WithWriter`          | `os.WriteFile` | nil is ignored.                             |


## Errors


| Error                  | Condition                                                            |
| ---------------------- | -------------------------------------------------------------------- |
| `ErrInvalidInput`      | dst is not a non-nil pointer, or src is nil.                         |
| `ErrUnsupportedFormat` | Unknown file extension, or `FormatAuto` passed to `Parse`/`Marshal`. |
| `ErrNotFound`          | File absent and `WithCreateIfMissing` not set.                       |
| `ErrReadFailed`        | File exists but cannot be read.                                      |
| `ErrParseFailed`       | Data cannot be decoded into the struct.                              |
| `ErrWriteFailed`       | Encoding or writing failed.                                          |
| `ErrValidationFailed`  | `Validator` reported errors that remain after the fix pass.          |


## Pitfalls

> [!WARNING]
> `Parse` and `Marshal` require an explicit format. There is no extension to infer from a byte slice, so `FormatAuto` (the zero value) returns `ErrUnsupportedFormat`. Always pass `WithFormat(...)` to `Parse` and a concrete `Format` to `Marshal`.

> [!WARNING]
> Validate at the *end* of the pipeline, not inside `Load`, when you layer env and flags on top. `Load` validates the file-only state; if env or flags will still override fields, call `cfg.Validate(false)` once after the whole chain so you validate the final values.

> [!WARNING]
> `WithAutoFix` still reports. A repaired field does not suppress the error — `Load` returns `ErrValidationFailed` describing what was wrong. Check `errors.Is(err, ErrValidationFailed)` and decide whether a repaired config is acceptable.

> [!NOTE]
> Codecs normalise some strings: JSON rewrites invalid UTF-8 to U+FFFD and YAML trims surrounding whitespace. Treat config values as well-formed UTF-8; do not rely on byte-exact preservation of pathological strings.

## Safety and Concurrency

`Load`, `Parse`, `Save`, and `Marshal` are pure functions over their arguments and hold no package state — they are safe to call concurrently *as long as each call targets a distinct struct*. Concurrent calls that share the same `dst`/`src` pointer race, exactly as any concurrent struct access would. There is no global state, no caching, and no background goroutine.

## Benchmarks

> CPU: Intel i7-10510U · OS: Windows 10 · Go 1.24 · `-benchmem -count=1`


| Benchmark           | ns/op | B/op | allocs/op |
| ------------------- | ----- | ---- | --------- |
| Parse_YAML          | 10188 | 7472 | 54        |
| Parse_JSON          | 1039  | 272  | 7         |
| Parse_TOML          | 5767  | 3544 | 37        |
| Marshal_YAML        | 7998  | 6832 | 27        |
| Marshal_JSON        | 921   | 96   | 2         |
| Load_InjectedReader | 29359 | 7472 | 54        |
| ResolveFormat       | 43    | 0    | 0         |


### Analysis

- **JSON is the cheapest codec by an order of magnitude** — `Parse_JSON` is ~10× faster than YAML and allocates 7 objects versus 54. The stdlib `encoding/json` decoder reuses buffers aggressively; YAML (gopkg.in/yaml.v3) builds an intermediate node tree, which dominates both its time and allocation count.
- **TOML sits between the two.** BurntSushi/toml decodes in a single pass but still allocates per-key metadata.
- **ResolveFormat is 0 allocs / ~43 ns** — extension detection is a lowercase + switch, off the decode hot path entirely.
- **Load_InjectedReader ≈ Parse_YAML + reader overhead.** The gap reflects the closure call and the reflect-based pointer check in `requirePointer`; both are one-time per load.
- **Allocation floor is the codec's, not cfgx's.** cfgx adds a constant handful of allocations (the option closure application and the validator interface check); everything else is the third-party decoder. Config loading happens once at startup, so absolute speed matters less than correctness — but JSON is the clear choice when it does.

## Quality


| Metric         | Value                                             |
| -------------- | ------------------------------------------------- |
| Test functions | 20                                                |
| Benchmarks     | 7                                                 |
| Fuzz targets   | 2                                                 |
| Examples       | 4                                                 |
| Coverage       | 96.7%                                             |
| Race detector  | All pass                                          |
| External deps  | 2 (yaml.v3, BurntSushi/toml; testify in dev only) |


## File Structure

```
cfgx/
├── cfgx.go            # package doc, Load, Parse, Save, Marshal, internal helpers
├── types.go           # Format enum + String, Validator interface
├── options.go         # config struct, defaults, WithXxx options
├── codec.go           # resolveFormat, marshal, unmarshal
├── errors.go          # sentinel errors + internal wrapper helpers
├── cfgx_test.go       # unit + table-driven tests
├── bench_test.go      # benchmarks per codec
├── fuzz_test.go       # never-panic + round-trip fuzz targets
├── example_test.go    # runnable GoDoc examples (incl. the pipeline)
└── README.md          # this file
```

## License

MIT — see [LICENSE](../LICENSE) in the repository root.