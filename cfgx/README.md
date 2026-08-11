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
│  cfgx   Load/Parse/Save · YAML/JSON/TOML · Validator     │
└──────────────┬────────────────────────┬──────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼──────────────────┐
│  envx (env overlay)    │   │  clix (flag overlay)        │
│  BindTo shared fields  │   │  flags → shared fields      │
└────────────────────────┘   └─────────────────────────────┘
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


| Invariant           | Guarantee                                                                                                                                                |
| ------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Partial-file safety | Fields not present in the file retain their prior values.                                                                                                |
| Validator timing    | `Validate` runs after a successful decode, or before writing when creating a missing file.                                                               |
| AutoFix reporting   | When `fix=true` repairs values but violations remain, they are joined under `ErrValidationFailed`. A validator that returns `nil` after repair succeeds. |
| Marshal safety      | Unencodable values (including YAML encoder panics) return `ErrWriteFailed`; callers never crash.                                                         |
| Text-file newline   | Every [`Marshal`]/`Save` payload ends with a trailing `\n`.                                                                                              |
| Sentinel errors     | Every failure wraps a package sentinel; use [`errors.Is`].                                                                                               |
| Injectable I/O      | `Load`/`Save` never touch disk when [`WithReader`]/[`WithWriter`] are supplied.                                                                          |
| No urx imports      | cfgx imports no other urx subpackage; layering is via pointer sharing.                                                                                   |




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
> `WithAutoFix` still reports when the [`Validator`] returns errors after the fix pass. A validator that repairs every violation and returns `nil` produces no error — `Load`/`Parse` succeed. When errors remain, check `errors.Is(err, ErrValidationFailed)` and decide whether a partially repaired config is acceptable.

> [!WARNING]
> Empty input is codec-dependent. YAML and TOML accept an empty byte slice (dst keeps its prior values); JSON returns [`ErrParseFailed`]. Do not assume all three codecs treat `""` the same way.

> [!NOTE]
> Every [`Save`] and create-if-missing write ends with a trailing newline (POSIX text-file convention). Codecs may still normalise strings: JSON rewrites invalid UTF-8 to U+FFFD and YAML trims surrounding whitespace. Treat config values as well-formed UTF-8; do not rely on byte-exact preservation of pathological strings.



## Safety and Concurrency

`Load`, `Parse`, `Save`, and `Marshal` are pure functions over their arguments and hold no package state — they are safe to call concurrently *as long as each call targets a distinct struct*. Concurrent calls that share the same `dst`/`src` pointer race, exactly as any concurrent struct access would. There is no global state, no caching, and no background goroutine.

## Benchmarks

Three environments, two hardware classes, two operating systems. All values are **medians**. `B/op` and `allocs/op` are deterministic — they depend on code, not hardware.

### Environments


|            | Laptop                      | CI Server (Linux)     | CI Server (Windows)   |
| ---------- | --------------------------- | --------------------- | --------------------- |
| CPU | Intel Core i7-10510U, 4C/8T | Intel Xeon 6973P-C | AMD EPYC 7763 |
| TDP        | 15W (mobile, throttles)     | 280W (server, stable) | server, stable        |
| OS         | Windows 10                  | Ubuntu                | Windows Server 2022   |
| Go         | 1.26.2                      | 1.26                  | 1.26                  |
| GOMAXPROCS | 8                           | 4                     | 4                     |
| Runs       | 3 (`-count=3`)              | 3 (`-count=3`)        | 3 (`-count=3`)        |




### Parse / Marshal


| Benchmark                    | What it measures      | Laptop      | Linux       | Windows | B/op | allocs/op |
| ---------------------------- | --------------------- | ----------- | ----------- | ------- | ---- | --------- |
| Parse_YAML                   | yaml.v3 decode        | **6.59 µs** | 4.23 µs | 9.94 µs | 7472 | 54 |
| Parse_JSON                   | stdlib JSON decode    | **823 ns**  | 509.2 ns | 1010 ns | 272 | 7 |
| Parse_TOML                   | BurntSushi decode     | **4.22 µs** | 2.54 µs | 5.72 µs | 3544 | 37 |
| Parse_JSON_Parallel          | JSON decode, parallel | **389 ns**  | 223 ns | 506.5 ns | 272 | 7 |
| Marshal_YAML                 | yaml.v3 encode        | 4.30 µs     | **2.66 µs** | 5.84 µs | 6832 | 27 |
| Marshal_JSON                 | stdlib JSON encode    | **497 ns**  | 298.7 ns | 547.2 ns | 96 | 2 |
| Load_InjectedReader          | Parse via `io.Reader` | **6.81 µs** | 4.35 µs | 10.02 µs | 7472 | 54 |
| Load_InjectedReader_Parallel | Load, parallel        | 6.01 µs     | **2.71 µs** | 8.52 µs | 7472 | 54 |
| ResolveFormat                | Extension → `Format`  | **13.8 ns** | 11.5 ns | 14.2 ns | 0 | 0 |




### Analysis

**JSON is an order of magnitude cheaper than YAML.** `Parse_JSON` (509.2 ns, 7 allocs) vs `Parse_YAML` (4.2 µs, 54 allocs) on Linux CI. The stdlib decoder reuses buffers aggressively; yaml.v3 builds an intermediate node tree that dominates both time and heap traffic. TOML sits between (~4.5 µs, 37 allocs).

**Linux wins decode; laptop wins YAML on this run.** Linux CI is faster on YAML parse (4.2 µs vs 9.9 µs Windows) and ~7% on JSON. The laptop beat both CI platforms on YAML decode (6.59 µs) — likely turbo on a cold, short benchmark — but CI medians are more stable for regression tracking.

**Marshal_JSON is flat across all three environments (~500 ns, 2 allocs).** Encode is CPU-bound and OS-independent; differences are within noise. `Marshal_YAML` spreads wider (2.7 µs Linux vs 5.8 µs Windows) because yaml.v3 reflection work amplifies small CPU/OS gaps.

**ResolveFormat is 0 allocs / ~14 ns** — extension detection is a lowercase pass + switch, completely off the decode hot path.

**Load_InjectedReader ≈ Parse + reader overhead.** The delta over bare `Parse_YAML` is the injected reader call and the reflect-based pointer check in `requirePointer` — both paid once per load.

**Parallel benchmarks confirm statelessness.** `Parse_JSON_Parallel` scales to ~223 ns/op on CI because each goroutine owns its own `dst` — no mutex, no shared cache. `Load_InjectedReader_Parallel` is faster than serial on Linux (2.7 µs vs 4.3 µs) because four decoders saturate cores; laptop parallel load (6.0 µs) does not beat serial due to memory bandwidth limits on 15W silicon.

**Allocation floor is the codec's, not cfgx's.** cfgx adds a handful of allocations (option closures, validator interface check); everything else is the third-party decoder. Config loading runs once at startup — pick JSON when format choice is yours.

## Quality


| Metric         | Value                                             |
| -------------- | ------------------------------------------------- |
| Test functions | 32                                                |
| Benchmarks     | 9                                                 |
| Fuzz targets   | 2                                                 |
| Examples       | 5                                                 |
| Coverage       | 99.2%                                             |
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
├── footprint_test.go  # testx.AssertFootprint bounds for core types
├── bench_test.go      # benchmarks per codec (sequential + parallel)
├── fuzz_test.go       # never-panic + round-trip fuzz targets
├── example_test.go    # runnable GoDoc examples (incl. the pipeline)
└── README.md          # this file
```



## License

MIT — see [LICENSE](../LICENSE) in the repository root.