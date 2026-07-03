package circuitx

import (
	"errors"
	"fmt"
)

var (
	// ErrOpen is returned by [Execute] when the circuit is Open (or HalfOpen
	// with a probe already in flight) and the call is rejected without
	// invoking fn. After the reset timeout elapses the circuit admits a single
	// probe; concurrent callers continue to receive ErrOpen until the probe
	// completes. Safe to compare with == or [errors.Is].
	ErrOpen = errors.New("circuitx: circuit breaker is open")

	// ErrNilFunc is returned by [Execute] when the supplied function is nil.
	// Safe to compare with == or [errors.Is].
	ErrNilFunc = errors.New("circuitx: nil function")

	// ErrClosed is returned by [Execute] after [Breaker.Close] has been called.
	// It is distinct from the [Closed] circuit state: a closed [Breaker] is shut
	// down for good, whereas the Closed state is the healthy operating mode.
	// Safe to compare with == or [errors.Is].
	ErrClosed = errors.New("circuitx: breaker is closed")

	// ErrCancelled is returned by [Execute] when the supplied context is already
	// cancelled (or its deadline has expired) at admission time, so the breaker
	// state is left untouched and fn is never invoked. The joined error carries
	// ctx.Err(); reach it with [errors.Unwrap] or test it with [errors.Is]. Safe
	// to compare with == or [errors.Is].
	ErrCancelled = errors.New("circuitx: context cancelled")
)

// errCancelled wraps [ErrCancelled] joining the underlying context cause.
func errCancelled(cause error) error {
	return fmt.Errorf("%w: %w", ErrCancelled, cause)
}
