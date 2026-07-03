package lrux

import (
	"context"
	"sync"
	"time"
)

// parallelBatchThreshold is the minimum batch size at which [ShardedCache]
// dispatches per-shard work to goroutines. Below it, the goroutine overhead
// outweighs the parallelism.
const parallelBatchThreshold = 64

// ShardedCache distributes keys across several independent [Cache] shards to
// reduce lock contention under high concurrency. Each shard is a full cache
// with its own lock, capacity, TTL, and eviction callback; total capacity is
// shardCount * per-shard capacity. It is safe for concurrent use.
//
// Create with [NewSharded] and configure via [ShardedOption] functions. Call
// [ShardedCache.Close] when done to stop every shard's cleanup goroutine.
type ShardedCache[K comparable, V any] struct {
	shards    []*Cache[K, V]
	shardMask uint64
	hasher    func(K) uint64
}

// NewSharded creates a [ShardedCache] with the given options applied over the
// defaults: 16 shards, unbounded per-shard capacity, no TTL, lazy cleanup.
func NewSharded[K comparable, V any](opts ...ShardedOption[K, V]) *ShardedCache[K, V] {
	cfg := newShardedConfig(opts)
	count := nextPow2(cfg.shardCount)

	shards := make([]*Cache[K, V], count)
	for i := range shards {
		shards[i] = New[K, V](
			WithCapacity[K, V](cfg.capacity),
			WithTTL[K, V](cfg.ttl),
			WithOnEvict[K, V](cfg.onEvict),
			WithCleanupInterval[K, V](cfg.cleanupInterval),
		)
	}
	return &ShardedCache[K, V]{
		shards:    shards,
		shardMask: uint64(count - 1),
		hasher:    newHasher[K](),
	}
}

// shard returns the [Cache] responsible for key.
func (c *ShardedCache[K, V]) shard(key K) *Cache[K, V] {
	return c.shards[c.hasher(key)&c.shardMask]
}

// Set inserts or updates value under key using the global TTL.
func (c *ShardedCache[K, V]) Set(key K, value V) { c.shard(key).Set(key, value) }

// SetWithTTL inserts or updates value under key with a per-entry TTL.
func (c *ShardedCache[K, V]) SetWithTTL(key K, value V, ttl time.Duration) {
	c.shard(key).SetWithTTL(key, value, ttl)
}

// Get returns the value under key and promotes it to most recently used.
func (c *ShardedCache[K, V]) Get(key K) (V, bool) { return c.shard(key).Get(key) }

// GetFast returns the value under key without promoting it in LRU order.
func (c *ShardedCache[K, V]) GetFast(key K) (V, bool) { return c.shard(key).GetFast(key) }

// Peek returns the value under key without updating LRU order or statistics.
func (c *ShardedCache[K, V]) Peek(key K) (V, bool) { return c.shard(key).Peek(key) }

// Has reports whether key exists and has not expired.
func (c *ShardedCache[K, V]) Has(key K) bool { return c.shard(key).Has(key) }

// Touch refreshes key, resetting its TTL and promoting it to most recently used.
func (c *ShardedCache[K, V]) Touch(key K) bool { return c.shard(key).Touch(key) }

// Delete removes key. It returns true if the key existed.
func (c *ShardedCache[K, V]) Delete(key K) bool { return c.shard(key).Delete(key) }

// GetEntry returns an immutable [Entry] snapshot for key, or nil if absent.
func (c *ShardedCache[K, V]) GetEntry(key K) *Entry[K, V] { return c.shard(key).GetEntry(key) }

// TTL returns the remaining time-to-live for key.
func (c *ShardedCache[K, V]) TTL(key K) time.Duration { return c.shard(key).TTL(key) }

// GetOrCompute returns the value under key or computes and stores one.
func (c *ShardedCache[K, V]) GetOrCompute(key K, compute func() V, opts ...ComputeOption) V {
	return c.shard(key).GetOrCompute(key, compute, opts...)
}

// GetOrComputeCtx returns the value under key or computes one with context
// and error support. See [Cache.GetOrComputeCtx].
func (c *ShardedCache[K, V]) GetOrComputeCtx(ctx context.Context, key K, compute func(ctx context.Context) (V, error), opts ...ComputeOption) (V, error) {
	return c.shard(key).GetOrComputeCtx(ctx, key, compute, opts...)
}

// Resize sets the per-shard capacity, evicting least-recently-used entries in
// each shard as needed. Total capacity becomes shardCount * capacity.
func (c *ShardedCache[K, V]) Resize(perShardCapacity int) {
	for _, s := range c.shards {
		s.Resize(perShardCapacity)
	}
}

// Len returns the total number of entries across all shards.
func (c *ShardedCache[K, V]) Len() int {
	total := 0
	for _, s := range c.shards {
		total += s.Len()
	}
	return total
}

// LenValid returns the total number of non-expired entries across all shards.
func (c *ShardedCache[K, V]) LenValid() int {
	total := 0
	for _, s := range c.shards {
		total += s.LenValid()
	}
	return total
}

// Clear removes every entry from every shard.
func (c *ShardedCache[K, V]) Clear() {
	for _, s := range c.shards {
		s.Clear()
	}
}

// ExpireOld removes expired entries from every shard and returns the total
// number removed.
func (c *ShardedCache[K, V]) ExpireOld() int {
	total := 0
	for _, s := range c.shards {
		total += s.ExpireOld()
	}
	return total
}

// Close closes every shard. It is idempotent.
func (c *ShardedCache[K, V]) Close() {
	for _, s := range c.shards {
		s.Close()
	}
}

