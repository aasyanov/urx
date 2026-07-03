package shedx

const (
	// DefaultCapacity is the maximum number of concurrent in-flight operations
	// applied when [WithCapacity] is not supplied.
	DefaultCapacity = 1000

	// DefaultThreshold is the load fraction at which shedding begins, applied
	// when [WithThreshold] is not supplied. Below this load every request is
	// admitted; above it, requests are shed progressively by priority.
	DefaultThreshold = 0.8

	// minCapacity is the floor [New] enforces: a non-positive capacity degrades
	// to a single in-flight slot rather than dividing by zero in [Shedder.Load].
	minCapacity = 1

	// thresholdFloor and thresholdCeil bound a valid threshold. A value outside
	// (thresholdFloor, thresholdCeil] is rejected and replaced by the default.
	thresholdFloor = 0.0
	thresholdCeil  = 1.0
)

// Option configures a [Shedder] created by [New].
type Option func(*config)

// config holds resolved shedder parameters.
type config struct {
	capacity  int
	threshold float64
	op        string
}

// newConfig resolves the effective configuration: defaults first, then options
// in order, with the capacity floor and threshold bounds applied last so an
// invalid option can never produce an unusable shedder.
func newConfig(opts []Option) config {
	cfg := config{
		capacity:  DefaultCapacity,
		threshold: DefaultThreshold,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.capacity < minCapacity {
		cfg.capacity = minCapacity
	}
	if cfg.threshold <= thresholdFloor || cfg.threshold > thresholdCeil {
		cfg.threshold = DefaultThreshold
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

// WithCapacity sets the maximum number of in-flight operations the shedder
// tracks. Load is measured as inflight/capacity. Default: [DefaultCapacity].
// Values <= 0 are ignored (the default is kept), and a final capacity below
// [minCapacity] is floored to 1.
func WithCapacity(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.capacity = n
		}
	}
}

// WithThreshold sets the load fraction at which shedding begins. Below the
// threshold every request is admitted; above it, lower-priority requests are
// shed first. Default: [DefaultThreshold]. Values outside (0, 1] are ignored.
func WithThreshold(t float64) Option {
	return func(c *config) {
		if t > thresholdFloor && t <= thresholdCeil {
			c.threshold = t
		}
	}
}

// WithOp sets the logical operation name attached to panic reports raised by
// the callback (e.g. "api.search", "db.query"). Default: [opExecute] for
// [Execute] and [opTryExecute] for [TryExecute]. An empty name is ignored.
func WithOp(op string) Option {
	return func(c *config) {
		if op != "" {
			c.op = op
		}
	}
}
