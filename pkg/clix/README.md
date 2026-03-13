# clix

Declarative, type-safe CLI parser with generic flag binding, nested subcommands, and structured error reporting via `errx`.

## Philosophy

**One job: parse CLI arguments.** `clix` turns `os.Args` into typed Go variables using generics. Flags are bound to `*T` pointers via `AddFlag[T]`; subcommands are nested with `SubCommand`. Every parse error is a structured `*errx.Error` with domain `CLI`. No reflection, no struct tags, no code generation.

## Quick start

```go
var host string
var port int

p := clix.New(os.Args[1:], "myapp", "My service",
    clix.AddFlag(&host, "host", "H", "localhost", "Bind address"),
    clix.AddFlag(&port, "port", "p", 8080, "Listen port"),
    clix.Version("1.0.0"),
    clix.SubCommand("serve", "start the server",
        clix.Run(func(c *clix.Context) error {
            fmt.Printf("Listening on %s:%d\n", host, port)
            return nil
        }),
    ),
)

if errors.Is(p.Err(), clix.ErrHelp) {
    fmt.Println(p.Help())
    os.Exit(0)
}
if errors.Is(p.Err(), clix.ErrVersion) {
    fmt.Println(p.Version())
    os.Exit(0)
}
if err := p.Err(); err != nil {
    fmt.Fprintln(os.Stderr, err)
    os.Exit(1)
}
if err := p.Run(); err != nil {
    fmt.Fprintln(os.Stderr, err)
    os.Exit(1)
}
```

## API

| Function / Method | Description |
|---|---|
| `New(osArgs, name, desc, opts...) *Parser` | Build command tree, parse args, return Parser |
| `p.Err() error` | First parse error, `ErrHelp` for `--help`, `ErrVersion` for `--version` |
| `p.Help() string` | Formatted help text for the matched command |
| `p.Version() string` | Version string set via `Version()`, or `""` |
| `p.Run() error` | Execute the matched command's action |
| `AddFlag[T](target, name, short, def, usage, extras...) Option` | Register a typed flag bound to `*T` |
| `Required() func(*flagMeta)` | Mark flag as required |
| `Enum(vals...) func(*flagMeta)` | Restrict flag to allowed values |
| `SubCommand(name, desc, opts...) Option` | Register a subcommand |
| `Run(fn Action) Option` | Set the action for the command |
| `Alias(names...) Option` | Register alternative names for a subcommand |
| `Version(v string) Option` | Enable `--version` / `-V` handling |
| `(c *Context) Args() []string` | Positional arguments after `--` |
| `(c *Context) Command() *Command` | Matched command node |
| `(c *Context) Parser() *Parser` | Parser that produced this context |
| `(cmd *Command) Name() string` | Command name |
| `(cmd *Command) Description() string` | Command description |
| `(cmd *Command) Parent() *Command` | Parent command (nil for root) |

### Supported flag types

`string`, `int`, `float64`, `bool`, `time.Duration`, `time.Time` — resolved via `AddFlag[T]` generic constraint.

## Behavior details

- **Parse / Run separation**: `New` builds the command tree and parses arguments but does NOT execute the action. Call `p.Run()` explicitly. This allows callers to inspect parse results, add middleware, or skip execution in tests.

- **Single-pass parsing**: `New` builds the command tree and parses arguments in one call. The returned `Parser` holds the result — no mutable state after construction.

- **Flag inheritance**: parent flags are inherited by subcommands. A flag `--verbose` on the root is available in every subcommand.

- **`--help` / `-h`**: triggers `ErrHelp` sentinel. Callers check `p.Err()` and print `p.Help()`. Works at any nesting level — `app serve --help` shows help for `serve`. Also works inside POSIX grouped flags: `-vh` triggers help.

- **`--version` / `-V`**: triggers `ErrVersion` sentinel when `Version()` is set. Callers check `p.Err()` and print `p.Version()`. When `Version()` is not set, `--version` is treated as an unknown flag.

- **Subcommand aliases**: `Alias("x", "ex")` registers alternative names that resolve to the same subcommand. Aliases appear in help output next to the primary name.

- **POSIX grouped short flags**: `-vdq` expands to `-v -d -q`. All flags in the group must be bool except optionally the last, which may consume the next argument or the remainder of the group as its value (`-vp 3000` or `-vp3000`).

- **Required & Enum**: `Required()` validates that the flag was explicitly provided (including inherited required flags from parent commands). `Enum(vals...)` restricts the value to an allowed set.

- **`--` separator**: arguments after `--` are collected as positional args and available via `Context.Args()`.

- **Adaptive help**: column widths in help output adjust automatically to the longest flag name, subcommand name (including aliases), and default value — no truncation, no misalignment.

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

### Sentinels

| Sentinel | When |
|---|---|
| `ErrHelp` | `--help` / `-h` |
| `ErrVersion` | `--version` / `-V` (only when `Version()` is set) |

### Example

```text
CLI.UNKNOWN_FLAG: unknown flag --foo | meta: flag=--foo
CLI.REQUIRED: required flag not set | meta: flag=--host
```

## Fail-fast panics

Programming mistakes are caught at construction time:

| Mistake | Panic |
|---|---|
| Duplicate flag name | `clix: duplicate flag --name` |
| Duplicate short alias | `clix: duplicate short flag -x` |
| Duplicate subcommand name | `clix: duplicate subcommand "name"` |
| Duplicate subcommand/alias collision | `clix: duplicate subcommand/alias "name"` |
| Duplicate `Run` on same command | `clix: duplicate Run on command "name"` |
| Unsupported flag type | `clix: unsupported flag type T` |
| Enum type mismatch | `clix: enum value ... does not match flag type T` |
| Nil flag target | `clix: nil target for --name` |

## Thread safety

`Parser` and `Command` are immutable after `New` — safe for concurrent reads. No mutable state after construction, no locks needed.

## Tests

**83 tests, 96.0% statement coverage.**

```bash
go test -race -count=1 -coverprofile=coverage.out ./...
ok  github.com/aasyanov/urx/pkg/clix  coverage: 96.0% of statements
```

Coverage includes: flag parsing (string, int, float64, bool, duration, time), flag extras (Required, Enum, defaults), inherited required flags, short flags (`-p`, POSIX grouped `-vdq`, trailing value `-vp 3000`), `--key=value` and `--key value` syntax, subcommands (nested, multi-level, unknown command, aliases), help generation (root, subcommand, inherited flags, adaptive columns), positional args (`--` separator), all 6 error codes, Parse/Run separation, Version (`--version`, `-V`, not-set fallback), aliases (dispatch, multiple, duplicate panic), duplicate Run panic.

## Benchmarks

Environment: `go1.24.0 windows/amd64`, Intel Core i7-10510U @ 1.80 GHz. Each benchmark was run 3 times (`-count=3`); the table shows median values.

```text
BenchmarkNew_Simple       ~2116 ns/op    1768 B/op    26 allocs/op
BenchmarkNew_SubCommand   ~3120 ns/op    2872 B/op    38 allocs/op
BenchmarkHelp             ~1125 ns/op     520 B/op     8 allocs/op
BenchmarkAddFlag_Generic  ~1001 ns/op    1088 B/op    13 allocs/op
```

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

```
pkg/clix/
    clix.go        -- Parser, Command, Context, New(), AddFlag(), SubCommand(), Alias(), Version()
    errors.go      -- DomainCLI, Code constants, ErrHelp, ErrVersion, error constructors
    clix_test.go   -- 83 tests, 96.0% coverage
    bench_test.go  -- 4 benchmarks
    README.md
```
