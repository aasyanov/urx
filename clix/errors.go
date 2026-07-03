package clix

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrHelp is returned by [Parser.Err] when --help or -h is encountered.
	// It is a control signal, not a failure: compare with [errors.Is] and
	// print [Parser.Help]. Safe to compare with == or [errors.Is].
	ErrHelp = errors.New("clix: help requested")

	// ErrVersion is returned by [Parser.Err] when --version or -V is
	// encountered and [Version] was provided. It is a control signal, not a
	// failure. Safe to compare with == or [errors.Is].
	ErrVersion = errors.New("clix: version requested")

	// ErrUnknownFlag is returned when an argument looks like a flag but no
	// matching flag is defined on the command or any ancestor. Safe to
	// compare with == or [errors.Is].
	ErrUnknownFlag = errors.New("clix: unknown flag")

	// ErrUnknownCommand is returned when a command has subcommands but no
	// action, and an unrecognised positional token is encountered. Safe to
	// compare with == or [errors.Is].
	ErrUnknownCommand = errors.New("clix: unknown command")

	// ErrMissingValue is returned when a non-bool flag is the last token or
	// is followed by another flag, leaving it without a value. Safe to
	// compare with == or [errors.Is].
	ErrMissingValue = errors.New("clix: missing value for flag")

	// ErrInvalidValue is returned when a flag value cannot be parsed into the
	// flag's declared type (e.g. "abc" for an int flag). Safe to compare with
	// == or [errors.Is].
	ErrInvalidValue = errors.New("clix: invalid value for flag")

	// ErrRequired is returned when a flag marked with [Required] is not
	// provided on the command line. Safe to compare with == or [errors.Is].
	ErrRequired = errors.New("clix: required flag not provided")

	// ErrEnumViolated is returned when a flag value is not a member of the
	// closed set declared via [Enum]. Safe to compare with == or
	// [errors.Is].
	ErrEnumViolated = errors.New("clix: value not in allowed enum set")
)

func errUnknownCommand(cmd string, available []string) error {
	return fmt.Errorf("%w: %s; available: %s", ErrUnknownCommand, cmd, strings.Join(available, ", "))
}

func errUnknownFlag(flag string) error {
	return fmt.Errorf("%w: %s", ErrUnknownFlag, flag)
}

func errMissingValue(flag string) error {
	return fmt.Errorf("%w: %s", ErrMissingValue, flag)
}

func errInvalidValue(flag, raw string, cause error) error {
	return fmt.Errorf("%w: --%s (raw=%s): %w", ErrInvalidValue, flag, raw, cause)
}

func errRequired(flag string) error {
	return fmt.Errorf("%w: --%s", ErrRequired, flag)
}

func errEnumViolated(flag, raw string, allowed []any) error {
	return fmt.Errorf("%w: --%s (raw=%s, allowed=%v)", ErrEnumViolated, flag, raw, allowed)
}
