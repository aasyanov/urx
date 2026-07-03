package clix

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// shortGroupMinLen is the minimum token length that triggers POSIX grouped
// short-flag expansion: "-" + at least two characters (e.g. "-vh").
const shortGroupMinLen = 2

// noNegationPrefix is the prefix used to negate a bool flag: "--no-verbose".
const noNegationPrefix = "no-"

// longHelpFlag is the POSIX long option that triggers [ErrHelp].
const longHelpFlag = "--help"

// parseArgs walks the argument list, dispatching to subcommands, resolving
// flags (including inherited ones and --no-* negation), and collecting
// positional arguments. It does NOT execute the matched action.
func parseArgs(cmd *Command, args []string, p *Parser) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			cmd.args = append(cmd.args, args[i+1:]...)
			break
		}

		if arg == longHelpFlag || arg == "-h" {
			p.matched = cmd
			return ErrHelp
		}

		if p.version != "" && (arg == "--version" || arg == "-V") {
			p.matched = cmd
			return ErrVersion
		}

		if !strings.HasPrefix(arg, "-") {
			if sub, ok := cmd.subcommands[arg]; ok {
				p.matched = sub
				return parseArgs(sub, args[i+1:], p)
			}
			if len(cmd.subcommands) > 0 && cmd.action == nil {
				return errUnknownCommand(arg, cmd.subOrder)
			}
			cmd.args = append(cmd.args, arg)
			continue
		}

		// POSIX grouped short flags: -vh expands to -v -h.
		// The last flag in the group may be non-bool and consume the next arg.
		if !strings.HasPrefix(arg, "--") && !strings.ContainsRune(arg, '=') && len(arg) > shortGroupMinLen {
			if err := parseShortGroup(cmd, args, &i, p); err != nil {
				return err
			}
			continue
		}

		if err := parseLongOrShort(cmd, args, &i, arg); err != nil {
			return err
		}
	}

	return checkRequired(cmd)
}

// parseShortGroup expands a grouped short-flag token such as "-vdq" into the
// individual flags. The last flag may be non-bool and consume the remainder
// of the group ("-vp3000") or the next argument ("-vp 3000"). It advances i
// when the trailing flag consumes the following argument.
func parseShortGroup(cmd *Command, args []string, i *int, p *Parser) error {
	chars := args[*i][1:]
	for ci := 0; ci < len(chars); ci++ {
		short := string(chars[ci])
		if short == "h" {
			p.matched = cmd
			return ErrHelp
		}
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
		if ci+1 < len(chars) {
			return meta.setFunc(chars[ci+1:])
		}
		if *i+1 >= len(args) {
			return errMissingValue("-" + short)
		}
		*i++
		return meta.setFunc(args[*i])
	}
	return nil
}

// parseLongOrShort handles a single long (--port), short (-p), or inline
// (--port=8080) flag token, including --no-* bool negation. It advances i
// when the flag consumes the following argument as its value.
func parseLongOrShort(cmd *Command, args []string, i *int, arg string) error {
	name, inlineVal, hasEq := splitFlag(arg)

	if !strings.HasPrefix(arg, "--") {
		canonical, ok := resolveShort(cmd, name)
		if !ok {
			return errUnknownFlag(arg)
		}
		name = canonical
	}

	meta, ok := resolveFlag(cmd, name)
	if !ok && strings.HasPrefix(arg, "--") && strings.HasPrefix(name, noNegationPrefix) {
		if negated, found := resolveFlag(cmd, name[len(noNegationPrefix):]); found && negated.isBool {
			return negated.setFunc("false")
		}
	}
	if !ok {
		return errUnknownFlag(arg)
	}

	if meta.isBool {
		if hasEq {
			return meta.setFunc(inlineVal)
		}
		return meta.setFunc("true")
	}

	if hasEq {
		return meta.setFunc(inlineVal)
	}
	if *i+1 >= len(args) {
		return errMissingValue(arg)
	}
	*i++
	return meta.setFunc(args[*i])
}

// checkRequired walks the command chain and returns [ErrRequired] for the
// first required flag that was never set during parsing.
func checkRequired(cmd *Command) error {
	for cur := cmd; cur != nil; cur = cur.parent {
		for _, f := range cur.flags {
			if f.required && !f.set {
				return errRequired(f.name)
			}
		}
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
		return zero, fmt.Errorf("unsupported flag type %T", zero)
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
