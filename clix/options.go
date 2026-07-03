package clix

import (
	"fmt"
	"reflect"
)

// SubCommand registers a nested subcommand with its own flags, action, and
// optional deeper subcommands. The child inherits all parent flags via the
// resolution chain (see package-level documentation on flag inheritance).
//
// Panics if name is empty, or if a subcommand with the same name is already
// registered on the same parent.
func SubCommand(name, desc string, opts ...Option) Option {
	return func(parent *Command) {
		if name == "" {
			panic("clix: empty subcommand name")
		}
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

		for _, alias := range child.aliases {
			if _, dup := parent.subcommands[alias]; dup {
				panic(fmt.Sprintf("clix: duplicate subcommand/alias %q", alias))
			}
			parent.subcommands[alias] = child
		}
	}
}

// Run sets the [Action] that is executed when the command is matched.
// Only one Run per command is allowed — a second Run panics.
func Run(fn Action) Option {
	return func(c *Command) {
		if c.hasAction {
			panic(fmt.Sprintf("clix: duplicate Run on command %q", c.name))
		}
		c.action = fn
		c.hasAction = true
	}
}

// Alias registers alternative names for a subcommand. Aliases are resolved
// the same way as the primary name. Pass as an option to [SubCommand]:
//
//	clix.SubCommand("extract", "extract data",
//	    clix.Alias("x", "ex"),
//	)
func Alias(names ...string) Option {
	return func(c *Command) {
		for _, name := range names {
			if name == "" {
				panic("clix: empty subcommand alias")
			}
		}
		c.aliases = append(c.aliases, names...)
	}
}

// Version enables --version / -V handling. When the user passes --version
// or -V, parsing returns [ErrVersion] and [Parser.Version] returns the
// string. Pass as an option to [New]:
//
//	clix.New(os.Args[1:], "myapp", "my tool", clix.Version("1.2.3"))
func Version(v string) Option {
	return func(c *Command) { c.version = v }
}

// AddFlag registers a typed flag on the command and binds it to target.
// The default value def is applied immediately; parsing overwrites it.
//
// Supported types: string, int, bool, float64, [time.Duration], and
// [time.Time] (parsed as [time.RFC3339]).
//
// The optional extras modify the flag metadata. Built-in extras are
// [Required] (makes the flag mandatory) and [Enum] (restricts the value
// to a closed set). Custom extras may implement [FlagOption].
//
// AddFlag panics at construction time if:
//   - T is not one of the supported types;
//   - name is empty;
//   - a flag with the same long or short name already exists on this command;
//   - a flag with the same long or short name is already defined on an ancestor;
//   - an [Enum] value has a different type than T.
func AddFlag[T any](target *T, name, short string, def T, usage string, extras ...FlagOption) Option {
	assertSupportedType(def)
	return func(c *Command) {
		if name == "" {
			panic("clix: empty flag name")
		}
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
		assertNoShadowFlag(c, name, short)

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
		meta.resetFunc = func() { *target = def }

		c.flags = append(c.flags, meta)
		c.flagMap[name] = meta
		if short != "" {
			c.shortMap[short] = name
		}
		*target = def
	}
}

// Required marks a flag as mandatory. When the flag is not provided by the
// user, parsing fails with an [ErrRequired] error. Returns a [FlagOption]
// for use as an extra to [AddFlag]:
//
//	clix.AddFlag(&host, "host", "", "localhost", "server host", clix.Required())
func Required() FlagOption { return func(f *flagMeta) { f.required = true } }

// Enum restricts a flag's accepted values to the given set. Values that
// fall outside the set produce an [ErrEnumViolated] error. Each value must
// have the same type as the flag's T; a type mismatch causes a construction-
// time panic. Returns a [FlagOption] for use as an extra to [AddFlag]:
//
//	clix.AddFlag(&level, "level", "l", "info", "log level",
//	    clix.Enum("debug", "info", "warn", "error"),
//	)
func Enum(vals ...any) FlagOption { return func(f *flagMeta) { f.enumValues = vals } }

// assertNoShadowFlag panics when name or short would hide an inherited flag
// from an ancestor command. Shadowing would make help ambiguous and split
// binding across two targets for the same flag token.
func assertNoShadowFlag(c *Command, name, short string) {
	for p := c.parent; p != nil; p = p.parent {
		if _, exists := p.flagMap[name]; exists {
			panic(fmt.Sprintf("clix: flag --%s on %q shadows inherited flag from %q", name, c.name, p.name))
		}
		if short != "" {
			if _, exists := p.shortMap[short]; exists {
				panic(fmt.Sprintf("clix: short flag -%s on %q shadows inherited flag from %q", short, c.name, p.name))
			}
		}
	}
}
