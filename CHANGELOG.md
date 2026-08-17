# Changelog

All notable changes to URX are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.5.2] — 2026-08-17

Sole consumer; breaking resilience contracts in this minor are intentional.

### Breaking

- **`adaptx` control laws** now match their names: AIMD/Vegas/Gradient adjust **once per `WithSampleWindow`**. AIMD increases only when window `maxInFlight >= limit * WithUtilization` (default **0.9**) and accumulates fractional `WithIncreaseRate`. Vegas queue is `limit * (1 - minRTT/RTT)` (was `/minRTT`). Gradient EMA initializes to the first window sample; the first window **holds** (does not +1). **`Close()`** does not wait for in-flight work (use **`CloseWithTimeout`**); incomplete drain returns **`ErrDrainTimeout`**; `Close()` is nil-idempotent (second call no longer returns `ErrClosed`).
- **`ratex` / `quotax`:** `WaitN(n > burst)` returns **`ErrExceedsBurst`** immediately instead of hanging until context cancel. `errors.Is(err, ratex.ErrExceedsBurst)` works for both packages.
- **`circuitx`:** a callback error that is `context.Canceled` is no longer a consecutive failure. Opt in with `WithCountCanceled()`. `DeadlineExceeded` still counts.
- **`retryx`:** a cancelled last attempt returns **`ErrCancelled`**, not `ErrExhausted`.
- **`toutx`:** an inner `context.DeadlineExceeded` / `Canceled` while this call's timeout is still live is **propagated**, not rewritten to `toutx.ErrDeadlineExceeded` / `ErrCancelled`.
- **`bulkx`:** `TryExecute` no longer barges ahead of parked `Execute` waiters (rejects while `waiters > 0`).
- **`fallx`:** `StrategyCached` no longer starts a background TTL sweeper by default. Opt in with `WithCleanupInterval`.
- **`hedgex`:** `WithOnHedge` runs synchronously (no extra goroutine per hedge). `HedgeController.Cancel()` cancels that copy's context.
- **`warmupx`:** Exponential curve is normalized so `f(1) = 1` (mid-ramp values change slightly). `StartAt` continues along the curve instead of dropping to min on the first tick.

### Added

- **`clix`:** `WithHelpLabels` / `HelpLabels` — replace help chrome (headings, metavars, built-in `--help`/`--version` usage, required/enum suffixes) without translating command or flag names. Empty fields keep English defaults. Must be passed to `New`.

- **`cfgx`:** nested [Validator] walk (post-order: children, then parent) with field-path prefixes on `ErrValidationFailed`. Public `Validate(dst, fix, opts...)` re-runs the same walk after env/flags; `WithFormat` keeps path prefixes on the file codec's tags. Walk descends slice/array/map elements that can hold a Validator (`servers[0].port`, `limits[api].rate`). Leaf slices (`[]string`, …) are not descended. Enhancement — may surface previously ignored nested validation errors, including on collection elements. Not a break for honest `Validate` implementations that did not recurse into children.
- **`cfgx`:** default writer creates missing parent directories (`0o755`) before `Save` / create-if-missing. Injected `WithWriter` is unchanged.
- **`envx`:** `parse` accepts defined types whose underlying kind is a supported builtin, and `encoding.TextUnmarshaler` on `*T`. Exact `int`/`string`/… type-switch is unchanged (reflect only on the named/default branch).
- **`envx`:** `WithFallbackPrefix` — first-fill-wins lookup after `WithPrefix`. `Var.Key()` / `Vars()` report the actually found key; missing required lists every tried name.
- **`envx`:** opt-in `Walk` + `BindField`. Default key source is the `env` tag allowlist (`KeysFromEnvTag`); `KeysFromYAML` / `KeysFromJSON` / `KeysFromTOML` are explicit. Walk does not read env and does not mutate (nil pointer branches are skipped). Bind remains the canonical overlay.
- **`adaptx`:** `WithUtilization`, `ErrDrainTimeout`.
- **`ratex`:** `ErrExceedsBurst`.
- **`circuitx`:** `WithFailureIf`, `WithCountCanceled`. Internal HalfOpen generation so a stale probe cannot heal or `Trip` a newer epoch.
- **`fallx`:** `WithFallbackIf`, `WithClone`, `WithCleanupInterval`.
- **`retryx`:** `WithMaxElapsed` / `ErrMaxElapsed`, `WithDelayFunc`, `WithEqualJitter`.
- **`hedgex`:** `WithHedgeProbability` (default 1.0).
- **`bulkx`:** `WithMaxWaiters`, `ErrWaitersExceeded`, `Stats.Waiters`.
- **`shedx`:** `WithHysteresis`, `ShedController.SkipSlot`.
- **`warmupx`:** `Close` / `IsClosed` / `ErrClosed` (`Stop` remains freeze-and-admit).
- **`quotax`:** Wait/Execute pin the per-key bucket so the sweeper cannot evict it mid-wait or mid-callback.

