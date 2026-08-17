# clix — Declarative, type-safe CLI parser with subcommands

[CI](https://github.com/aasyanov/urx/actions/workflows/ci.yml)
[Go Reference](https://pkg.go.dev/github.com/aasyanov/urx/clix)
[Changelog](../CHANGELOG.md)
[License: MIT](../LICENSE)

A declarative command-line parser with generic flag binding, nested subcommands, flag inheritance, and structured error reporting. Zero external dependencies, Go 1.24+.

```
go get github.com/aasyanov/urx
```

> [!IMPORTANT]
> `clix` separates parsing from execution. [`New`] builds the command tree and parses arguments synchronously, but it does **not** run the matched action. You call [`Parser.Run`] yourself after inspecting [`Parser.Err`]. This is deliberate: it lets you check parse errors, print help, add middleware, or skip execution entirely in tests.



## The Problem

CLI tools accrete flags and subcommands over time. A tool that starts as `mytool --verbose input.txt` grows into `mytool serve --port 8080 --no-color` with a dozen flags split across commands. Hand-rolled parsers handle this badly:

1. **Type assertions everywhere.** A flag declared as an int is read back via `flags["port"].(int)`, and a typo or type mismatch surfaces at runtime in production, not at compile time.
2. **Silent coercion.** Many minimal parsers fall back to a default when `--port abc` fails to parse, hiding user error instead of reporting it.
3. **No structured errors.** Parse failures arrive as bare strings, so callers cannot map them to exit codes, translate them, or branch on the failure kind.
4. **Subcommands bolted on late.** Adding `serve`/`migrate`/`version` to a flat parser usually means rewriting the dispatch loop.
5. **Global flags duplicated.** A `--verbose` flag that should apply to every subcommand ends up redeclared on each one.

`clix` addresses these by binding flags to typed pointers via generics, failing fast on bad values with sentinel errors, modelling the command tree as first-class data, and resolving flags up the parent chain so globals are declared once.

## Architectural Position

`clix` **is** a parser and command-tree builder for single-process CLI tools. It **is not**:

- a shell-completion engine (no bash/zsh completion generation);
- a config system — pair it with `cfgx`/`envx` for file/env layering;
- an interactive prompt or TUI library;
- a long-running command framework — it parses, dispatches once, and returns.

It occupies the niche of "Cobra-style command tree with generics and zero dependencies."

### Position in the urx Stack

```text
┌──────────────────────────────────────────────────────────┐
│  main(): parse flags, dispatch subcommand, run action    │
└────────────────────────┬─────────────────────────────────┘
                         │
┌────────────────────────▼─────────────────────────────────┐
│  clix   Parser · Command tree · generic flag binding     │
└──────────────┬────────────────────────┬──────────────────┘
               │                        │
┌──────────────▼─────────┐   ┌──────────▼──────────────────┐
│  cfgx (file defaults)  │   │  envx (env overrides)       │
│  shared struct fields  │   │  shared struct fields       │
└────────────────────────┘   └─────────────────────────────┘
```



## Architecture

```
                       New(osArgs, name, desc, opts...)
                                   │
                 ┌─────────────────┼──────────────────┐
                 │ build root Command (apply options) │
                 └─────────────────┬──────────────────┘
                                   │
                            parseArgs(root)
                                   │
        ┌──────────────┬───────────┼───────────┬──────────────┐
        │              │           │           │              │
   "--" stops     --help / -h  subcommand   flag token    positional
    flag parse    → ErrHelp    → recurse    resolve+set    → cmd.args
        │              │           │           │              │
        └──────────────┴───────────┴───────────┴──────────────┘
                                   │
                          checkRequired(chain)
                                   │
                         Parser{ matched, parseErr }
                                   │
                ┌──────────────────┼───────────────────┐
                │ Err()    Help()   IsSet()   Run()    │
                └──────────────────────────────────────┘

  Command tree (flags inherit downward):

     root ── flags: --verbose (global)
       ├── serve  ── flags: --port
       └── migrate ── flags: --steps
```



## How It Works

Parsing is a single left-to-right pass over the argument slice:

1. **Terminator.** A bare `--` stops flag parsing; everything after it becomes positional.
2. **Help / version.** `--help`/`-h` returns [`ErrHelp`]; `--version`/`-V` returns [`ErrVersion`] when [`Version`] was set. Both record the matched command so [`Parser.Help`] targets the right level. These control flags are reserved (construction panics if you register the same names) and appear in the FLAGS legend.
3. **Subcommand dispatch.** A non-flag token that matches a registered subcommand (or alias) recurses into that command with the remaining args. A lone `-` is a positional argument (stdin convention, same as `flag.FlagSet`), not a flag; on a routing node (subcommands, no action) it is [`ErrUnknownCommand`] because `-` is not a command name.
4. **Grouped short flags.** A token like `-vdq` expands to `-v -d -q`. The last flag in the group may be non-bool and consume either the rest of the group (`-vp3000`) or the next argument (`-vp 3000`).
5. **Long / short / inline flags.** `--port 8080`, `-p 8080`, `--port=8080`, and `-p=8080` are all recognised. Bool flags accept `--flag`, `--flag=true`, and the negation `--no-flag`. A non-bool flag whose next token looks like another flag (`--port --verbose`, `--name --`, `-p -v`) returns [`ErrMissingValue`]; bind a dash-prefixed string with the inline form (`--name=--raw`). Signed numbers (`-5`, `-1s`) and a bare `-` are values, not flags. `-5` as a token is still [`ErrUnknownFlag`]. `--helpers` is not `--help`.
6. **Flag resolution.** A flag is looked up on the current command, then up the parent chain — this is how global flags inherit into subcommands.
7. **Required check.** After the pass, every [`Required`] flag that never appeared on argv produces [`ErrRequired`]. A prefilled pointer from file/env does not count.

Each flag carries a generic setter captured at [`AddFlag`] time. The setter parses the raw string into the flag's type `T`, validates against any [`Enum`] set, writes the bound pointer, and marks the flag as set so [`Parser.IsSet`] (long name or short alias) can distinguish a user-supplied zero from a default.

### Help legend

[`Parser.Help`] is the user-facing contract for the command tree. A nested `--help` prints the full path, immediate subcommands (with aliases), local flags plus the built-in control flags, and inherited flags under GLOBAL FLAGS (root first):

```
USAGE: myapp db migrate [flags]

run migrations

FLAGS:
  --steps, -s            [1]        migration steps
  --help, -h                        show help
  --version, -V                     print version

GLOBAL FLAGS:
  --verbose, -v                     verbose output
```

`--help` / `-h` always appear. `--version` / `-V` appear only when [`Version`] was passed to [`New`]. They are not user-bindable flags: the parser intercepts them and returns [`ErrHelp`] / [`ErrVersion`].

Headings and built-in usage strings are English by default. [`WithHelpLabels`] replaces that chrome (for i18n) without translating command names, aliases, or flag names.

## Normative Contracts


| Invariant              | Guarantee                                                                                                                      |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| Parse ≠ Run            | [`New`] never executes an action; only [`Parser.Run`] does.                                                                    |
| Run after error        | [`Parser.Run`] returns nil (no-op) when [`Parser.Err`] is non-nil.                                                             |
| IsSet semantics        | True only when the flag appeared on the command line, even for zero values. `name` may be the long flag or the short alias.    |
| Reset idempotence      | [`Parser.Reset`] restores bound targets to defaults and clears positionals before re-parsing.                                  |
| Fail-fast construction | Misconfiguration (empty names, duplicates, shadow flags, bad types, enum mismatch, reserved names, `Version`/`WithHelpLabels` on a subcommand) panics inside [`New`], never at parse time. |
| Structured errors      | Every parse error wraps a sentinel; compare with [`errors.Is`].                                                                |
| Flag-like next token   | A non-bool flag does not swallow `--foo` / `-v` / `--` as a value ([`ErrMissingValue`]). `-5` / `-1s` / `-` still bind. Inline `--flag=--raw` binds a dash-prefixed string. |




## Quick Start

```go
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/aasyanov/urx/clix"
)

func main() {
	var port int
	var verbose bool

	p := clix.New(os.Args[1:], "myapp", "my awesome tool",
		clix.AddFlag(&verbose, "verbose", "v", false, "enable verbose output"),
		clix.AddFlag(&port, "port", "p", 8080, "listen port"),
		clix.SubCommand("serve", "start the server",
			clix.Run(func(*clix.Context) error {
				fmt.Printf("serving on %d (verbose=%v)\n", port, verbose)
				return nil
			}),
		),
		clix.Version("1.2.3"),
	)

	if errors.Is(p.Err(), clix.ErrHelp) {
		fmt.Println(p.Help())
		return
	}
	if errors.Is(p.Err(), clix.ErrVersion) {
		fmt.Println(p.Version())
		return
	}
	if err := p.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
```



## Usage Scenarios



### Required flag with enum validation

```go
var env string
p := clix.New(os.Args[1:], "deploy", "deploy a service",
	clix.AddFlag(&env, "env", "e", "dev", "target environment",
		clix.Required(),
		clix.Enum("dev", "staging", "prod"),
	),
)
if errors.Is(p.Err(), clix.ErrEnumViolated) {
	fmt.Fprintln(os.Stderr, "env must be one of dev/staging/prod")
	os.Exit(2)
}
```



### Global flag inherited by subcommands

```go
var verbose bool
p := clix.New(os.Args[1:], "app", "my app",
	clix.AddFlag(&verbose, "verbose", "v", false, "verbose output"),
	clix.SubCommand("serve", "start server", clix.Run(serve)),
	clix.SubCommand("migrate", "run migrations", clix.Run(migrate)),
)
// "app serve --verbose" and "app migrate --verbose" both work —
// verbose is resolved from the root command.
```



### Distinguishing an explicit zero from a default

```go
var replicas int
p := clix.New(os.Args[1:], "scale", "scale a deployment",
	clix.AddFlag(&replicas, "replicas", "r", 3, "desired replicas"),
)
if p.IsSet("replicas") {
	applyScale(replicas) // user asked for exactly this, even 0
} else {
	// keep current replica count; the default 3 was not requested
}
```



### Re-parsing in table-driven tests

```go
var count int
p := clix.New(nil, "tool", "desc",
	clix.AddFlag(&count, "count", "c", 0, "iterations"),
)
for _, tc := range cases {
	require.NoError(t, p.Reset(tc.args))
	assert.Equal(t, tc.want, count)
}
```



## API


| Symbol                | Signature                                                                                         | Description                                                  |
| --------------------- | ------------------------------------------------------------------------------------------------- | ------------------------------------------------------------ |
| `New`                 | `New(osArgs []string, name, desc string, opts ...Option) *Parser`                                 | Build the command tree and parse arguments.                  |
| `Action`              | `func(*Context) error`                                                                            | Handler run by [`Parser.Run`] when the command matches.      |
| `Option`              | `func(*Command)`                                                                                  | Construction option for [`New`] / [`SubCommand`].            |
| `FlagOption`          | `type FlagOption func(*flagMeta)`                                                                 | Type of [`Required`] / [`Enum`]; not implementable outside this package. |
| `Parser`              | `struct`                                                                                          | Parse result from [`New`]: Err / Help / Run / Reset.         |
| `Command`             | `struct`                                                                                          | Node in the command tree (name, flags, children, action).    |
| `Context`             | `struct`                                                                                          | Runtime view passed to an [`Action`].                        |
| `HelpLabels`          | `struct`                                                                                          | Optional translations for help headings / metavars / suffixes. |
| `AddFlag`             | `AddFlag[T any](target *T, name, short string, def T, usage string, extras ...FlagOption) Option` | Register a typed flag bound to a pointer.                    |
| `SubCommand`          | `SubCommand(name, desc string, opts ...Option) Option`                                            | Register a nested subcommand.                                |
| `Run`                 | `Run(fn Action) Option`                                                                           | Set the action executed when the command matches.            |
| `Alias`               | `Alias(names ...string) Option`                                                                   | Register alternative names for a subcommand.                 |
| `Version`             | `Version(v string) Option`                                                                        | Enable `--version` / `-V` handling.                          |
| `WithHelpLabels`      | `WithHelpLabels(HelpLabels) Option`                                                               | Replace help chrome (headings, builtins); names stay as given. |
| `Required`            | `Required() FlagOption`                                                                           | Mark a flag mandatory (extra for `AddFlag`).                 |
| `Enum`                | `Enum(vals ...any) FlagOption`                                                                    | Restrict a flag to a closed value set (extra for `AddFlag`). |
| `Parser.Err`          | `Err() error`                                                                                     | First parse error, or `ErrHelp`/`ErrVersion`.                |
| `Parser.Run`          | `Run() error`                                                                                     | Execute the matched action (no-op on parse error).           |
| `Parser.Help`         | `Help() string`                                                                                   | Formatted help for the matched command, including built-in `--help`/`--version`. |
| `Parser.Version`      | `Version() string`                                                                                | Version string, or "".                                       |
| `Parser.Command`      | `Command() *Command`                                                                              | The matched command (root if none matched).                  |
| `Parser.IsSet`        | `IsSet(name string) bool`                                                                         | Whether a flag was explicitly provided (long name or short alias). |
| `Parser.Reset`        | `Reset(osArgs []string) error`                                                                    | Re-parse new args against the same tree.                     |
| `Context.Args`        | `Args() []string`                                                                                 | Live positional slice for the matched command (not a copy).  |
| `Context.Command`     | `Command() *Command`                                                                              | The matched command.                                         |
| `Context.Parser`      | `Parser() *Parser`                                                                                | The owning parser.                                           |
| `Command.Name`        | `Name() string`                                                                                   | Command name.                                                |
| `Command.Description` | `Description() string`                                                                            | One-line description.                                        |
| `Command.Parent`      | `Parent() *Command`                                                                               | Parent command (nil for root).                               |
| `Command.Args`        | `Args() []string`                                                                                 | Live positional slice (not a copy).                          |




## Flag Types


| Type            | Parsed via                     | Example input               |
| --------------- | ------------------------------ | --------------------------- |
| `string`        | identity                       | `--name foo`                |
| `int`           | `strconv.Atoi`                 | `--port 8080`               |
| `bool`          | `strconv.ParseBool` + presence | `--verbose`, `--no-verbose` |
| `float64`       | `strconv.ParseFloat`           | `--ratio 0.75`              |
| `time.Duration` | `time.ParseDuration`           | `--timeout 30s`             |
| `time.Time`     | `time.Parse(time.RFC3339)`     | `--at 2025-01-02T15:04:05Z` |


Any other type panics inside [`New`] at construction time.

## Errors


| Error               | Condition                                                                        |
| ------------------- | -------------------------------------------------------------------------------- |
| `ErrHelp`           | `--help` or `-h` encountered (control signal, not a failure).                    |
| `ErrVersion`        | `--version` or `-V` encountered and `Version` was set.                           |
| `ErrUnknownFlag`    | A flag-like token has no matching definition on the command or any ancestor.     |
| `ErrUnknownCommand` | A command has subcommands but no action, and an unrecognised positional appears. |
| `ErrMissingValue`   | A non-bool flag has no value: last token, or the next token looks like another flag. Dash-prefixed strings need `--flag=value`. Signed numbers (`-5`, `-1s`) are values. |
| `ErrInvalidValue`   | A value cannot be parsed into the flag's declared type.                          |
| `ErrRequired`       | A `Required` flag was not provided.                                              |
| `ErrEnumViolated`   | A value is outside the `Enum` set.                                               |




## Pitfalls

> [!WARNING]
> `New` does not run actions. Forgetting to call `Parser.Run` after a successful parse means your handler never executes. Always: check `Err`, then call `Run`.

> [!WARNING]
> Construction-time mistakes panic. Empty command/flag/alias names, duplicate flag/short names, shadowing an inherited flag on a subcommand, duplicate subcommands, duplicate `Run`, unsupported flag types, enum/type mismatches, reserved `--help`/`-h` (and `--version`/`-V` when `Version` is set), multi-character shorts, `Version` on a subcommand, and `WithHelpLabels` on a subcommand panic inside `New`. This is intentional — these are programming errors that must never ship. Do not wrap `New` in `recover` to mask them.

> [!WARNING]
> Parse errors are not transactional. When parsing fails midway (`--port 9090 --bad`), flags parsed before the error remain written to their bound pointers. Always check [`Parser.Err`] before acting on flag values.

> [!WARNING]
> String flags consume exactly one token. `--msg hello world` sets `msg=hello` and leaves `world` as a positional. Quote multi-word values: `--msg "hello world"`. A following flag-like token is not a value: `--msg --raw` is [`ErrMissingValue`]. Use `--msg=--raw` (or `--msg -` for a bare dash).

> [!WARNING]
> [`AddFlag`] writes `def` into `*target` at construction. After `cfgx.Load` / `envx.BindTo`, pass the **current field value** as `def` (`AddFlag(&cfg.Port, "port", "p", cfg.Port, ...)`). A literal `8080` overwrites the file/env value even when the user passed no flag.

> [!WARNING]
> [`Required`] means the flag **appeared on argv**, not that the bound pointer already has a value. File/env cannot satisfy it. Enforce cross-source presence in a `cfgx.Validator`.

> [!WARNING]
> Flag types are a closed subset: `string`, `int`, `bool`, `float64`, `time.Duration`, and `time.Time`. Named types, `[]string`, and `int64` panic at [`AddFlag`]. Map those fields before binding.

## Known Limitations

- **POSIX grouped short ambiguity.** When a non-bool flag appears before the end of a cluster (`-pv` with `p`=port and `v`=verbose), the remainder of the token is consumed as that flag's value (`port="v"`), not parsed as further flags. `-vp 3000` and `-vp3000` bind verbose+port as expected. Use `-v -p 3000` when both flags need distinct values.
- **cfgx/envx type subset.** CLI flags support `string`, `int`, `bool`, `float64`, `time.Duration`, and `time.Time`. Environment-only types (`[]string`, `int64`, `uint`) must be mapped before sharing a struct field with [`AddFlag`].
- **Version and HelpLabels are root-scoped.** Pass [`Version`] and [`WithHelpLabels`] to the root [`New`] call. Passing either to [`SubCommand`] panics.
- **No `--no-*` rows in help.** Bool negation is parsed, but the legend lists the positive flag only.
- **No shell completion generation.** Help text is for human `--help` only.



## Safety and Concurrency

A `Parser` and its `Command` tree are **not** safe for concurrent use. The intended pattern is to build and consume the parser on a single goroutine during startup. The tree is effectively immutable after `New` returns (aside from `Reset`, which mutates parse state in place), so reading flag targets after parsing completes is safe once all writers have stopped.

## Benchmarks

Three environments, two hardware classes, two operating systems. All values are **medians** of `-count=3`. `B/op` and `allocs/op` are from this package on 2026-08-17 (code-determined). Laptop ns refreshed the same day on the i7-10510U. Linux/Windows ns are the last CI medians (Xeon 6973P-C / EPYC 7763, Go 1.26) — ranking holds; absolute Help ns on CI may read slightly low versus the current 14-alloc render.

### Environments


|            | Laptop                      | CI Server (Linux)     | CI Server (Windows)   |
| ---------- | --------------------------- | --------------------- | --------------------- |
| CPU | Intel Core i7-10510U, 4C/8T | Intel Xeon 6973P-C | AMD EPYC 7763 |
| TDP        | 15W (mobile, throttles)     | 280W (server, stable) | server, stable        |
| OS         | Windows 10                  | Ubuntu                | Windows Server 2022   |
| Go         | 1.26.2                      | 1.26                  | 1.26                  |
| GOMAXPROCS | 8                           | 4                     | 4                     |
| Runs       | 3 (`-count=3`)              | 3 (`-count=3`)        | 3 (`-count=3`)        |




### Parser Construction


| Benchmark      | What it measures           | Laptop  | Linux       | Windows     | B/op | allocs/op |
| -------------- | -------------------------- | ------- | ----------- | ----------- | ---- | --------- |
| New_SingleFlag | One typed flag + maps      | 1.39 µs | **471.6 ns** | 1005 ns | 1232 | 15 |
| New_ManyFlags  | Five flags                 | 4.36 µs | **1.31 µs** | 2.87 µs | 2576 | 34 |
| New_Subcommand | Subcommand tree            | 2.53 µs | **0.7 µs** | 1.45 µs | 1808 | 21 |
| Parser_Reset   | Reuse tree, zero allocs    | 145 ns | **28.6 ns** | 50.6 ns | 0 | 0 |
| Parser_Help    | Help text render           | 1.64 µs | **499.1 ns** | 1008 ns | 1200 | 14 |
| New_Parallel   | Independent `New` per iter | 1.26 µs | **488.4 ns** | 1.25 µs | 1560 | 20 |




### Analysis

**Construction is a one-time startup cost — Linux CI is ~2× faster than Windows.** `New_SingleFlag` is 471.6 ns (Linux) vs 1005 ns (Windows) in the table above. Each `AddFlag` allocates a `flagMeta`, a closure pair (`setFunc`/`resetFunc`) that escapes because it captures the bound pointer, and map entries. This is paid once per process, not per request. Laptop numbers swing with thermal throttle (1.39 µs here).

**Scales linearly with flag count.** `New_ManyFlags` (five flags) is ~2.8× `New_SingleFlag` on Linux (1.31 µs vs 471.6 ns) — dominated by per-flag closure and map insertions.

**Parser_Reset — 0 allocs on all platforms.** Reset reuses the existing command tree; it only clears `set` markers, resets the positional slice header, and calls captured `resetFunc`. Linux CI 28.6 ns; laptop 145 ns on a throttled 15 W chip. Re-running `New` per test case would reallocate the entire tree — `Reset` is the right tool for table-driven tests.

**Parser_Help — not a hot path.** 14 allocs / 1200 B come from `strings.Builder` growth, builtin `--help`/`--version` rows, and `HelpLabels` copies. Help renders at most once per invocation on `--help`.

**Parallel New builds independent parsers.** `New_Parallel` scales with cores and shows no shared-state contention. Linux (488.4 ns) beats laptop (1.26 µs) and Windows (1.25 µs) because server CPUs sustain higher single-thread throughput on allocation-heavy setup work.

**Allocation floor is architectural.** Generic type-safe binding without reflection requires a per-flag setter closing over `*T`. Eliminating the closures would mean reflection (slower) or losing compile-time type safety. The 0-alloc `Reset` proves steady-state parse cost once the tree exists.

## Quality


| Metric         | Value                   |
| -------------- | ----------------------- |
| Test functions | 43                      |
| Benchmarks     | 6                       |
| Fuzz targets   | 1                       |
| Examples       | 5                       |
| Coverage       | 98.5% statements        |
| Race detector  | All pass                |
| External deps  | 0 (testify in tests only) |

```
go test -race -count=1 ./clix/
golangci-lint run ./clix/
go test ./clix/ -coverprofile=cover.out
go test ./clix/ -run='^$' -bench=. -benchmem -count=3
go test ./clix/ -fuzz=FuzzParse -fuzztime=30s
```




## File Structure

```
clix/
├── clix.go            # package doc, New, Parser methods (Err/Run/Help/IsSet/Reset)
├── types.go           # Command, Context, Parser, flagMeta + accessors
├── options.go         # AddFlag, SubCommand, Run, Alias, Version, Required, Enum
├── parse.go           # parse engine, short-group expansion, flag-like next token, value conversion
├── help.go            # help text rendering + column-width constants
├── help_labels.go     # HelpLabels + WithHelpLabels
├── errors.go          # sentinel errors + internal wrapper helpers
├── clix_test.go       # unit + table-driven tests
├── bench_test.go      # benchmarks (sequential + parallel)
├── fuzz_test.go       # fuzz target (never-panic oracle)
├── footprint_test.go  # testx.AssertFootprint bounds for core types
├── example_test.go    # runnable GoDoc examples
└── README.md          # this file
```



## License

MIT — see [LICENSE](../LICENSE) in the repository root.