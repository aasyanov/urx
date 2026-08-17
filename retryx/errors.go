package retryx

import (
	"errors"
	"fmt"
)

var (
	// ErrExhausted is returned by [Do] when every attempt failed or a
	// non-retryable error stopped the loop early, and the context is still
	// live. The joined error carries attempts=N and the last underlying cause:
	// N is the 1-based attempt that stopped the loop when [WithRetryIf]
	// rejects an error; when the full attempt budget is consumed, N equals
	// maxAttempts. If the context is already done after a failed attempt
	// (including the last), [Do] returns [ErrCancelled] instead. Safe to
	// compare with == or [errors.Is].
	ErrExhausted = errors.New("retryx: all retry attempts exhausted")

	// ErrCancelled is returned by [Do] when the context is cancelled or its
	// deadline expires, either before an attempt, after a failed attempt
	// (including the last), or during a backoff sleep. The joined error
	// carries ctx.Err(). Safe to compare with == or [errors.Is].
	ErrCancelled = errors.New("retryx: retry cancelled by context")

	// ErrAborted is returned by [Do] when the callback calls
	// [RetryController.Abort]. The joined error carries the last attempt's
	// cause. Safe to compare with == or [errors.Is].
	ErrAborted = errors.New("retryx: retry aborted by caller")

	// ErrNilFunc is returned by [Do] when the supplied function is nil.
	// Safe to compare with == or [errors.Is].
	ErrNilFunc = errors.New("retryx: nil function")

	// ErrMaxElapsed is returned by [Do] when [WithMaxElapsed] is set and the
	// wall-clock budget is exhausted before a later attempt can start. The
	// first attempt always runs. The joined error carries the last attempt's
	// cause. Safe to compare with == or [errors.Is].
	ErrMaxElapsed = errors.New("retryx: max elapsed time exceeded")
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

// errMaxElapsed wraps [ErrMaxElapsed] with the last attempt's cause.
func errMaxElapsed(cause error) error {
	return fmt.Errorf("%w: %w", ErrMaxElapsed, cause)
}
