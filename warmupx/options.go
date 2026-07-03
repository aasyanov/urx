package warmupx

import "time"

const (
	// DefaultStrategy is the ramp-up curve applied when [WithStrategy] is not
	// supplied.
	DefaultStrategy = Linear

	// DefaultMinCapacity is the starting capacity applied when
	// [WithMinCapacity] is not supplied.
	DefaultMinCapacity = 0.1

	// DefaultMaxCapacity is the target capacity applied when [WithMaxCapacity]
	// is not supplied.
	DefaultMaxCapacity = 1.0

	// DefaultDuration is the total warmup duration applied when [WithDuration]
	// is not supplied.
	DefaultDuration = 1 * time.Minute

	// DefaultStepCount is the number of discrete jumps the [Step] strategy uses
	// when [WithStepCount] is not supplied.
	DefaultStepCount = 10

	// DefaultExpFactor is the steepness of the [Exponential] strategy applied
	// when [WithExpFactor] is not supplied. Higher values ramp faster initially.
	DefaultExpFactor = 3.0

	// minCapacity and maxCapacity bound every capacity value.
	minCapacity = 0.0
	maxCapacity = 1.0

	// minInterval and maxInterval clamp the auto-derived update interval.
	minInterval = 10 * time.Millisecond
	maxInterval = 1 * time.Second

	// intervalDivisor derives the default update interval from the duration:
	// interval = duration / intervalDivisor, clamped to [minInterval, maxInterval].
	intervalDivisor = 100

	// capacityEpsilon is the minimum capacity delta that triggers the
	// [WithOnCapacityChange] callback, avoiding a flood of near-identical events.
	capacityEpsilon = 0.01
)

// Option configures a [Warmer] created with [New].
type Option func(*config)

// config holds resolved warmer parameters.
type config struct {
	strategy    Strategy
	minCap      float64
	maxCap      float64
	duration    time.Duration
	interval    time.Duration
	stepCount   int
	expFactor   float64
	onCapChange func(oldCap, newCap float64)
	onComplete  func()
}

// newConfig resolves the effective configuration: defaults first, then options
// in order, then invariants (max >= min, interval clamp) applied last.
func newConfig(opts []Option) config {
	cfg := config{
		strategy:  DefaultStrategy,
		minCap:    DefaultMinCapacity,
		maxCap:    DefaultMaxCapacity,
		duration:  DefaultDuration,
		stepCount: DefaultStepCount,
		expFactor: DefaultExpFactor,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.maxCap < cfg.minCap {
		cfg.maxCap = cfg.minCap
	}
	if cfg.interval <= 0 {
		cfg.interval = cfg.duration / intervalDivisor
	}
	if cfg.interval < minInterval {
		cfg.interval = minInterval
	}
	if cfg.interval > maxInterval {
		cfg.interval = maxInterval
	}
	return cfg
}

// WithStrategy sets the ramp-up strategy.
// Default: [DefaultStrategy] ([Linear]).
func WithStrategy(s Strategy) Option {
	return func(c *config) { c.strategy = s }
}

// WithMinCapacity sets the starting capacity, clamped to [0, 1].
// Default: [DefaultMinCapacity]. Values outside [0, 1] are ignored.
func WithMinCapacity(v float64) Option {
	return func(c *config) {
		if v >= minCapacity && v <= maxCapacity {
			c.minCap = v
		}
	}
}

// WithMaxCapacity sets the target capacity, clamped to (0, 1].
// Default: [DefaultMaxCapacity]. Values outside (0, 1] are ignored.
func WithMaxCapacity(v float64) Option {
	return func(c *config) {
		if v > minCapacity && v <= maxCapacity {
			c.maxCap = v
		}
	}
}

// WithDuration sets the total warmup duration.
// Default: [DefaultDuration]. Values <= 0 are ignored.
func WithDuration(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.duration = d
		}
	}
}

// WithInterval overrides the capacity-update interval. When unset the interval
// is derived from the duration (duration/100) and clamped to [10ms, 1s].
// Values <= 0 are ignored.
func WithInterval(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.interval = d
		}
	}
}

// WithStepCount sets the number of discrete jumps used by the [Step] strategy.
// Default: [DefaultStepCount]. Values <= 0 are ignored.
func WithStepCount(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.stepCount = n
		}
	}
}

// WithExpFactor sets the steepness of the [Exponential] strategy; higher values
// ramp faster initially.
// Default: [DefaultExpFactor]. Values <= 0 are ignored.
func WithExpFactor(f float64) Option {
	return func(c *config) {
		if f > 0 {
			c.expFactor = f
		}
	}
}

// WithOnCapacityChange registers a callback invoked asynchronously when
// capacity changes by more than 1%. Callbacks run in their own goroutine and
// may be delivered out of order. Default: nil.
func WithOnCapacityChange(fn func(oldCap, newCap float64)) Option {
	return func(c *config) { c.onCapChange = fn }
}

// WithOnComplete registers a callback invoked asynchronously once warmup
// reaches full capacity. The callback runs in its own goroutine. Default: nil.
func WithOnComplete(fn func()) Option {
	return func(c *config) { c.onComplete = fn }
}
