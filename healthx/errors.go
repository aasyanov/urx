package healthx

import (
	"errors"
	"fmt"
)

var (
	// ErrUnhealthy is the sentinel joined into the error recorded for a
	// component whose check returned a non-nil error or panicked. The
	// component name and underlying cause are joined with it. The message is
	// stored as a string in [ComponentStatus.Error]; classify it with
	// strings.Contains on ErrUnhealthy.Error(). Safe to compare with == or
	// [errors.Is] on the internal errUnhealthy wrapper.
	ErrUnhealthy = errors.New("healthx: component unhealthy")

	// ErrTimeout is the sentinel joined into the error recorded for a
	// component whose check did not complete within the configured timeout
	// (see [WithTimeout]). Classify [ComponentStatus.Error] with
	// strings.Contains on ErrTimeout.Error(). Safe to compare with == or
	// [errors.Is] on the internal errTimeout wrapper.
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
