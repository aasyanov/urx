package shedx

const (
	// DefaultCapacity is the maximum number of concurrent in-flight operations
	// applied when [WithCapacity] is not supplied.
	DefaultCapacity = 1000

	// DefaultThreshold is the load fraction at which shedding begins, applied
	// when [WithThreshold] is not supplied. Below this load every request is
	// admitted; above it, requests are shed progressively by priority.
	DefaultThreshold = 0.8

	// DefaultCutoffLow is the overload fraction below which [PriorityLow]
	// requests remain admitted once load is above [DefaultThreshold].
	DefaultCutoffLow = 0.25

	// DefaultCutoffNormal is the overload fraction below which [PriorityNormal]
	// requests remain admitted once load is above [DefaultThreshold].
	DefaultCutoffNormal = 0.60

	// DefaultCutoffHigh is the overload fraction below which [PriorityHigh]
	// requests remain admitted once load is above [DefaultThreshold].
	DefaultCutoffHigh = 0.90

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
	capacity     int
	threshold    float64
	hysteresis   float64
	cutoffLow    float64
	cutoffNormal float64
	cutoffHigh   float64
	op           string
}

// newConfig resolves the effective configuration: defaults first, then options
// in order, with the capacity floor, threshold bounds, and cutoff ordering
// applied last so an invalid option can never produce an unusable shedder.
func newConfig(opts []Option) config {
	cfg := config{
		capacity:     DefaultCapacity,
		threshold:    DefaultThreshold,
		cutoffLow:    DefaultCutoffLow,
		cutoffNormal: DefaultCutoffNormal,
		cutoffHigh:   DefaultCutoffHigh,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.capacity < minCapacity {
		cfg.capacity = minCapacity
	}
	if cfg.threshold <= thresholdFloor || cfg.threshold > thresholdCeil {
		cfg.threshold = DefaultThreshold
	}
	if cfg.hysteresis <= 0 || cfg.hysteresis >= cfg.threshold {
		cfg.hysteresis = 0
	}
	cfg.normalizeCutoffs()
	return cfg
}

// normalizeCutoffs resets cutoffs to the package defaults when any value is
// outside (0, 1] or the low ≤ normal ≤ high ordering is violated.
func (c *config) normalizeCutoffs() {
	if !validCutoff(c.cutoffLow) || !validCutoff(c.cutoffNormal) || !validCutoff(c.cutoffHigh) {
		c.cutoffLow = DefaultCutoffLow
		c.cutoffNormal = DefaultCutoffNormal
		c.cutoffHigh = DefaultCutoffHigh
		return
	}
	if c.cutoffLow > c.cutoffNormal || c.cutoffNormal > c.cutoffHigh {
		c.cutoffLow = DefaultCutoffLow
		c.cutoffNormal = DefaultCutoffNormal
		c.cutoffHigh = DefaultCutoffHigh
	}
}

func validCutoff(v float64) bool {
	return v > 0 && v <= 1
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

// WithCutoffs sets the overload fractions at which each non-critical priority
// is shed once load is above the threshold. A priority is admitted while
// overload = (load − threshold) / (1 − threshold) stays strictly below its
// cutoff. Defaults: [DefaultCutoffLow], [DefaultCutoffNormal],
// [DefaultCutoffHigh]. Values outside (0, 1] or an ordering where
// low > normal > high is not satisfied reset all three cutoffs to the defaults.
func WithCutoffs(low, normal, high float64) Option {
	return func(c *config) {
		c.cutoffLow = low
		c.cutoffNormal = normal
		c.cutoffHigh = high
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

// WithHysteresis sets how far load must fall below the threshold before
// shedding clears. Default 0 preserves today's trip-at-threshold behaviour.
// Values <= 0 or >= the resolved threshold are ignored.
func WithHysteresis(delta float64) Option {
	return func(c *config) {
		if delta > 0 {
			c.hysteresis = delta
		}
	}
}
