package lrux

import (
	"encoding/binary"
	"fmt"
	"hash/maphash"
	"math"
	"sync"
	"time"
)

// --- Sharded LRU cache ---

// ShardedLRU distributes keys across multiple independent [LRU] shards to
// reduce lock contention under high concurrency. Each shard is a full LRU
// with its own capacity, TTL, and eviction callbacks.
//
// Total capacity = ShardCount * per-shard Capacity.
//
// Create with [NewSharded]. Call [ShardedLRU.Close] when done.
type ShardedLRU[K comparable, V any] struct {
	shards    []*LRU[K, V]
	shardMask uint64
	hasher    func(K) uint64
}

// NewSharded creates a sharded LRU cache with the given options applied on
// top of defaults (16 shards, unlimited capacity, no TTL).
func NewSharded[K comparable, V any](opts ...ShardedOption[K, V]) *ShardedLRU[K, V] {
	var cfg shardedConfig[K, V]
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.shardCount <= 0 {
		cfg.shardCount = 16
	}
	count := 1
	for count < cfg.shardCount {
		count <<= 1
	}

	shards := make([]*LRU[K, V], count)
	for i := range shards {
		shards[i] = New[K, V](
			WithCapacity[K, V](cfg.capacity),
			WithTTL[K, V](cfg.ttl),
			WithOnEvict[K, V](cfg.onEvict),
			WithCleanupInterval[K, V](cfg.cleanupInterval),
		)
	}

	return &ShardedLRU[K, V]{
		shards:    shards,
		shardMask: uint64(count - 1),
		hasher:    newHasher[K](),
	}
}

// shard returns the LRU shard responsible for key.
func (c *ShardedLRU[K, V]) shard(key K) *LRU[K, V] {
	return c.shards[c.hasher(key)&c.shardMask]
}

// --- Basic operations ---

// Set adds or updates a value.
func (c *ShardedLRU[K, V]) Set(key K, value V) { c.shard(key).Set(key, value) }

// SetWithTTL adds or updates a value with a per-entry TTL.
func (c *ShardedLRU[K, V]) SetWithTTL(key K, value V, ttl time.Duration) {
	c.shard(key).SetWithTTL(key, value, ttl)
}

// Get retrieves a value.
func (c *ShardedLRU[K, V]) Get(key K) (V, bool) { return c.shard(key).Get(key) }

// Peek retrieves a value without updating LRU position or statistics.
func (c *ShardedLRU[K, V]) Peek(key K) (V, bool) { return c.shard(key).Peek(key) }

// Has reports whether key exists and is not expired.
func (c *ShardedLRU[K, V]) Has(key K) bool { return c.shard(key).Has(key) }

// Delete removes a key.
func (c *ShardedLRU[K, V]) Delete(key K) bool { return c.shard(key).Delete(key) }

// TTL returns the remaining time-to-live for a key.
func (c *ShardedLRU[K, V]) TTL(key K) time.Duration { return c.shard(key).TTL(key) }

// --- Compute ---

// GetOrCompute returns the cached value or computes one.
func (c *ShardedLRU[K, V]) GetOrCompute(key K, compute func() V, opts ...ComputeOption) V {
	return c.shard(key).GetOrCompute(key, compute, opts...)
}

// --- Aggregate operations ---

// Len returns the total number of entries across all shards.
func (c *ShardedLRU[K, V]) Len() int {
	total := 0
	for _, s := range c.shards {
		total += s.Len()
	}
	return total
}

// Clear clears all shards.
func (c *ShardedLRU[K, V]) Clear() {
	for _, s := range c.shards {
		s.Clear()
	}
}

// ExpireOld removes expired entries from all shards.
func (c *ShardedLRU[K, V]) ExpireOld() int {
	total := 0
	for _, s := range c.shards {
		total += s.ExpireOld()
	}
	return total
}

// Close shuts down all shards.
func (c *ShardedLRU[K, V]) Close() {
	for _, s := range c.shards {
		s.Close()
	}
}

// IsClosed reports whether all shards are closed.
func (c *ShardedLRU[K, V]) IsClosed() bool {
	if len(c.shards) == 0 {
		return true
	}
	return c.shards[0].IsClosed()
}

