# Changelog

All notable changes to URX are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] — 2026-02-22

Initial public release.

### Added

- **30 packages** covering resilience, infrastructure, configuration, and data
- **Generic-first API** — all execution wrappers use `[T any]` package-level functions
- **Execution controllers** in 6 resilience packages:
  `RetryController`, `CircuitController`, `BulkController`,
  `ShedController`, `AdaptController`, `HedgeController`
- **Unified error model** — `errx.Error` with Domain, Code, Severity,
  RetryClass, metadata, and trace correlation
- **Panic safety** — every `Execute`/`Do` path recovers panics via `panix.Safe`
- **1267 tests**, **218 benchmarks**, coverage 90.3%–100% per package
- **Zero-allocation hot paths** in `bulkx`, `ratex`, `panix`, `circuitx`
- `llm.md` — machine-optimized reference guide for LLM-assisted development
- CI workflow (GitHub Actions) with lint, test (90% coverage gate),
  and benchmark jobs; issue/PR templates; dependabot
- 6 runnable examples: `api-client`, `full-service`, `http-client`,
  `worker`, `rate-middleware`, `config-di`
- Testable example functions for all key packages (visible on pkg.go.dev)

### Dependencies

- Go 1.24+
- `golang.org/x/sync`
- `golang.org/x/crypto`
- `gopkg.in/yaml.v3`
- `github.com/BurntSushi/toml`
