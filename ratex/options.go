package ratex

const (
	// DefaultRate is the sustained rate in requests per second applied when
	// [WithRate] is not supplied.
	DefaultRate = 10.0

	// DefaultBurst is the bucket capacity applied when [WithBurst] is not
	// supplied. It bounds the largest momentary spike above the sustained
	// rate.
	DefaultBurst = 20

	// minRate is the floor [New] enforces: a non-positive rate degrades to one
	// token per second so the bucket always refills.
	minRate = 1.0

	// minBurst is the floor [New] enforces: a non-positive burst degrades to a
	// single-token bucket.
	minBurst = 1
)

// Option configures a [Limiter] created by [New].
type Option func(*config)

// config holds resolved limiter parameters.
type config struct {
	rate  float64
	burst int
}

// newConfig resolves the effective configuration: defaults first, then options
// in order, with the rate and burst floors applied last so the bucket is
// always usable.
func newConfig(opts []Option) config {
	cfg := config{
		rate:  DefaultRate,
		burst: DefaultBurst,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.rate < minRate {
		cfg.rate = minRate
	}
	if cfg.burst < minBurst {
		cfg.burst = minBurst
	}
	return cfg
}

// WithRate sets the sustained rate in requests per second — the long-run
// average number of tokens added to the bucket each second.
// Default: [DefaultRate]. Values <= 0 are ignored.
func WithRate(r float64) Option {
	return func(c *config) {
		if r > 0 {
			c.rate = r
		}
	}
}

// WithBurst sets the bucket capacity: the maximum number of tokens that can
// accumulate, and therefore the largest momentary burst allowed above the
// sustained rate.
// Default: [DefaultBurst]. Values <= 0 are ignored.
func WithBurst(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.burst = n
		}
	}
}
