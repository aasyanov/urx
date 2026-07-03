# Changelog

All notable changes to URX are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.4.0] — 2026-07-03

Complete greenfield rewrite. **There is no migration path from ≤1.3.0** — no deprecated shims, no compatibility layer, no migration guide. Treat 1.4.0 as a new library that reuses familiar package names and the same module path (`github.com/aasyanov/urx`).

### Identity

- **Renamed:** *Unified Resilience eXtensions* → **Unified Runtime eXtensions**.
- **Re-scoped:** from a broad “31-package toolkit” (resilience + DI + logging + i18n + crypto + …) to **20 focused runtime primitives** that compose through `context.Context`, plain interfaces, and package-level generics — no framework runtime, no central dependency, no code generation.

### Breaking — module layout

- **Import paths changed:** `github.com/aasyanov/urx/pkg/<name>` → `github.com/aasyanov/urx/<name>`.
- **Flat module tree:** packages live at the repository root (`retryx/`, `circuitx/`, …), not under `pkg/`.
- **Public surface reduced:** `testx` is no longer importable; test helpers moved to `internal/testx`.

### Breaking — error model

- **Removed `errx` entirely.** There is no shared structured error type, no Domain/Code/Severity/RetryClass, no `errx.NewMulti()`.
- **Per-package sentinel errors** via `errors.New`, comparable with `==` and `errors.Is`. Context causes are joined with `fmt.Errorf("%w: %w", …)` where useful.
- **Panic recovery** produces `*panix.PanicError` (inspect with `errors.As`), not `*errx.Error`.
- **`retryx` retry policy:** retryability is driven by `WithRetryIf` when supplied; otherwise every error is retryable until the attempt budget is exhausted (no automatic `errx.Error.Retryable` lookup).

### Breaking — API conventions

- **All execution callbacks receive `context.Context` as the first argument** — e.g. `func(ctx context.Context, rc RetryController) (T, error)`. In ≤1.3.0 several packages omitted `ctx` from the callback signature.
- **Standardized entry points** across resilience packages: `Execute` / `TryExecute`, functional `WithXxx` options on private `config` structs, lifecycle (`Close`, `IsClosed`, `Stats`, `ResetStats`) where applicable.
- **Execution controllers expanded to eleven packages** — callbacks receive a controller interface to read state and influence wrapper behavior (`SkipFailure`, `Abort`, `SkipToken`, `SkipSample`, `Shed`, `Cancel`, `Reject`, …). See the controller table in [README.md](README.md).
- **`TryExecute` semantics unified:** when work is rejected without running the callback, returns `(false, zero, nil)` unless a sentinel error applies (`ErrOpen`, `ErrClosed`, `ErrCancelled`, …).
- **Non-blocking probes:** `Allow()` (and related read-only checks) added or standardized on limiters and shedders — 0-allocation hot paths for edge admission decisions.

### Removed packages

These packages from ≤1.3.0 are **gone**, not relocated:

| Package | Was |
| ------- | --- |
| `errx` | Structured application error model |
| `dicx` | Reflection-free DI container |
| `busx` | In-process event bus |
| `logx` | Structured logging wrapper |
| `cronx` | Cron scheduler |
| `ctxx` | Context helper utilities |
| `env2x` | Alternate env binding API |
| `hashx` | Password hashing (Argon2/bcrypt) |
| `i18n` | Dictionary-based localization |
| `validx` | Struct validation helpers |
| `testx` | Public test helpers (now `internal/testx`) |

Also removed: `examples/` (six runnable demos), `llm.md`, `CONTRIBUTING.md`.

### Added

- **`quality.ps1` / `quality.sh`** — single local quality run: `go vet`, `golangci-lint`, race+coverage tests, benchmarks, fuzz smoke.
- **Root [README.md](README.md)** — module overview: composition model, configuration boot/runtime modes, usage scenarios, quality bar.
- **Fuzz targets** across resilience and config packages (52 `Fuzz*` functions).
- **`footprint_test.go`** in every package — struct size guards via `internal/testx.AssertFootprint`.
- **`Allow()` fast paths** on adaptive/bulkhead/warmup limiters for allocation-free admission hints.
- **`WithOp`** on resilience packages — custom operation names in panic reports.

