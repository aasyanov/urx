package ratex

import "time"

const (
	// DefaultRate is the sustained rate in requests per second applied when
	// [WithRate] is not supplied.
	DefaultRate = 10.0

	// DefaultBurst is the bucket capacity applied when [WithBurst] is not
	// supplied. It bounds the largest momentary spike above the sustained
	// rate.
	DefaultBurst = 20

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
	now   func() time.Time
}

// newConfig resolves the effective configuration: defaults first, then options
// in order, with the burst floor applied last so the bucket is always usable.
// Positive fractional rates are preserved; only a non-positive rate falls back
// to [DefaultRate]. A nil Option in opts is skipped.
func newConfig(opts []Option) config {
	cfg := config{
		rate:  DefaultRate,
		burst: DefaultBurst,
		now:   time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.rate <= 0 {
		cfg.rate = DefaultRate
	}
	if cfg.burst < minBurst {
		cfg.burst = minBurst
	}
	return cfg
}

// WithRate sets the sustained rate in requests per second — the long-run
// average number of tokens added to the bucket each second.
// Default: [DefaultRate]. Values <= 0 are ignored. Positive fractional rates
// (e.g. 0.2 for one request every 5 seconds) are preserved as-is.
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
// Default: [DefaultBurst]. Values below the [minBurst] floor are raised to
// [minBurst] when [New] resolves the final configuration.
func WithBurst(n int) Option {
	return func(c *config) {
		c.burst = n
	}
}

// withClock replaces the time source used for token accrual. A nil now is
// ignored so the limiter keeps the real clock. Unexported: production callers
// must not inject a clock; tests use it to drive refill without sleeping.
func withClock(now func() time.Time) Option {
	return func(c *config) {
		if now != nil {
			c.now = now
		}
	}
}
