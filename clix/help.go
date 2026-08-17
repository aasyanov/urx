package clix

import (
	"fmt"
	"strings"
	"time"
)

// Help layout constants. Column widths are clamped to these minimums so help
// output stays aligned even when the longest entry is short.
const (
	// minCommandColWidth is the minimum width of the subcommand-name column.
	minCommandColWidth = 14
	// minFlagColWidth is the minimum width of the flag-name column.
	minFlagColWidth = 22
	// minDefaultColWidth is the minimum width of the default-value column.
	minDefaultColWidth = 10
	// colPadding is the trailing padding added to a column's longest entry.
	colPadding = 2
)

// help builds the formatted help text for this command, including the USAGE
// line (full command path), description, subcommand list, own flags plus
// built-in --help/--version, and flags inherited from ancestor commands
// (shown under GLOBAL FLAGS, root-first). Headings come from [HelpLabels].
func (c *Command) help() string {
	var b strings.Builder
	lb := c.labels()

	b.WriteString(lb.Usage)
	b.WriteString(": ")
	b.WriteString(commandPath(c))
	b.WriteByte(' ')
	b.WriteString(lb.FlagsMetavar)
	if len(c.subcommands) > 0 {
		b.WriteByte(' ')
		b.WriteString(lb.CommandMetavar)
	}
	b.WriteByte('\n')

	if c.description != "" {
		b.WriteByte('\n')
		b.WriteString(c.description)
		b.WriteByte('\n')
	}

	if len(c.subcommands) > 0 {
		writeCommandBlock(&b, c, lb)
	}

	local := append(append([]*flagMeta{}, c.flags...), builtinFlagMetas(c, lb)...)
	b.WriteByte('\n')
	b.WriteString(lb.Flags)
	b.WriteString(":\n")
	writeFlagBlock(&b, local, lb)

	if inherited := collectInheritedFlags(c); len(inherited) > 0 {
		b.WriteByte('\n')
		b.WriteString(lb.GlobalFlags)
		b.WriteString(":\n")
		writeFlagBlock(&b, inherited, lb)
	}

	return b.String()
}

// commandPath returns the space-separated names from the root command down
// to c, e.g. "myapp db migrate".
func commandPath(c *Command) string {
	n := 0
	for cur := c; cur != nil; cur = cur.parent {
		n++
	}
	parts := make([]string, n)
	i := n - 1
	for cur := c; cur != nil; cur = cur.parent {
		parts[i] = cur.name
		i--
	}
	return strings.Join(parts, " ")
}

// builtinFlagMetas returns render-only flag rows for control flags. They are
// not registered in flagMap — the parser intercepts them before lookup.
func builtinFlagMetas(c *Command, lb HelpLabels) []*flagMeta {
	out := []*flagMeta{{
		name:     helpFlagName,
		short:    helpFlagShort,
		usage:    lb.HelpFlag,
		isBool:   true,
		defValue: false,
	}}
	if c.root().version != "" {
		out = append(out, &flagMeta{
			name:     versionFlagName,
			short:    versionFlagShort,
			usage:    lb.VersionFlag,
			isBool:   true,
			defValue: false,
		})
	}
	return out
}

// writeCommandBlock writes the COMMANDS section listing each subcommand (with
// its aliases) and description in a two-column layout.
func writeCommandBlock(b *strings.Builder, c *Command, lb HelpLabels) {
	b.WriteByte('\n')
	b.WriteString(lb.Commands)
	b.WriteString(":\n")
	labels := make([]string, 0, len(c.subOrder))
	for _, name := range c.subOrder {
		sub := c.subcommands[name]
		label := name
		if len(sub.aliases) > 0 {
			label += ", " + strings.Join(sub.aliases, ", ")
		}
		labels = append(labels, label)
	}
	cmdWidth := maxLen(labels) + colPadding
	if cmdWidth < minCommandColWidth {
		cmdWidth = minCommandColWidth
	}
	for i, name := range c.subOrder {
		sub := c.subcommands[name]
		writeAligned(b, labels[i], sub.description, cmdWidth)
	}
}

// writeFlagBlock writes a group of flags to b in a three-column layout:
// flag names, default value, and usage description.
func writeFlagBlock(b *strings.Builder, flags []*flagMeta, lb HelpLabels) {
	flagCol := 0
	defCol := 0
	for _, f := range flags {
		if w := flagDisplayWidth(f); w > flagCol {
			flagCol = w
		}
		if w := len(formatDefault(f.defValue, lb.EmptyString)); w > defCol {
			defCol = w
		}
	}
	flagCol += colPadding
	if flagCol < minFlagColWidth {
		flagCol = minFlagColWidth
	}
	defCol += colPadding
	if defCol < minDefaultColWidth {
		defCol = minDefaultColWidth
	}

	for _, f := range flags {
		flag := "--" + f.name
		if f.short != "" {
			flag = "--" + f.name + ", -" + f.short
		}

		td := formatDefault(f.defValue, lb.EmptyString)

		comment := f.usage
		if f.required {
			comment += " (" + lb.Required + ")"
		}
		if len(f.enumValues) > 0 {
			comment += " (" + formatOneOf(lb.OneOf, formatEnum(f.enumValues)) + ")"
		}

		writePadded(b, flag, td, comment, flagCol, defCol)
	}
}

// flagDisplayWidth returns the rendered width of a flag's name column entry,
// accounting for the "--" prefix and the optional ", -x" short alias.
func flagDisplayWidth(f *flagMeta) int {
	w := len("--") + len(f.name)
	if f.short != "" {
		w += len(", -") + len(f.short)
	}
	return w
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
// be displayed in a "GLOBAL FLAGS" section. Groups are root-first so true
// globals appear above intermediate-command flags.
func collectInheritedFlags(cmd *Command) []*flagMeta {
	var layers [][]*flagMeta
	for p := cmd.parent; p != nil; p = p.parent {
		if len(p.flags) > 0 {
			layers = append(layers, p.flags)
		}
	}
	var inherited []*flagMeta
	for i := len(layers) - 1; i >= 0; i-- {
		inherited = append(inherited, layers[i]...)
	}
	return inherited
}

// formatEnum joins allowed enum values for the help comment, e.g. "dev, prod".
func formatEnum(vals []any) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = fmt.Sprint(v)
	}
	return strings.Join(parts, ", ")
}

// formatDefault produces a human-readable representation of a flag's default
// value for the help output. Bool flags return an empty string (their
// presence/absence is self-explanatory); empty strings show "<string>".
func formatDefault(v any, emptyString string) string {
	switch d := v.(type) {
	case bool:
		return ""
	case string:
		if d == "" {
			return emptyString
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

// maxLen returns the length of the longest string in the slice.
func maxLen(ss []string) int {
	m := 0
	for _, s := range ss {
		if len(s) > m {
			m = len(s)
		}
	}
	return m
}
