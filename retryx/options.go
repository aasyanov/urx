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

// Option configures a [Do] call.
type Option func(*config)

// config holds resolved retry parameters.
type config struct {
	maxAttempts int
	backoff     time.Duration
	maxBackoff  time.Duration
	jitter      bool
	retryIf     func(error) bool
	onRetry     func(attempt int, err error)
	op          string
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
		jitter:      true,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxAttempts < minAttempts {
		cfg.maxAttempts = minAttempts
	}
	return cfg
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

// WithJitter enables or disables random jitter on the backoff delay. When
// enabled, the capped delay is multiplied by a random factor in [0.5, 1.5) to
// decorrelate retries across callers. Default: enabled.
func WithJitter(enabled bool) Option {
	return func(c *config) { c.jitter = enabled }
}

// WithRetryIf sets a predicate that decides whether a non-nil error is
// retryable. Return true to retry, false to stop immediately with
// [ErrExhausted]. When unset, every error is considered retryable.
// Default: nil (retry all errors).
func WithRetryIf(fn func(error) bool) Option {
	return func(c *config) { c.retryIf = fn }
}

// WithOnRetry sets a callback invoked after each failed-but-retryable attempt,
// just before the backoff sleep. The attempt number is 1-based. Useful for
// logging or metrics. Default: nil.
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
