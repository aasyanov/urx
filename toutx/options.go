package toutx

import "time"

// DefaultTimeout is the timeout applied when neither the timeout argument,
// an [Option], nor a [Timer] specifies one.
const DefaultTimeout = 30 * time.Second

// Option configures a single [Execute] call or a reusable [Timer].
type Option func(*config)

// config holds resolved timeout parameters.
type config struct {
	timeout time.Duration
	op      string
}

// newConfig resolves the effective configuration: defaults first, then options
// in order, then the positional timeout (when > 0) last so it always wins.
// The trailing override matters because [WithTimer] replaces the whole config.
func newConfig(timeout time.Duration, opts []Option) config {
	cfg := config{timeout: DefaultTimeout}
	for _, opt := range opts {
		opt(&cfg)
	}
	if timeout > 0 {
		cfg.timeout = timeout
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

// WithTimeout sets the maximum duration the function may execute.
// Default: [DefaultTimeout]. Values <= 0 are ignored.
func WithTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.timeout = d
		}
	}
}

// WithOp sets the logical operation name attached to timeout errors and panic
// reports (e.g. "db.query", "http.fetch"). Default: "toutx.Execute".
func WithOp(op string) Option {
	return func(c *config) {
		if op != "" {
			c.op = op
		}
	}
}

// Timer is a reusable, immutable preset of timeout and operation-label defaults.
// Create one with [New] and apply it to [Execute] via [WithTimer].
//
// A Timer is safe for concurrent use from any number of goroutines: it is never
// mutated after construction and holds no per-call state.
type Timer struct {
	cfg config
}

// New creates an immutable [Timer] with the given options applied on top of the
// package defaults ([DefaultTimeout]). The returned Timer is safe for concurrent
// reuse across any number of [Execute] calls.
func New(opts ...Option) *Timer {
	return &Timer{cfg: newConfig(0, opts)}
}

// Timeout returns the timeout the [Timer] was configured with.
func (t *Timer) Timeout() time.Duration { return t.cfg.timeout }

// Op returns the operation name the [Timer] was configured with, or the empty
// string if none was set.
func (t *Timer) Op() string { return t.cfg.op }

// WithTimer seeds a call from a [Timer]'s pre-configured defaults. Options that
// follow [WithTimer] in the variadic list override the timer's values; the
// positional timeout argument to [Execute] still takes precedence when > 0.
// A nil Timer is ignored.
func WithTimer(t *Timer) Option {
	return func(c *config) {
		if t != nil {
			*c = t.cfg
		}
	}
}
