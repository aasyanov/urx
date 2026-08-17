package adaptx

import "time"

const (
	// DefaultInitialLimit is the concurrency limit a [Limiter] starts at before
	// any adaptation, applied when [WithInitialLimit] is not supplied.
	DefaultInitialLimit = 10

	// DefaultMinLimit is the floor the adaptive limit is never driven below,
	// applied when [WithMinLimit] is not supplied. A floor of 1 keeps the
	// limiter able to make forward progress even under sustained failure.
	DefaultMinLimit = 1

	// DefaultMaxLimit is the ceiling the adaptive limit is never driven above,
	// applied when [WithMaxLimit] is not supplied.
	DefaultMaxLimit = 1000

	// DefaultSmoothing is the EMA weight applied to each window's mean RTT when
	// updating the smoothed average, applied when [WithSmoothing] is not
	// supplied. Higher reacts faster but is noisier.
	DefaultSmoothing = 0.2

	// DefaultIncreaseRate is the additive credit [AIMD] accumulates on each
	// successful, high-utilization window, applied when [WithIncreaseRate] is
	// not supplied. Values below 1 grow the limit every few windows (0.5 → +1
	// every two windows).
	DefaultIncreaseRate = 1.0

	// DefaultDecreaseRatio is the multiplicative factor the limit is scaled by
	// on a backoff window, applied when [WithDecreaseRatio] is not supplied.
	// 0.5 halves the limit, matching TCP multiplicative decrease.
	DefaultDecreaseRatio = 0.5

	// DefaultUtilization is the in-flight fraction of the live limit that [AIMD]
	// requires before it will add credit, applied when [WithUtilization] is not
	// supplied. A window whose peak in-flight is below ceil(limit·utilization)
	// holds the limit even if every sample succeeded.
	DefaultUtilization = 0.9

	// DefaultTargetLatency is the latency [Vegas] treats as the operating point,
	// applied when [WithTargetLatency] is not supplied.
	DefaultTargetLatency = 100 * time.Millisecond

	// DefaultTolerance is the fractional latency deviation [Vegas] and
	// [Gradient] tolerate before reacting, applied when [WithTolerance] is not
	// supplied.
	DefaultTolerance = 0.1

	// DefaultSampleWindow is the interval over which samples are aggregated
	// into one control-law adjustment and over which [Stats] computes latency
	// percentiles, applied when [WithSampleWindow] is not supplied.
	DefaultSampleWindow = 1 * time.Second

	// DefaultWarmupSamples is the number of recorded samples collected before
	// adaptation begins, applied when [WithWarmupSamples] is not supplied. It
	// stops the controller from reacting to the first few unrepresentative
	// calls.
	DefaultWarmupSamples = 10

	// DefaultMinLatencyDecay is the fraction by which RTT_min drifts toward the
	// running average on each completed window, applied when
	// [WithMinLatencyDecay] is not supplied. It prevents [Vegas] from sticking
	// to an anomalously low minimum forever. 0 disables decay.
	DefaultMinLatencyDecay = 0.001

	// DefaultJitter is the fraction of each limit increase that may be randomly
	// withheld to desynchronize many limiters, applied when [WithJitter] is not
	// supplied. 0 disables jitter.
	DefaultJitter = 0.1
)

const (
	// opExecute labels panics recovered while running an [Execute] callback and
	// is the default operation name when [WithOp] is not supplied.
	opExecute = "adaptx.Execute"

	// opTryExecute labels panics recovered while running a [TryExecute] callback.
	opTryExecute = "adaptx.TryExecute"

	// minLimitFloor is the absolute floor [New] enforces on the resolved
	// minimum limit: a non-positive minimum would let the limiter stall.
	minLimitFloor = 1

	// samplesPerWindowSecond scales the ring-buffer capacity to the configured
	// window, sized for a high but bounded sample rate.
	samplesPerWindowSecond = 10000

	// minSamples and maxSamples bound the ring-buffer capacity so a tiny window
	// still retains useful history and a huge window cannot allocate without
	// limit.
	minSamples = 100
	maxSamples = 10000

	// jitterCoinFlip is the probability that a jittered increase is withheld
	// rather than applied, keeping the expected limit unbiased.
	jitterCoinFlip = 0.5
)