// Stats returns aggregated statistics across all shards.
func (c *ShardedLRU[K, V]) Stats() Stats {
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

// ResetStats zeroes counters on all shards.
func (c *ShardedLRU[K, V]) ResetStats() {
	for _, s := range c.shards {
		s.ResetStats()
	}
}

// --- Iteration ---

// Keys returns all non-expired keys from all shards.
func (c *ShardedLRU[K, V]) Keys() []K {
	var keys []K
	for _, s := range c.shards {
		keys = append(keys, s.Keys()...)
	}
	return keys
}

// Values returns all non-expired values from all shards.
func (c *ShardedLRU[K, V]) Values() []V {
	var values []V
	for _, s := range c.shards {
		values = append(values, s.Values()...)
	}
	return values
}

// Range iterates over all non-expired entries in all shards.
func (c *ShardedLRU[K, V]) Range(fn func(key K, value V) bool) {
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

// --- Batch operations ---

// parallelThreshold is the minimum batch size that triggers parallel shard dispatch.
const parallelThreshold = 64

// SetMulti adds or updates multiple entries. Large batches (>=64 items)
// are distributed across shards in parallel.
func (c *ShardedLRU[K, V]) SetMulti(items map[K]V) {
	if len(items) == 0 {
		return
	}

	groups := make([]map[K]V, len(c.shards))
	for i := range groups {
		groups[i] = make(map[K]V)
	}
	for k, v := range items {
		idx := c.hasher(k) & c.shardMask
		groups[idx][k] = v
	}

	if len(items) >= parallelThreshold {
		var wg sync.WaitGroup
		for i, s := range c.shards {
			if len(groups[i]) > 0 {
				wg.Add(1)
				go func(s *LRU[K, V], m map[K]V) {
					defer wg.Done()
					s.SetMulti(m)
				}(s, groups[i])
			}
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

// GetMulti retrieves multiple values. Large batches are parallelized.
func (c *ShardedLRU[K, V]) GetMulti(keys []K) map[K]V {
	if len(keys) == 0 {
		return make(map[K]V)
	}

	groups := make([][]K, len(c.shards))
	for _, k := range keys {
		idx := c.hasher(k) & c.shardMask
		groups[idx] = append(groups[idx], k)
	}

	if len(keys) >= parallelThreshold {
		results := make([]map[K]V, len(c.shards))
		var wg sync.WaitGroup
		for i, s := range c.shards {
			if len(groups[i]) > 0 {
				wg.Add(1)
				go func(idx int, s *LRU[K, V], ks []K) {
					defer wg.Done()
					results[idx] = s.GetMulti(ks)
				}(i, s, groups[i])
			}
		}
		wg.Wait()
		result := make(map[K]V, len(keys))
		for _, r := range results {
			for k, v := range r {
				result[k] = v
			}
		}
		return result
	}

	result := make(map[K]V, len(keys))
	for i, s := range c.shards {
		if len(groups[i]) > 0 {
			for k, v := range s.GetMulti(groups[i]) {
				result[k] = v
			}
		}
	}
	return result
}

// DeleteMulti removes multiple keys. Large batches are parallelized.
func (c *ShardedLRU[K, V]) DeleteMulti(keys []K) int {
	if len(keys) == 0 {
		return 0
	}

	groups := make([][]K, len(c.shards))
	for _, k := range keys {
		idx := c.hasher(k) & c.shardMask
		groups[idx] = append(groups[idx], k)
	}

	if len(keys) >= parallelThreshold {
		counts := make([]int, len(c.shards))
		var wg sync.WaitGroup
		for i, s := range c.shards {
			if len(groups[i]) > 0 {
				wg.Add(1)
				go func(idx int, s *LRU[K, V], ks []K) {
					defer wg.Done()
					counts[idx] = s.DeleteMulti(ks)
				}(i, s, groups[i])
			}
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

// --- Hasher ---

// newHasher returns a hash function for keys of type K, used for shard selection.
func newHasher[K comparable]() func(K) uint64 {
	seed := maphash.MakeSeed()
	return func(key K) uint64 {
		var h maphash.Hash
		h.SetSeed(seed)
		switch k := any(key).(type) {
		case string:
			h.WriteString(k)
		case int:
			var buf [8]byte
			binary.LittleEndian.PutUint64(buf[:], uint64(k))
			h.Write(buf[:])
		case int64:
			var buf [8]byte
			binary.LittleEndian.PutUint64(buf[:], uint64(k))
			h.Write(buf[:])
		case int32:
			var buf [4]byte
			binary.LittleEndian.PutUint32(buf[:], uint32(k))
			h.Write(buf[:])
		case uint:
			var buf [8]byte
			binary.LittleEndian.PutUint64(buf[:], uint64(k))
			h.Write(buf[:])
		case uint64:
			var buf [8]byte
			binary.LittleEndian.PutUint64(buf[:], k)
			h.Write(buf[:])
		case uint32:
			var buf [4]byte
			binary.LittleEndian.PutUint32(buf[:], k)
			h.Write(buf[:])
		case float64:
			var buf [8]byte
			binary.LittleEndian.PutUint64(buf[:], math.Float64bits(k))
			h.Write(buf[:])
		case float32:
			var buf [4]byte
			binary.LittleEndian.PutUint32(buf[:], math.Float32bits(k))
			h.Write(buf[:])
		case bool:
			if k {
				h.WriteByte(1)
			} else {
				h.WriteByte(0)
			}
		default:
			fmt.Fprint(&h, key)
		}
		return h.Sum64()
	}
}
