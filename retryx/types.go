package retryx

import (
	"context"
	"time"
)

// RetryController provides per-attempt context and control to the retried
// function. The implementation is private; callers interact only through this
// interface. A RetryController is bound to a single [Do] call and must not be
// retained after Do returns. It is accessed only from the goroutine running the
// callback and is not safe for concurrent use across goroutines.
type RetryController interface {
	// Number returns the 1-based number of the current attempt.
	Number() int

	// LastErr returns the error returned by the previous attempt, or nil on
	// the first attempt. Use it to adapt behaviour to the prior failure (for
	// example, switching endpoints after a specific error).
	LastErr() error

	// Elapsed returns the wall-clock time spent in [Do] so far, measured from
	// the start of the first attempt. It includes execution and backoff time.
	Elapsed() time.Duration

	// Abort signals that the retry loop must stop after the current attempt,
	// regardless of the error's retryability. It is safe to call multiple
	// times; only the current attempt's result is returned.
	Abort()
}

// attempt is the private implementation of [RetryController]. It is created
// fresh per attempt and accessed only from the calling goroutine, so it needs
// no synchronization.
type attempt struct {
	number  int
	lastErr error
	start   time.Time
	aborted bool
}

// Number implements [RetryController].
func (a *attempt) Number() int { return a.number }

// LastErr implements [RetryController].
func (a *attempt) LastErr() error { return a.lastErr }

// Elapsed implements [RetryController].
func (a *attempt) Elapsed() time.Duration { return time.Since(a.start) }

// Abort implements [RetryController].
func (a *attempt) Abort() { a.aborted = true }

// compile-time assertion that attempt satisfies the public interface.
var _ RetryController = (*attempt)(nil)

// RetryFunc is the unit of work retried by [Do]. It receives the call context
// and a [RetryController], and runs under panic recovery: a panicking function
// becomes a [*panix.PanicError] and is treated as a retryable failure (subject
// to [WithRetryIf]).
type RetryFunc[T any] func(ctx context.Context, rc RetryController) (T, error)