### Changed — kept packages (rewritten)

All twenty public packages were reimplemented on the conventions above. Highlights:

| Area | Packages |
| ---- | -------- |
| **Resilience** | `retryx`, `circuitx`, `bulkx`, `shedx`, `adaptx`, `hedgex`, `toutx`, `fallx`, `ratex`, `quotax`, `warmupx` |
| **Infrastructure** | `panix`, `signalx`, `healthx`, `syncx`, `poolx` |
| **Configuration** | `cfgx`, `envx`, `clix` |
| **Data** | `lrux` |

Notable behavioural themes:

- **Allocation-conscious hot paths** — `Allow`, open-circuit rejects, cache hits, and object-pool get/put benchmark at 0 allocs/op; `Execute` admit paths typically 1 alloc (controller escape).
- **`adaptx`** — AIMD, Vegas, and Gradient adaptive limiters with `SkipSample`, permit reconciliation, and `CloseWithTimeout` drain.
- **`lrux`** — generic LRU with TTL, sharded variant, `GetOrCompute` + singleflight, intrusive list (one node alloc per entry).
- **`fallx`** — static, func, and sharded cached fallback strategies with `FallController`.
- **`quotax`** — per-key token buckets with shard partitioning and key eviction.
- **`warmupx`** — slow-start capacity ramp (linear / exponential / step) with probabilistic admission during warmup.
- **`cfgx` / `envx` / `clix`** — file/env/CLI config without reflection in env binding; cfgx supports YAML, JSON, TOML with injectable readers/writers.
- **`syncx`** — generic `Lazy[T]`, bounded `Group`, typed `Map` with O(1) `Len`.
- **`poolx`** — worker pool, object pool, batch collector with lifecycle-aware flush.

### Dependencies

- **Removed:** `golang.org/x/crypto` (was required by `hashx`).
- **Runtime:** stdlib + `golang.org/x/sync` (singleflight in `lrux`) + `gopkg.in/yaml.v3` + `github.com/BurntSushi/toml`.
- **Test/dev:** `github.com/stretchr/testify`.

Requires **Go 1.24+** (CI runs on Go 1.26).

### Quality (1.4.0 release bar)

| Metric | Value |
| ------ | ----- |
| Public packages | 20 |
| Test functions | ~1110 |
| Benchmarks | ~160 |
| Fuzz targets | 52 |
| Statement coverage | **98.6%** (race detector on all tests) |
| `golangci-lint` | 0 issues |

### Documentation

- Root [README.md](README.md) rewritten: problem statement, design principles, controller pattern, package index, “when to use / when not”.
- Every package ships a standalone README with API tables, benchmark analysis, and quality metrics.
- Explicit policy: **greenfield project — APIs may change freely; no backward-compatibility promise unless stated otherwise.**

---

## [1.3.0] — 2026-03-13

> **Historical note:** releases through 1.3.0 used `pkg/` import paths, the `errx` error model, and the *Unified Resilience eXtensions* scope (31 packages). Superseded entirely by **1.4.0**.

### Changed (errx)

- **Breaking:** `MarshalJSON` now serializes `cause` recursively. If the cause is `*errx.Error`, it becomes a nested JSON object preserving all structured fields (Domain, Code, Severity, Meta, etc.). Non-`errx` errors remain plain strings. Recursion depth is unlimited.

### Fixed (syncx)

- **Critical race fix:** `Lazy.Get` had a TOCTOU race with `Lazy.Reset`. The atomic fast path observed `done == 1`, but between the check and acquiring the mutex, `Reset` could clear all fields. `Get` then returned `(zero, nil)` — a zero value with no error, without re-running init. Removed the racy fast path; `Get` now always checks `done` under the mutex.
- `NewLazy` now panics immediately if init is nil (was deferred to first `Get`).

