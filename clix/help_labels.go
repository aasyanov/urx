package clix

import "strings"

// Default help-chrome strings. Overridden via [WithHelpLabels]; empty fields
// on the caller's struct fall back to these values.
const (
	defaultLabelUsage          = "USAGE"
	defaultLabelCommands       = "COMMANDS"
	defaultLabelFlags          = "FLAGS"
	defaultLabelGlobalFlags    = "GLOBAL FLAGS"
	defaultLabelFlagsMetavar   = "[flags]"
	defaultLabelCommandMetavar = "[command]"
	defaultLabelHelpFlag       = "show help"
	defaultLabelVersionFlag    = "print version"
	defaultLabelRequired       = "required"
	defaultLabelOneOf          = "one of: %s"
	defaultLabelEmptyString    = "<string>"
)

// HelpLabels replaces the English chrome of [Parser.Help]. Empty fields keep
// the built-in English defaults. Pass via [WithHelpLabels] on [New] only.
//
// Command names, aliases, and flag names are never translated — only section
// headings, metavars, built-in flag usage, and the required/enum suffixes.
type HelpLabels struct {
	// Usage is the USAGE-line heading (no colon). Default "USAGE".
	Usage string
	// Commands is the subcommand-list heading. Default "COMMANDS".
	Commands string
	// Flags is the local-flags heading. Default "FLAGS".
	Flags string
	// GlobalFlags is the inherited-flags heading. Default "GLOBAL FLAGS".
	GlobalFlags string
	// FlagsMetavar is appended on the USAGE line. Default "[flags]".
	FlagsMetavar string
	// CommandMetavar is appended on the USAGE line when the command has
	// subcommands. Default "[command]".
	CommandMetavar string
	// HelpFlag is the usage text for the built-in --help / -h row.
	// Default "show help".
	HelpFlag string
	// VersionFlag is the usage text for the built-in --version / -V row.
	// Default "print version".
	VersionFlag string
	// Required is the suffix (without parentheses) for required flags.
	// Default "required".
	Required string
	// OneOf formats the enum suffix. Must contain "%s" for the joined
	// values (default "one of: %s"). If "%s" is absent, the values are
	// appended after a space.
	OneOf string
	// EmptyString is the placeholder for an empty string flag default.
	// Default "<string>".
	EmptyString string
}

// WithHelpLabels installs help chrome on the root command. Applying it to a
// [SubCommand] panics. Empty fields keep English defaults. A second call
// replaces the previous labels (again filling empties from defaults).
//
//	clix.New(os.Args[1:], "app", "desc",
//	    clix.WithHelpLabels(clix.HelpLabels{
//	        Usage:    "SYNOPSIS",
//	        HelpFlag: "show this help text",
//	    }),
//	)
func WithHelpLabels(l HelpLabels) Option {
	return func(c *Command) {
		if c.parent != nil {
			panic("clix: WithHelpLabels must be passed to New, not SubCommand")
		}
		merged := l.withDefaults()
		c.helpLabels = &merged
	}
}

func defaultHelpLabels() HelpLabels {
	return HelpLabels{
		Usage:          defaultLabelUsage,
		Commands:       defaultLabelCommands,
		Flags:          defaultLabelFlags,
		GlobalFlags:    defaultLabelGlobalFlags,
		FlagsMetavar:   defaultLabelFlagsMetavar,
		CommandMetavar: defaultLabelCommandMetavar,
		HelpFlag:       defaultLabelHelpFlag,
		VersionFlag:    defaultLabelVersionFlag,
		Required:       defaultLabelRequired,
		OneOf:          defaultLabelOneOf,
		EmptyString:    defaultLabelEmptyString,
	}
}

func (l HelpLabels) withDefaults() HelpLabels {
	d := defaultHelpLabels()
	if l.Usage == "" {
		l.Usage = d.Usage
	}
	if l.Commands == "" {
		l.Commands = d.Commands
	}
	if l.Flags == "" {
		l.Flags = d.Flags
	}
	if l.GlobalFlags == "" {
		l.GlobalFlags = d.GlobalFlags
	}
	if l.FlagsMetavar == "" {
		l.FlagsMetavar = d.FlagsMetavar
	}
	if l.CommandMetavar == "" {
		l.CommandMetavar = d.CommandMetavar
	}
	if l.HelpFlag == "" {
		l.HelpFlag = d.HelpFlag
	}
	if l.VersionFlag == "" {
		l.VersionFlag = d.VersionFlag
	}
	if l.Required == "" {
		l.Required = d.Required
	}
	if l.OneOf == "" {
		l.OneOf = d.OneOf
	}
	if l.EmptyString == "" {
		l.EmptyString = d.EmptyString
	}
	return l
}

func (c *Command) labels() HelpLabels {
	if l := c.root().helpLabels; l != nil {
		return *l
	}
	return defaultHelpLabels()
}

func formatOneOf(tmpl, values string) string {
	if strings.Contains(tmpl, "%s") {
		return strings.Replace(tmpl, "%s", values, 1)
	}
	if tmpl == "" {
		return values
	}
	return tmpl + " " + values
}