### Changed

- Resilience packages skip nil `Option` values; `On*` hooks run synchronously under recover (no `go hook()` per event).
- **`clix`:** `Command` footprint ceiling 192→200 (`*HelpLabels` on the root; help chrome is startup-only).
- **`clix` help legend:** `Parser.Help()` always lists built-in `--help` / `-h`, and `--version` / `-V` when `Version` is set. USAGE shows the full command path (`app db migrate`). GLOBAL FLAGS are root-first. Enum comments render as `(one of: dev, prod)`.
- **`cfgx`:** nil `Option` values are ignored. Unknown `Format` values return `ErrUnsupportedFormat` instead of panicking. `FormatAuto` on `Validate` falls through a `"-"` tag to the next codec (yaml → json → toml) so `yaml:"-" json:"listen"` is still walked.
- **`envx`:** nil `Option` values to `New` are ignored (matching `Walk` / clix / cfgx). `uint` parse uses platform width (`bits.UintSize`). Lookup without fallback prefixes skips the candidate-key map.
- **`envx`:** `type MyDur time.Duration` no longer accepts `"5s"` (integer parse only; use `time.Duration` or `TextUnmarshaler`).
- **`envx`:** named `time.Time` (`type Stamp time.Time`) binds RFC3339, matching exact `time.Time`.
- **`cfgx` docs:** JSON/YAML `null` does not zero non-pointer scalars (codec no-op; Go defaults stay).

### Fixed

