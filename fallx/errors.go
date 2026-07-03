package fallx

import (
	"errors"
	"fmt"
)

var (
	// ErrNoFunc is returned by [Execute] under [StrategyFunc] when no fallback
	// function was configured (or a nil one was supplied). Safe to compare with
	// == or [errors.Is].
	ErrNoFunc = errors.New("fallx: no fallback function configured")

	// ErrNoCached is returned by [Execute] under [StrategyCached] when the
	// primary fails and no live cached value exists for the request key. The
	// wrapped error carries the key. Safe to compare with == or [errors.Is].
	ErrNoCached = errors.New("fallx: no cached result available")

	// ErrClosed is returned by [Execute] and [ExecuteWithKey] after
	// [Fallback.Close] has been called. [Fallback.Seed], [Fallback.SeedWithTTL],
	// and [Fallback.ClearCache] become no-ops instead of returning this error.
	// Safe to compare with == or [errors.Is].
	ErrClosed = errors.New("fallx: fallback is closed")

	// ErrNilFunc is returned by [Execute] and [ExecuteWithKey] when the supplied
	// primary function is nil. Safe to compare with == or [errors.Is].
	ErrNilFunc = errors.New("fallx: nil primary function")

	// ErrFallbackFailed is returned by [Execute] under [StrategyFunc] when the
	// fallback function itself returns an error. The joined error carries that
	// cause; reach it with [errors.Unwrap] or test it with [errors.Is]. Safe to
	// compare with == or [errors.Is].
	ErrFallbackFailed = errors.New("fallx: fallback function failed")
)

// errNoCached wraps [ErrNoCached] with the key that had no live entry.
func errNoCached(key string) error {
	return fmt.Errorf("%w (key=%s)", ErrNoCached, key)
}

// errFallbackFailed wraps [ErrFallbackFailed] joining the cause returned by the
// fallback function.
func errFallbackFailed(cause error) error {
	return fmt.Errorf("%w: %w", ErrFallbackFailed, cause)
}
