# Changelog

All notable changes to URX are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed (errx)

- **Breaking:** `MarshalJSON` now serializes `cause` recursively. If the cause is `*errx.Error`, it becomes a nested JSON object preserving all structured fields (Domain, Code, Severity, Meta, etc.). Non-`errx` errors remain plain strings. Recursion depth is unlimited.

### Fixed (logx)

- `TestFromContext_Nil` and `TestWithLogger_NilContext` now actually test nil context (were using `context.TODO()`, masking uncovered nil paths). Coverage restored to 100.0% (was 94.7%).

### Fixed (lrux)

- `TTL()` and `Len()` now return 0 on a closed cache, consistent with all other read methods (`Get`, `Peek`, `Has`, `Keys`, `Values`, `Range`).

### Fixed (dicx)

- `formatCycle` no longer duplicates the closing element in cyclic dependency error messages (was "A -> B -> A -> A", now "A -> B -> A").

### Fixed (hashx)

- `WithAlgorithm` and `WithTier` no longer silently discard a previously configured pepper. Option ordering is now irrelevant.
- `Generate` panics on unsupported `Algorithm` value instead of silently falling back to Argon2id (fail-fast for programmer error).
- `WithBcryptCost` panics on out-of-range cost instead of silently ignoring the value.

### Fixed (i18n)

- `processDictionaries` now creates the language folder with `0755` permissions (was `0777`).
- Fixed internal `setLanguage` godoc (claimed "returns the previous one", actually returns the resulting locale).
- Renamed internal `clearCacheLocked` to `resetCache` (the function does not require the caller to hold a lock).
- Removed dead code from `BenchmarkTranslateError_PlainError`.
- Replaced custom `contains`/`containsSubstring` test helpers with `strings.Contains`.

### Tests (lrux)

- 139 tests (was 136), 98.8% coverage.
- Added: `TestTTL_OnClosed`, `TestLen_OnClosed`.

### Tests (hashx)

- 57 tests (was 54), 96.0% coverage (was 95.9%).
- Added: `TestPepper_PreservedByWithAlgorithm`, `TestPepper_PreservedByWithTier`, `TestWithBcryptCost_InvalidCost_Panics`.

## [1.2.0] — 2026-03-13

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