- **`quotax`:** sweeper can no longer split one key into two token buckets while Wait/Execute is in flight. `Remove`/`Reset` skip pinned buckets (no ghost dual-bucket). `SkipToken` never drives `Allowed` below zero.
- **`circuitx`:** HalfOpen `inflight.Store(0)` no longer lets a stale probe heal or trip a newer generation. Healing `HalfOpen → Closed` and every `Reset` bump generation so a leftover probe cannot fail Closed.
- **`adaptx`:** `SkipSample` no longer raises the AIMD window peak in-flight. Gradient's first window holds when `avgLat` is unset.
- **`fallx`:** `WithClone` on cache replay runs outside the shard lock (store already did).
- **`shedx`:** hysteresis in-band (`resume ≤ load < threshold`) keeps shedding all non-critical traffic; cutoffs apply only at/above the threshold.
- **`toutx`:** `fn` returning `context.Canceled` after this call's timeout fired (parent still live) remaps to `ErrDeadlineExceeded`, not `ErrCancelled`.
- **`bulkx`:** slot release decrements `Active` before returning the semaphore; wait duration is `min(timeout, ctx remaining)`. Fast-path `Execute`/`TryExecute` refund a claimed slot when waiters appeared mid-send.
- **`clix`:** `--help=...` and `--version=...` are recognised as the built-in control flags (previously `ErrUnknownFlag`). `-V` inside a POSIX short group triggers `ErrVersion`. `--no-<bool>=false` now writes true instead of always writing false.
- **`clix`:** construction panics on reserved `--help`/`-h` (and `--version`/`-V` when `Version` is set), multi-character short aliases, `Version` or `WithHelpLabels` applied to a subcommand, and duplicate `Version`. Nil options are ignored.
- **`clix`:** a non-bool flag no longer consumes a following flag-like token as its value (`--port --verbose` → `ErrMissingValue`, matching the documented contract). Signed numbers (`-5`, `-1s`) and a bare `-` still bind as values; dash-prefixed strings use the inline form (`--name=--raw`).
- **`cfgx`:** public `Load`/`Parse`/`Save`/`Marshal`/`Validate` no longer panic on a nil option or an out-of-range `Format` value.
- **`envx`:** `Walk`/`BindField` parse of `time.Duration` and builtin `int64` now matches `Bind` (and clix): unitless `"90"` is `ErrInvalid` for Duration; `"1s"` is `ErrInvalid` for `int64`. The reflect path no longer treats Duration as raw nanoseconds or `int64` as a duration.
- **`cfgx`:** exported embed `Validator` is no longer double-invoked; a nil pointer-embed no longer panics.
- **`cfgx`:** not-exist detection uses `errors.Is`, so wrapped `WithReader` errors create/not-found correctly.
- **`cfgx`:** `Marshal` rejects typed nil pointers like `Save`.
- **`envx`:** named `int64` no longer parses duration strings (`type UserID int64` + `5s` is `ErrInvalid`).
- **`clix`:** positional `-` is an arg (stdin convention); `IsSet` accepts short aliases.
- **docs:** 12-factor snippets handle `ErrHelp` / `ErrVersion` before `Join`; `AddFlag` `def` must be the live field value.

## [1.5.1] — 2026-08-11

### Added

- **`circuitx`:** `WithSuccessThreshold(n)` — consecutive probe successes in `HalfOpen` required to heal to `Closed` (default `1`, same as before). Probe failure resets the counter and re-opens immediately. Values `<= 0` ignored; resolved config floors below 1 to 1.

## [1.5.0] — 2026-08-11

### Changed
- Refreshed README benchmark tables from CI (Linux Xeon 6973P-C / Windows EPYC 7763).
- Removed local `quality.ps1` / `quality.sh` wrappers; quality bar is CI + gate commands (`go test` / `golangci-lint` / pprof). Published docs must be English-only.
- Ship-kit unified with org canon: identical `.gitignore`, `.golangci.yml`, MIT `LICENSE`, and `.github/workflows/ci.yml` (lint + OS×Go matrix + fuzz discover + bench/pprof on main).
- Root README gate table aligned with canon (Gate M + Gates 0–5, coverage ship bar ≥90%).
- `lrux`: `TestCache_Touch` no longer relies on a second tight sleep after TTL slide (flake under `-race` / loaded hosts).

### Breaking

- **`ratex` / `quotax`:** fractional rates below 1.0 req/s are now honored. Previously `newConfig` floored any positive rate `< 1` to `1.0`, which broke slow limits (e.g. `0.2` for `rate_ms=5000`, or `10/60 ≈ 0.167` req/s). Non-positive rates still fall back to `DefaultRate`; burst still floors at 1.

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

[Unreleased]: https://github.com/aasyanov/urx/compare/v1.5.2...HEAD
[1.5.2]: https://github.com/aasyanov/urx/compare/v1.5.1...v1.5.2
[1.5.1]: https://github.com/aasyanov/urx/compare/v1.5.0...v1.5.1
[1.5.0]: https://github.com/aasyanov/urx/compare/v1.4.0...v1.5.0
[1.4.0]: https://github.com/aasyanov/urx/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/aasyanov/urx/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/aasyanov/urx/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/aasyanov/urx/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/aasyanov/urx/releases/tag/v1.0.0
