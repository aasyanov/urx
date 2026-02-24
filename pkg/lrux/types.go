package lrux

import (
	"sync"
	"time"
)

// --- Eviction reasons ---

// EvictionReason indicates why an entry was removed from the cache.
type EvictionReason uint8

const (
	// Capacity means the entry was evicted to make room for new entries.
	Capacity EvictionReason = iota
	// Expired means the entry's TTL elapsed.
	Expired
	// Deleted means the entry was explicitly removed via Delete or DeleteMulti.
	Deleted
	// Cleared means the cache was cleared via Clear or Close.
	Cleared
	// Replaced means the value was overwritten by Set or SetWithTTL.
	Replaced
)

// String labels for [EvictionReason] values.
const (
	labelCapacity = "capacity"
	labelExpired  = "expired"
	labelDeleted  = "deleted"
	labelCleared  = "cleared"
	labelReplaced = "replaced"
	labelUnknown  = "unknown"
)

// String returns a human-readable label.
func (r EvictionReason) String() string {
	switch r {
	case Capacity:
		return labelCapacity
	case Expired:
		return labelExpired
	case Deleted:
		return labelDeleted
	case Cleared:
		return labelCleared
	case Replaced:
		return labelReplaced
	default:
		return labelUnknown
	}
}

// --- Options ---

// Option configures [New] behavior.
type Option[K comparable, V any] func(*config[K, V])

// WithCapacity sets the maximum number of entries. Zero means unlimited.
// Values < 0 are ignored.
func WithCapacity[K comparable, V any](n int) Option[K, V] {
	return func(c *config[K, V]) {
		if n >= 0 {
			c.capacity = n
		}
	}
}

// WithTTL sets the default time-to-live for entries. Zero means no
// expiration. Values < 0 are ignored.
func WithTTL[K comparable, V any](d time.Duration) Option[K, V] {
	return func(c *config[K, V]) {
		if d >= 0 {
			c.ttl = d
		}
	}
}

// WithOnEvict sets a callback invoked after an entry is removed from the
// cache. The callback runs outside the lock, so it may safely access the
// cache. Nil disables the callback.
func WithOnEvict[K comparable, V any](fn func(key K, value V, reason EvictionReason)) Option[K, V] {
	return func(c *config[K, V]) { c.onEvict = fn }
}

// WithCleanupInterval enables a background goroutine that removes expired
// entries at the given interval. Zero disables automatic cleanup (expired
// entries are removed lazily on access). The goroutine is stopped when
// [LRU.Close] is called. Values <= 0 are ignored.
func WithCleanupInterval[K comparable, V any](d time.Duration) Option[K, V] {
	return func(c *config[K, V]) {
		if d > 0 {
			c.cleanupInterval = d
		}
	}
}

// config holds LRU cache parameters.
type config[K comparable, V any] struct {
	capacity        int
	ttl             time.Duration
	onEvict         func(key K, value V, reason EvictionReason)
	cleanupInterval time.Duration
}

// --- Sharded options ---

// ShardedOption configures [NewSharded] behavior.
type ShardedOption[K comparable, V any] func(*shardedConfig[K, V])

// WithShardCount sets the number of independent LRU shards. Rounded up to
// the nearest power of two. Default: 16. Values <= 0 are ignored.
func WithShardCount[K comparable, V any](n int) ShardedOption[K, V] {
	return func(c *shardedConfig[K, V]) {
		if n > 0 {
			c.shardCount = n
		}
	}
}

// WithShardCapacity sets the per-shard maximum number of entries.
// Zero means unlimited. Values < 0 are ignored.
func WithShardCapacity[K comparable, V any](n int) ShardedOption[K, V] {
	return func(c *shardedConfig[K, V]) {
		if n >= 0 {
			c.capacity = n
		}
	}
}

// WithShardTTL sets the default per-entry TTL for all shards.
// Zero means no expiration. Values < 0 are ignored.
func WithShardTTL[K comparable, V any](d time.Duration) ShardedOption[K, V] {
	return func(c *shardedConfig[K, V]) {
		if d >= 0 {
			c.ttl = d
		}
	}
}

// WithShardOnEvict sets a callback invoked after an entry is removed from
// any shard.
func WithShardOnEvict[K comparable, V any](fn func(key K, value V, reason EvictionReason)) ShardedOption[K, V] {
	return func(c *shardedConfig[K, V]) { c.onEvict = fn }
}

// WithShardCleanupInterval enables automatic expired-entry cleanup on
// every shard. Values <= 0 are ignored.
func WithShardCleanupInterval[K comparable, V any](d time.Duration) ShardedOption[K, V] {
	return func(c *shardedConfig[K, V]) {
		if d > 0 {
			c.cleanupInterval = d
		}
	}
}

// shardedConfig holds [NewSharded] parameters.
type shardedConfig[K comparable, V any] struct {
	shardCount      int
	capacity        int
	ttl             time.Duration
	onEvict         func(key K, value V, reason EvictionReason)
	cleanupInterval time.Duration
}

// --- Statistics ---

// Stats holds cache statistics. Counters are read atomically.
type Stats struct {
	Size      int     `json:"size"`      // current number of entries (includes expired but not yet cleaned)
	Capacity  int     `json:"capacity"`  // configured maximum (0 = unlimited)
	Hits      uint64  `json:"hits"`      // successful Get / GetOrCompute cache hits
	Misses    uint64  `json:"misses"`    // Get misses or GetOrCompute computes
	Evictions uint64  `json:"evictions"` // entries removed due to capacity or TTL
	HitRate   float64 `json:"hit_rate"`  // Hits / (Hits + Misses), 0 when no requests
}

// --- Compute options ---

// ComputeOption configures [LRU.GetOrCompute] behavior.
type ComputeOption func(*computeConfig)

// computeConfig holds [GetOrCompute] parameters.
type computeConfig struct {
	ttl          time.Duration
	singleflight bool
}

// WithComputeTTL sets a per-entry TTL for the computed value.
// Zero falls back to the cache's global TTL.
func WithComputeTTL(d time.Duration) ComputeOption {
	return func(c *computeConfig) { c.ttl = d }
}

// WithSingleflight deduplicates concurrent computes for the same key.
// Only one goroutine runs the compute function; others wait for its result.
func WithSingleflight() ComputeOption {
	return func(c *computeConfig) { c.singleflight = true }
}

// --- Intrusive linked-list node ---

// node is a doubly-linked list entry storing a cached key-value pair with timestamps.
type node[K comparable, V any] struct {
	key        K
	value      V
	prev, next *node[K, V]
	expiresAt  time.Time
	accessedAt time.Time
	createdAt  time.Time
}

// --- Eviction event (deferred callback) ---

// evictEvent records a pending eviction callback to be fired outside the lock.
type evictEvent[K comparable, V any] struct {
	key    K
	value  V
	reason EvictionReason
}

// --- Cleanup ticker ---

// cleanupTicker manages the background expired-entry sweeper.
type cleanupTicker struct {
	ticker   *time.Ticker
	stop     chan struct{}
	stopOnce sync.Once
}

// Stop halts the ticker and closes the stop channel, safe for concurrent calls.
func (ct *cleanupTicker) Stop() {
	ct.stopOnce.Do(func() {
		ct.ticker.Stop()
		close(ct.stop)
	})
}