### Fixed (poolx)

- `NewObjectPool` now panics immediately if factory is nil (was deferred to first `Get()` on empty pool).
- `NewBatch` now panics immediately if flush function is nil (was deferred to first `Flush()`).
- Fixed `WithFlushInterval` godoc: said "Values <= 0 disable periodic flushing" but actually values <= 0 were ignored and the default was kept.

### Fixed (testx)

- `makeError` now falls back to default `TEST.SIMULATED` error when a custom `WithErrorFunc` returns nil. Previously, a nil-returning factory silently swallowed the failure.

### Fixed (lrux)

- `TTL()` and `Len()` now return 0 on a closed cache, consistent with all other read methods.

### Fixed (dicx)

- `formatCycle` no longer duplicates the closing element in cyclic dependency error messages.

### Fixed (hashx)

- `WithAlgorithm` and `WithTier` no longer silently discard a previously configured pepper.
- `Generate` panics on unsupported `Algorithm` value instead of silently falling back to Argon2id.
- `WithBcryptCost` panics on out-of-range cost instead of silently ignoring the value.

### Fixed (i18n)

- `processDictionaries` now creates the language folder with `0755` permissions (was `0777`).
- Renamed internal `clearCacheLocked` to `resetCache` (misleading name implied caller held a lock).

### Improved (busx)

- Added `WithOnError` tests. Coverage 94.6% → 98.9%, tests 39 → 41.

### Improved (logx, signalx)

- Tests now actually exercise nil context paths (were using `context.TODO()`). logx coverage restored to 100.0%, signalx to 97.3%.

### Documentation

- Full re-audit of all 31 packages. Test counts verified via `go test -v ./pkg/...` and synchronized across all package READMEs and main README. Total: 1335 tests, 207 benchmarks.
- Removed soft line wraps from all READMEs (busx, poolx, syncx, testx, healthx, and 5 Resilience packages).
- Fixed healthx README: clarified `errUnhealthy`/`errTimeout` are internal.
- Fixed errx README: 86→89 tests (examples were uncounted).
- Fixed panix README: 20→21 tests (example was uncounted).
- Fixed circuitx, fallx, retryx, shedx, toutx README test counts.

## [1.2.0] — 2026-03-13

> **Historical note:** superseded by **1.4.0**.

### Changed (envx)

- **Breaking:** `WithLookup` signature changed from `func(string) string` to `func(string) (string, bool)`. Default is now `os.LookupEnv` instead of `os.Getenv`. This correctly distinguishes "variable not set" from "variable set to empty string".
- **Breaking:** `MapLookup` returns `(string, bool)` — missing keys return `("", false)`.
- Internal: `Bind` and `BindRequired` deduplicated into shared `bindVar` helper.
- `Validate` uses `errx.NewMulti()` directly instead of intermediate slice.

### Fixed (envx)

- `Bind`/`BindRequired` now correctly report `Found() == true` for variables set to empty string. Previously, empty string was indistinguishable from unset.
- `ExampleBind` now uses `WithPrefix("APP")` consistently with the key map.

### Tests (envx)

- 37 tests (was 35), 97.5% coverage (was 96.7%).
- Added: `TestBind_EmptyStringIsFound`, `TestBindRequired_EmptyStringIsFound`.

### Fixed (env2x)

- `Result.Err()` now returns `me.Err()` instead of raw `*errx.MultiError`, consistent with `envx.Validate()`.

### Fixed (cfgx)

- `CreateIfMissing` now calls `Validator` before writing defaults to disk. With `WithAutoFix()`, defaults are corrected first, then saved. Previously, invalid defaults were written as-is.
- `unmarshal`/`marshal` default branches now panic instead of silently returning nil. These branches are unreachable by design (`resolveFormat` guards against `FormatAuto`), but the panic ensures any future format addition that misses the switch is caught immediately.

### Added (cfgx)

- Tests for: `Save` with non-pointer src, `Save` with nil pointer, `Save` with unsupported format, `CreateIfMissing` + write failure, `CreateIfMissing` + Validator (autofix and no-fix paths).

