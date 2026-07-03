// Package clix provides a declarative, type-safe CLI parser with generic flag
// binding, nested subcommands, and structured error reporting.
//
// # Quick start
//
// The entire command tree is built in a single [New] call using functional
// options. Generic [AddFlag] binds a flag directly to a typed pointer,
// so the compiler guarantees the type — no runtime assertions needed.
//
//	var port int
//	var verbose bool
//
//	p := clix.New(os.Args[1:], "myapp", "my awesome tool",
//	    clix.AddFlag(&verbose, "verbose", "v", false, "enable verbose output"),
//	    clix.AddFlag(&port, "port", "p", 8080, "listen port"),
//	    clix.SubCommand("serve", "start the server",
//	        clix.Run(func(ctx *clix.Context) error {
//	            fmt.Println("serving on", port)
//	            return nil
//	        }),
//	    ),
//	)
//	if errors.Is(p.Err(), clix.ErrHelp) {
//	    fmt.Println(p.Help())
//	    os.Exit(0)
//	}
//	if err := p.Err(); err != nil {
//	    fmt.Fprintln(os.Stderr, err)
//	    os.Exit(1)
//	}
//	if err := p.Run(); err != nil {
//	    fmt.Fprintln(os.Stderr, err)
//	    os.Exit(1)
//	}
//
// # Flag types
//
// [AddFlag] supports string, int, bool, float64, [time.Duration], and
// [time.Time] (parsed as RFC 3339). Using any other type causes a panic
// at construction time so the mistake is caught on first startup.
//
// # Flag syntax
//
// The parser recognises long (--port 8080), short (-p 8080), and inline
// (--port=8080, -p=8080) forms. POSIX-style grouped short flags are
// supported: -vdq expands to -v -d -q. The last flag in a group may be
// non-bool and consume the next argument or the rest of the group as its
// value (e.g. -vp 3000 or -vp3000). A bare "--" stops flag parsing and
// sends the remaining tokens to positional arguments.
//
// # Bool negation
//
// Bool flags can be negated with the --no- prefix:
//
//	clix.AddFlag(&verbose, "verbose", "v", true, "verbose output")
//	// --no-verbose sets verbose to false
//
// # Flag inheritance
//
// Flags registered on a parent command are visible to all its subcommands.
// When a subcommand encounters an unknown flag, the parser walks up the
// parent chain looking for a match. Inherited flags appear under a
// separate "GLOBAL FLAGS" section in help output.
//
//	p := clix.New(os.Args[1:], "app", "my app",
//	    clix.AddFlag(&verbose, "verbose", "v", false, "verbose output"),
//	    clix.SubCommand("serve", "start server",
//	        clix.Run(serveAction),
//	    ),
//	)
//	// "app serve --verbose" works — verbose is resolved from root.
//
// # Required flags and enum validation
//
// Pass [Required] or [Enum] as extras to [AddFlag]:
//
//	clix.AddFlag(&env, "env", "e", "dev", "target env",
//	    clix.Required(),
//	    clix.Enum("dev", "staging", "prod"),
//	)
//
// [Enum] values must match the flag type at construction time; a type
// mismatch causes a panic.
//
// # Detecting explicitly-set flags
//
// [Parser.IsSet] reports whether a flag was provided on the command line,
// distinguishing a user-supplied zero value (--port 0) from the default.
//
// # Subcommand aliases
//
// [SubCommand] can take an [Alias] option to register alternative names:
//
//	clix.SubCommand("extract", "extract data",
//	    clix.Alias("x"),
//	    clix.Run(extractAction),
//	)
//	// Both "app extract" and "app x" match.
//
// # Version
//
// Pass [Version] to [New] to enable automatic --version / -V handling:
//
//	p := clix.New(os.Args[1:], "myapp", "my tool",
//	    clix.Version("1.2.3"),
//	)
//
// # Parse / Run separation
//
// [New] builds the command tree and parses arguments but does NOT execute
// the matched action. Call [Parser.Run] explicitly to execute:
//
//	p := clix.New(os.Args[1:], "app", "desc", ...)
//	if err := p.Err(); err != nil { ... }  // check parse errors
//	if err := p.Run(); err != nil { ... }  // execute the action
//
// This separation allows callers to inspect parse results, add middleware,
// or skip execution in tests. [Parser.Reset] re-parses a new argument slice
// against the same command tree, which keeps table-driven tests concise.
//
// # Unknown command detection
//
// If a command has registered subcommands but no [Action], any unrecognised
// positional token produces an [ErrUnknownCommand] error listing the
// available subcommands. Commands that have both subcommands and an action
// treat unrecognised tokens as positional arguments.
//
// # Structured errors
//
// Every parse error wraps one of the sentinel error values
// ([ErrUnknownFlag], [ErrUnknownCommand], [ErrMissingValue],
// [ErrInvalidValue], [ErrRequired], [ErrEnumViolated]). Callers can use
// [errors.Is] for programmatic handling. The special sentinel [ErrHelp]
// is returned for --help / -h. [ErrVersion] is returned for --version / -V.
//
// # cfgx → envx → clix pipeline
//
// clix is the CLI override layer in the urx configuration stack. Bind the
// same struct fields that [cfgx] loads from files and [envx] overlays from
// the environment — all through plain pointer sharing:
//
//	cfgx.Load("config.yaml", &cfg)
//	envx.BindTo(env, "PORT", &cfg.Port)
//	clix.AddFlag(&cfg.Port, "port", "p", cfg.Port, "listen port")
//
// Supported flag types (string, int, bool, float64, [time.Duration],
// [time.Time]) cover the common subset shared with envx. Types such as
// []string, int64, and uint are env-only; map them explicitly before binding.
//
// # Fail-fast panics
//
// Programming mistakes are caught at construction time via panics:
// empty command or flag names, duplicate flag names or short aliases,
// shadowing an inherited flag on a subcommand, duplicate subcommand names,
// duplicate [Run] on the same command, unsupported flag types, and enum
// type mismatches. These fire on the very first run, making
// misconfiguration impossible to ship.
//
// # Help output
//
// [Parser.Help] returns a formatted string with USAGE, COMMANDS, FLAGS,
// and GLOBAL FLAGS sections. It is generated for whichever command was
// matched (or root if none). Column widths adapt to the actual content.
//
// # Zero dependencies
//
// clix depends only on the Go standard library.
package clix

