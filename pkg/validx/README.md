# validx

Composable, pure-function field validators and auto-fixers returning structured `*errx.Error` values.

## Philosophy

**One job: validate fields.** `validx` provides pure functions that accept a field name and value, returning `nil` on success or a structured `*errx.Error` on failure. Fix-functions (`Clamp`, `Default`, etc.) mutate the value in place and return an informational `CodeFixed` error. `Collect` aggregates results into an `*errx.MultiError`. No struct tags, no reflection, no side effects.

## Quick start

```go
// Validation
err := validx.Collect(
    validx.Required("name", req.Name),
    validx.MinLen("name", req.Name, 2),
    validx.MaxLen("name", req.Name, 100),
    validx.Email("email", req.Email),
    validx.Between("age", req.Age, 18, 120),
    validx.OneOf("role", req.Role, []string{"admin", "user", "guest"}),
)

// Auto-fix
fixed := validx.Collect(
    validx.Clamp("age", &req.Age, 18, 120),
    validx.DefaultStr("name", &req.Name, "anonymous"),
    validx.DefaultOneOf("role", &req.Role, []string{"admin", "user"}, "user"),
)
```

## API

### Validators

| Function | Description |
|---|---|
| `Required(field, value) *errx.Error` | Non-empty string (after trim) |
| `MinLen(field, value, min) *errx.Error` | Minimum rune length |
| `MaxLen(field, value, max) *errx.Error` | Maximum rune length |
| `Between(field, value, min, max) *errx.Error` | Integer in [min, max] |
| `Match(field, value, pattern) *errx.Error` | Value matches regex |
| `OneOf(field, value, allowed) *errx.Error` | Value in allowed set |
| `Email(field, value) *errx.Error` | Valid email format |
| `URL(field, value) *errx.Error` | Valid absolute URL |
| `BetweenTime(field, t, min, max) *errx.Error` | Time in [min, max] |
| `Collect(errs...) error` | Aggregate non-nil errors into `*errx.MultiError` |

### Auto-fixers

| Function | Description |
|---|---|
| `Clamp[T](field, val, min, max) *errx.Error` | Clamp to [min, max]; returns `CodeFixed` if changed |
| `ClampTime(field, val, min, max) *errx.Error` | Clamp time to [min, max] |
| `Default[T](field, val, def) *errx.Error` | Set default if zero value |
| `DefaultStr(field, val, def) *errx.Error` | Set default if empty/whitespace |
| `DefaultTime(field, val, def) *errx.Error` | Set default if zero time |
| `DefaultOneOf[T](field, val, allowed, def) *errx.Error` | Set default if not in allowed set |

## Behavior details

- **Pure functions**: every validator is a standalone function with no shared state. Compose freely by passing results to `Collect`.

- **Structured errors**: each error includes `field` in `Meta` and a specific `Code` (e.g. `TOO_SHORT`, `OUT_OF_RANGE`). The caller can inspect `errx.Error.Code` to build localized messages.

- **Auto-fixers**: `Clamp`, `Default`, etc. mutate `*T` in place. If the value was changed, they return `CodeFixed` (informational, severity `Info`). If unchanged, they return `nil`.

- **Collect**: filters out `nil` errors and wraps the rest into `*errx.MultiError`. Returns `nil` if all validators pass.

- **Generics**: `Clamp[T]`, `Default[T]`, `DefaultOneOf[T]` use `cmp.Ordered` / `comparable` constraints — no reflection.

## Error diagnostics

All errors are `*errx.Error` with `Domain = "VALIDATION"`.

### Codes

| Code | When |
|---|---|
| `REQUIRED` | Required field was empty |
| `TOO_SHORT` | Value shorter than minimum length |
| `TOO_LONG` | Value exceeds maximum length |
| `OUT_OF_RANGE` | Value outside allowed range |
| `INVALID_FORMAT` | Value doesn't match pattern |
| `INVALID_VALUE` | Value not in allowed set |
| `FIXED` | Value was auto-corrected (informational) |
| `NIL_POINTER` | Nil pointer passed to a fix function |

### Example

