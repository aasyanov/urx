// Package clix provides a declarative, type-safe CLI parser with generic flag
// binding, nested subcommands, and structured error reporting via [errx].
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
// # Unknown command detection
//
// If a command has registered subcommands but no [Action], any unrecognised
// positional token produces a [CodeUnknownCommand] error listing the
// available subcommands. Commands that have both subcommands and an action
// treat unrecognised tokens as positional arguments.
//
// # Structured errors
//
// Every parse error is an [*errx.Error] with domain [DomainCLI] and one of
// the Code* constants ([CodeUnknownFlag], [CodeUnknownCommand],
// [CodeMissingValue], [CodeInvalidValue], [CodeRequired],
// [CodeEnumViolated]). Callers can switch on the code for programmatic
// handling. The special sentinel [ErrHelp] is returned for --help / -h.
//
// # Fail-fast panics
//
// Programming mistakes are caught at construction time via panics:
// duplicate flag names or short aliases, duplicate subcommand names,
// unsupported flag types, and enum type mismatches. These fire on the
// very first run, making misconfiguration impossible to ship.
//
// # Help output
//
// [Parser.Help] returns a formatted string with USAGE, COMMANDS, FLAGS,
// and GLOBAL FLAGS sections. It is generated for whichever command was
// matched (or root if none).
//
package clix

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// --- Types ---

// Action is a function executed when a [Command] is matched during parsing.
// It receives a [Context] with access to positional arguments, the matched
// command, and the parent [Parser].
type Action func(*Context) error

// Context is the runtime state passed to an [Action]. Use [Context.Args] for
// positional arguments, [Context.Command] for the matched command's metadata,
// and [Context.Parser] to access [Parser.Help] from within a handler.
type Context struct {
	command *Command
	parser  *Parser
}

// Args returns the positional arguments collected for the matched command.
// Positional arguments are tokens that appear after flags and are not
// recognised as subcommands, plus everything after a bare "--" terminator.
// Returns nil when the context has no command (zero-value Context).
func (c *Context) Args() []string {
	if c.command == nil {
		return nil
	}
	return c.command.args
}

// Command returns the matched [Command].
func (c *Context) Command() *Command { return c.command }

// Parser returns the [Parser] that produced this context, giving actions
// access to [Parser.Help] and other parser state.
func (c *Context) Parser() *Parser { return c.parser }

// Option configures a [Command] during construction. The built-in options
// are [AddFlag], [SubCommand], and [Run]. Options compose: they can be
// nested inside [SubCommand] to build arbitrarily deep command trees.
type Option func(*Command)

// Command represents a node in the CLI command tree. Each command has its
// own flags, subcommands, an optional [Action], and collected positional
// arguments. Use [Command.Name], [Command.Description], and
// [Command.Parent] to inspect the tree from within an [Action].
type Command struct {
	name        string
	description string
	parent      *Command
	flags       []*flagMeta
	subcommands map[string]*Command
	subOrder    []string
	action      Action
	flagMap     map[string]*flagMeta
	shortMap    map[string]string
	args        []string
}

// Name returns the command name as passed to [New] or [SubCommand].
func (c *Command) Name() string { return c.name }

// Description returns the one-line description shown in help output.
func (c *Command) Description() string { return c.description }

// Parent returns the parent command in the tree. Returns nil for the root
// command created by [New].
func (c *Command) Parent() *Command { return c.parent }

// flagMeta stores the definition, state, and setter for a single flag.
// Created by [AddFlag] and attached to the owning [Command].
type flagMeta struct {
	name       string
	short      string
	usage      string
	isBool     bool
	required   bool
	set        bool
	defValue   any
	enumValues []any
	setFunc    func(string) error
}

// Parser holds the result of parsing a command line. Create one with [New],
// then check [Parser.Err] for errors and [Parser.Help] for the formatted
// help string of the matched command.
type Parser struct {
	root     *Command
	matched  *Command
	parseErr error
}