// IsClosed reports whether the shards have been closed.
func (c *ShardedCache[K, V]) IsClosed() bool {
	if len(c.shards) == 0 {
		return true
	}
	return c.shards[0].IsClosed()
}

// Stats returns aggregated counters across all shards.
func (c *ShardedCache[K, V]) Stats() Stats {
	var s Stats
	for _, sh := range c.shards {
		ss := sh.Stats()
		s.Size += ss.Size
		s.Capacity += ss.Capacity
		s.Hits += ss.Hits
		s.Misses += ss.Misses
		s.Evictions += ss.Evictions
	}
	total := s.Hits + s.Misses
	if total > 0 {
		s.HitRate = float64(s.Hits) / float64(total)
	}
	return s
}

// ResetStats zeroes the counters on every shard.
func (c *ShardedCache[K, V]) ResetStats() {
	for _, s := range c.shards {
		s.ResetStats()
	}
}

// Keys returns all non-expired keys across every shard. Order across shards is
// unspecified; within a shard it is most-recently-used first.
func (c *ShardedCache[K, V]) Keys() []K {
	var keys []K
	for _, s := range c.shards {
		keys = append(keys, s.Keys()...)
	}
	return keys
}

// Values returns all non-expired values across every shard.
func (c *ShardedCache[K, V]) Values() []V {
	var values []V
	for _, s := range c.shards {
		values = append(values, s.Values()...)
	}
	return values
}

// Snapshot returns immutable [Entry] snapshots across every shard.
func (c *ShardedCache[K, V]) Snapshot() []*Entry[K, V] {
	var entries []*Entry[K, V]
	for _, s := range c.shards {
		entries = append(entries, s.Snapshot()...)
	}
	return entries
}

// Range calls fn for every non-expired entry across all shards, stopping early
// if fn returns false. fn must not call back into the cache.
func (c *ShardedCache[K, V]) Range(fn func(key K, value V) bool) {
	for _, s := range c.shards {
		stopped := false
		s.Range(func(key K, value V) bool {
			if !fn(key, value) {
				stopped = true
				return false
			}
			return true
		})
		if stopped {
			return
		}
	}
}

// SetMulti inserts or updates every entry in items. Batches of at least
// [parallelBatchThreshold] are dispatched across shards in parallel.
func (c *ShardedCache[K, V]) SetMulti(items map[K]V) {
	if len(items) == 0 {
		return
	}

	groups := make([]map[K]V, len(c.shards))
	for i := range groups {
		groups[i] = make(map[K]V)
	}
	for k, v := range items {
		groups[c.hasher(k)&c.shardMask][k] = v
	}

	if len(items) >= parallelBatchThreshold {
		var wg sync.WaitGroup
		for i, s := range c.shards {
			if len(groups[i]) == 0 {
				continue
			}
			wg.Add(1)
			go func(s *Cache[K, V], m map[K]V) {
				defer wg.Done()
				s.SetMulti(m)
			}(s, groups[i])
		}
		wg.Wait()
		return
	}

	for i, s := range c.shards {
		if len(groups[i]) > 0 {
			s.SetMulti(groups[i])
		}
	}
}

// GetMulti returns the values for keys that are present and live. Large
// batches are dispatched across shards in parallel.
func (c *ShardedCache[K, V]) GetMulti(keys []K) map[K]V {
	if len(keys) == 0 {
		return make(map[K]V)
	}

	groups := c.groupKeys(keys)

	if len(keys) >= parallelBatchThreshold {
		results := make([]map[K]V, len(c.shards))
		var wg sync.WaitGroup
		for i, s := range c.shards {
			if len(groups[i]) == 0 {
				continue
			}
			wg.Add(1)
			go func(idx int, s *Cache[K, V], ks []K) {
				defer wg.Done()
				results[idx] = s.GetMulti(ks)
			}(i, s, groups[i])
		}
		wg.Wait()
		return mergeMaps(results, len(keys))
	}

	result := make(map[K]V, len(keys))
	for i, s := range c.shards {
		if len(groups[i]) == 0 {
			continue
		}
		for k, v := range s.GetMulti(groups[i]) {
			result[k] = v
		}
	}
	return result
}

// DeleteMulti removes every key in keys and returns the number removed.
// Large batches are dispatched across shards in parallel.
func (c *ShardedCache[K, V]) DeleteMulti(keys []K) int {
	if len(keys) == 0 {
		return 0
	}

	groups := c.groupKeys(keys)

	if len(keys) >= parallelBatchThreshold {
		counts := make([]int, len(c.shards))
		var wg sync.WaitGroup
		for i, s := range c.shards {
			if len(groups[i]) == 0 {
				continue
			}
			wg.Add(1)
			go func(idx int, s *Cache[K, V], ks []K) {
				defer wg.Done()
				counts[idx] = s.DeleteMulti(ks)
			}(i, s, groups[i])
		}
		wg.Wait()
		total := 0
		for _, n := range counts {
			total += n
		}
		return total
	}

	total := 0
	for i, s := range c.shards {
		if len(groups[i]) > 0 {
			total += s.DeleteMulti(groups[i])
		}
	}
	return total
}

// groupKeys buckets keys by their target shard index.
func (c *ShardedCache[K, V]) groupKeys(keys []K) [][]K {
	groups := make([][]K, len(c.shards))
	for _, k := range keys {
		idx := c.hasher(k) & c.shardMask
		groups[idx] = append(groups[idx], k)
	}
	return groups
}

// mergeMaps flattens per-shard result maps into one.
func mergeMaps[K comparable, V any](maps []map[K]V, hint int) map[K]V {
	result := make(map[K]V, hint)
	for _, m := range maps {
		for k, v := range m {
			result[k] = v
		}
	}
	return result
}
