package quotax

import (
	"errors"
	"fmt"
)

var (
	// ErrLimited is returned by [Quota.AllowOrError] when the key's token
	// bucket is empty and the request is denied. The joined error carries the
	// key. Safe to compare with == or [errors.Is].
	ErrLimited = errors.New("quotax: rate limit exceeded")

	// ErrMaxKeys is returned by [Quota.WaitN], [Execute], and [TryExecute] when
	// a new key cannot be admitted because the [WithMaxKeys] cap is reached.
	// The joined error carries the key. Safe to compare with == or
	// [errors.Is].
	ErrMaxKeys = errors.New("quotax: maximum tracked keys reached")

	// ErrCancelled is returned by [Quota.Wait], [Quota.WaitN], [Execute], and
	// [TryExecute] when the context is cancelled or its deadline expires before
	// a token becomes available, or before the admitted callback runs. The
	// joined error carries ctx.Err(). Safe to compare with == or [errors.Is].
	ErrCancelled = errors.New("quotax: wait cancelled")

	// ErrClosed is returned by every admission method after [Quota.Close] has
	// been called. Safe to compare with == or [errors.Is].
	ErrClosed = errors.New("quotax: limiter is closed")

	// ErrNilFunc is returned by [Execute] and [TryExecute] when the supplied
	// function is nil. Safe to compare with == or [errors.Is].
	ErrNilFunc = errors.New("quotax: nil function")
)

// errLimited wraps [ErrLimited] with the denied key.
func errLimited(key string) error {
	return fmt.Errorf("%w (key=%s)", ErrLimited, key)
}

// errMaxKeys wraps [ErrMaxKeys] with the rejected key.
func errMaxKeys(key string) error {
	return fmt.Errorf("%w (key=%s)", ErrMaxKeys, key)
}

// errCancelled wraps [ErrCancelled] with the context cause.
func errCancelled(cause error) error {
	return fmt.Errorf("%w: %w", ErrCancelled, cause)
}
