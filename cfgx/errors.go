package cfgx

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound is returned by [Load] when the config file does not exist
	// and [WithCreateIfMissing] was not set. Safe to compare with == or
	// [errors.Is].
	ErrNotFound = errors.New("cfgx: config file not found")

	// ErrReadFailed is returned by [Load] when the file exists but cannot be
	// read (permissions, I/O error). Wraps the underlying cause. Safe to
	// compare with == or [errors.Is].
	ErrReadFailed = errors.New("cfgx: could not read config file")

	// ErrParseFailed is returned by [Load] or [Parse] when the data cannot be
	// decoded into the destination (malformed YAML/JSON/TOML, type mismatch).
	// Wraps the underlying codec error. Safe to compare with == or
	// [errors.Is].
	ErrParseFailed = errors.New("cfgx: could not parse config file")

	// ErrWriteFailed is returned by [Save] or [Marshal] (and by [Load] with
	// [WithCreateIfMissing]) when encoding or writing fails. Wraps the
	// underlying cause. Safe to compare with == or [errors.Is].
	ErrWriteFailed = errors.New("cfgx: could not write config file")

	// ErrUnsupportedFormat is returned when the format cannot be resolved:
	// an unknown file extension, or [FormatAuto] passed to [Parse]/[Marshal]
	// where there is no path to infer from. Safe to compare with == or
	// [errors.Is].
	ErrUnsupportedFormat = errors.New("cfgx: unsupported config file format")

	// ErrInvalidInput is returned when an argument is invalid: a nil or
	// non-pointer destination for [Load]/[Parse], or a nil source for
	// [Save]/[Marshal]. Safe to compare with == or [errors.Is].
	ErrInvalidInput = errors.New("cfgx: invalid input")

	// ErrValidationFailed wraps every error returned by a [Validator] that
	// remains after the (optional) fix pass. Use [errors.Is] to detect a
	// validation failure regardless of the specific field errors. Safe to
	// compare with == or [errors.Is].
	ErrValidationFailed = errors.New("cfgx: config validation failed")
)

func errNotFound(path string) error {
	return fmt.Errorf("%w: %s", ErrNotFound, path)
}

func errReadFailed(path string, cause error) error {
	return fmt.Errorf("%w: %s: %w", ErrReadFailed, path, cause)
}

func errParseFailed(path string, cause error) error {
	return fmt.Errorf("%w: %s: %w", ErrParseFailed, path, cause)
}

func errWriteFailed(path string, cause error) error {
	return fmt.Errorf("%w: %s: %w", ErrWriteFailed, path, cause)
}

func errUnsupportedFormat(path, ext string) error {
	return fmt.Errorf("%w: %s (ext=%s)", ErrUnsupportedFormat, path, ext)
}

func errInvalidInput(param, reason string) error {
	return fmt.Errorf("%w: %s (%s)", ErrInvalidInput, param, reason)
}

// errValidationFailed joins the non-nil causes under [ErrValidationFailed],
// keeping the file path and optional field path for context. Returns nil when
// there are no causes, so callers can return its result directly.
func errValidationFailed(file, field string, causes []error) error {
	var errs []error
	for _, cause := range causes {
		if cause != nil {
			if field == "" {
				errs = append(errs, fmt.Errorf("%w: %s: %w", ErrValidationFailed, file, cause))
			} else {
				errs = append(errs, fmt.Errorf("%w: %s: %s: %w", ErrValidationFailed, file, field, cause))
			}
		}
	}
	return errors.Join(errs...)
}
