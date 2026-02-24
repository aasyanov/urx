package validx

import (
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- Domain ---

// DomainValidation is the [errx] domain for all validation errors.
const DomainValidation = "VALIDATION"

// --- Code constants ---

const (
	// CodeRequired indicates a required field was empty.
	CodeRequired = "REQUIRED"

	// CodeTooShort indicates the value is shorter than the minimum length.
	CodeTooShort = "TOO_SHORT"

	// CodeTooLong indicates the value exceeds the maximum length.
	CodeTooLong = "TOO_LONG"

	// CodeOutOfRange indicates the value is outside the allowed numeric or time range.
	CodeOutOfRange = "OUT_OF_RANGE"

	// CodeInvalidFormat indicates the value does not match the required pattern.
	CodeInvalidFormat = "INVALID_FORMAT"

	// CodeInvalidValue indicates the value is not among the allowed set.
	CodeInvalidValue = "INVALID_VALUE"

	// CodeFixed indicates the value was auto-corrected (informational, not a failure).
	CodeFixed = "FIXED"
)

// --- Error constructors ---

// errRequired builds a structured error for a missing required field.
func errRequired(field string) *errx.Error {
	return errx.New(DomainValidation, CodeRequired, field+" is required",
		errx.WithMeta("field", field),
	)
}

// errTooShort builds a structured error when a string is below the minimum length.
func errTooShort(field string, min int) *errx.Error {
	return errx.New(DomainValidation, CodeTooShort, field+" is too short",
		errx.WithMeta("field", field),
		errx.WithMeta("min", min),
	)
}

// errTooLong builds a structured error when a string exceeds the maximum length.
func errTooLong(field string, max int) *errx.Error {
	return errx.New(DomainValidation, CodeTooLong, field+" is too long",
		errx.WithMeta("field", field),
		errx.WithMeta("max", max),
	)
}

// errOutOfRange builds a structured error when a numeric value is outside [min, max].
func errOutOfRange(field string, min, max int) *errx.Error {
	return errx.New(DomainValidation, CodeOutOfRange, field+" is out of range",
		errx.WithMeta("field", field),
		errx.WithMeta("min", min),
		errx.WithMeta("max", max),
	)
}

// errInvalidFormat builds a structured error when a value fails pattern matching.
func errInvalidFormat(field, pattern string) *errx.Error {
	return errx.New(DomainValidation, CodeInvalidFormat, field+" has invalid format",
		errx.WithMeta("field", field),
		errx.WithMeta("pattern", pattern),
	)
}

// errInvalidValue builds a structured error when a value is not in the allowed set.
func errInvalidValue(field string, allowed []string) *errx.Error {
	return errx.New(DomainValidation, CodeInvalidValue, field+" has invalid value",
		errx.WithMeta("field", field),
		errx.WithMeta("allowed", allowed),
	)
}

// errOutOfRangeTime builds a structured error when a time value is outside [min, max].
func errOutOfRangeTime(field string, min, max time.Time) *errx.Error {
	return errx.New(DomainValidation, CodeOutOfRange, field+" is out of range",
		errx.WithMeta("field", field),
		errx.WithMeta("min", min.Format(time.RFC3339)),
		errx.WithMeta("max", max.Format(time.RFC3339)),
	)
}

// errFixed builds an informational error recording that a value was auto-corrected.
func errFixed(field string, from, to any) *errx.Error {
	return errx.New(DomainValidation, CodeFixed, field+" was auto-fixed",
		errx.WithSeverity(errx.SeverityInfo),
		errx.WithMeta("field", field),
		errx.WithMeta("from", from),
		errx.WithMeta("to", to),
		errx.WithMeta("fixed", true),
	)
}
