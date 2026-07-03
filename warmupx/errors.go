package warmupx

import (
	"errors"
	"fmt"
)

var (
	// ErrRejected is returned by [Warmer.AllowOrError] and [Execute] when
	// probabilistic admission fails, and by [Execute] or an admitted [TryExecute]
	// when the callback invokes [WarmupController.Reject]. The returned error
	// wraps ErrRejected with the capacity and progress at the moment of
	// rejection. Safe to compare with == or [errors.Is].
	ErrRejected = errors.New("warmupx: request rejected during warmup")

	// ErrNilFunc is returned by [Execute] and [TryExecute] when the supplied
	// function is nil. Safe to compare with == or [errors.Is].
	ErrNilFunc = errors.New("warmupx: nil function")

	// ErrCancelled is returned by [Execute] and [TryExecute] when the supplied
	// context is already cancelled (or its deadline has expired) at admission
	// time, so no probabilistic admission is attempted and fn is never invoked.
	// The joined error carries ctx.Err(); reach it with [errors.Unwrap] or test
	// it with [errors.Is]. Safe to compare with == or [errors.Is].
	ErrCancelled = errors.New("warmupx: context cancelled")
)

// errRejected wraps [ErrRejected] with the capacity and progress observed when
// the request was rejected.
func errRejected(capacity, progress float64) error {
	return fmt.Errorf("%w (capacity=%.2f, progress=%.2f)", ErrRejected, capacity, progress)
}

// errCancelled wraps [ErrCancelled] joining the underlying context cause.
func errCancelled(cause error) error {
	return fmt.Errorf("%w: %w", ErrCancelled, cause)
}
