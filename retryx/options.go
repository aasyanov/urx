package retryx

import "time"

const (
	// DefaultMaxAttempts is the total number of attempts (including the first)
	// applied when [WithMaxAttempts] is not supplied.
	DefaultMaxAttempts = 3

	// DefaultBackoff is the base backoff duration applied when [WithBackoff] is
	// not supplied.
	DefaultBackoff = 100 * time.Millisecond

	// DefaultMaxBackoff is the upper bound on a single backoff delay applied
	// when [WithMaxBackoff] is not supplied.
	DefaultMaxBackoff = 10 * time.Second

	// minAttempts is the floor [Do] enforces: a non-positive WithMaxAttempts
	// degrades to a single execution with no retries.
	minAttempts = 1

	// opDo labels panics recovered while running a [Do] attempt and is the
	// default value when [WithOp] is not supplied.
	opDo = "retryx.Do"
)

// jitterMode selects how [backoff] decorrelates delays. The zero value is
// multiplicative jitter so a zero config still matches package defaults.
type jitterMode uint8

const (
	jitterModeMultiplicative jitterMode = iota
	jitterModeOff
	jitterModeEqual
)

// Option configures a [Do] call.
type Option func(*config)

// config holds resolved retry parameters.
type config struct {
	maxAttempts int
	backoff     time.Duration
	maxBackoff  time.Duration
	maxElapsed  time.Duration
	retryIf     func(error) bool
	onRetry     func(attempt int, err error)
	delayFunc   func(attempt int, err error) time.Duration
	nowFn       func() time.Time
	op          string
	jitterMode  jitterMode
}

// opOrDefault returns the configured operation name, or [opDo] when none was
// set (or an empty string was supplied).
func (c config) opOrDefault() string {
	if c.op != "" {
		return c.op
	}
	return opDo
}

// newConfig resolves the effective configuration: defaults first, then options
// in order, with the attempt floor applied last.
func newConfig(opts []Option) config {
	cfg := config{
		maxAttempts: DefaultMaxAttempts,
		backoff:     DefaultBackoff,
		maxBackoff:  DefaultMaxBackoff,
		jitterMode:  jitterModeMultiplicative,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.maxAttempts < minAttempts {
		cfg.maxAttempts = minAttempts
	}
	return cfg
}

// now returns the configured clock, or [time.Now] when none was injected.
func (c config) now() time.Time {
	if c.nowFn != nil {
		return c.nowFn()
	}
	return time.Now()
}

// WithMaxAttempts sets the maximum number of attempts, including the first.
// Default: [DefaultMaxAttempts]. Values <= 0 degrade to a single attempt
// (execute once, no retry).
func WithMaxAttempts(n int) Option {
	return func(c *config) { c.maxAttempts = n }
}

// WithBackoff sets the base backoff duration for exponential backoff. The
// nominal delay before retry i (0-based) is base * 2^i, capped at the
// configured maximum and optionally jittered.
// Default: [DefaultBackoff]. Values <= 0 are ignored.
func WithBackoff(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.backoff = d
		}
	}
}

// WithMaxBackoff sets the upper bound on any single backoff delay.
// Default: [DefaultMaxBackoff]. Values <= 0 are ignored.
func WithMaxBackoff(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.maxBackoff = d
		}
	}
}

// WithJitter enables or disables multiplicative jitter on the backoff delay.
// When enabled, the capped delay is multiplied by a random factor in [0.5, 1.5)
// to decorrelate retries across callers. Passing false disables all jitter,
// including equal jitter from [WithEqualJitter]. Default: enabled.
func WithJitter(enabled bool) Option {
	return func(c *config) {
		if enabled {
			c.jitterMode = jitterModeMultiplicative
		} else {
			c.jitterMode = jitterModeOff
		}
	}
}

// WithEqualJitter selects equal jitter instead of the default multiplicative
// window: after the cap, the delay is d/2 + rand*(d/2), so it lands in [d/2, d).
// [WithDelayFunc] replaces backoff and jitter entirely when set.
func WithEqualJitter() Option {
	return func(c *config) { c.jitterMode = jitterModeEqual }
}

// WithMaxElapsed caps the wall-clock time [Do] may spend across attempts.
// Values <= 0 are ignored (no elapsed cap). The first attempt always runs;
// before each later attempt, if the elapsed time is already >= d, [Do] returns
// [ErrMaxElapsed] wrapping the last error. The sleep before a retry is shortened
// to the remaining budget.
func WithMaxElapsed(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.maxElapsed = d
		}
	}
}

// WithDelayFunc replaces exponential backoff and jitter with a caller-supplied
// delay. attempt is 1-based, matching [WithOnRetry]. A nil fn is ignored. The
// function is not an HTTP Retry-After parser — the caller computes the duration.
func WithDelayFunc(fn func(attempt int, err error) time.Duration) Option {
	return func(c *config) {
		if fn != nil {
			c.delayFunc = fn
		}
	}
}

// withClock injects a clock used to measure elapsed time for [WithMaxElapsed].
// A nil fn is ignored. Unexported: tests only; [WithClock] is not part of the
// public API.
func withClock(now func() time.Time) Option {
	return func(c *config) {
		if now != nil {
			c.nowFn = now
		}
	}
}

// WithRetryIf sets a predicate that decides whether a non-nil error is
// retryable. Return true to retry, false to stop immediately with
// [ErrExhausted]. When unset, every error is considered retryable.
// Default: nil (retry all errors).
func WithRetryIf(fn func(error) bool) Option {
	return func(c *config) { c.retryIf = fn }
}

// WithOnRetry sets a callback invoked after each failed-but-retryable attempt,
// just before the backoff sleep. The attempt number is 1-based. The hook runs
// synchronously under panic recovery and must not block or panic; a panic
// becomes a [*panix.PanicError] and stops retrying. Default: nil.
func WithOnRetry(fn func(attempt int, err error)) Option {
	return func(c *config) { c.onRetry = fn }
}

// WithOp sets the operation name reported in [*panix.PanicError] when the
// callback panics. Default: "retryx.Do". Empty strings are ignored.
func WithOp(op string) Option {
	return func(c *config) {
		if op != "" {
			c.op = op
		}
	}
}