// Option configures a [Limiter] created by [New].
type Option func(*config)

// config holds resolved limiter parameters.
type config struct {
	algorithm     Algorithm
	initialLimit  int
	minLimit      int
	maxLimit      int
	warmupSamples int
	smoothing     float64
	increaseRate  float64
	decreaseRatio float64
	tolerance     float64
	minLatDecay   float64
	jitter        float64
	utilization   float64
	targetLatency time.Duration
	sampleWindow  time.Duration
	op            string
	onLimitChange func(oldLimit, newLimit int)
	clock         func() time.Time
}

// newConfig resolves the effective configuration: defaults first, then options
// in order, with cross-field invariants applied last so an invalid combination
// can never produce an unusable limiter. Nil options are skipped.
func newConfig(opts []Option) config {
	cfg := config{
		algorithm:     AIMD,
		initialLimit:  DefaultInitialLimit,
		minLimit:      DefaultMinLimit,
		maxLimit:      DefaultMaxLimit,
		smoothing:     DefaultSmoothing,
		increaseRate:  DefaultIncreaseRate,
		decreaseRatio: DefaultDecreaseRatio,
		utilization:   DefaultUtilization,
		targetLatency: DefaultTargetLatency,
		tolerance:     DefaultTolerance,
		sampleWindow:  DefaultSampleWindow,
		warmupSamples: DefaultWarmupSamples,
		minLatDecay:   DefaultMinLatencyDecay,
		jitter:        DefaultJitter,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	if cfg.minLimit < minLimitFloor {
		cfg.minLimit = minLimitFloor
	}
	if cfg.maxLimit < cfg.minLimit {
		cfg.maxLimit = cfg.minLimit
	}
	if cfg.initialLimit < cfg.minLimit {
		cfg.initialLimit = cfg.minLimit
	}
	if cfg.initialLimit > cfg.maxLimit {
		cfg.initialLimit = cfg.maxLimit
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

// ringCapacity derives the ring-buffer size from the sample window, clamped to
// [minSamples, maxSamples].
func (c config) ringCapacity() int {
	n := int(samplesPerWindowSecond * c.sampleWindow.Seconds())
	if n < minSamples {
		return minSamples
	}
	if n > maxSamples {
		return maxSamples
	}
	return n
}

// WithAlgorithm selects the adaptation strategy. Default: [AIMD]. An unknown
// value falls back to [AIMD] at adaptation time.
func WithAlgorithm(a Algorithm) Option {
	return func(c *config) { c.algorithm = a }
}

// WithInitialLimit sets the concurrency limit the limiter starts at before any
// adaptation. Default: [DefaultInitialLimit]. Values <= 0 are ignored; the
// final value is clamped into [min, max].
func WithInitialLimit(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.initialLimit = n
		}
	}
}

// WithMinLimit sets the floor the adaptive limit is never driven below.
// Default: [DefaultMinLimit]. Values <= 0 are ignored; the final value is
// floored to 1.
func WithMinLimit(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.minLimit = n
		}
	}
}

// WithMaxLimit sets the ceiling the adaptive limit is never driven above and
// the hard cap on concurrently admitted operations. Default: [DefaultMaxLimit].
// Values <= 0 are ignored; a value below the minimum is raised to it.
func WithMaxLimit(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.maxLimit = n
		}
	}
}

// WithSmoothing sets the EMA weight applied to each window's mean RTT.
// Default: [DefaultSmoothing]. Values outside (0, 1] are ignored.
func WithSmoothing(f float64) Option {
	return func(c *config) {
		if f > 0 && f <= 1 {
			c.smoothing = f
		}
	}
}

// WithIncreaseRate sets the additive credit [AIMD] accumulates on each
// successful window that meets the utilization gate. Default:
// [DefaultIncreaseRate]. Values <= 0 are ignored. Fractional rates keep a
// remainder so 0.5 grows the limit by 1 every two windows.
func WithIncreaseRate(r float64) Option {
	return func(c *config) {
		if r > 0 {
			c.increaseRate = r
		}
	}
}

