# clix

Declarative, type-safe CLI parser with generic flag binding, nested subcommands,
and structured error reporting via `errx`.

## Philosophy

**One job: parse CLI arguments.** `clix` turns `os.Args` into typed Go
variables using generics. Flags are bound to `*T` pointers via `AddFlag[T]`;
subcommands are nested with `SubCommand`. Every parse error is a structured
`*errx.Error` with domain `CLI`. No reflection, no struct tags, no code
generation.

## Quick start

```go
var host string
var port int

p := clix.New(os.Args, "myapp", "My service",
    clix.AddFlag(&host, "host", "H", "localhost", "Bind address"),
    clix.AddFlag(&port, "port", "p", 8080, "Listen port"),
    clix.Run(func(c *clix.Context) error {
        fmt.Printf("Listening on %s:%d\n", host, port)
        return nil
    }),
)

if err := p.Err(); err != nil {
    fmt.Fprintln(os.Stderr, p.Help())
    os.Exit(1)
}
```

## API

| Function / Method | Description |
|---|---|
| `New(osArgs, name, desc, opts...) *Parser` | Build command tree, parse args, return Parser |
| `p.Err() error` | First parse/action error, or `ErrHelp` for `--help` |
| `p.Help() string` | Formatted help text for the matched command |
| `AddFlag[T](target, name, short, def, usage, extras...) Option` | Register a typed flag bound to `*T` |
| `Required() func(*flagMeta)` | Mark flag as required |
| `Enum(vals...) func(*flagMeta)` | Restrict flag to allowed values |
| `SubCommand(name, desc, opts...) Option` | Register a subcommand |
| `Run(fn Action) Option` | Set the action for the command |
| `(c *Context) Args() []string` | Positional arguments after `--` |
| `(c *Context) Command() *Command` | Matched command node |
| `(c *Context) Parser() *Parser` | Parser that produced this context |
| `(cmd *Command) Name() string` | Command name |
| `(cmd *Command) Parent() *Command` | Parent command (nil for root) |

### Supported flag types

`string`, `int`, `float64`, `bool`, `time.Duration`, `time.Time` — resolved via
`AddFlag[T]` generic constraint.

## Behavior details

- **Single-pass parsing**: `New` builds the command tree and parses arguments
  in one call. The returned `Parser` holds the result — no mutable state after
  construction.

- **Flag inheritance**: parent flags are inherited by subcommands. A flag
  `--verbose` on the root is available in every subcommand.

- **`--help` / `-h`**: triggers `ErrHelp` sentinel. Callers check `p.Err()`
  and print `p.Help()`.

- **POSIX grouped short flags**: `-vdq` expands to `-v -d -q`. All flags in
  the group must be bool except optionally the last, which may consume the
  next argument or the remainder of the group as its value (`-vp 3000` or
  `-vp3000`).

- **Required & Enum**: `Required()` validates that the flag was explicitly
  provided (including inherited required flags from parent commands).
  `Enum(vals...)` restricts the value to an allowed set.

- **`--` separator**: arguments after `--` are collected as positional args
  and available via `Context.Args()`.

## Error diagnostics

All errors are `*errx.Error` with `Domain = "CLI"`.

### Codes

| Code | When |
|---|---|
| `UNKNOWN_FLAG` | Unrecognized `--flag` |
| `UNKNOWN_COMMAND` | Unrecognized subcommand |
| `MISSING_VALUE` | Flag requires a value but none given |
| `INVALID_VALUE` | Value cannot be parsed as the target type |
| `REQUIRED` | Required flag not provided |
| `ENUM_VIOLATED` | Value not in the allowed set |

### Example

```text
CLI.UNKNOWN_FLAG: unknown flag --foo | meta: flag=--foo
CLI.REQUIRED: required flag not set | meta: flag=--host
```

## Thread safety

- `Parser` and `Command` are immutable after `New` — safe for concurrent reads
- No mutable state after construction — no locks needed

## Tests

**67 tests, 95.1% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/clix  coverage: 95.1% of statements
```

Coverage includes:
- Flag parsing: string, int, float64, bool, duration, time (RFC 3339)
- Flag extras: Required, Enum, default values
- Inherited required flags are enforced in subcommands
- Short flags: `-p`, POSIX grouped `-vdq`, grouped with trailing value `-vp 3000`
- `--key=value` and `--key value` syntax
- Subcommands: nested, multi-level, unknown command
- Help generation: root, subcommand, inherited flags
- Positional args: `--` separator
- Error diagnostics: all 6 error codes

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz.
Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkNew_Simple       ~2116 ns/op    1768 B/op    26 allocs/op
BenchmarkNew_SubCommand   ~3120 ns/op    2872 B/op    38 allocs/op
BenchmarkHelp             ~1125 ns/op     520 B/op     8 allocs/op
BenchmarkAddFlag_Generic  ~1001 ns/op    1088 B/op    13 allocs/op
```

### Analysis

**New (simple):** ~2.1 us, 26 allocs. Builds command tree + parses flags in a single pass. The allocations are flag structs, map entries, and the parser itself. One-time startup cost.

**New (subcommand):** ~3.1 us, 38 allocs. Additional command node and flag inheritance. Still under 5 us.

**Help:** ~1.1 us, 8 allocs. String building with `strings.Builder`. Called at most once per invocation.

**AddFlag (generic):** ~1.0 us, 13 allocs. Type-erased flag registration with generics. One-time registration cost.

### Performance summary

| Path | Budget | Verdict |
|------|--------|---------|
| New (simple) | < 3 us | One-time startup, negligible |
| New (subcommand) | < 4 us | One-time startup, negligible |
| Help | < 2 us | Rare call, negligible |
| AddFlag | < 2 us | One-time registration |

CLI parsing happens once at startup. Even at 3 us, it is invisible in any real application.

## What clix does NOT do

| Concern | Owner |
|---------|-------|
| Environment variables | `os.Getenv` / caller |
| Config files | `viper`, `koanf`, or caller |
| Validation beyond enum | `validx` |
| Interactive prompts | caller |
| Shell completion | caller |

## File structure

```text
pkg/clix/
    clix.go        -- Parser, Command, Context, New(), AddFlag(), SubCommand()
    errors.go      -- DomainCLI, Code constants, ErrHelp, error constructors
    clix_test.go   -- 67 tests, 95.1% coverage
    bench_test.go  -- 4 benchmarks
    README.md
```
