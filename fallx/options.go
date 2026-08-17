package fallx

import (
	"context"
	"time"
)

const (
	// DefaultCacheTTL is the lifetime of a cached entry applied when [WithCached]
	// is given a non-positive TTL. Used only under [StrategyCached].
	DefaultCacheTTL = 5 * time.Minute

	// DefaultMaxCacheSize is the maximum number of live cache entries applied
	// when [WithCached] is given a non-positive size. Used only under
	// [StrategyCached].
	DefaultMaxCacheSize = 1000

	// DefaultShards is the number of cache shards applied when [WithShards] is
	// not supplied. More shards reduce lock contention under concurrent caching.
	DefaultShards = 16

	// DefaultKey is the cache key used when neither [WithKeyFunc] nor
	// [ExecuteWithKey] supplies one, so all calls share a single cached slot.
	DefaultKey = "default"

	// opExecute labels panics recovered while running the primary callback and
	// is the default operation name when [WithOp] is not set.
	opExecute = "fallx.Execute"

	// opFallback labels panics recovered while running the [WithFunc] fallback.
	opFallback = "fallx.Fallback"

	// minShards is the floor on shard count; a non-positive request degrades to
	// a single shard rather than a zero-length shard slice.
	minShards = 1
)

// Option configures a [Fallback] created by [New]. Exactly one of [WithStatic],
// [WithFunc], or [WithCached] selects the strategy; the last one applied wins.
type Option[T any] func(*config[T])

// config holds resolved fallback parameters.
type config[T any] struct {
	strategy    Strategy
	staticValue T
	fallbackFn  FallbackFunc[T]
	keyFn       func(ctx context.Context) string

	cacheTTL        time.Duration
	maxCacheSize    int
	shardCount      int
	cleanupInterval time.Duration

	onFallback func(err error, strategy Strategy)
	fallbackIf func(error) bool
	clone      func(T) T
	op         string
}

// newConfig resolves the effective configuration: defaults first, then options
// in order, with shard and cache bounds applied last so an invalid option can
// never produce an unusable fallback.
func newConfig[T any](opts []Option[T]) config[T] {
	cfg := config[T]{
		strategy:     StrategyStatic,
		cacheTTL:     DefaultCacheTTL,
		maxCacheSize: DefaultMaxCacheSize,
		shardCount:   DefaultShards,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.shardCount < minShards {
		cfg.shardCount = minShards
	}
	// A shard slice longer than the capacity wastes memory and starves each
	// shard of entries; bound it to a quarter of the cache so eviction stays
	// meaningful per shard.
	if cfg.maxCacheSize > 0 && cfg.shardCount > cfg.maxCacheSize {
		cfg.shardCount = max(minShards, cfg.maxCacheSize/shardCapDivisor)
	}
	return cfg
}

// shardCapDivisor bounds shard count to maxCacheSize/shardCapDivisor so each
// shard holds several entries rather than at most one.
const shardCapDivisor = 4

// opOrDefault returns the configured primary operation name, or [opExecute]
// when none was set.
func (c config[T]) opOrDefault() string {
	if c.op != "" {
		return c.op
	}
	return opExecute
}

// WithStatic selects [StrategyStatic]: on primary failure the fallback returns
// value with a nil error. The fallback can never fail. This is the default
// strategy when no strategy option is supplied (with the zero value of T).
func WithStatic[T any](value T) Option[T] {
	return func(c *config[T]) {
		c.strategy = StrategyStatic
		c.staticValue = value
	}
}

// WithFunc selects [StrategyFunc]: on primary failure fn is invoked with a
// [FallController] whose [FallController.OnFallback] is true and whose
// [FallController.Error] carries the primary failure. A nil fn is ignored, so
// the strategy is unchanged. The function runs under panic recovery.
func WithFunc[T any](fn FallbackFunc[T]) Option[T] {
	return func(c *config[T]) {
		if fn != nil {
			c.strategy = StrategyFunc
			c.fallbackFn = fn
		}
	}
}

// WithCached selects [StrategyCached]: successful primary results are cached per
// key and replayed on later failures. ttl is the entry lifetime and maxSize the
// maximum number of live entries; non-positive values fall back to
// [DefaultCacheTTL] and [DefaultMaxCacheSize]. Expired entries are removed
// lazily on lookup and on capacity eviction; there is no background sweeper
// unless [WithCleanupInterval] is set, in which case [Fallback.Close] is
// required to stop it.
func WithCached[T any](ttl time.Duration, maxSize int) Option[T] {
	return func(c *config[T]) {
		c.strategy = StrategyCached
		if ttl > 0 {
			c.cacheTTL = ttl
		}
		if maxSize > 0 {
			c.maxCacheSize = maxSize
		}
	}
}

// WithKeyFunc sets a function that derives a cache key from the call context.
// Used only under [StrategyCached]; if unset, all calls share [DefaultKey].
// [ExecuteWithKey] overrides this per call. A nil fn is ignored.
func WithKeyFunc[T any](fn func(ctx context.Context) string) Option[T] {
	return func(c *config[T]) {
		if fn != nil {
			c.keyFn = fn
		}
	}
}

// WithShards sets the number of cache shards used to spread lock contention
// under concurrent caching. Default: [DefaultShards]. Values < 1 are ignored;
// the final count is bounded so it never exceeds the cache capacity. Used only
// under [StrategyCached].
func WithShards[T any](n int) Option[T] {
	return func(c *config[T]) {
		if n >= minShards {
			c.shardCount = n
		}
	}
}

// WithOnFallback registers a callback invoked each time the fallback path is
// taken, before the strategy runs. It receives the primary error and the active
// strategy. Use it for metrics or logging; it runs synchronously on the
// driving goroutine and must not block. A panic in the hook is recovered and
// discarded. A nil callback is ignored.
func WithOnFallback[T any](fn func(err error, strategy Strategy)) Option[T] {
	return func(c *config[T]) {
		if fn != nil {
			c.onFallback = fn
		}
	}
}

// WithFallbackIf sets a predicate that decides whether a primary error should
// take the fallback path. When fn is nil the option is ignored and every
// primary error — including [context.Canceled] — triggers fallback (the
// default). When fn is set and returns false, [Execute] returns the primary
// error and does not increment FallbackUsed.
func WithFallbackIf[T any](fn func(error) bool) Option[T] {
	return func(c *config[T]) {
		if fn != nil {
			c.fallbackIf = fn
		}
	}
}

// WithClone registers a function applied to values on cache store and on
// replay so callers do not share a mutable T with the cache. When fn is nil
// the option is ignored; without a clone, pointer (and other mutable) values
// are stored and replayed by alias.
func WithClone[T any](fn func(T) T) Option[T] {
	return func(c *config[T]) {
		if fn != nil {
			c.clone = fn
		}
	}
}

// WithCleanupInterval starts a background goroutine that sweeps expired cache
// entries at the given interval under [StrategyCached]. Values <= 0 disable
// the sweeper (the default): expiry is still enforced lazily on lookup and
// capacity eviction still runs on insert. When the interval is positive,
// [Fallback.Close] is required to stop the loop.
func WithCleanupInterval[T any](d time.Duration) Option[T] {
	return func(c *config[T]) {
		if d > 0 {
			c.cleanupInterval = d
		}
	}
}

// WithOp sets the logical operation name attached to panic reports raised by
// the callbacks (e.g. "api.fetch"). Default: "fallx.Execute" for the primary
// and "fallx.Fallback" for the [WithFunc] callback. An empty name is ignored.
func WithOp[T any](op string) Option[T] {
	return func(c *config[T]) {
		if op != "" {
			c.op = op
		}
	}
}