// --- Constructor ---

// New builds the command tree from opts, parses osArgs, and returns a
// ready-to-use [Parser]. Parsing and action execution happen synchronously
// inside New — when it returns, the entire parse is complete.
//
// If --help or -h is encountered at any level, [Parser.Err] returns
// [ErrHelp] and [Parser.Help] returns the help text for that level.
//
// All parse errors are [*errx.Error] values with domain [DomainCLI].
// Use [errors.As] to extract them for programmatic handling.
func New(osArgs []string, name, desc string, opts ...Option) *Parser {
	root := newCommand(name, desc)
	for _, opt := range opts {
		opt(root)
	}

	p := &Parser{root: root, matched: root}
	p.parseErr = runParser(root, osArgs, p)
	return p
}

// Err returns the first error encountered during parsing or action execution,
// or nil when everything succeeded. Returns [ErrHelp] when --help / -h was
// encountered — use [errors.Is](err, [ErrHelp]) to distinguish help requests
// from real errors.
func (p *Parser) Err() error { return p.parseErr }

// Help returns the formatted help string for whichever command was matched
// during parsing. If no subcommand was matched, it returns help for root.
// The output includes USAGE, COMMANDS, FLAGS, and GLOBAL FLAGS sections
// as applicable.
func (p *Parser) Help() string { return p.matched.help() }

// --- Declarative options ---

// SubCommand registers a nested subcommand with its own flags, action, and
// optional deeper subcommands. The child inherits all parent flags via the
// resolution chain (see package-level documentation on flag inheritance).
//
// Panics if a subcommand with the same name is already registered on the
// same parent.
func SubCommand(name, desc string, opts ...Option) Option {
	return func(parent *Command) {
		if _, dup := parent.subcommands[name]; dup {
			panic(fmt.Sprintf("clix: duplicate subcommand %q", name))
		}
		child := newCommand(name, desc)
		child.parent = parent
		for _, opt := range opts {
			opt(child)
		}
		parent.subcommands[name] = child
		parent.subOrder = append(parent.subOrder, name)
	}
}

// Run sets the [Action] that is executed when the command is matched. Only
// one action per command is meaningful — a second Run overwrites the first.
// If an action returns a non-nil error, that error becomes [Parser.Err].
func Run(fn Action) Option {
	return func(c *Command) { c.action = fn }
}

// AddFlag registers a typed flag on the command and binds it to target.
// The default value def is applied immediately; parsing overwrites it.
//
// Supported types: string, int, bool, float64, [time.Duration], and
// [time.Time] (parsed as [time.RFC3339]).
//
// The optional extras modify the flag metadata. Built-in extras are
// [Required] (makes the flag mandatory) and [Enum] (restricts the value
// to a closed set).
//
// AddFlag panics at construction time if:
//   - T is not one of the supported types;
//   - a flag with the same long name already exists on this command;
//   - a flag with the same short alias already exists on this command;
//   - an [Enum] value has a different type than T.
func AddFlag[T any](target *T, name, short string, def T, usage string, extras ...func(*flagMeta)) Option {
	assertSupportedType(def)
	return func(c *Command) {
		if target == nil {
			panic(fmt.Sprintf("clix: nil target for --%s", name))
		}
		if _, dup := c.flagMap[name]; dup {
			panic(fmt.Sprintf("clix: duplicate flag --%s", name))
		}
		if short != "" {
			if _, dup := c.shortMap[short]; dup {
				panic(fmt.Sprintf("clix: duplicate short flag -%s", short))
			}
		}

		_, isBool := any(def).(bool)
		meta := &flagMeta{
			name:     name,
			short:    short,
			usage:    usage,
			defValue: def,
			isBool:   isBool,
		}
		for _, ex := range extras {
			ex(meta)
		}

		for _, ev := range meta.enumValues {
			if reflect.TypeOf(ev) != reflect.TypeOf(def) {
				panic(fmt.Sprintf("clix: enum value %v (%T) does not match flag type %T", ev, ev, def))
			}
		}

		meta.setFunc = func(s string) error {
			parsed, err := parseValue[T](s)
			if err != nil {
				return errInvalidValue(name, s, err)
			}
			if len(meta.enumValues) > 0 && !enumAllowed(parsed, meta.enumValues) {
				return errEnumViolated(name, s, meta.enumValues)
			}
			*target = parsed
			meta.set = true
			return nil
		}

		c.flags = append(c.flags, meta)
		c.flagMap[name] = meta
		if short != "" {
			c.shortMap[short] = name
		}
		*target = def
	}
}

