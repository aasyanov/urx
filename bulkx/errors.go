package bulkx

import (
	"errors"
	"fmt"
)

var (
	// ErrTimeout is returned by [Execute] when the configured timeout elapses
	// before a slot becomes available. Safe to compare with == or [errors.Is].
	ErrTimeout = errors.New("bulkx: slot acquisition timed out")

	// ErrClosed is returned by [Execute], [TryExecute], and [Bulkhead.Acquire]
	// after [Bulkhead.Close] has been called. Safe to compare with == or
	// [errors.Is].
	ErrClosed = errors.New("bulkx: bulkhead closed")

	// ErrCancelled is returned by [Execute] when the supplied context is
	// cancelled (or its deadline expires) before a slot is acquired, so no slot
	// is consumed and fn is never invoked. The joined error carries ctx.Err();
	// reach it with [errors.Unwrap] or test it with [errors.Is]. Safe to
	// compare with == or [errors.Is].
	ErrCancelled = errors.New("bulkx: slot wait cancelled")

	// ErrNilFunc is returned by [Execute] and [TryExecute] when the supplied
	// function is nil. Safe to compare with == or [errors.Is].
	ErrNilFunc = errors.New("bulkx: nil function")
)

// errCancelled wraps [ErrCancelled] joining the underlying context cause.
func errCancelled(cause error) error {
	return fmt.Errorf("%w: %w", ErrCancelled, cause)
}
