package hedgex

import (
	"errors"
	"fmt"
)

var (
	// ErrNoFunc is returned by [Execute] and [ExecuteMulti] when no function is
	// supplied (a nil function for [Execute], an empty or all-nil slice for
	// [ExecuteMulti]). Safe to compare with == or [errors.Is].
	ErrNoFunc = errors.New("hedgex: no function provided")

	// ErrAllFailed is returned when every hedge copy completed with an error
	// and none succeeded. The joined error carries the first failure observed;
	// reach it with [errors.Unwrap] or test it with [errors.Is]. Safe to
	// compare with == or [errors.Is].
	ErrAllFailed = errors.New("hedgex: all hedged copies failed")

	// ErrCancelled is returned when the caller's context is cancelled (or its
	// deadline expires) before any copy succeeds. The joined error carries
	// ctx.Err(); reach it with [errors.Unwrap] or test it with [errors.Is].
	// Safe to compare with == or [errors.Is].
	ErrCancelled = errors.New("hedgex: context cancelled")
)

// errAllFailed wraps [ErrAllFailed] joining the first underlying failure.
func errAllFailed(cause error) error {
	return fmt.Errorf("%w: %w", ErrAllFailed, cause)
}

// errCancelled wraps [ErrCancelled] joining the underlying context cause.
func errCancelled(cause error) error {
	return fmt.Errorf("%w: %w", ErrCancelled, cause)
}
