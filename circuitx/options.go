package circuitx

import "time"

const (
	// DefaultMaxFailures is the number of consecutive failures that trips the
	// breaker from [Closed] to [Open], applied when [WithMaxFailures] is not
	// supplied.
	DefaultMaxFailures = 5

	// DefaultResetTimeout is how long the breaker stays [Open] before admitting
	// a probe in [HalfOpen], applied when [WithResetTimeout] is not supplied.
	DefaultResetTimeout = 10 * time.Second

	// DefaultHalfOpenMax is the number of concurrent probe calls admitted in
	// [HalfOpen], applied when [WithHalfOpenMax] is not supplied.
	DefaultHalfOpenMax = 1

	// DefaultSuccessThreshold is the number of consecutive probe successes in
	// [HalfOpen] required to heal to [Closed], applied when
	// [WithSuccessThreshold] is not supplied.
	DefaultSuccessThreshold = 1

	// opExecute labels panics recovered while running an [Execute] callback and
	// is the default operation name when [WithOp] is not set.
	opExecute = "circuitx.Execute"

	// opTryExecute labels panics recovered while running a [TryExecute] callback.
	opTryExecute = "circuitx.TryExecute"

	// minMaxFailures is the floor [New] enforces: a non-positive threshold
	// degrades to a single failure rather than a breaker that never trips.
	minMaxFailures = 1

	// minHalfOpenMax is the floor [New] enforces on the probe budget.
	minHalfOpenMax = 1

	// minSuccessThreshold is the floor [New] enforces on the heal threshold.
	minSuccessThreshold = 1
)

// Option configures a [Breaker] created by [New].
type Option func(*config)

// config holds resolved breaker parameters.
type config struct {
	maxFailures      int
	resetTimeout     time.Duration
	halfOpenMax      int
	successThreshold int
	onStateChange    func(from, to State)
	failureIf        func(error) bool
	countCanceled    bool
	op               string
}

// newConfig resolves the effective configuration: defaults first, then options
// in order, with the failure-threshold and probe-budget floors applied last so
// an invalid option can never produce an unusable breaker.
func newConfig(opts []Option) config {
	cfg := config{
		maxFailures:      DefaultMaxFailures,
		resetTimeout:     DefaultResetTimeout,
		halfOpenMax:      DefaultHalfOpenMax,
		successThreshold: DefaultSuccessThreshold,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.maxFailures < minMaxFailures {
		cfg.maxFailures = minMaxFailures
	}
	if cfg.resetTimeout <= 0 {
		cfg.resetTimeout = DefaultResetTimeout
	}
	if cfg.halfOpenMax < minHalfOpenMax {
		cfg.halfOpenMax = minHalfOpenMax
	}
	if cfg.successThreshold < minSuccessThreshold {
		cfg.successThreshold = minSuccessThreshold
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

// opOrDefaultTry returns the configured operation name, or [opTryExecute] when
// none was set.
func (c config) opOrDefaultTry() string {
	if c.op != "" {
		return c.op
	}
	return opTryExecute
}

// WithMaxFailures sets the number of consecutive failures that trips the
// breaker from [Closed] to [Open]. A success in [Closed] resets the count to
// zero, so it counts a run of back-to-back failures, not a lifetime total.
// Default: [DefaultMaxFailures]. Values < 1 are floored to 1.
func WithMaxFailures(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxFailures = n
		}
	}
}

// WithResetTimeout sets how long the breaker stays [Open] before it admits a
// probe in [HalfOpen]. Default: [DefaultResetTimeout]. Values <= 0 are ignored
// (the default is kept).
func WithResetTimeout(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.resetTimeout = d
		}
	}
}

// WithHalfOpenMax sets how many probe calls may run concurrently in [HalfOpen]
// before the breaker decides to close or re-open. A single probe (the default)
// is the safest: it admits exactly one call to test recovery and rejects the
// rest with [ErrOpen] or, for [TryExecute], (false, zero, nil). A larger budget
// lets several probes run at once to reach a verdict faster at the cost of more
// load on a possibly-unhealthy downstream. Default: [DefaultHalfOpenMax]. Values
// < 1 are floored to 1.
func WithHalfOpenMax(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.halfOpenMax = n
		}
	}
}

// WithSuccessThreshold sets how many consecutive probe successes in [HalfOpen]
// are required before the breaker heals to [Closed]. A probe failure resets
// the counter and re-opens the circuit immediately. Default:
// [DefaultSuccessThreshold]. Values <= 0 are ignored; values < 1 are floored
// to 1.
func WithSuccessThreshold(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.successThreshold = n
		}
	}
}

// WithOnStateChange registers a callback invoked on every state transition,
// receiving the previous and new [State]. Use it for metrics or logging; it
// runs synchronously on the goroutine that drives the transition and must not
// block. A panic in the hook is recovered and discarded so it cannot crash the
// caller. The hook is not fired by [Breaker.Stats] — only by [Breaker.State],
// [Execute], [TryExecute], and [Breaker.Reset]. A nil callback is ignored.
func WithOnStateChange(fn func(from, to State)) Option {
	return func(c *config) {
		if fn != nil {
			c.onStateChange = fn
		}
	}
}

// WithFailureIf sets a predicate that decides whether a remaining error counts
// as a circuit failure. [CircuitController.SkipFailure] always wins and is not
// filtered here. [context.Canceled] is ignored before this predicate runs
// unless [WithCountCanceled] is set. A recovered panic always counts and is
// not passed to the predicate. When fn is nil the option is ignored and every
// remaining error counts (the default).
func WithFailureIf(fn func(error) bool) Option {
	return func(c *config) {
		if fn != nil {
			c.failureIf = fn
		}
	}
}

// WithCountCanceled restores counting of post-admission [context.Canceled] as
// a circuit failure. By default a cancel after the call is admitted is returned
// to the caller but does not increment the consecutive-failure counter or trip
// the breaker. [context.DeadlineExceeded] is counted regardless of this option.
func WithCountCanceled() Option {
	return func(c *config) {
		c.countCanceled = true
	}
}

// WithOp sets the logical operation name attached to panic reports raised by
// the callback (e.g. "api.charge", "db.query"). Default: [opExecute] for
// [Execute] and [opTryExecute] for [TryExecute]. An empty name is ignored.
func WithOp(op string) Option {
	return func(c *config) {
		if op != "" {
			c.op = op
		}
	}
}