// Required marks a flag as mandatory. When the flag is not provided by the
// user, parsing fails with a [CodeRequired] error. Pass as an extra to
// [AddFlag]:
//
//	clix.AddFlag(&host, "host", "", "localhost", "server host", clix.Required())
func Required() func(*flagMeta) { return func(f *flagMeta) { f.required = true } }

// Enum restricts a flag's accepted values to the given set. Values that
// fall outside the set produce a [CodeEnumViolated] error. Each value must
// have the same type as the flag's T; a type mismatch causes a construction-
// time panic. Pass as an extra to [AddFlag]:
//
//	clix.AddFlag(&level, "level", "l", "info", "log level",
//	    clix.Enum("debug", "info", "warn", "error"),
//	)
func Enum(vals ...any) func(*flagMeta) { return func(f *flagMeta) { f.enumValues = vals } }

// --- Parse engine ---

// runParser walks the argument list, dispatching to subcommands, resolving
// flags (including inherited ones and --no-* negation), collecting positional
// arguments, and finally executing the matched command's action.
func runParser(cmd *Command, args []string, p *Parser) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			cmd.args = append(cmd.args, args[i+1:]...)
			break
		}

		if arg == "--help" || arg == "-h" {
			p.matched = cmd
			return ErrHelp
		}

		if !strings.HasPrefix(arg, "-") {
			if sub, ok := cmd.subcommands[arg]; ok {
				p.matched = sub
				return runParser(sub, args[i+1:], p)
			}
			if len(cmd.subcommands) > 0 && cmd.action == nil {
				return errUnknownCommand(arg, cmd.subOrder)
			}
			cmd.args = append(cmd.args, arg)
			continue
		}

		// POSIX grouped short flags: -vh expands to -v -h.
		// The last flag in the group may be non-bool and consume the next arg.
		if !strings.HasPrefix(arg, "--") && !strings.ContainsRune(arg, '=') && len(arg) > 2 {
			chars := arg[1:]
			for ci := 0; ci < len(chars); ci++ {
				short := string(chars[ci])
				canonical, ok := resolveShort(cmd, short)
				if !ok {
					return errUnknownFlag("-" + short)
				}
				meta, ok := resolveFlag(cmd, canonical)
				if !ok {
					return errUnknownFlag("-" + short)
				}
				if meta.isBool {
					if err := meta.setFunc("true"); err != nil {
						return err
					}
					continue
				}
				// Non-bool flag: rest of the group or next arg is the value.
				if ci+1 < len(chars) {
					if err := meta.setFunc(chars[ci+1:]); err != nil {
						return err
					}
				} else {
					if i+1 >= len(args) {
						return errMissingValue("-" + short)
					}
					i++
					if err := meta.setFunc(args[i]); err != nil {
						return err
					}
				}
				break
			}
			continue
		}

		name, inlineVal, hasEq := splitFlag(arg)

		if strings.HasPrefix(arg, "--") {
			// nothing to resolve for long flags
		} else {
			canonical, ok := resolveShort(cmd, name)
			if !ok {
				return errUnknownFlag(arg)
			}
			name = canonical
		}

		meta, ok := resolveFlag(cmd, name)
		if !ok && strings.HasPrefix(arg, "--") && strings.HasPrefix(name, "no-") {
			if negated, found := resolveFlag(cmd, name[3:]); found && negated.isBool {
				if err := negated.setFunc("false"); err != nil {
					return err
				}
				continue
			}
		}
		if !ok {
			return errUnknownFlag(arg)
		}

		if meta.isBool {
			if hasEq {
				if err := meta.setFunc(inlineVal); err != nil {
					return err
				}
			} else {
				if err := meta.setFunc("true"); err != nil {
					return err
				}
			}
		} else {
			var val string
			if hasEq {
				val = inlineVal
			} else {
				if i+1 >= len(args) {
					return errMissingValue(arg)
				}
				i++
				val = args[i]
			}
			if err := meta.setFunc(val); err != nil {
				return err
			}
		}
	}

	for cur := cmd; cur != nil; cur = cur.parent {
		for _, f := range cur.flags {
			if f.required && !f.set {
				return errRequired(f.name)
			}
		}
	}

	if cmd.action != nil {
		return cmd.action(&Context{command: cmd, parser: p})
	}
	return nil
}

