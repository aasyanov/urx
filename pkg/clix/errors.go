package clix

import (
	"errors"
	"strings"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- Domain ---

// DomainCLI is the [errx] domain for every structured error produced by
// the clix parser. Use it with [errors.As] and the exported fields to
// programmatically distinguish error kinds:
//
//	var ce *errx.Error
//	if errors.As(err, &ce) && ce.Domain == clix.DomainCLI {
//	    switch ce.Code {
//	    case clix.CodeUnknownFlag:
//	        // ...
//	    }
//	}
const DomainCLI = "CLI"

// --- Code constants ---

// Error codes returned by the parser. Each parse error is an [*errx.Error]
// carrying one of these codes.
const (
	// CodeUnknownFlag — the user supplied a flag that is not registered on
	// the matched command or any of its ancestors.
	CodeUnknownFlag = "UNKNOWN_FLAG"

	// CodeUnknownCommand — a positional token did not match any registered
	// subcommand, and the current command has no [Action] to accept it.
	// The error's metadata includes the available subcommand names.
	CodeUnknownCommand = "UNKNOWN_COMMAND"

	// CodeMissingValue — a non-bool flag was provided without a value
	// (e.g. --port at the end of the argument list).
	CodeMissingValue = "MISSING_VALUE"

	// CodeInvalidValue — the value could not be converted to the flag's
	// declared type (e.g. "abc" for an int flag). The underlying parse
	// error is wrapped as the cause.
	CodeInvalidValue = "INVALID_VALUE"

	// CodeRequired — a flag marked with [Required] was not supplied.
	CodeRequired = "REQUIRED"

	// CodeEnumViolated — the value is valid for the flag's type but not
	// among the allowed [Enum] values.
	CodeEnumViolated = "ENUM_VIOLATED"
)

// ErrHelp is the sentinel returned by [Parser.Err] when --help or -h is
// encountered at any nesting level. It is not a parse error — callers
// should test with [errors.Is] and print [Parser.Help]:
//
//	if errors.Is(p.Err(), clix.ErrHelp) {
//	    fmt.Println(p.Help())
//	    os.Exit(0)
//	}
var ErrHelp = errors.New("clix: help requested") //nolint:forbidigo // sentinel error

// --- Error constructors ---

// errUnknownCommand builds a structured error for a positional token that
// did not match any registered subcommand.
func errUnknownCommand(cmd string, available []string) *errx.Error {
	return errx.New(DomainCLI, CodeUnknownCommand,
		"unknown command: "+cmd+"; available: "+strings.Join(available, ", "),
		errx.WithMeta("command", cmd),
		errx.WithMeta("available", available),
	)
}

// errUnknownFlag builds a structured error for an unrecognised flag token.
func errUnknownFlag(flag string) *errx.Error {
	return errx.New(DomainCLI, CodeUnknownFlag, "unknown flag: "+flag,
		errx.WithMeta("flag", flag),
	)
}

// errMissingValue builds a structured error when a non-bool flag has no
// value following it.
func errMissingValue(flag string) *errx.Error {
	return errx.New(DomainCLI, CodeMissingValue, "missing value for flag: "+flag,
		errx.WithMeta("flag", flag),
	)
}

// errInvalidValue wraps a conversion error when the raw string cannot be
// parsed into the flag's declared type.
func errInvalidValue(flag, raw string, cause error) *errx.Error {
	return errx.Wrap(cause, DomainCLI, CodeInvalidValue, "invalid value for flag: "+flag,
		errx.WithMeta("flag", flag),
		errx.WithMeta("raw", raw),
	)
}

// errRequired builds a structured error when a required flag was not
// supplied by the user.
func errRequired(flag string) *errx.Error {
	return errx.New(DomainCLI, CodeRequired, "required flag not provided: --"+flag,
		errx.WithMeta("flag", flag),
	)
}

// errEnumViolated builds a structured error when the value is valid for the
// flag's type but not in the allowed [Enum] set.
func errEnumViolated(flag, raw string, allowed []any) *errx.Error {
	return errx.New(DomainCLI, CodeEnumViolated, "invalid value for flag: --"+flag,
		errx.WithMeta("flag", flag),
		errx.WithMeta("raw", raw),
		errx.WithMeta("allowed", allowed),
	)
}
