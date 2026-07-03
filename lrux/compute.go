package lrux

import (
	"context"
	"time"

	"golang.org/x/sync/singleflight"
)

// newSingleflightGroup constructs the lazily-initialized singleflight group.
func newSingleflightGroup() *singleflight.Group {
	return &singleflight.Group{}
}

// GetOrCompute returns the value cached under key, or runs compute to produce
// one when the key is missing or expired. The computed value is stored before
// being returned. Panics inside compute are recovered and yield the zero
// value (which is still cached).
//
// Options:
//   - [WithComputeTTL] sets a per-entry TTL for the computed value.
//   - [WithSingleflight] deduplicates concurrent computes for the same key.
//
// It returns the zero value if the cache is closed.
func (c *Cache[K, V]) GetOrCompute(key K, compute func() V, opts ...ComputeOption) V {
	var zero V
	if c.closed.Load() {
		return zero
	}
	if v, ok := c.Get(key); ok {
		return v
	}

	cfg := newComputeConfig(opts)
	if cfg.singleflight {
		return c.computeSingle(key, compute, cfg.ttl)
	}
	return c.computeDirect(key, compute, cfg.ttl)
}

// computeDirect runs compute under a double-checked lock without singleflight.
//
// The originating miss was already counted by the public GetOrCompute call, so
// the double-check here adjusts statistics only when it converts that miss into
// a hit (another goroutine populated the key in the meantime).
func (c *Cache[K, V]) computeDirect(key K, compute func() V, ttl time.Duration) V {
	var events []evictEvent[K, V]

	c.mu.Lock()
	if n, ok := c.items[key]; ok && !c.isExpired(n) {
		n.accessedAt = time.Now()
		c.listMoveToFront(n)
		v := n.value
		c.mu.Unlock()
		c.convertMissToHit()
		return v
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

	value := safeCompute(compute)

	c.mu.Lock()
	if n, ok := c.items[key]; ok && !c.isExpired(n) {
		v := n.value
		c.mu.Unlock()
		c.convertMissToHit()
		return v
	}
	if n, ok := c.items[key]; ok {
		if ev := c.removeNodeLocked(n, EvictionExpired); ev != nil {
			events = append(events, *ev)
		}
		c.evictions.Add(1)
	}
	c.insertLocked(key, value, ttl, &events)
	c.mu.Unlock()
	c.fireCallbacks(events)
	return value
}

// convertMissToHit rebalances the counters when a compute double-check finds
// that the key was populated concurrently: the public Get already recorded a
// miss for this call, so credit a hit and cancel that miss.
func (c *Cache[K, V]) convertMissToHit() {
	c.hits.Add(1)
	c.misses.Add(^uint64(0))
}

// computeSingle deduplicates concurrent computes for key via singleflight.
//
// The leader performs a lock-free recheck via peekPromote (no statistics side
// effects: the originating miss was already counted by the public GetOrCompute
// call) and, on a confirmed miss, runs compute and stores the result.
func (c *Cache[K, V]) computeSingle(key K, compute func() V, ttl time.Duration) V {
	c.sfOnce.Do(c.initSingleflight)

	result, _, _ := c.sf.Do(keyString(key), func() (any, error) {
		if v, ok := c.peekPromote(key); ok {
			c.convertMissToHit()
			return v, nil
		}
		v := safeCompute(compute)
		c.SetWithTTL(key, v, ttl)
		return v, nil
	})
	return result.(V)
}

// GetOrComputeCtx is the context-aware form of [Cache.GetOrCompute]. The
// compute function may return an error, in which case nothing is cached and
// the error is returned to the caller. The context is checked before and
// after compute and propagated into it.
//
// Options behave as in [Cache.GetOrCompute]. With [WithSingleflight] the
// compute function receives a detached context so one caller's cancellation
// does not abort the shared computation for other waiters.
//
// It returns [ErrClosed] if the cache is closed.
func (c *Cache[K, V]) GetOrComputeCtx(ctx context.Context, key K, compute func(ctx context.Context) (V, error), opts ...ComputeOption) (V, error) {
	var zero V
	if c.closed.Load() {
		return zero, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	if v, ok := c.Get(key); ok {
		return v, nil
	}

	cfg := newComputeConfig(opts)
	if cfg.singleflight {
		return c.computeSingleCtx(ctx, key, compute, cfg.ttl)
	}
	return c.computeDirectCtx(ctx, key, compute, cfg.ttl)
}

// computeDirectCtx runs the context-aware compute under a double-checked lock.
func (c *Cache[K, V]) computeDirectCtx(ctx context.Context, key K, compute func(ctx context.Context) (V, error), ttl time.Duration) (V, error) {
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
	value, err := compute(ctx)
	if err != nil {
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

// computeSingleCtx deduplicates context-aware computes via singleflight,
// detaching the compute context so a single caller's cancellation does not
// abort the shared work for the other waiters.
func (c *Cache[K, V]) computeSingleCtx(ctx context.Context, key K, compute func(ctx context.Context) (V, error), ttl time.Duration) (V, error) {
	var zero V
	c.sfOnce.Do(c.initSingleflight)

	detached := context.WithoutCancel(ctx)
	ch := c.sf.DoChan(keyString(key), func() (any, error) {
		if v, ok := c.peekPromote(key); ok {
			c.convertMissToHit()
			return v, nil
		}
		v, err := compute(detached)
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
	c.sf = newSingleflightGroup()
}

// safeCompute runs compute, recovering from panics and returning the zero
// value when one occurs.
func safeCompute[V any](compute func() V) (val V) {
	defer func() { _ = recover() }()
	return compute()
}
