package errx

import (
	"errors"
	"fmt"
	"strings"
)

// --- MultiError ---

// MultiError aggregates multiple errors and derives combined retry/severity
// from the contained [Error] instances.
type MultiError struct {
	Errors   []error
	retry    RetryClass
	severity Severity
	isPanic  bool
}

// NewMulti creates a [MultiError] from the given errors (nils are skipped).
func NewMulti(errs ...error) *MultiError {
	filtered := make([]error, 0, len(errs))
	for _, err := range errs {
		if err != nil {
			filtered = append(filtered, err)
		}
	}
	me := &MultiError{Errors: filtered}
	me.aggregate()
	return me
}

// Add appends a non-nil error and re-aggregates.
func (me *MultiError) Add(err error) {
	if err == nil {
		return
	}
	me.Errors = append(me.Errors, err)
	me.aggregate()
}

// Len returns the number of contained errors.
func (me *MultiError) Len() int { return len(me.Errors) }

// Err returns nil when the MultiError contains no errors, or itself otherwise.
// Useful for returning from functions: return me.Err()
func (me *MultiError) Err() error {
	if len(me.Errors) == 0 {
		return nil
	}
	return me
}

// aggregate recomputes retry class, severity, and isPanic from the contained errors.
func (me *MultiError) aggregate() {
	me.retry = RetryNone
	me.severity = SeverityInfo
	me.isPanic = false
	for _, err := range me.Errors {
		var xe *Error
		if errors.As(err, &xe) {
			if xe.Retry > me.retry {
				me.retry = xe.Retry
			}
			if xe.Severity > me.severity {
				me.severity = xe.Severity
			}
			if xe.isPanic {
				me.isPanic = true
			}
		}
	}
	if me.isPanic {
		me.retry = RetryNone
		me.severity = SeverityCritical
	}
}

// Error returns a numbered list of all contained errors.
func (me *MultiError) Error() string {
	if len(me.Errors) == 0 {
		return labelNoErrors
	}
	var sb strings.Builder
	for i, err := range me.Errors {
		fmt.Fprintf(&sb, "[%d] %s\n", i, err.Error())
	}
	return sb.String()
}

// Severity returns the highest severity among contained errors.
func (me *MultiError) Severity() Severity {
	return me.severity
}

// Retryable reports whether any contained error permits retrying.
func (me *MultiError) Retryable() bool {
	return me.retry.Retryable()
}

// IsPanic reports whether any contained error originated from a panic.
func (me *MultiError) IsPanic() bool {
	return me.isPanic
}

// Unwrap returns the contained errors for use with [errors.Is] / [errors.As].
func (me *MultiError) Unwrap() []error {
	return me.Errors
}
