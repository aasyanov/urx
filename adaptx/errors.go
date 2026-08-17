package adaptx

import (
	"errors"
	"fmt"
)

var (
	// ErrClosed is returned by [Limiter.Acquire], [Execute], and related methods
	// after [Limiter.Close] has been called. Safe to compare with == or
	// [errors.Is]. Also returned by a second [Limiter.CloseWithTimeout] after
	// the first call has already begun shutdown.
	ErrClosed = errors.New("adaptx: limiter is closed")

	// ErrTimeout is returned when a blocking acquire exceeds its deadline before
	// a slot becomes available. The joined error carries [context.DeadlineExceeded];
	// reach it with [errors.Unwrap] or test it with [errors.Is]. Safe to compare
	// with == or [errors.Is].
	ErrTimeout = errors.New("adaptx: acquire timed out")

	// ErrCancelled is returned when the caller's context is cancelled before a
	// slot becomes available. The joined error carries ctx.Err(); reach it with
	// [errors.Unwrap] or test it with [errors.Is]. Safe to compare with == or
	// [errors.Is].
	ErrCancelled = errors.New("adaptx: acquire cancelled")

	// ErrNilFunc is returned by [Execute] and [TryExecute] when the supplied
	// function is nil. Safe to compare with == or [errors.Is].
	ErrNilFunc = errors.New("adaptx: nil function")

	// ErrDrainTimeout is returned by [Limiter.CloseWithTimeout] when in-flight
	// work is still running after the drain deadline. The limiter stays closed;
	// remaining work is not cancelled. Safe to compare with == or [errors.Is].
	ErrDrainTimeout = errors.New("adaptx: drain timed out")
)

// errTimeout wraps [ErrTimeout] joining the underlying context cause.
func errTimeout(cause error) error {
	return fmt.Errorf("%w: %w", ErrTimeout, cause)
}

// errCancelled wraps [ErrCancelled] joining the underlying context cause.
func errCancelled(cause error) error {
	return fmt.Errorf("%w: %w", ErrCancelled, cause)
}
