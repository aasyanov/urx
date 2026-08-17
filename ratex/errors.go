package ratex

import (
	"errors"
	"fmt"
)

var (
	// ErrCancelled is returned by [Limiter.Wait], [Limiter.WaitN], [Execute], and
	// [TryExecute] when the context is cancelled or its deadline expires before a
	// token becomes available, or before the admitted callback runs. The joined
	// error carries ctx.Err(). Safe to compare with == or [errors.Is].
	ErrCancelled = errors.New("ratex: rate limiter wait cancelled")

	// ErrNilFunc is returned by [Execute] and [TryExecute] when the supplied
	// function is nil. Safe to compare with == or [errors.Is].
	ErrNilFunc = errors.New("ratex: nil function")

	// ErrExceedsBurst is returned by [Limiter.WaitN] when n is greater than the
	// bucket capacity. Such a request can never be satisfied, so WaitN fails
	// immediately without blocking or consuming tokens. Safe to compare with
	// == or [errors.Is].
	ErrExceedsBurst = errors.New("ratex: n exceeds burst")
)

// errCancelled wraps [ErrCancelled] with the context cause.
func errCancelled(cause error) error {
	return fmt.Errorf("%w: %w", ErrCancelled, cause)
}
