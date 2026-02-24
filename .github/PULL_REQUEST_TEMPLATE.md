## What

<!-- One sentence: what does this PR do? -->

## Why

<!-- Why is this change needed? Link related issues with "Closes #123". -->

## How

<!-- Brief description of the approach. Mention affected packages. -->

## Checklist

- [ ] `go vet ./...` passes
- [ ] `go test -race ./...` passes
- [ ] New/changed public API has tests (>= 90% coverage)
- [ ] Performance-critical paths have benchmarks
- [ ] Package README updated (if public API changed)
- [ ] Errors use `errx.New` / `errx.Wrap` with `Domain` and `Code`
- [ ] No new external dependencies (or discussed in an issue first)
- [ ] No direct `slog` / logger calls from packages
