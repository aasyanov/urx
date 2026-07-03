package healthx

import (
	"errors"
	"fmt"
)

var (
	// ErrUnhealthy is the sentinel joined into the error recorded for a
	// component whose check returned a non-nil error or panicked. The
	// component name and underlying cause are joined with it. It is safe to
	// compare with == or [errors.Is].
	ErrUnhealthy = errors.New("healthx: component unhealthy")

	// ErrTimeout is the sentinel joined into the error recorded for a
	// component whose check did not complete within the configured timeout
	// (see [WithTimeout]). It is safe to compare with == or [errors.Is].
	ErrTimeout = errors.New("healthx: health check timed out")
)

// errUnhealthy wraps [ErrUnhealthy] with the component name and its cause.
func errUnhealthy(name string, cause error) error {
	return fmt.Errorf("%w: %s: %w", ErrUnhealthy, name, cause)
}

// errTimeout wraps [ErrTimeout] with the component name.
func errTimeout(name string) error {
	return fmt.Errorf("%w: %s", ErrTimeout, name)
}
