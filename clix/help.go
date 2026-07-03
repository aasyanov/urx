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
		writeCommandBlock(&b, c)
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

// writeCommandBlock writes the COMMANDS section listing each subcommand (with
// its aliases) and description in a two-column layout.
func writeCommandBlock(b *strings.Builder, c *Command) {
	b.WriteString("\nCOMMANDS:\n")
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
func writeFlagBlock(b *strings.Builder, flags []*flagMeta) {
	flagCol := 0
	defCol := 0
	for _, f := range flags {
		if w := flagDisplayWidth(f); w > flagCol {
			flagCol = w
		}
		if w := len(formatDefault(f.defValue)); w > defCol {
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

		td := formatDefault(f.defValue)

		comment := f.usage
		if f.required {
			comment += " (required)"
		}
		if len(f.enumValues) > 0 {
			comment += " (one of: " + fmt.Sprint(f.enumValues) + ")"
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
