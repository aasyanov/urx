package toutx

import (
	"errors"
	"fmt"
	"time"
)

var (
	// ErrDeadlineExceeded is returned by [Execute] when the function does not
	// complete before the configured timeout fires. It is safe to compare with
	// == or [errors.Is].
	ErrDeadlineExceeded = errors.New("toutx: deadline exceeded")

	// ErrCancelled is returned by [Execute] when the parent context is
	// cancelled (or its deadline expires) before the function completes. The
	// joined error carries the underlying cause; reach it with [errors.Unwrap]
	// or test it with [errors.Is]. Safe to compare with == or [errors.Is].
	ErrCancelled = errors.New("toutx: context cancelled")

	// ErrNilFunc is returned by [Execute] when the supplied function is nil.
	// It is safe to compare with == or [errors.Is].
	ErrNilFunc = errors.New("toutx: nil function")
)

// errDeadlineExceeded wraps [ErrDeadlineExceeded] with the operation name and
// the configured budget for diagnostics.
func errDeadlineExceeded(op string, timeout time.Duration) error {
	return fmt.Errorf("%w (op=%s, timeout=%s)", ErrDeadlineExceeded, op, timeout)
}

// errCancelled wraps [ErrCancelled] with the operation name and joins the
// underlying cause.
func errCancelled(op string, cause error) error {
	return fmt.Errorf("%w (op=%s): %w", ErrCancelled, op, cause)
}

// errNilFunc wraps [ErrNilFunc] with the operation name.
func errNilFunc(op string) error {
	return fmt.Errorf("%w (op=%s)", ErrNilFunc, op)
}
