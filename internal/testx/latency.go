package testx

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------------
// Latency simulation
// ---------------------------------------------------------------------------

// LatencySim adds deterministic delays to function calls for testing
// timeout, hedge, and deadline behavior. It is safe for concurrent use.
type LatencySim struct {
	delay   time.Duration
	calls   atomic.Int64
	errFn   func() error
}

// LatencyOption configures [NewLatencySim] behavior.
type LatencyOption func(*LatencySim)

// WithLatency sets the simulated delay per call.
func WithLatency(d time.Duration) LatencyOption {
	return func(l *LatencySim) { l.delay = d }
}

// WithLatencyError sets a custom error to return after the delay.
// If nil, [LatencySim.Call] returns nil after sleeping.
func WithLatencyError(fn func() error) LatencyOption {
	return func(l *LatencySim) { l.errFn = fn }
}

// NewLatencySim creates a latency simulator. Default: zero delay, no error.
func NewLatencySim(opts ...LatencyOption) *LatencySim {
	l := &LatencySim{}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// Call blocks for the configured delay (respecting ctx cancellation),
// then returns the configured error (or nil).
func (l *LatencySim) Call(ctx context.Context) error {
	l.calls.Add(1)

	if l.delay <= 0 {
		if l.errFn != nil {
			return l.errFn()
		}
		return nil
	}

	select {
	case <-time.After(l.delay):
		if l.errFn != nil {
			return l.errFn()
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("testx: latency sim cancelled: %w", ctx.Err())
	}
}

// Calls returns the total number of calls made.
func (l *LatencySim) Calls() int64 { return l.calls.Load() }

// SlowCall creates a [LatencySim] that sleeps for d and returns nil.
func SlowCall(d time.Duration) *LatencySim {
	return NewLatencySim(WithLatency(d))
}

// SlowThenFail creates a [LatencySim] that sleeps for d and returns
// [ErrSimulated].
func SlowThenFail(d time.Duration) *LatencySim {
	return NewLatencySim(
		WithLatency(d),
		WithLatencyError(func() error {
			return fmt.Errorf("%w: slow call failed", ErrSimulated)
		}),
	)
}
