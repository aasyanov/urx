// Package lrux provides a generic, thread-safe LRU cache with TTL expiration,
// eviction callbacks, singleflight compute, and optional sharding for
// high-concurrency workloads.
//
// The cache uses an intrusive doubly-linked list for O(1) eviction with zero
// per-entry wrapper allocations, and atomic counters for lock-free statistics.
//
//	c := lrux.New[string, int](
//	    lrux.WithCapacity[string, int](1000),
//	    lrux.WithTTL[string, int](time.Hour),
//	)
//	defer c.Close()
//
//	c.Set("key", 42)
//	v, ok := c.Get("key")
//
//	v = c.GetOrCompute("key", func() int { return expensive() },
//	    lrux.WithSingleflight(),
//	)
//
// For high concurrency (>4 goroutines), use [NewSharded] to reduce lock
// contention across independent shards.
package lrux

import (
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// --- LRU cache ---

// LRU is a thread-safe generic cache with least-recently-used eviction,
// optional TTL, eviction callbacks, and singleflight compute.
//
// Create with [New]. Call [LRU.Close] when the cache is no longer needed
// to stop the background cleanup goroutine (if configured).
type LRU[K comparable, V any] struct {
	mu       sync.RWMutex
	items    map[K]*node[K, V]
	head     *node[K, V]
	tail     *node[K, V]
	len      int
	capacity int
	ttl      time.Duration

	onEvict func(key K, value V, reason EvictionReason)

	hits      atomic.Uint64
	misses    atomic.Uint64
	evictions atomic.Uint64

	sfOnce sync.Once
	sf     *singleflight.Group

	cleanup *cleanupTicker
	closed  atomic.Bool
}

// --- Constructor ---

// New creates an [LRU] cache with the given options applied on top of
// zero-value defaults (unlimited capacity, no TTL, no eviction callback,
// no automatic cleanup).
func New[K comparable, V any](opts ...Option[K, V]) *LRU[K, V] {
	var cfg config[K, V]
	for _, opt := range opts {
		opt(&cfg)
	}
	cap := cfg.capacity
	if cap < 0 {
		cap = 0
	}
	ttl := cfg.ttl
	if ttl < 0 {
		ttl = 0
	}
	c := &LRU[K, V]{
		items:    make(map[K]*node[K, V]),
		capacity: cap,
		ttl:      ttl,
		onEvict:  cfg.onEvict,
	}
	if cfg.cleanupInterval > 0 {
		c.startCleanup(cfg.cleanupInterval)
	}
	return c
}

// --- Basic operations ---

// Set adds or updates a value in the cache using the global TTL.
func (c *LRU[K, V]) Set(key K, value V) {
	c.SetWithTTL(key, value, 0)
}

// SetWithTTL adds or updates a value with a per-entry TTL.
// A zero ttl falls back to the cache's global TTL.
func (c *LRU[K, V]) SetWithTTL(key K, value V, ttl time.Duration) {
	if c.closed.Load() {
		return
	}

	var events []evictEvent[K, V]
	now := time.Now()
	exp := c.expireTime(now, ttl)

	c.mu.Lock()

	if n, ok := c.items[key]; ok {
		if c.onEvict != nil {
			events = append(events, evictEvent[K, V]{n.key, n.value, Replaced})
		}
		n.value = value
		n.accessedAt = now
		n.expiresAt = exp
		c.listMoveToFront(n)
		c.mu.Unlock()
		c.fireCallbacks(events)
		return
	}

	n := &node[K, V]{
		key:        key,
		value:      value,
		createdAt:  now,
		accessedAt: now,
		expiresAt:  exp,
	}
	c.items[key] = n
	c.listPushFront(n)

	if c.capacity > 0 && c.len > c.capacity {
		if ev := c.removeTailLocked(); ev != nil {
			events = append(events, *ev)
		}
		c.evictions.Add(1)
	}

	c.mu.Unlock()
	c.fireCallbacks(events)
}

// Get retrieves a value and promotes it to the front of the LRU list.
// Returns the zero value and false if the key is missing or expired.
func (c *LRU[K, V]) Get(key K) (V, bool) {
	var zero V
	if c.closed.Load() {
		return zero, false
	}

	var evict *evictEvent[K, V]

	c.mu.Lock()
	n, ok := c.items[key]
	if !ok {
		c.mu.Unlock()
		c.misses.Add(1)
		return zero, false
	}

	if c.isExpired(n) {
		evict = c.removeNodeLocked(n, Expired)
		c.mu.Unlock()
		c.fireCallback(evict)
		c.misses.Add(1)
		return zero, false
	}

	n.accessedAt = time.Now()
	c.listMoveToFront(n)
	v := n.value
	c.mu.Unlock()

	c.hits.Add(1)
	return v, true
}

// Peek retrieves a value without updating access time, LRU position, or
// statistics. Uses a read lock for maximum concurrency.
// Expired entries are not removed (lazy cleanup on next write or Get).
func (c *LRU[K, V]) Peek(key K) (V, bool) {
	var zero V
	if c.closed.Load() {
		return zero, false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	n, ok := c.items[key]
	if !ok || c.isExpired(n) {
		return zero, false
	}
	return n.value, true
}

// Has reports whether key exists and is not expired.
func (c *LRU[K, V]) Has(key K) bool {
	if c.closed.Load() {
		return false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	n, ok := c.items[key]
	return ok && !c.isExpired(n)
}

// Delete removes a key from the cache. Returns true if the key existed.
func (c *LRU[K, V]) Delete(key K) bool {
	if c.closed.Load() {
		return false
	}

	c.mu.Lock()
	n, ok := c.items[key]
	if !ok {
		c.mu.Unlock()
		return false
	}
	ev := c.removeNodeLocked(n, Deleted)
	c.mu.Unlock()
	c.fireCallback(ev)
	return true
}

// Clear removes all entries from the cache.
func (c *LRU[K, V]) Clear() {
	if c.closed.Load() {
		return
	}

	var events []evictEvent[K, V]

	c.mu.Lock()
	if c.onEvict != nil {
		events = make([]evictEvent[K, V], 0, len(c.items))
		for n := c.head; n != nil; n = n.next {
			events = append(events, evictEvent[K, V]{n.key, n.value, Cleared})
		}
	}
	c.items = make(map[K]*node[K, V])
	c.head = nil
	c.tail = nil
	c.len = 0
	c.mu.Unlock()

	c.fireCallbacks(events)
}

// Len returns the number of entries in the cache, including expired entries
// that have not yet been cleaned up.
func (c *LRU[K, V]) Len() int {
	if c.closed.Load() {
		return 0
	}
	c.mu.RLock()
	n := c.len
	c.mu.RUnlock()
	return n
}

// TTL returns the remaining time-to-live for a key.
// Returns 0 if the key does not exist or is expired.
// Returns -1 if the key has no expiration.
func (c *LRU[K, V]) TTL(key K) time.Duration {
	if c.closed.Load() {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	n, ok := c.items[key]
	if !ok {
		return 0
	}
	if n.expiresAt.IsZero() {
		return -1
	}
	rem := time.Until(n.expiresAt)
	if rem <= 0 {
		return 0
	}
	return rem
}

// --- Compute ---

// GetOrCompute returns the cached value for key, or calls compute to produce
// one if the key is missing or expired. The computed value is stored in the
// cache before being returned.
//
// Options:
//   - [WithComputeTTL]: set a per-entry TTL for the computed value.
//   - [WithSingleflight]: deduplicate concurrent computes for the same key.
func (c *LRU[K, V]) GetOrCompute(key K, compute func() V, opts ...ComputeOption) V {
	var zero V
	if c.closed.Load() {
		return zero
	}

	if v, ok := c.Get(key); ok {
		return v
	}

	var cfg computeConfig
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.singleflight {
		return c.getOrComputeSingle(key, compute, cfg.ttl)
	}
	return c.getOrComputeDirect(key, compute, cfg.ttl)
}

// getOrComputeDirect runs compute under a double-checked lock without singleflight dedup.
func (c *LRU[K, V]) getOrComputeDirect(key K, compute func() V, ttl time.Duration) V {
	var events []evictEvent[K, V]

	c.mu.Lock()
	// Double-check after lock
	if n, ok := c.items[key]; ok && !c.isExpired(n) {
		n.accessedAt = time.Now()
		c.listMoveToFront(n)
		c.hits.Add(1)
		v := n.value
		c.mu.Unlock()
		return v
	}
	// Remove expired entry if present
	if n, ok := c.items[key]; ok {
		if ev := c.removeNodeLocked(n, Expired); ev != nil {
			events = append(events, *ev)
		}
	}
	c.misses.Add(1)
	c.mu.Unlock()
	c.fireCallbacks(events)
	events = nil

	value := safeCompute(compute)

	c.mu.Lock()
	// Re-check: another goroutine may have stored a value
	if n, ok := c.items[key]; ok && !c.isExpired(n) {
		c.mu.Unlock()
		return n.value
	}
	if n, ok := c.items[key]; ok {
		if ev := c.removeNodeLocked(n, Expired); ev != nil {
			events = append(events, *ev)
		}
	}

	now := time.Now()
	n := &node[K, V]{
		key:        key,
		value:      value,
		createdAt:  now,
		accessedAt: now,
		expiresAt:  c.expireTime(now, ttl),
	}
	c.items[key] = n
	c.listPushFront(n)

	if c.capacity > 0 && c.len > c.capacity {
		if ev := c.removeTailLocked(); ev != nil {
			events = append(events, *ev)
		}
		c.evictions.Add(1)
	}

	c.mu.Unlock()
	c.fireCallbacks(events)
	return value
}

// getOrComputeSingle runs compute via [singleflight.Group] to deduplicate concurrent calls for the same key.
func (c *LRU[K, V]) getOrComputeSingle(key K, compute func() V, ttl time.Duration) V {
	c.sfOnce.Do(func() { c.sf = &singleflight.Group{} })

	keyStr := keyToString(key)
	result, _, _ := c.sf.Do(keyStr, func() (any, error) {
		if v, ok := c.Get(key); ok {
			return v, nil
		}
		v := safeCompute(compute)
		c.SetWithTTL(key, v, ttl)
		return v, nil
	})
	return result.(V)
}

// --- Batch operations ---

// SetMulti adds or updates multiple entries using the global TTL.
func (c *LRU[K, V]) SetMulti(items map[K]V) {
	if c.closed.Load() || len(items) == 0 {
		return
	}

	var events []evictEvent[K, V]
	now := time.Now()
	exp := c.expireTime(now, 0)

	c.mu.Lock()
	for key, value := range items {
		if n, ok := c.items[key]; ok {
			if c.onEvict != nil {
				events = append(events, evictEvent[K, V]{n.key, n.value, Replaced})
			}
			n.value = value
			n.accessedAt = now
			n.expiresAt = exp
			c.listMoveToFront(n)
		} else {
			n := &node[K, V]{
				key:        key,
				value:      value,
				createdAt:  now,
				accessedAt: now,
				expiresAt:  exp,
			}
			c.items[key] = n
			c.listPushFront(n)
		}
	}
	if c.capacity > 0 {
		for c.len > c.capacity {
			if ev := c.removeTailLocked(); ev != nil {
				events = append(events, *ev)
			}
			c.evictions.Add(1)
		}
	}
	c.mu.Unlock()
	c.fireCallbacks(events)
}

// GetMulti retrieves multiple values. Missing or expired keys are omitted.
func (c *LRU[K, V]) GetMulti(keys []K) map[K]V {
	result := make(map[K]V, len(keys))
	for _, key := range keys {
		if v, ok := c.Get(key); ok {
			result[key] = v
		}
	}
	return result
}

// DeleteMulti removes multiple keys. Returns the number of keys removed.
func (c *LRU[K, V]) DeleteMulti(keys []K) int {
	if c.closed.Load() || len(keys) == 0 {
		return 0
	}

	var events []evictEvent[K, V]

	c.mu.Lock()
	count := 0
	for _, key := range keys {
		if n, ok := c.items[key]; ok {
			if ev := c.removeNodeLocked(n, Deleted); ev != nil {
				events = append(events, *ev)
			}
			count++
		}
	}
	c.mu.Unlock()
	c.fireCallbacks(events)
	return count
}

// --- Iteration ---

// Keys returns all non-expired keys in LRU order (most recent first).
// Expired entries are removed during iteration.
func (c *LRU[K, V]) Keys() []K {
	if c.closed.Load() {
		return nil
	}

	var events []evictEvent[K, V]

	c.mu.Lock()
	keys := make([]K, 0, c.len)
	for n := c.head; n != nil; {
		next := n.next
		if c.isExpired(n) {
			if ev := c.removeNodeLocked(n, Expired); ev != nil {
				events = append(events, *ev)
			}
			c.evictions.Add(1)
		} else {
			keys = append(keys, n.key)
		}
		n = next
	}
	c.mu.Unlock()
	c.fireCallbacks(events)
	return keys
}

// Values returns all non-expired values in LRU order (most recent first).
// Expired entries are removed during iteration.
func (c *LRU[K, V]) Values() []V {
	if c.closed.Load() {
		return nil
	}

	var events []evictEvent[K, V]

	c.mu.Lock()
	values := make([]V, 0, c.len)
	for n := c.head; n != nil; {
		next := n.next
		if c.isExpired(n) {
			if ev := c.removeNodeLocked(n, Expired); ev != nil {
				events = append(events, *ev)
			}
			c.evictions.Add(1)
		} else {
			values = append(values, n.value)
		}
		n = next
	}
	c.mu.Unlock()
	c.fireCallbacks(events)
	return values
}

// Range iterates over all non-expired entries in LRU order.
// If fn returns false, iteration stops. Expired entries are removed.
// The callback runs while the lock is held; for non-blocking iteration
// use [LRU.Keys] or [LRU.Values] and iterate over the result.
func (c *LRU[K, V]) Range(fn func(key K, value V) bool) {
	if c.closed.Load() {
		return
	}

	var events []evictEvent[K, V]

	c.mu.Lock()
	for n := c.head; n != nil; {
		next := n.next
		if c.isExpired(n) {
			if ev := c.removeNodeLocked(n, Expired); ev != nil {
				events = append(events, *ev)
			}
			c.evictions.Add(1)
			n = next
			continue
		}
		if !fn(n.key, n.value) {
			break
		}
		n = next
	}
	c.mu.Unlock()
	c.fireCallbacks(events)
}

// --- Expiration ---

// ExpireOld removes all expired entries. Returns the number removed.
// By default expired entries are removed lazily on access; call this
// periodically for proactive cleanup.
func (c *LRU[K, V]) ExpireOld() int {
	if c.closed.Load() {
		return 0
	}

	var events []evictEvent[K, V]

	c.mu.Lock()
	count := 0
	for n := c.head; n != nil; {
		next := n.next
		if c.isExpired(n) {
			if ev := c.removeNodeLocked(n, Expired); ev != nil {
				events = append(events, *ev)
			}
			c.evictions.Add(1)
			count++
		}
		n = next
	}
	c.mu.Unlock()
	c.fireCallbacks(events)
	return count
}

// --- Statistics ---

// Stats returns a snapshot of cache statistics.
func (c *LRU[K, V]) Stats() Stats {
	c.mu.RLock()
	size := c.len
	c.mu.RUnlock()

	hits := c.hits.Load()
	misses := c.misses.Load()
	total := hits + misses

	var hitRate float64
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return Stats{
		Size:      size,
		Capacity:  c.capacity,
		Hits:      hits,
		Misses:    misses,
		Evictions: c.evictions.Load(),
		HitRate:   hitRate,
	}
}

// ResetStats zeroes all counters.
func (c *LRU[K, V]) ResetStats() {
	c.hits.Store(0)
	c.misses.Store(0)
	c.evictions.Store(0)
}

// --- Lifecycle ---

// Close shuts down the cache: stops the cleanup goroutine (if any),
// clears all entries (firing eviction callbacks), and marks the cache
// as closed. Subsequent operations are no-ops. Close is idempotent.
func (c *LRU[K, V]) Close() {
	if c.closed.Swap(true) {
		return
	}
	if c.cleanup != nil {
		c.cleanup.Stop()
	}

	var events []evictEvent[K, V]

	c.mu.Lock()
	if c.onEvict != nil {
		events = make([]evictEvent[K, V], 0, len(c.items))
		for n := c.head; n != nil; n = n.next {
			events = append(events, evictEvent[K, V]{n.key, n.value, Cleared})
		}
	}
	c.items = make(map[K]*node[K, V])
	c.head = nil
	c.tail = nil
	c.len = 0
	c.mu.Unlock()

	c.fireCallbacks(events)
}

// IsClosed reports whether the cache has been closed.
func (c *LRU[K, V]) IsClosed() bool {
	return c.closed.Load()
}

// --- Intrusive doubly-linked list operations (caller must hold mu) ---

// listPushFront inserts n at the head of the intrusive linked list. Caller must hold mu.
func (c *LRU[K, V]) listPushFront(n *node[K, V]) {
	n.prev = nil
	n.next = c.head
	if c.head != nil {
		c.head.prev = n
	}
	c.head = n
	if c.tail == nil {
		c.tail = n
	}
	c.len++
}

// listRemove unlinks n from the intrusive linked list. Caller must hold mu.
func (c *LRU[K, V]) listRemove(n *node[K, V]) {
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		c.head = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		c.tail = n.prev
	}
	n.prev = nil
	n.next = nil
	c.len--
}

// listMoveToFront promotes n to the head of the list. No-op if already head. Caller must hold mu.
func (c *LRU[K, V]) listMoveToFront(n *node[K, V]) {
	if c.head == n {
		return
	}
	c.listRemove(n)
	c.listPushFront(n)
}

// --- Internal helpers ---

// isExpired reports whether n's TTL has elapsed.
func (c *LRU[K, V]) isExpired(n *node[K, V]) bool {
	return !n.expiresAt.IsZero() && time.Now().After(n.expiresAt)
}

// expireTime computes the expiration instant, falling back to the cache-level TTL when ttl is zero.
func (c *LRU[K, V]) expireTime(now time.Time, ttl time.Duration) time.Time {
	if ttl > 0 {
		return now.Add(ttl)
	}
	if c.ttl > 0 {
		return now.Add(c.ttl)
	}
	return time.Time{}
}

// removeNodeLocked removes a node from the map and list.
// Returns an eviction event if a callback is configured.
func (c *LRU[K, V]) removeNodeLocked(n *node[K, V], reason EvictionReason) *evictEvent[K, V] {
	delete(c.items, n.key)
	c.listRemove(n)
	if c.onEvict != nil {
		return &evictEvent[K, V]{n.key, n.value, reason}
	}
	return nil
}

// removeTailLocked removes the least-recently-used entry.
func (c *LRU[K, V]) removeTailLocked() *evictEvent[K, V] {
	if c.tail == nil {
		return nil
	}
	return c.removeNodeLocked(c.tail, Capacity)
}

// fireCallback invokes the onEvict callback for a single eviction event, if configured.
func (c *LRU[K, V]) fireCallback(ev *evictEvent[K, V]) {
	if ev == nil || c.onEvict == nil {
		return
	}
	c.safeOnEvict(ev.key, ev.value, ev.reason)
}

// fireCallbacks invokes the onEvict callback for each event in the batch.
func (c *LRU[K, V]) fireCallbacks(events []evictEvent[K, V]) {
	if len(events) == 0 || c.onEvict == nil {
		return
	}
	for i := range events {
		c.safeOnEvict(events[i].key, events[i].value, events[i].reason)
	}
}

// safeOnEvict calls the onEvict callback, recovering from any panic to
// protect the cache from user-code crashes. Logging panics is the caller's
// responsibility (wrap the callback with panix.Safe if observability is needed).
func (c *LRU[K, V]) safeOnEvict(key K, value V, reason EvictionReason) {
	defer func() { _ = recover() }()
	c.onEvict(key, value, reason)
}

// safeCompute calls compute, recovering from any panic and returning the zero
// value. Logging panics is the caller's responsibility.
func safeCompute[V any](compute func() V) (val V) {
	defer func() { _ = recover() }()
	return compute()
}

// startCleanup launches the background goroutine that periodically removes expired entries.
func (c *LRU[K, V]) startCleanup(interval time.Duration) {
	ct := &cleanupTicker{
		ticker:   time.NewTicker(interval),
		stop:     make(chan struct{}),
		stopOnce: sync.Once{},
	}
	c.cleanup = ct
	go func() {
		for {
			select {
			case <-ct.ticker.C:
				c.ExpireOld()
			case <-ct.stop:
				return
			}
		}
	}()
}

// --- Key-to-string for singleflight ---

// keyToString converts a comparable key to a string for use as a singleflight dedup key.
func keyToString[K comparable](key K) string {
	switch k := any(key).(type) {
	case string:
		return k
	case int:
		return strconv.FormatInt(int64(k), 10)
	case int64:
		return strconv.FormatInt(k, 10)
	case int32:
		return strconv.FormatInt(int64(k), 10)
	case uint:
		return strconv.FormatUint(uint64(k), 10)
	case uint64:
		return strconv.FormatUint(k, 10)
	default:
		return fmt.Sprint(key)
	}
}
