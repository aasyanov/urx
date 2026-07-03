package lrux

import "time"

const (
	// defaultCapacity is the capacity applied when none is configured. Zero
	// means the cache is unbounded.
	defaultCapacity = 0

	// defaultTTL is the time-to-live applied when none is configured. Zero
	// means entries never expire.
	defaultTTL time.Duration = 0

	// defaultCleanupInterval is the background sweep interval applied when none
	// is configured. Zero disables the background sweeper (expired entries are
	// removed lazily on access).
	defaultCleanupInterval time.Duration = 0

	// defaultShardCount is the shard count used by [NewSharded] when none is
	// configured. It is rounded up to the nearest power of two.
	defaultShardCount = 16
)

// Option configures a [Cache] created with [New].
type Option[K comparable, V any] func(*config[K, V])

// config holds resolved [Cache] settings.
type config[K comparable, V any] struct {
	capacity        int
	ttl             time.Duration
	onEvict         OnEvictFunc[K, V]
	cleanupInterval time.Duration
}

func newConfig[K comparable, V any](opts []Option[K, V]) config[K, V] {
	cfg := config[K, V]{
		capacity:        defaultCapacity,
		ttl:             defaultTTL,
		cleanupInterval: defaultCleanupInterval,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// WithCapacity sets the maximum number of entries the cache retains.
// Default: 0 (unbounded). Negative values are clamped to 0.
func WithCapacity[K comparable, V any](n int) Option[K, V] {
	return func(c *config[K, V]) {
		if n < 0 {
			n = 0
		}
		c.capacity = n
	}
}

// WithTTL sets the default time-to-live applied to every entry.
// Default: 0 (no expiration). Negative values are clamped to 0.
// Per-entry overrides are available via [Cache.SetWithTTL].
func WithTTL[K comparable, V any](d time.Duration) Option[K, V] {
	return func(c *config[K, V]) {
		if d < 0 {
			d = 0
		}
		c.ttl = d
	}
}

// WithOnEvict registers a callback invoked after an entry is removed for any
// [EvictionReason]. The callback runs outside the cache lock and is recovered
// against panics. Default: nil (no callback).
func WithOnEvict[K comparable, V any](fn OnEvictFunc[K, V]) Option[K, V] {
	return func(c *config[K, V]) { c.onEvict = fn }
}

// WithCleanupInterval starts a background goroutine that removes expired
// entries at the given interval. The goroutine stops when [Cache.Close] is
// called. Default: 0 (disabled, lazy cleanup). Non-positive values disable it.
func WithCleanupInterval[K comparable, V any](d time.Duration) Option[K, V] {
	return func(c *config[K, V]) {
		if d > 0 {
			c.cleanupInterval = d
		}
	}
}

// ShardedOption configures a [ShardedCache] created with [NewSharded].
type ShardedOption[K comparable, V any] func(*shardedConfig[K, V])

// shardedConfig holds resolved [ShardedCache] settings.
type shardedConfig[K comparable, V any] struct {
	shardCount      int
	capacity        int
	ttl             time.Duration
	onEvict         OnEvictFunc[K, V]
	cleanupInterval time.Duration
}

func newShardedConfig[K comparable, V any](opts []ShardedOption[K, V]) shardedConfig[K, V] {
	cfg := shardedConfig[K, V]{
		shardCount:      defaultShardCount,
		capacity:        defaultCapacity,
		ttl:             defaultTTL,
		cleanupInterval: defaultCleanupInterval,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// WithShardCount sets the number of independent shards. The value is rounded
// up to the nearest power of two for fast shard selection.
// Default: 16. Non-positive values are ignored.
func WithShardCount[K comparable, V any](n int) ShardedOption[K, V] {
	return func(c *shardedConfig[K, V]) {
		if n > 0 {
			c.shardCount = n
		}
	}
}

// WithShardCapacity sets the per-shard maximum number of entries. Total cache
// capacity is shardCount * n. Default: 0 (unbounded). Negative values are
// clamped to 0.
func WithShardCapacity[K comparable, V any](n int) ShardedOption[K, V] {
	return func(c *shardedConfig[K, V]) {
		if n < 0 {
			n = 0
		}
		c.capacity = n
	}
}

// WithShardTTL sets the default time-to-live for entries in every shard.
// Default: 0 (no expiration). Negative values are clamped to 0.
func WithShardTTL[K comparable, V any](d time.Duration) ShardedOption[K, V] {
	return func(c *shardedConfig[K, V]) {
		if d < 0 {
			d = 0
		}
		c.ttl = d
	}
}

// WithShardOnEvict registers an eviction callback shared by every shard.
// Default: nil (no callback).
func WithShardOnEvict[K comparable, V any](fn OnEvictFunc[K, V]) ShardedOption[K, V] {
	return func(c *shardedConfig[K, V]) { c.onEvict = fn }
}

// WithShardCleanupInterval starts a background expired-entry sweeper on every
// shard at the given interval. Default: 0 (disabled). Non-positive values
// disable it.
func WithShardCleanupInterval[K comparable, V any](d time.Duration) ShardedOption[K, V] {
	return func(c *shardedConfig[K, V]) {
		if d > 0 {
			c.cleanupInterval = d
		}
	}
}

// ComputeOption configures a single [Cache.GetOrCompute] call.
type ComputeOption func(*computeConfig)

// computeConfig holds resolved per-call compute settings.
type computeConfig struct {
	ttl          time.Duration
	singleflight bool
}

func newComputeConfig(opts []ComputeOption) computeConfig {
	var cfg computeConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// WithComputeTTL sets a per-entry TTL for the value produced by the compute
// function. Default: 0 (falls back to the cache's global TTL).
func WithComputeTTL(d time.Duration) ComputeOption {
	return func(c *computeConfig) {
		if d < 0 {
			d = 0
		}
		c.ttl = d
	}
}

// WithSingleflight deduplicates concurrent compute calls for the same key:
// only one goroutine runs the compute function while the others wait for and
// share its result. Default: disabled.
func WithSingleflight() ComputeOption {
	return func(c *computeConfig) { c.singleflight = true }
}
