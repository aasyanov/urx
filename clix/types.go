package clix

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
	return c.command.Args()
}

// Command returns the matched [Command].
func (c *Context) Command() *Command { return c.command }

// Parser returns the [Parser] that produced this context, giving actions
// access to [Parser.Help] and other parser state.
func (c *Context) Parser() *Parser { return c.parser }

// Option configures a [Command] during construction. The built-in options
// are [AddFlag], [SubCommand], [Run], and [Alias]. Options compose: they
// can be nested inside [SubCommand] to build arbitrarily deep command trees.
type Option func(*Command)

// Command represents a node in the CLI command tree. Each command has its
// own flags, subcommands, an optional [Action], and collected positional
// arguments. Use [Command.Name], [Command.Description], and
// [Command.Parent] to inspect the tree from within an [Action].
//
// A Command is not safe for concurrent modification; build the tree once in
// [New] and treat it as immutable afterwards.
type Command struct {
	name        string
	description string
	parent      *Command
	flags       []*flagMeta
	subcommands map[string]*Command
	subOrder    []string
	aliases     []string
	action      Action
	hasAction   bool
	version     string
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

// Args returns the positional arguments collected for this command during
// parsing: tokens that are neither flags nor subcommands, plus everything
// after a bare "--" terminator. Returns nil when no positionals were seen.
func (c *Command) Args() []string { return c.args }

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
	resetFunc  func()
}

// Parser holds the result of parsing a command line. Create one with [New],
// then check [Parser.Err] for parse errors, [Parser.Help] for the formatted
// help string, and [Parser.Run] to execute the matched action.
//
// A Parser is not safe for concurrent use; it is intended to be created and
// consumed on a single goroutine during program startup.
type Parser struct {
	root     *Command
	matched  *Command
	version  string
	parseErr error
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
