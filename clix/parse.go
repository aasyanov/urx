package clix

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// shortGroupMinLen is the minimum token length that triggers POSIX grouped
	// short-flag expansion: "-" + at least two characters (e.g. "-vh").
	shortGroupMinLen = 2

	// noNegationPrefix is the prefix used to negate a bool flag: "--no-verbose".
	noNegationPrefix = "no-"

	longHelpFlag     = "--help"
	shortHelpFlag    = "-h"
	longVersionFlag  = "--version"
	shortVersionFlag = "-V"

	// positionalStdinArg is the POSIX / flag.FlagSet convention for stdin
	// as a positional operand: a lone dash.
	positionalStdinArg = "-"

	helpFlagName     = "help"
	helpFlagShort    = "h"
	versionFlagName  = "version"
	versionFlagShort = "V"
)

// parseArgs walks the argument list, dispatching to subcommands, resolving
// flags (including inherited ones and --no-* negation), and collecting
// positional arguments. A lone "-" is a positional (stdin convention),
// matching flag.FlagSet. It does NOT execute the matched action.
func parseArgs(cmd *Command, args []string, p *Parser) error {
	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			cmd.args = append(cmd.args, args[i+1:]...)
			break
		}

		if isHelpToken(arg) {
			p.matched = cmd
			return ErrHelp
		}

		if p.version != "" && isVersionToken(arg) {
			p.matched = cmd
			return ErrVersion
		}

		if arg == positionalStdinArg {
			// Same as flag.FlagSet: "-" is a positional, not a flag.
			// On a routing node (subcommands, no action) it cannot be a
			// command name, so it is still [ErrUnknownCommand].
			if len(cmd.subcommands) > 0 && cmd.action == nil {
				return errUnknownCommand(arg, cmd.subOrder)
			}
			cmd.args = append(cmd.args, arg)
			continue
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
		if short == helpFlagShort {
			p.matched = cmd
			return ErrHelp
		}
		if p.version != "" && short == versionFlagShort {
			p.matched = cmd
			return ErrVersion
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
		val, err := takeNextFlagValue(args, i, "-"+short)
		if err != nil {
			return err
		}
		return meta.setFunc(val)
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
			return applyBoolNegation(negated, name, inlineVal, hasEq)
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
	val, err := takeNextFlagValue(args, i, arg)
	if err != nil {
		return err
	}
	return meta.setFunc(val)
}

// takeNextFlagValue consumes args[*i+1] as a non-bool flag value. A missing
// token or a token that looks like another flag yields [ErrMissingValue]
// (use --flag=value to bind a dash-prefixed string). Signed numbers such as
// -5 or -1s are values, not flags. A bare "-" (stdin convention) is a value.
func takeNextFlagValue(args []string, i *int, flagTok string) (string, error) {
	if *i+1 >= len(args) {
		return "", errMissingValue(flagTok)
	}
	next := args[*i+1]
	if nextTokenLooksLikeFlag(next) {
		return "", errMissingValue(flagTok)
	}
	*i++
	return next, nil
}

// nextTokenLooksLikeFlag reports whether arg would be parsed as a flag or
// the "--" terminator rather than a scalar value.
func nextTokenLooksLikeFlag(arg string) bool {
	if arg == "" || arg == positionalStdinArg {
		return false
	}
	if strings.HasPrefix(arg, "--") {
		return true
	}
	if arg[0] != '-' || len(arg) < 2 {
		return false
	}
	return !isASCIIDigit(arg[1])
}

func isASCIIDigit(b byte) bool {
	return b >= '0' && b <= '9'
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

// isHelpToken reports whether arg is the built-in help flag, including the
// inline form (--help=true, -h=true). "--helpers" is not help.
func isHelpToken(arg string) bool {
	return tokenIs(arg, longHelpFlag, shortHelpFlag)
}

// isVersionToken reports whether arg is the built-in version flag, including
// the inline form (--version=1, -V=1).
func isVersionToken(arg string) bool {
	return tokenIs(arg, longVersionFlag, shortVersionFlag)
}

// tokenIs matches an exact long/short flag or the same flag with an inline
// "=value" suffix.
func tokenIs(arg, long, short string) bool {
	return arg == long || arg == short ||
		strings.HasPrefix(arg, long+"=") ||
		strings.HasPrefix(arg, short+"=")
}

// applyBoolNegation sets a bool flag from a --no-<name> token. A bare
// --no-<name> writes false. An inline value is parsed as a bool: --no-x=true
// writes false, --no-x=false writes true.
func applyBoolNegation(meta *flagMeta, name, inlineVal string, hasEq bool) error {
	if !hasEq {
		return meta.setFunc("false")
	}
	parsed, err := strconv.ParseBool(inlineVal)
	if err != nil {
		return errInvalidValue(name, inlineVal, err)
	}
	if parsed {
		return meta.setFunc("false")
	}
	return meta.setFunc("true")
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