// WithDecreaseRatio sets the multiplicative backoff factor applied to the limit
// on a failure or overload window. Default: [DefaultDecreaseRatio]. Values
// outside (0, 1) are ignored.
func WithDecreaseRatio(r float64) Option {
	return func(c *config) {
		if r > 0 && r < 1 {
			c.decreaseRatio = r
		}
	}
}

// WithUtilization sets the in-flight fraction of the live limit that [AIMD]
// requires before it will add increase credit. Default: [DefaultUtilization].
// Values outside (0, 1] are ignored. A window whose peak in-flight is below
// ceil(limit·utilization) holds the limit.
func WithUtilization(f float64) Option {
	return func(c *config) {
		if f > 0 && f <= 1 {
			c.utilization = f
		}
	}
}

// WithTargetLatency sets the round-trip latency [Vegas] treats as the operating
// point when scaling the queue target band. Default: [DefaultTargetLatency].
// Values <= 0 are ignored. When target latency is at or below the observed
// minimum RTT the band falls back to limit·tolerance.
func WithTargetLatency(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.targetLatency = d
		}
	}
}

// WithTolerance sets the fractional latency deviation [Vegas] and [Gradient]
// tolerate before reacting. Default: [DefaultTolerance]. Values outside (0, 1]
// are ignored.
func WithTolerance(f float64) Option {
	return func(c *config) {
		if f > 0 && f <= 1 {
			c.tolerance = f
		}
	}
}

// WithSampleWindow sets the interval over which completed operations are
// aggregated into one control-law adjustment, and over which [Stats] computes
// latency percentiles. Default: [DefaultSampleWindow]. Values <= 0 are ignored.
func WithSampleWindow(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.sampleWindow = d
		}
	}
}

// WithWarmupSamples sets the number of recorded samples collected before
// adaptation begins. Default: [DefaultWarmupSamples]. 0 disables warmup so
// adaptation starts on the first completed window. Negative values are ignored.
func WithWarmupSamples(n int) Option {
	return func(c *config) {
		if n >= 0 {
			c.warmupSamples = n
		}
	}
}

// WithMinLatencyDecay sets the fraction by which the observed minimum latency
// drifts toward the running average on each completed window, preventing
// [Vegas] from sticking to an anomalously low minimum. Default:
// [DefaultMinLatencyDecay]. 0 disables decay. Values outside [0, 1] are
// ignored.
func WithMinLatencyDecay(f float64) Option {
	return func(c *config) {
		if f >= 0 && f <= 1 {
			c.minLatDecay = f
		}
	}
}

// WithJitter sets the fraction of each limit increase that may be randomly
// withheld, desynchronizing many limiters so they do not all step up in
// lockstep (thundering herd). Default: [DefaultJitter]. 0 disables jitter.
// Values outside [0, 1] are ignored.
func WithJitter(f float64) Option {
	return func(c *config) {
		if f >= 0 && f <= 1 {
			c.jitter = f
		}
	}
}

// WithOp sets the logical operation name attached to panic reports raised by
// the callback (e.g. "db.query", "api.search"). Default: [opExecute] for
// [Execute] and [opTryExecute] for [TryExecute]. Empty values are ignored.
func WithOp(op string) Option {
	return func(c *config) {
		if op != "" {
			c.op = op
		}
	}
}

// WithOnLimitChange registers a callback invoked synchronously whenever the
// adaptive limit changes, receiving the old and new values. Default: none.
// The callback must not block or panic; it runs on the goroutine that closed
// the sample window, and a panic is recovered and discarded.
func WithOnLimitChange(fn func(oldLimit, newLimit int)) Option {
	return func(c *config) { c.onLimitChange = fn }
}

// withClock injects a clock used by windowing, stats cutoffs, and construction.
// Nil is ignored. Production uses time.Now. Unexported: tests only.
func withClock(now func() time.Time) Option {
	return func(c *config) {
		if now != nil {
			c.clock = now
		}
	}
}
