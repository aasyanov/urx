package lrux

import (
	"context"
	"time"

	"github.com/aasyanov/urx/panix"
	"golang.org/x/sync/singleflight"
)

const (
	// opGetOrCompute labels panics recovered while running a [Cache.GetOrCompute]
	// compute function.
	opGetOrCompute = "lrux.GetOrCompute"

	// opOnEvict labels panics recovered while running an [OnEvictFunc] callback.
	opOnEvict = "lrux.onEvict"
)

// GetOrCompute returns the value cached under key, or runs compute to produce
// one when the key is missing or expired. The computed value is stored before
// being returned on success. Panics inside compute are recovered via
// [github.com/aasyanov/urx/panix] and returned as a [*panix.PanicError]; nothing
// is cached on panic.
//
// Options:
//   - [WithComputeTTL] sets a per-entry TTL for the computed value.
//   - [WithSingleflight] deduplicates concurrent computes for the same key.
//
// The context is checked before compute starts and again before storing the
// result; a cancelled or expired context after compute returns the context
// error without caching. With [WithSingleflight] each caller's context governs
// only its own wait: the shared compute runs under a detached context, and a
// caller that cancels while waiting receives its cancellation error without
// aborting peers.
//
// Returns [ErrClosed] if the cache is closed before or after compute.
func (c *Cache[K, V]) GetOrCompute(ctx context.Context, key K, compute func(ctx context.Context) (V, error), opts ...ComputeOption) (V, error) {
	var zero V
	if c.closed.Load() {
		return zero, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if v, ok := c.peekPromote(key); ok {
		c.hits.Add(1)
		return v, nil
	}

	cfg := newComputeConfig(opts)
	if cfg.singleflight {
		return c.computeSingle(ctx, key, compute, cfg.ttl)
	}

	c.misses.Add(1)
	return c.computeDirect(ctx, key, compute, cfg.ttl)
}

// computeDirect runs compute under a double-checked lock without singleflight.
//
// The originating miss was already counted by [Cache.GetOrCompute], so the
// double-check here adjusts statistics only when it converts that miss into
// a hit (another goroutine populated the key in the meantime).
func (c *Cache[K, V]) computeDirect(ctx context.Context, key K, compute func(ctx context.Context) (V, error), ttl time.Duration) (V, error) {
	var zero V
	var events []evictEvent[K, V]

	c.mu.Lock()
	if n, ok := c.items[key]; ok && !c.isExpired(n) {
		n.accessedAt = time.Now()
		c.listMoveToFront(n)
		v := n.value
		c.mu.Unlock()
		c.convertMissToHit()
		return v, nil
	}
	if n, ok := c.items[key]; ok {
		if ev := c.removeNodeLocked(n, EvictionExpired); ev != nil {
			events = append(events, *ev)
		}
		c.evictions.Add(1)
	}
	c.mu.Unlock()
	c.fireCallbacks(events)
	events = nil

	if err := ctx.Err(); err != nil {
		return zero, err
	}
	value, err := panix.Safe(opGetOrCompute, func() (V, error) {
		return compute(ctx)
	})
	if err != nil {
		return zero, err
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}

	c.mu.Lock()
	if n, ok := c.items[key]; ok && !c.isExpired(n) {
		v := n.value
		c.mu.Unlock()
		c.convertMissToHit()
		return v, nil
	}
	if n, ok := c.items[key]; ok {
		if ev := c.removeNodeLocked(n, EvictionExpired); ev != nil {
			events = append(events, *ev)
		}
		c.evictions.Add(1)
	}
	if !c.insertLocked(key, value, ttl, &events) {
		c.mu.Unlock()
		return zero, ErrClosed
	}
	c.mu.Unlock()
	c.fireCallbacks(events)
	return value, nil
}

// convertMissToHit rebalances the counters when a compute double-check finds
// that the key was populated concurrently: [Cache.GetOrCompute] already
// recorded a miss for this call, so credit a hit and cancel that miss.
func (c *Cache[K, V]) convertMissToHit() {
	c.hits.Add(1)
	c.misses.Add(^uint64(0))
}

// computeSingle deduplicates concurrent computes for key via singleflight.
//
// The leader performs a lock-free recheck via peekPromote (no statistics side
// effects) and, on a confirmed miss, records exactly one miss, runs compute,
// and stores the result.
func (c *Cache[K, V]) computeSingle(ctx context.Context, key K, compute func(ctx context.Context) (V, error), ttl time.Duration) (V, error) {
	var zero V
	c.sfOnce.Do(c.initSingleflight)

	detached := context.WithoutCancel(ctx)
	ch := c.sf.DoChan(keyString(key), func() (any, error) {
		if v, ok := c.peekPromote(key); ok {
			return v, nil
		}
		c.misses.Add(1)
		v, err := panix.Safe(opGetOrCompute, func() (V, error) {
			return compute(detached)
		})
		if err != nil {
			return zero, err
		}
		c.SetWithTTL(key, v, ttl)
		return v, nil
	})

	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case res := <-ch:
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if res.Err != nil {
			return zero, res.Err
		}
		if c.closed.Load() {
			return zero, ErrClosed
		}
		return res.Val.(V), nil
	}
}

// insertLocked stores key=value with the given per-entry TTL and evicts the
// LRU tail if capacity is exceeded. The caller must hold mu. It returns false
// without storing when the cache is closed.
func (c *Cache[K, V]) insertLocked(key K, value V, ttl time.Duration, events *[]evictEvent[K, V]) bool {
	if c.closed.Load() {
		return false
	}
	now := time.Now()
	n := &node[K, V]{key: key, value: value, createdAt: now, accessedAt: now, expiresAt: c.expireTime(now, ttl)}
	c.items[key] = n
	c.listPushFront(n)

	if c.capacity > 0 && c.size > c.capacity {
		if ev := c.removeTailLocked(); ev != nil {
			*events = append(*events, *ev)
		}
		c.evictions.Add(1)
	}
	return true
}

func (c *Cache[K, V]) initSingleflight() {
	c.sf = &singleflight.Group{}
}