```text
VALIDATION.REQUIRED: name is required | meta: field=name
VALIDATION.TOO_SHORT: name is too short | meta: field=name, min=2
VALIDATION.FIXED: age was auto-fixed | meta: field=age, from=150, to=120, fixed=true
```

## Thread safety

All functions are pure (no shared state) — safe for concurrent use. `Collect` is a pure aggregation function — safe for concurrent use.

## Tests

**67 tests, 100% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/validx  coverage: 100% of statements
```

Coverage includes: Required (empty, whitespace, valid), MinLen / MaxLen (boundary values, Unicode runes), Between (in range, below, above, boundary), Match (valid, invalid, bad pattern), OneOf (valid, invalid), Email (valid, invalid formats), URL (valid, invalid, relative), Clamp (in range, below, above for int, float64, time), Default (zero, non-zero, string, time), DefaultOneOf (valid, invalid), Collect (all valid, some invalid, empty), nil pointer safety (all fix functions return `CodeNilPointer` instead of panicking), meta fields (field name present in every error).

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz. Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkRequired_Valid          ~6 ns/op       0 B/op     0 allocs/op
BenchmarkRequired_Invalid      ~446 ns/op     560 B/op     5 allocs/op
BenchmarkEmail_Valid            ~443 ns/op       0 B/op     0 allocs/op
BenchmarkCollect_3Fields        ~312 ns/op       0 B/op     0 allocs/op
BenchmarkMatch                 ~4656 ns/op    4033 B/op    46 allocs/op
BenchmarkClamp_InRange            ~3 ns/op       0 B/op     0 allocs/op
BenchmarkClamp_Fix              ~575 ns/op     576 B/op     6 allocs/op
BenchmarkClampTime_Fix          ~629 ns/op     616 B/op     7 allocs/op
BenchmarkDefault_Zero           ~527 ns/op     568 B/op     5 allocs/op
BenchmarkDefaultStr_Empty       ~804 ns/op     584 B/op     6 allocs/op
BenchmarkDefaultOneOf_Invalid   ~812 ns/op     600 B/op     7 allocs/op
```

### Analysis

**Required (valid):** ~6 ns, 0 allocs. `strings.TrimSpace` + length check. Effectively free.

**Required (invalid):** ~446 ns, 5 allocs. Error construction dominates — `errx.New` + metadata.

**Email:** ~443 ns, 0 allocs. Regex match — no allocation on valid input.

**Collect (3 fields, all valid):** ~312 ns, 0 allocs. Three nil checks + return. No work when all pass.

**Match:** ~4.7 us, 46 allocs. `regexp.Compile` on every call. Cache the `regexp.Regexp` for hot paths.

**Clamp (in range):** ~3 ns, 0 allocs. Simple comparison. Free.

**Clamp (fix):** ~575 ns, 6 allocs. Mutation + `errx.New` with `CodeFixed`.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| Valid field | < 10 ns, 0 allocs | Free |
| Invalid field | < 500 ns, 5 allocs | Error construction cost |
| Clamp (no-op) | < 5 ns, 0 allocs | Free |
| Clamp (fix) | < 600 ns, 6 allocs | Acceptable |
| Collect (all valid) | < 350 ns, 0 allocs | Fast aggregation |
| Match (regex) | < 5 us, 46 allocs | Cache regex for hot paths |

## What validx does NOT do

| Concern | Owner |
|---------|-------|
| Struct-level validation | caller iterates fields |
| Struct tags / reflection | `go-playground/validator` |
| Custom error messages | `i18n.TranslateError` |
| Database constraints | DB layer |
| Sanitization (XSS) | `html.EscapeString` / caller |

## File structure

```text
pkg/validx/
    validx.go      -- Required(), MinLen(), MaxLen(), Between(), Match(), OneOf(), Email(), URL(), Collect()
    fix.go         -- Clamp(), Default(), DefaultStr(), DefaultTime(), DefaultOneOf(), ClampTime(), BetweenTime()
    errors.go      -- DomainValidation, Code constants, error constructors
    validx_test.go -- 25 tests
    fix_test.go    -- 42 tests
    bench_test.go  -- 11 benchmarks
    README.md
```
