// Package testx provides deterministic failure and latency simulation for
// testing resilience patterns in industrial Go services.
//
// A [Simulator] produces errors on demand according to a configurable
// schedule. Use it to test retryx, circuitx, bulkx, and other resilience
// wrappers without flaky sleeps or randomness.
//
//	sim := testx.NewSimulator(testx.WithFailPattern("SSFS"))
//	err := retryx.Do(ctx, func(ctx context.Context, rc retryx.RetryController) error {
//	    return sim.Call()
//	})
//
// A [LatencySim] adds deterministic delays to simulate slow downstream
// services for timeout and hedge testing.
//
// Helper functions for footprint testing ([AssertSize], [AssertFootprint]),
// context creation ([CancelledCtx], [TimedCtx]), and concurrency stress
// testing ([Hammer]) reduce boilerplate in every package's test suite.
//
// All types are safe for concurrent use.
package testx

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// ---------------------------------------------------------------------------
// Failure schedule
// ---------------------------------------------------------------------------

// FailMode defines when the simulator produces errors.
type FailMode uint8

const (
	// FailNever never fails (default).
	FailNever FailMode = iota
	// FailAlways fails on every call.
	FailAlways
	// FailPattern follows a repeating pattern string (S=success, F=fail).
	FailPattern
	// FailAfterN succeeds N times, then fails forever.
	FailAfterN
	// FailUntilN fails N times, then succeeds forever.
	FailUntilN
	// FailEveryN fails every Nth call (1-based).
	FailEveryN
)

// String returns a human-readable label.
func (m FailMode) String() string {
	switch m {
	case FailNever:
		return "never"
	case FailAlways:
		return "always"
	case FailPattern:
		return "pattern"
	case FailAfterN:
		return "after_n"
	case FailUntilN:
		return "until_n"
	case FailEveryN:
		return "every_n"
	default:
		return "unknown"
	}
}

// ---------------------------------------------------------------------------
// Simulator configuration
// ---------------------------------------------------------------------------

type simConfig struct {
	mode    FailMode
	n       int
	pattern string
	msg     string
	errFn   func() error
}

func defaultSimConfig() simConfig {
	return simConfig{
		mode: FailNever,
		msg:  "simulated failure",
	}
}

// SimOption configures [NewSimulator] behavior.
type SimOption func(*simConfig)

// WithFailAlways makes the simulator fail on every call.
func WithFailAlways() SimOption {
	return func(c *simConfig) { c.mode = FailAlways }
}

// WithFailPattern sets a repeating pattern. Each character is one call:
// 'S' or 's' = success, 'F' or 'f' = failure.
// Example: "SSFS" → success, success, fail, success, ...
func WithFailPattern(pattern string) SimOption {
	return func(c *simConfig) {
		c.mode = FailPattern
		c.pattern = pattern
	}
}

// WithFailAfterN succeeds for the first n calls, then fails forever.
func WithFailAfterN(n int) SimOption {
	return func(c *simConfig) {
		c.mode = FailAfterN
		if n > 0 {
			c.n = n
		}
	}
}

// WithFailUntilN fails for the first n calls, then succeeds forever.
func WithFailUntilN(n int) SimOption {
	return func(c *simConfig) {
		c.mode = FailUntilN
		if n > 0 {
			c.n = n
		}
	}
}

// WithFailEveryN fails every nth call (1-based).
func WithFailEveryN(n int) SimOption {
	return func(c *simConfig) {
		c.mode = FailEveryN
		if n > 0 {
			c.n = n
		}
	}
}

// WithMessage sets the error message for simulated failures.
func WithMessage(msg string) SimOption {
	return func(c *simConfig) {
		if msg != "" {
			c.msg = msg
		}
	}
}

// WithErrorFunc sets a custom error factory. When set, this function is
// called instead of the default error constructor.
func WithErrorFunc(fn func() error) SimOption {
	return func(c *simConfig) { c.errFn = fn }
}

// ---------------------------------------------------------------------------
// Simulator
// ---------------------------------------------------------------------------

// Simulator is a deterministic failure generator for testing resilience
// patterns. It is safe for concurrent use.
type Simulator struct {
	cfg        simConfig
	calls      atomic.Int64
	failures   atomic.Int64
	mu         sync.Mutex
	patternIdx int
}

// NewSimulator creates a [Simulator] with the given options.
// Default: never fail.
func NewSimulator(opts ...SimOption) *Simulator {
	cfg := defaultSimConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Simulator{cfg: cfg}
}

// Call executes one simulated call. Returns nil on success or an error
// on simulated failure. The returned error wraps [ErrSimulated] unless
// a custom [WithErrorFunc] is set.
func (s *Simulator) Call() error {
	n := s.calls.Add(1)
	if s.shouldFail(n) {
		s.failures.Add(1)
		return s.makeError()
	}
	return nil
}

// Calls returns the total number of calls made.
func (s *Simulator) Calls() int64 { return s.calls.Load() }

// Failures returns the total number of failures produced.
func (s *Simulator) Failures() int64 { return s.failures.Load() }

// Reset zeroes all counters and rewinds the pattern index.
func (s *Simulator) Reset() {
	s.calls.Store(0)
	s.failures.Store(0)
	s.mu.Lock()
	s.patternIdx = 0
	s.mu.Unlock()
}

func (s *Simulator) shouldFail(callNum int64) bool {
	switch s.cfg.mode {
	case FailNever:
		return false
	case FailAlways:
		return true
	case FailPattern:
		return s.patternFail()
	case FailAfterN:
		return callNum > int64(s.cfg.n)
	case FailUntilN:
		return callNum <= int64(s.cfg.n)
	case FailEveryN:
		return s.cfg.n > 0 && callNum%int64(s.cfg.n) == 0
	default:
		return false
	}
}

func (s *Simulator) patternFail() bool {
	p := s.cfg.pattern
	if len(p) == 0 {
		return false
	}
	s.mu.Lock()
	ch := p[s.patternIdx%len(p)]
	s.patternIdx++
	s.mu.Unlock()
	return ch == 'F' || ch == 'f'
}

func (s *Simulator) makeError() error {
	if s.cfg.errFn != nil {
		return s.cfg.errFn()
	}
	return fmt.Errorf("%w: %s", ErrSimulated, s.cfg.msg)
}

// ---------------------------------------------------------------------------
// Convenience constructors
// ---------------------------------------------------------------------------

// AlwaysFail returns a simulator that fails on every call.
func AlwaysFail() *Simulator { return NewSimulator(WithFailAlways()) }

// NeverFail returns a simulator that never fails.
func NeverFail() *Simulator { return NewSimulator() }

// FailAfter returns a simulator that succeeds n times, then fails forever.
func FailAfter(n int) *Simulator { return NewSimulator(WithFailAfterN(n)) }

// FailUntil returns a simulator that fails n times, then succeeds forever.
func FailUntil(n int) *Simulator { return NewSimulator(WithFailUntilN(n)) }

// FailEvery returns a simulator that fails every nth call.
func FailEvery(n int) *Simulator { return NewSimulator(WithFailEveryN(n)) }

// Pattern returns a simulator following a repeating S/F pattern.
func Pattern(p string) *Simulator { return NewSimulator(WithFailPattern(p)) }
