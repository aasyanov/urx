# Contributing to URX

## Getting started

```bash
git clone https://github.com/aasyanov/urx.git
cd urx
go test -race ./...
```

Requires **Go 1.24+**.

## Guidelines

1. **One package, one concern.** If your change touches multiple packages,
   make sure each change is self-contained and justified.

2. **Generic-first with controller pattern.** Execution wrappers (`Execute`,
   `Do`) must be package-level generic functions returning `(T, error)`.
   If the package introduces a new execution wrapper, it must provide a
   controller interface injected into the callback (see `retryx`,
   `circuitx`, `bulkx`, etc.).

3. **Structured errors only.** Use `errx.New` / `errx.Wrap` — never
   `fmt.Errorf`, `errors.New`, or sentinel errors. Every error needs a
   `Domain` and `Code`.

4. **Panic safety.** Every public `Execute`/`Do` path must be wrapped
   with `panix.Safe`. Never let panics escape public APIs.

5. **No logging.** Packages never write to `slog` or any logger directly.
   Logging is the caller's responsibility. If a callback panic must be
   observable, the caller wraps it with `panix.Safe`.

6. **Tests and benchmarks.** Every new function needs tests (aim for
   >= 90% coverage). Performance-critical paths need benchmarks with
   `b.Loop()` or `b.ResetTimer()`.

7. **Zero new dependencies.** If your change requires a new external
   module, open an issue first to discuss.

## Pull requests

- Fork the repo and create a feature branch
- Run `go vet ./...` and `go test -race ./...` before submitting
- Keep PRs focused — one feature or fix per PR
- Update the package README if you change the public API

## Reporting issues

Open a GitHub issue with:
- Go version (`go version`)
- URX version or commit hash
- Minimal reproduction code
- Expected vs actual behavior

## Code style

Follow standard Go conventions. Run `golangci-lint run` if available.

CI enforces: `go vet`, `golangci-lint` (including `forbidigo` rules for
`fmt.Errorf` / `errors.New`), race detector, and a **90% coverage threshold**.
