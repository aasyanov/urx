# Changelog

All notable changes to URX are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