// New builds the command tree from opts, parses osArgs, and returns a
// [Parser]. Parsing happens synchronously inside New but the matched
// action is NOT executed — call [Parser.Run] to run it.
//
// If name is empty, New panics.
//
// If --help or -h is encountered at any level, [Parser.Err] returns
// [ErrHelp] and [Parser.Help] returns the help text for that level.
//
// If --version or -V is encountered and [Version] was provided,
// [Parser.Err] returns [ErrVersion].
//
// All parse errors wrap sentinel error values from this package.
// Use [errors.Is] to distinguish them for programmatic handling.
func New(osArgs []string, name, desc string, opts ...Option) *Parser {
	if name == "" {
		panic("clix: empty command name")
	}
	root := newCommand(name, desc)
	for _, opt := range opts {
		opt(root)
	}

	p := &Parser{root: root, matched: root, version: root.version}
	p.parseErr = parseArgs(root, osArgs, p)
	return p
}

// Err returns the first error encountered during parsing, or nil when
// parsing succeeded. Returns [ErrHelp] when --help / -h was encountered
// and [ErrVersion] when --version / -V was encountered — use
// [errors.Is] to distinguish them from real errors.
func (p *Parser) Err() error { return p.parseErr }

// Help returns the formatted help string for whichever command was matched
// during parsing. If no subcommand was matched, it returns help for root.
// The output includes USAGE, COMMANDS, FLAGS, and GLOBAL FLAGS sections
// as applicable.
func (p *Parser) Help() string { return p.matched.help() }

// Version returns the version string set via [Version], or "" if none.
func (p *Parser) Version() string { return p.version }

// Command returns the [Command] that was matched during parsing. When no
// subcommand matched, this is the root command created by [New].
func (p *Parser) Command() *Command { return p.matched }

// IsSet reports whether the named flag was explicitly provided on the
// command line for the matched command (or any ancestor, via inheritance).
// It distinguishes a user-supplied zero value such as "--port 0" from the
// flag's default. Unknown flag names return false.
func (p *Parser) IsSet(name string) bool {
	meta, ok := resolveFlag(p.matched, name)
	if !ok {
		return false
	}
	return meta.set
}

// Run executes the matched command's action. Returns nil if no action was
// registered or if parsing failed (check [Parser.Err] first). This method
// is separate from [New] so callers can inspect parse results, add
// middleware, or skip execution in tests.
func (p *Parser) Run() error {
	if p.parseErr != nil {
		return nil
	}
	if p.matched.action != nil {
		return p.matched.action(&Context{command: p.matched, parser: p})
	}
	return nil
}

// Reset clears parsed state (matched command, positional arguments, and the
// per-flag "set" markers) and re-parses osArgs against the same command tree.
// Flag definitions, defaults, subcommands, and actions are preserved.
//
// Bound flag targets are reset to their defaults before re-parsing, so a
// Parser can be reused across multiple argument slices — useful in
// table-driven tests. Returns the new parse error (also available via
// [Parser.Err]).
func (p *Parser) Reset(osArgs []string) error {
	resetCommand(p.root)
	p.matched = p.root
	p.parseErr = parseArgs(p.root, osArgs, p)
	return p.parseErr
}

// resetCommand recursively clears per-parse state on cmd and its subcommands:
// positional args and the flag "set" markers, restoring bound targets to
// their defaults. subOrder holds each child exactly once, so alias duplicates
// in the subcommands map are not visited twice.
func resetCommand(cmd *Command) {
	cmd.args = nil
	for _, f := range cmd.flags {
		f.set = false
		if f.resetFunc != nil {
			f.resetFunc()
		}
	}
	for _, name := range cmd.subOrder {
		resetCommand(cmd.subcommands[name])
	}
}
