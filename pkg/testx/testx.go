// Package testx provides deterministic failure simulation for testing
// resilience patterns in industrial Go services.
//
// A [Simulator] produces errors on demand according to a configurable
// schedule. Use it to test [retryx], [circuitx], [bulkx], and other
// resilience wrappers without flaky sleeps or randomness.
//
//	sim := testx.New(testx.WithFailPattern("SSFS"))
//	err := retryx.Do(ctx, func(rc retryx.RetryController) error {
//	    return sim.Call()
//	})
//
// All errors are [*errx.Error] with Domain "TEST" and Code "SIMULATED",
// marked [errx.RetrySafe] by default.
package testx

import (
	"sync"
	"sync/atomic"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- Failure schedule ---

// FailMode defines when the simulator produces errors.
type FailMode uint8

const (
	// FailNever never fails. Default.
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

const (
	labelNever   = "never"
	labelAlways  = "always"
	labelPattern = "pattern"
	labelAfterN  = "after_n"
	labelUntilN  = "until_n"
	labelEveryN  = "every_n"
	labelUnknown = "unknown"
)

// String returns a human-readable label.
func (m FailMode) String() string {
	switch m {
	case FailNever:
		return labelNever
	case FailAlways:
		return labelAlways
	case FailPattern:
		return labelPattern
	case FailAfterN:
		return labelAfterN
	case FailUntilN:
		return labelUntilN
	case FailEveryN:
		return labelEveryN
	default:
		return labelUnknown
	}
}

// --- Configuration ---

type config struct {
	mode    FailMode
	n       int
	pattern string
	msg     string
	errFn   func() *errx.Error
}

func defaultConfig() config {
	return config{
		mode: FailNever,
		msg:  "simulated failure",
	}
}

// --- Options ---

// Option configures [New] behavior.
type Option func(*config)

// WithFailAlways makes the simulator fail on every call.
func WithFailAlways() Option {
	return func(c *config) { c.mode = FailAlways }
}

// WithFailPattern sets a repeating pattern. Each character is one call:
// 'S' or 's' = success, 'F' or 'f' = failure.
// Example: "SSFS" → success, success, fail, success, success, success, fail, ...
func WithFailPattern(pattern string) Option {
	return func(c *config) {
		c.mode = FailPattern
		c.pattern = pattern
	}
}

// WithFailAfterN makes the simulator succeed for the first n calls,
// then fail on every subsequent call.
func WithFailAfterN(n int) Option {
	return func(c *config) {
		c.mode = FailAfterN
		if n > 0 {
			c.n = n
		}
	}
}

// WithFailUntilN makes the simulator fail for the first n calls,
// then succeed on every subsequent call.
func WithFailUntilN(n int) Option {
	return func(c *config) {
		c.mode = FailUntilN
		if n > 0 {
			c.n = n
		}
	}
}

// WithFailEveryN makes the simulator fail on every Nth call (1-based).
// Other calls succeed.
func WithFailEveryN(n int) Option {
	return func(c *config) {
		c.mode = FailEveryN
		if n > 0 {
			c.n = n
		}
	}
}

// WithMessage sets the error message for simulated failures.
func WithMessage(msg string) Option {
	return func(c *config) {
		if msg != "" {
			c.msg = msg
		}
	}
}

// WithErrorFunc sets a custom error factory. When set, this function is
// called instead of the default [errSimulated] constructor, giving full
// control over domain, code, severity, retryability, and metadata.
func WithErrorFunc(fn func() *errx.Error) Option {
	return func(c *config) { c.errFn = fn }
}

// --- Simulator ---

// Simulator is a deterministic failure generator for testing resilience
// patterns. It is safe for concurrent use.
type Simulator struct {
	cfg        config
	calls      atomic.Int64
	failures   atomic.Int64
	mu         sync.Mutex
	patternIdx int
}

// New creates a [Simulator] with the given options. Default: never fail.
func New(opts ...Option) *Simulator {
	cfg := defaultConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	return &Simulator{cfg: cfg}
}

// Call executes one simulated call. Returns nil on success or an
// [*errx.Error] on simulated failure.
func (s *Simulator) Call() error {
	n := s.calls.Add(1)

	if s.shouldFail(n) {
		s.failures.Add(1)
		return s.makeError()
	}
	return nil
}

// Stats returns a snapshot of call/failure counts.
func (s *Simulator) Stats() Stats {
	return Stats{
		Calls:    s.calls.Load(),
		Failures: s.failures.Load(),
	}
}

// Reset zeroes all counters and rewinds the pattern index.
func (s *Simulator) Reset() {
	s.calls.Store(0)
	s.failures.Store(0)
	s.mu.Lock()
	s.patternIdx = 0
	s.mu.Unlock()
}

// Stats holds simulator counters.
type Stats struct {
	Calls    int64 `json:"calls"`
	Failures int64 `json:"failures"`
}

// --- Failure logic ---

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

func (s *Simulator) makeError() *errx.Error {
	if s.cfg.errFn != nil {
		if e := s.cfg.errFn(); e != nil {
			return e
		}
	}
	return errSimulated(s.cfg.msg)
}

// --- Convenience constructors ---

// AlwaysFail creates a simulator that fails on every call.
func AlwaysFail() *Simulator { return New(WithFailAlways()) }

// NeverFail creates a simulator that never fails.
func NeverFail() *Simulator { return New() }

// FailAfter creates a simulator that succeeds n times, then fails.
func FailAfter(n int) *Simulator { return New(WithFailAfterN(n)) }

// FailUntil creates a simulator that fails n times, then succeeds.
func FailUntil(n int) *Simulator { return New(WithFailUntilN(n)) }

// FailEvery creates a simulator that fails every nth call.
func FailEvery(n int) *Simulator { return New(WithFailEveryN(n)) }

// Pattern creates a simulator following a repeating pattern.
func Pattern(p string) *Simulator { return New(WithFailPattern(p)) }
