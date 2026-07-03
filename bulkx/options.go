package bulkx

import "time"

const (
	// DefaultMaxConcurrent is the number of concurrent slots applied when
	// [WithMaxConcurrent] is not supplied.
	DefaultMaxConcurrent = 10

	// DefaultTimeout is the maximum time [Execute] waits to acquire a slot,
	// applied when [WithTimeout] is not supplied.
	DefaultTimeout = 30 * time.Second

	// minConcurrent is the floor [New] enforces: a non-positive slot count
	// degrades to a single slot rather than producing a zero-capacity channel
	// that would block every caller forever.
	minConcurrent = 1
)

// Option configures a [Bulkhead] created by [New].
type Option func(*config)

// config holds resolved bulkhead parameters.
type config struct {
	maxConcurrent int
	timeout       time.Duration
	op            string
}

// newConfig resolves the effective configuration: defaults first, then options
// in order, with the concurrency floor applied last so an invalid option can
// never produce an unusable bulkhead.
func newConfig(opts []Option) config {
	cfg := config{
		maxConcurrent: DefaultMaxConcurrent,
		timeout:       DefaultTimeout,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxConcurrent < minConcurrent {
		cfg.maxConcurrent = minConcurrent
	}
	return cfg
}

// opOrDefault returns the configured operation name, or [opExecute] when none
// was set.
func (c config) opOrDefault() string {
	if c.op != "" {
		return c.op
	}
	return opExecute
}

// WithMaxConcurrent sets the maximum number of operations that may execute
// simultaneously. Default: [DefaultMaxConcurrent]. Values <= 0 are ignored
// (the default is kept), and a final value below [minConcurrent] is floored
// to 1.
func WithMaxConcurrent(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxConcurrent = n
		}
	}
}

// WithTimeout sets the maximum duration [Execute] waits to acquire a slot when
// all slots are busy. Default: [DefaultTimeout]. Values <= 0 are ignored.
//
// The timeout governs only the wait for a slot — it does not bound the runtime
// of the callback itself. Compose with toutx for a per-operation deadline.
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithOp sets the logical operation name attached to panic reports raised by
// the callback (e.g. "api.search", "db.query"). Default: "bulkx.Execute".
func WithOp(op string) Option {
	return func(c *config) {
		if op != "" {
			c.op = op
		}
	}
}