### Tests (cfgx)

- 33 tests (was 27), 93.7% coverage (was 91.3%).

### Changed (infra)

- CI updated to match slabix: `checkout@v5`, `setup-go@v6`, `golangci-lint-action@v9`, `upload-artifact@v6`. Added `concurrency` (cancel-in-progress), `timeout=120s`. Removed strategy matrix and `Build examples` step.
- `.golangci.yml` aligned with slabix: removed `forbidigo` linter. Cleaned up duplicate exclusion rules.
- `.gitignore` simplified to match slabix style.

### Removed (infra)

- `dependabot.yml`, issue templates (`bug_report.yml`, `feature_request.yml`, `config.yml`), PR template — unnecessary for a personal project.
- All stale `//nolint:forbidigo` comments across `clix`, `errx`, `hashx`.

## [1.1.0] — 2026-03-13

> **Historical note:** superseded by **1.4.0**.

### Changed (clix)

- **Parse / Run separation** — `New` no longer executes the matched action. Call `p.Run()` explicitly. This allows callers to inspect parse results, add middleware, or skip execution in tests. **Breaking change.**
- **Adaptive help columns** — flag and subcommand column widths in help output now adjust dynamically to content length, including alias labels.

### Added (clix)

- `Alias(names...)` option — register alternative names for subcommands. Aliases resolve identically to the primary name and appear in help output.
- `Version(v string)` option — enable `--version` / `-V` handling. Returns `ErrVersion` sentinel; callers print `p.Version()`.
- `ErrVersion` sentinel with full godoc.
- `-h` inside POSIX grouped short flags (e.g. `-vh`) now triggers `ErrHelp`.
- `p.Run() error` method — explicit action execution after parsing.
- `p.Version() string` method — returns the version string set via `Version()`.
- Duplicate `Run` on the same command panics at construction time.

### Fixed (clix)

- `flagDisplayWidth` off-by-one: separator `", -"` counted as 2 chars instead of 3.
- `ErrVersion` did not set `p.matched`, so `p.Help()` after `--version` on a subcommand returned root help instead of subcommand help.
- COMMANDS adaptive width did not account for alias labels, causing misaligned descriptions.

### Tests (clix)

- 83 tests (was 67), 96.0% coverage (was 94.5%).

## [1.0.0] — 2026-02-24

> **Historical note:** superseded by **1.4.0**. Initial release under the name *Unified Resilience eXtensions*.

Initial public release.

### Added

- **30 packages** covering resilience, infrastructure, configuration, and data
- **Generic-first API** — all execution wrappers use `[T any]` package-level functions
- **Execution controllers** in 6 resilience packages: `RetryController`, `CircuitController`, `BulkController`, `ShedController`, `AdaptController`, `HedgeController`
- **Unified error model** — `errx.Error` with Domain, Code, Severity, RetryClass, metadata, and trace correlation
- **Panic safety** — every `Execute`/`Do` path recovers panics via `panix.Safe`
- **1267 tests**, **218 benchmarks**, coverage 90.3%–100% per package
- **Zero-allocation hot paths** in `bulkx`, `ratex`, `panix`, `circuitx`
- `llm.md` — machine-optimized reference guide for LLM-assisted development
- CI workflow (GitHub Actions) with lint, test (90% coverage gate), and benchmark jobs; issue/PR templates; dependabot
- 6 runnable examples: `api-client`, `full-service`, `http-client`, `worker`, `rate-middleware`, `config-di`
- Testable example functions for all key packages (visible on pkg.go.dev)

### Dependencies

- Go 1.24+
- `golang.org/x/sync`
- `golang.org/x/crypto`
- `gopkg.in/yaml.v3`
- `github.com/BurntSushi/toml`

[1.4.0]: https://github.com/aasyanov/urx/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/aasyanov/urx/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/aasyanov/urx/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/aasyanov/urx/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/aasyanov/urx/releases/tag/v1.0.0
