package retryx

import (
	"errors"
	"fmt"
)

var (
	// ErrExhausted is returned by [Do] when every attempt failed or a
	// non-retryable error stopped the loop. The joined error carries the last
	// underlying cause. Safe to compare with == or [errors.Is].
	ErrExhausted = errors.New("retryx: all retry attempts exhausted")

	// ErrCancelled is returned by [Do] when the context is cancelled or its
	// deadline expires, either before an attempt or during a backoff sleep.
	// The joined error carries ctx.Err(). Safe to compare with == or
	// [errors.Is].
	ErrCancelled = errors.New("retryx: retry cancelled by context")

	// ErrAborted is returned by [Do] when the callback calls
	// [RetryController.Abort]. The joined error carries the last attempt's
	// cause. Safe to compare with == or [errors.Is].
	ErrAborted = errors.New("retryx: retry aborted by caller")

	// ErrNilFunc is returned by [Do] when the supplied function is nil.
	// Safe to compare with == or [errors.Is].
	ErrNilFunc = errors.New("retryx: nil function")
)

// errExhausted wraps [ErrExhausted] with the attempt count and the last cause.
func errExhausted(attempts int, cause error) error {
	return fmt.Errorf("%w (attempts=%d): %w", ErrExhausted, attempts, cause)
}

// errCancelled wraps [ErrCancelled] with the context cause.
func errCancelled(cause error) error {
	return fmt.Errorf("%w: %w", ErrCancelled, cause)
}

// errAborted wraps [ErrAborted] with the aborting attempt number and cause.
func errAborted(attempt int, cause error) error {
	return fmt.Errorf("%w (attempt=%d): %w", ErrAborted, attempt, cause)
}
