package quotax

import "time"

const (
	// DefaultRate is the sustained per-key rate in requests per second applied
	// when [WithRate] is not supplied.
	DefaultRate = 10.0

	// DefaultBurst is the per-key bucket capacity applied when [WithBurst] is
	// not supplied. It bounds the largest momentary spike a single key may make
	// above its sustained rate.
	DefaultBurst = 20

	// DefaultShards is the number of internal shards applied when [WithShards]
	// is not supplied. Keys are distributed across shards to spread lock
	// contention; more shards reduce contention at the cost of memory.
	DefaultShards = 64

	// DefaultEvictionTTL is how long an inactive key is retained before the
	// background sweeper evicts it, applied when [WithEvictionTTL] is not set.
	DefaultEvictionTTL = 15 * time.Minute

	// DefaultEvictionInterval is how often the background sweeper runs, applied
	// when [WithEvictionInterval] is not set.
	DefaultEvictionInterval = time.Minute

	// unlimitedKeys is the sentinel for [WithMaxKeys]: zero means the number of
	// tracked keys is unbounded.
	unlimitedKeys = 0
)

// Option configures a [Quota] created by [New].
type Option func(*config)

// config holds resolved per-key limiter parameters.
type config struct {
	rate  float64
	burst int

	shards           int
	maxKeys          int64
	evictionTTL      time.Duration
	evictionInterval time.Duration

	onMaxKeys func(key string)
}

// newConfig resolves the effective configuration: defaults first, then each
// option applied in order. Every WithXxx ignores out-of-range values, so the
// resolved config is always usable without a post-pass.
func newConfig(opts []Option) config {
	cfg := config{
		rate:             DefaultRate,
		burst:            DefaultBurst,
		shards:           DefaultShards,
		maxKeys:          unlimitedKeys,
		evictionTTL:      DefaultEvictionTTL,
		evictionInterval: DefaultEvictionInterval,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// WithRate sets the sustained rate in requests per second applied to each key's
// token bucket. Default: [DefaultRate]. Values <= 0 are ignored.
func WithRate(r float64) Option {
	return func(c *config) {
		if r > 0 {
			c.rate = r
		}
	}
}

// WithBurst sets the token-bucket capacity (burst size) for each key: the
// largest momentary spike a single key may make above its sustained rate.
// Default: [DefaultBurst]. Values <= 0 are ignored.
func WithBurst(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.burst = n
		}
	}
}

// WithShards sets the number of internal shards used to spread lock contention
// across keys. Default: [DefaultShards]. Values <= 0 are ignored.
func WithShards(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.shards = n
		}
	}
}

// WithMaxKeys sets the maximum number of tracked keys. When the cap is reached,
// admission for a new key is denied and the [WithOnMaxKeys] callback (if any)
// is invoked. Default: 0 ([unlimitedKeys]) meaning no cap. Negative values are
// ignored.
func WithMaxKeys(n int64) Option {
	return func(c *config) {
		if n >= 0 {
			c.maxKeys = n
		}
	}
}

// WithEvictionTTL sets how long an inactive key is retained before the
// background sweeper evicts its bucket. Default: [DefaultEvictionTTL]. Values
// <= 0 are ignored.
func WithEvictionTTL(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.evictionTTL = d
		}
	}
}

// WithEvictionInterval sets how often the background sweeper scans for and
// evicts inactive keys. Default: [DefaultEvictionInterval]. Values <= 0 are
// ignored.
func WithEvictionInterval(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.evictionInterval = d
		}
	}
}

// WithOnMaxKeys registers a callback invoked when admission for a new key is
// denied because the [WithMaxKeys] cap is reached. The callback receives the
// rejected key and runs on the caller's goroutine, so it must not block.
// Default: none.
func WithOnMaxKeys(fn func(key string)) Option {
	return func(c *config) {
		c.onMaxKeys = fn
	}
}
