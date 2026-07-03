package syncx

import (
	"errors"
	"fmt"
)

var (
	// ErrInitFailed is returned by [Lazy.Get] when the user-supplied init
	// function returns a non-nil error. The joined error carries the
	// underlying cause; use [errors.Is] to test for it and [errors.Unwrap]
	// (or %w semantics) to reach the cause. It is safe to compare with ==
	// or [errors.Is].
	ErrInitFailed = errors.New("syncx: lazy init failed")

	// ErrNilInit is returned by [NewLazy] when the init function is nil.
	// It is safe to compare with == or [errors.Is].
	ErrNilInit = errors.New("syncx: nil init function")

	// ErrNilFunc is returned by [Group.Go] and [Group.TryGo] when the task
	// function is nil. It is safe to compare with == or [errors.Is].
	ErrNilFunc = errors.New("syncx: nil function")
)

// errInitFailed wraps [ErrInitFailed] with the underlying init cause.
func errInitFailed(cause error) error {
	return fmt.Errorf("%w: %w", ErrInitFailed, cause)
}