// splitFlag strips leading dashes from a flag token and splits on the first
// '=' to separate the name from an optional inline value.
//
//	"--port=8080" → ("port",    "8080", true)
//	"--verbose"   → ("verbose", "",     false)
//	"-p=8080"     → ("p",       "8080", true)
//	"-v"          → ("v",       "",     false)
func splitFlag(arg string) (name, value string, hasValue bool) {
	raw := arg
	if strings.HasPrefix(raw, "--") {
		raw = raw[2:]
	} else if strings.HasPrefix(raw, "-") {
		raw = raw[1:]
	}
	if idx := strings.IndexByte(raw, '='); idx >= 0 {
		return raw[:idx], raw[idx+1:], true
	}
	return raw, "", false
}

// --- Help generation ---

// help builds the formatted help text for this command, including the USAGE
// line, description, subcommand list, own flags, and flags inherited from
// ancestor commands (shown under GLOBAL FLAGS).
func (c *Command) help() string {
	var b strings.Builder

	b.WriteString("USAGE: ")
	b.WriteString(c.name)
	b.WriteString(" [flags]")
	if len(c.subcommands) > 0 {
		b.WriteString(" [command]")
	}
	b.WriteByte('\n')

	if c.description != "" {
		b.WriteByte('\n')
		b.WriteString(c.description)
		b.WriteByte('\n')
	}

	if len(c.subcommands) > 0 {
		b.WriteString("\nCOMMANDS:\n")
		for _, name := range c.subOrder {
			sub := c.subcommands[name]
			writeAligned(&b, name, sub.description, 14)
		}
	}

	if len(c.flags) > 0 {
		b.WriteString("\nFLAGS:\n")
		writeFlagBlock(&b, c.flags)
	}

	if inherited := collectInheritedFlags(c); len(inherited) > 0 {
		b.WriteString("\nGLOBAL FLAGS:\n")
		writeFlagBlock(&b, inherited)
	}

	return b.String()
}

// writeFlagBlock writes a group of flags to b in a three-column layout:
// flag names, default value, and usage description.
func writeFlagBlock(b *strings.Builder, flags []*flagMeta) {
	for _, f := range flags {
		flag := "--" + f.name
		if f.short != "" {
			flag = "--" + f.name + ", -" + f.short
		}

		td := formatDefault(f.defValue)

		comment := f.usage
		if f.required {
			comment += " (required)"
		}
		if len(f.enumValues) > 0 {
			comment += " (one of: " + fmt.Sprint(f.enumValues) + ")"
		}

		writePadded(b, flag, td, comment, 22, 18)
	}
}

// writeAligned writes a two-column row (e.g. subcommand + description)
// padded to width.
func writeAligned(b *strings.Builder, left, right string, width int) {
	b.WriteString("  ")
	b.WriteString(left)
	if pad := width - len(left); pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	}
	b.WriteByte(' ')
	b.WriteString(right)
	b.WriteByte('\n')
}

// writePadded writes a three-column row padded to w1 and w2 widths.
func writePadded(b *strings.Builder, col1, col2, col3 string, w1, w2 int) {
	b.WriteString("  ")
	b.WriteString(col1)
	if pad := w1 - len(col1); pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	}
	b.WriteByte(' ')
	b.WriteString(col2)
	if pad := w2 - len(col2); pad > 0 {
		b.WriteString(strings.Repeat(" ", pad))
	}
	b.WriteByte(' ')
	b.WriteString(col3)
	b.WriteByte('\n')
}

// collectInheritedFlags gathers flags from all ancestor commands so they can
// be displayed in a "GLOBAL FLAGS" section in help output.
func collectInheritedFlags(cmd *Command) []*flagMeta {
	var inherited []*flagMeta
	for p := cmd.parent; p != nil; p = p.parent {
		inherited = append(inherited, p.flags...)
	}
	return inherited
}

// formatDefault produces a human-readable representation of a flag's default
// value for the help output. Bool flags return an empty string (their
// presence/absence is self-explanatory); empty strings show "<string>".
func formatDefault(v any) string {
	switch d := v.(type) {
	case bool:
		return ""
	case string:
		if d == "" {
			return "<string>"
		}
		return "[" + d + "]"
	case time.Duration:
		return "[" + d.String() + "]"
	case time.Time:
		return "[" + d.Format(time.RFC3339) + "]"
	default:
		return "[" + fmt.Sprint(d) + "]"
	}
}

// --- Utilities ---

// resolveFlag searches for a flag by long name starting from cmd and walking
// up the parent chain. This implements flag inheritance — a flag defined on
// any ancestor is visible to all descendants.
func resolveFlag(cmd *Command, name string) (*flagMeta, bool) {
	for c := cmd; c != nil; c = c.parent {
		if meta, ok := c.flagMap[name]; ok {
			return meta, true
		}
	}
	return nil, false
}

// resolveShort maps a short flag alias to its canonical long name, walking
// the parent chain the same way [resolveFlag] does.
func resolveShort(cmd *Command, short string) (string, bool) {
	for c := cmd; c != nil; c = c.parent {
		if canonical, ok := c.shortMap[short]; ok {
			return canonical, true
		}
	}
	return "", false
}

// assertSupportedType panics if v is not one of the types that [parseValue]
// can handle. Called once per [AddFlag] at construction time.
func assertSupportedType(v any) {
	switch v.(type) {
	case string, int, bool, float64, time.Duration, time.Time:
	default:
		panic(fmt.Sprintf("clix: unsupported flag type %T", v))
	}
}

// newCommand allocates a Command with initialised maps ready for flag and
// subcommand registration.
func newCommand(name, desc string) *Command {
	return &Command{
		name:        name,
		description: desc,
		subcommands: make(map[string]*Command),
		flagMap:     make(map[string]*flagMeta),
		shortMap:    make(map[string]string),
	}
}

// parseValue converts the raw string s into type T. It supports the same set
// of types as [assertSupportedType]: string, int, bool, float64,
// [time.Duration], and [time.Time].
func parseValue[T any](s string) (T, error) {
	var zero T
	var val any
	var err error

	switch any(zero).(type) {
	case string:
		val = s
	case int:
		val, err = strconv.Atoi(s)
	case bool:
		val, err = strconv.ParseBool(s)
	case float64:
		val, err = strconv.ParseFloat(s, 64)
	case time.Duration:
		val, err = time.ParseDuration(s)
	case time.Time:
		val, err = time.Parse(time.RFC3339, s)
	default:
		return zero, fmt.Errorf("unsupported flag type %T", zero) //nolint:forbidigo // internal wrap
	}

	if err != nil {
		return zero, err
	}
	return val.(T), nil
}

// enumAllowed reports whether v is present in the allowed list.
func enumAllowed(v any, list []any) bool {
	for _, e := range list {
		if v == e {
			return true
		}
	}
	return false
}
