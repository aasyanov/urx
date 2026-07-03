package lrux

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/panix"
	"golang.org/x/sync/singleflight"
)

// Cache is a generic, thread-safe LRU cache with optional TTL expiration,
// eviction callbacks, and singleflight compute. It is safe for concurrent use
// from multiple goroutines.
//
// Entries are held in an intrusive doubly-linked list keyed by a map, giving
// O(1) lookup, insertion, promotion, and eviction with a single heap node per
// entry. Statistics use atomic counters for lock-free reads.
//
// Create with [New] and configure via [Option] functions. Call [Cache.Close]
// when the cache is no longer needed to stop the background cleanup goroutine
// (if one was configured) and release entries. Close is idempotent and returns nil.
//
// Compute and eviction callbacks run under [github.com/aasyanov/urx/panix]; a
// panicking callback cannot corrupt cache state.
//
// For high concurrency across many keys, prefer [ShardedCache] via
// [NewSharded] to reduce lock contention.
type Cache[K comparable, V any] struct {
	mu       sync.RWMutex
	items    map[K]*node[K, V]
	head     *node[K, V]
	tail     *node[K, V]
	size     int
	capacity int
	ttl      time.Duration

	onEvict OnEvictFunc[K, V]

	hits      atomic.Uint64
	misses    atomic.Uint64
	evictions atomic.Uint64

	sfOnce sync.Once
	sf     *singleflight.Group

	cleanup *cleanupTicker
	closed  atomic.Bool
}

// New creates a [Cache] with the given options applied over the defaults:
// unbounded capacity, no TTL, no eviction callback, and lazy cleanup.
func New[K comparable, V any](opts ...Option[K, V]) *Cache[K, V] {
	cfg := newConfig(opts)
	c := &Cache[K, V]{
		items:    make(map[K]*node[K, V]),
		capacity: cfg.capacity,
		ttl:      cfg.ttl,
		onEvict:  cfg.onEvict,
	}
	if cfg.cleanupInterval > 0 {
		c.startCleanup(cfg.cleanupInterval)
	}
	return c
}

// Set inserts or updates value under key using the cache's global TTL.
// It is a no-op if the cache is closed.
func (c *Cache[K, V]) Set(key K, value V) {
	c.SetWithTTL(key, value, 0)
}

// SetWithTTL inserts or updates value under key with a per-entry TTL.
// A zero ttl falls back to the cache's global TTL. It is a no-op if the cache
// is closed.
func (c *Cache[K, V]) SetWithTTL(key K, value V, ttl time.Duration) {
	if c.closed.Load() {
		return
	}

	var events []evictEvent[K, V]
	now := time.Now()
	exp := c.expireTime(now, ttl)

	c.mu.Lock()
	if n, ok := c.items[key]; ok {
		if c.onEvict != nil {
			events = append(events, evictEvent[K, V]{n.key, n.value, EvictionReplaced})
		}
		n.value = value
		n.accessedAt = now
		n.expiresAt = exp
		c.listMoveToFront(n)
		c.mu.Unlock()
		c.fireCallbacks(events)
		return
	}

	n := &node[K, V]{key: key, value: value, createdAt: now, accessedAt: now, expiresAt: exp}
	c.items[key] = n
	c.listPushFront(n)

	if c.capacity > 0 && c.size > c.capacity {
		if ev := c.removeTailLocked(); ev != nil {
			events = append(events, *ev)
		}
		c.evictions.Add(1)
	}

	c.mu.Unlock()
	c.fireCallbacks(events)
}

// Get returns the value stored under key and promotes the entry to most
// recently used. It returns the zero value and false if the key is missing,
// expired, or the cache is closed.
func (c *Cache[K, V]) Get(key K) (V, bool) {
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
		evict = c.removeNodeLocked(n, EvictionExpired)
		c.evictions.Add(1)
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

// peekPromote returns the live value under key and promotes it to most
// recently used, without updating hit/miss statistics. Expired entries are
// removed eagerly, matching [Cache.Get] semantics. It is used by the compute
// paths to avoid double-counting statistics under singleflight deduplication.
func (c *Cache[K, V]) peekPromote(key K) (V, bool) {
	var zero V
	var evict *evictEvent[K, V]

	c.mu.Lock()
	n, ok := c.items[key]
	if !ok {
		c.mu.Unlock()
		return zero, false
	}
	if c.isExpired(n) {
		evict = c.removeNodeLocked(n, EvictionExpired)
		c.evictions.Add(1)
		c.mu.Unlock()
		c.fireCallback(evict)
		return zero, false
	}
	n.accessedAt = time.Now()
	c.listMoveToFront(n)
	v := n.value
	c.mu.Unlock()
	return v, true
}

// GetFast returns the value stored under key without promoting it in the LRU
// order. It uses a read lock so concurrent GetFast calls proceed in parallel,
// making it preferable for read-heavy workloads where strict recency is not
// required. Expired entries are reported as missing but not removed; use
// [Cache.Get], [Cache.ExpireOld], or the background sweeper to reclaim them.
func (c *Cache[K, V]) GetFast(key K) (V, bool) {
	var zero V
	if c.closed.Load() {
		return zero, false
	}

	c.mu.RLock()
	n, ok := c.items[key]
	if !ok || c.isExpired(n) {
		c.mu.RUnlock()
		c.misses.Add(1)
		return zero, false
	}
	v := n.value
	c.mu.RUnlock()

	c.hits.Add(1)
	return v, true
}

// Peek returns the value stored under key without updating LRU order or
// statistics. Expired entries are reported as missing but not removed.
func (c *Cache[K, V]) Peek(key K) (V, bool) {
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

// Has reports whether key exists and has not expired.
func (c *Cache[K, V]) Has(key K) bool {
	if c.closed.Load() {
		return false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	n, ok := c.items[key]
	return ok && !c.isExpired(n)
}

// GetEntry returns an immutable [Entry] snapshot for key, including its
// timestamps, without updating LRU order. It returns nil if the key is
// missing, expired, or the cache is closed.
func (c *Cache[K, V]) GetEntry(key K) *Entry[K, V] {
	if c.closed.Load() {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	n, ok := c.items[key]
	if !ok || c.isExpired(n) {
		return nil
	}
	return &Entry[K, V]{
		Key:        n.key,
		Value:      n.value,
		CreatedAt:  n.createdAt,
		AccessedAt: n.accessedAt,
		ExpiresAt:  n.expiresAt,
	}
}

// TTL returns the remaining time-to-live for key. It returns 0 if the key is
// missing or expired, and -1 if the key exists but has no expiration.
func (c *Cache[K, V]) TTL(key K) time.Duration {
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

// Touch refreshes key: it promotes the entry to most recently used and slides
// the expiration forward. When the entry carries an absolute expiry, the
// remaining lifetime since the last access is preserved; otherwise a configured
// global TTL is applied. Entries without any TTL are promoted only. It returns
// false if the key is missing or already expired (an expired entry is removed).
func (c *Cache[K, V]) Touch(key K) bool {
	if c.closed.Load() {
		return false
	}

	var evict *evictEvent[K, V]

	c.mu.Lock()
	n, ok := c.items[key]
	if !ok {
		c.mu.Unlock()
		return false
	}
	if c.isExpired(n) {
		evict = c.removeNodeLocked(n, EvictionExpired)
		c.evictions.Add(1)
		c.mu.Unlock()
		c.fireCallback(evict)
		return false
	}

	now := time.Now()
	c.slideExpiration(n, now)
	n.accessedAt = now
	c.listMoveToFront(n)
	c.mu.Unlock()
	return true
}

// Delete removes key from the cache. It returns true if the key existed.
func (c *Cache[K, V]) Delete(key K) bool {
	if c.closed.Load() {
		return false
	}

	c.mu.Lock()
	n, ok := c.items[key]
	if !ok {
		c.mu.Unlock()
		return false
	}
	ev := c.removeNodeLocked(n, EvictionDeleted)
	c.mu.Unlock()
	c.fireCallback(ev)
	return true
}

// Clear removes every entry, firing the eviction callback for each with
// reason [EvictionCleared].
func (c *Cache[K, V]) Clear() {
	if c.closed.Load() {
		return
	}
	c.drain(EvictionCleared)
}

// Resize changes the capacity. A smaller capacity evicts least-recently-used
// entries until the cache fits. A capacity of 0 or negative means unbounded.
func (c *Cache[K, V]) Resize(capacity int) {
	if c.closed.Load() {
		return
	}
	if capacity < 0 {
		capacity = 0
	}

	var events []evictEvent[K, V]

	c.mu.Lock()
	c.capacity = capacity
	if capacity > 0 {
		for c.size > capacity {
			ev := c.removeTailLocked()
			if ev == nil && c.tail == nil {
				break
			}
			if ev != nil {
				events = append(events, *ev)
			}
			c.evictions.Add(1)
		}
	}
	c.mu.Unlock()
	c.fireCallbacks(events)
}

// Len returns the number of entries, including expired entries not yet swept.
func (c *Cache[K, V]) Len() int {
	if c.closed.Load() {
		return 0
	}
	c.mu.RLock()
	n := c.size
	c.mu.RUnlock()
	return n
}

// LenValid returns the number of non-expired entries. It scans all entries,
// so it is O(n).
func (c *Cache[K, V]) LenValid() int {
	if c.closed.Load() {
		return 0
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	count := 0
	for n := c.head; n != nil; n = n.next {
		if !c.isExpired(n) {
			count++
		}
	}
	return count
}

// SetMulti inserts or updates every entry in items using the global TTL.
func (c *Cache[K, V]) SetMulti(items map[K]V) {
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
				events = append(events, evictEvent[K, V]{n.key, n.value, EvictionReplaced})
			}
			n.value = value
			n.accessedAt = now
			n.expiresAt = exp
			c.listMoveToFront(n)
			continue
		}
		n := &node[K, V]{key: key, value: value, createdAt: now, accessedAt: now, expiresAt: exp}
		c.items[key] = n
		c.listPushFront(n)
	}
	if c.capacity > 0 {
		for c.size > c.capacity {
			if ev := c.removeTailLocked(); ev != nil {
				events = append(events, *ev)
			}
			c.evictions.Add(1)
		}
	}
	c.mu.Unlock()
	c.fireCallbacks(events)
}

// GetMulti returns the values for keys that are present and live. Missing or
// expired keys are omitted from the result.
func (c *Cache[K, V]) GetMulti(keys []K) map[K]V {
	result := make(map[K]V, len(keys))
	for _, key := range keys {
		if v, ok := c.Get(key); ok {
			result[key] = v
		}
	}
	return result
}

// DeleteMulti removes every key in keys. It returns the number removed.
func (c *Cache[K, V]) DeleteMulti(keys []K) int {
	if c.closed.Load() || len(keys) == 0 {
		return 0
	}

	var events []evictEvent[K, V]

	c.mu.Lock()
	count := 0
	for _, key := range keys {
		if n, ok := c.items[key]; ok {
			if ev := c.removeNodeLocked(n, EvictionDeleted); ev != nil {
				events = append(events, *ev)
			}
			count++
		}
	}
	c.mu.Unlock()
	c.fireCallbacks(events)
	return count
}

// Keys returns all non-expired keys in most-recently-used order. Expired
// entries are swept during the scan.
func (c *Cache[K, V]) Keys() []K {
	if c.closed.Load() {
		return nil
	}

	var events []evictEvent[K, V]

	c.mu.Lock()
	keys := make([]K, 0, c.size)
	for n := c.head; n != nil; {
		next := n.next
		if c.isExpired(n) {
			if ev := c.removeNodeLocked(n, EvictionExpired); ev != nil {
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

// Values returns all non-expired values in most-recently-used order. Expired
// entries are swept during the scan.
func (c *Cache[K, V]) Values() []V {
	if c.closed.Load() {
		return nil
	}

	var events []evictEvent[K, V]

	c.mu.Lock()
	values := make([]V, 0, c.size)
	for n := c.head; n != nil; {
		next := n.next
		if c.isExpired(n) {
			if ev := c.removeNodeLocked(n, EvictionExpired); ev != nil {
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

// Snapshot returns immutable [Entry] snapshots of all non-expired entries in
// most-recently-used order. Expired entries are swept during the scan.
func (c *Cache[K, V]) Snapshot() []*Entry[K, V] {
	if c.closed.Load() {
		return nil
	}

	var events []evictEvent[K, V]

	c.mu.Lock()
	entries := make([]*Entry[K, V], 0, c.size)
	for n := c.head; n != nil; {
		next := n.next
		if c.isExpired(n) {
			if ev := c.removeNodeLocked(n, EvictionExpired); ev != nil {
				events = append(events, *ev)
			}
			c.evictions.Add(1)
		} else {
			entries = append(entries, &Entry[K, V]{
				Key:        n.key,
				Value:      n.value,
				CreatedAt:  n.createdAt,
				AccessedAt: n.accessedAt,
				ExpiresAt:  n.expiresAt,
			})
		}
		n = next
	}
	c.mu.Unlock()
	c.fireCallbacks(events)
	return entries
}

// Range calls fn for every non-expired entry in most-recently-used order,
// stopping early if fn returns false. The callback runs while the cache lock
// is held, so it must not call back into the cache; for non-blocking
// iteration use [Cache.Snapshot]. Expired entries are swept during the scan.
func (c *Cache[K, V]) Range(fn func(key K, value V) bool) {
	if c.closed.Load() {
		return
	}

	var events []evictEvent[K, V]

	c.mu.Lock()
	for n := c.head; n != nil; {
		next := n.next
		if c.isExpired(n) {
			if ev := c.removeNodeLocked(n, EvictionExpired); ev != nil {
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

// ExpireOld removes every expired entry and returns the number removed.
// Expired entries are otherwise removed lazily on access; call this for
// proactive cleanup when no background sweeper is configured.
func (c *Cache[K, V]) ExpireOld() int {
	if c.closed.Load() {
		return 0
	}

	var events []evictEvent[K, V]

	c.mu.Lock()
	count := 0
	for n := c.head; n != nil; {
		next := n.next
		if c.isExpired(n) {
			if ev := c.removeNodeLocked(n, EvictionExpired); ev != nil {
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

// Stats returns a [Stats] snapshot of the cache counters.
func (c *Cache[K, V]) Stats() Stats {
	c.mu.RLock()
	size := c.size
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

// ResetStats zeroes the hit, miss, and eviction counters.
func (c *Cache[K, V]) ResetStats() {
	c.hits.Store(0)
	c.misses.Store(0)
	c.evictions.Store(0)
}

// Close stops the background cleanup goroutine (if any), removes all entries
// firing eviction callbacks with reason [EvictionCleared], and marks the
// cache closed. Subsequent operations are no-ops. Close is idempotent and
// always returns nil.
func (c *Cache[K, V]) Close() error {
	if c.closed.Swap(true) {
		return nil
	}
	if c.cleanup != nil {
		c.cleanup.Stop()
	}

	var events []evictEvent[K, V]

	c.mu.Lock()
	if c.onEvict != nil {
		events = make([]evictEvent[K, V], 0, len(c.items))
		for n := c.head; n != nil; n = n.next {
			events = append(events, evictEvent[K, V]{n.key, n.value, EvictionCleared})
		}
	}
	c.items = make(map[K]*node[K, V])
	c.head = nil
	c.tail = nil
	c.size = 0
	c.mu.Unlock()

	c.fireCallbacks(events)
	return nil
}

// IsClosed reports whether the cache has been closed.
func (c *Cache[K, V]) IsClosed() bool {
	return c.closed.Load()
}

// drain removes all entries under a single lock, firing callbacks with reason.
func (c *Cache[K, V]) drain(reason EvictionReason) {
	var events []evictEvent[K, V]

	c.mu.Lock()
	if c.onEvict != nil {
		events = make([]evictEvent[K, V], 0, len(c.items))
		for n := c.head; n != nil; n = n.next {
			events = append(events, evictEvent[K, V]{n.key, n.value, reason})
		}
	}
	c.items = make(map[K]*node[K, V])
	c.head = nil
	c.tail = nil
	c.size = 0
	c.mu.Unlock()

	c.fireCallbacks(events)
}

// --- Intrusive list operations (caller holds mu) ---

func (c *Cache[K, V]) listPushFront(n *node[K, V]) {
	n.prev = nil
	n.next = c.head
	if c.head != nil {
		c.head.prev = n
	}
	c.head = n
	if c.tail == nil {
		c.tail = n
	}
	c.size++
}

func (c *Cache[K, V]) listRemove(n *node[K, V]) {
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
	c.size--
}

func (c *Cache[K, V]) listMoveToFront(n *node[K, V]) {
	if c.head == n {
		return
	}
	c.listRemove(n)
	c.listPushFront(n)
}

// --- Internal helpers ---

func (c *Cache[K, V]) isExpired(n *node[K, V]) bool {
	return !n.expiresAt.IsZero() && time.Now().After(n.expiresAt)
}

func (c *Cache[K, V]) expireTime(now time.Time, ttl time.Duration) time.Time {
	if ttl > 0 {
		return now.Add(ttl)
	}
	if c.ttl > 0 {
		return now.Add(c.ttl)
	}
	return time.Time{}
}

// slideExpiration extends the entry's lifetime on activity. When the entry has
// an absolute expiry, the remaining window since the last access is preserved
// (sliding expiration). Otherwise a configured global TTL is applied.
func (c *Cache[K, V]) slideExpiration(n *node[K, V], now time.Time) {
	if !n.expiresAt.IsZero() {
		if remaining := n.expiresAt.Sub(n.accessedAt); remaining > 0 {
			n.expiresAt = now.Add(remaining)
		}
		return
	}
	if c.ttl > 0 {
		n.expiresAt = now.Add(c.ttl)
	}
}

// removeNodeLocked unlinks n and returns an eviction event when a callback is
// configured. The caller is responsible for updating eviction counters.
func (c *Cache[K, V]) removeNodeLocked(n *node[K, V], reason EvictionReason) *evictEvent[K, V] {
	delete(c.items, n.key)
	c.listRemove(n)
	if c.onEvict != nil {
		return &evictEvent[K, V]{n.key, n.value, reason}
	}
	return nil
}

// removeTailLocked removes the least-recently-used entry with reason
// [EvictionCapacity].
func (c *Cache[K, V]) removeTailLocked() *evictEvent[K, V] {
	if c.tail == nil {
		return nil
	}
	return c.removeNodeLocked(c.tail, EvictionCapacity)
}

func (c *Cache[K, V]) fireCallback(ev *evictEvent[K, V]) {
	if ev == nil || c.onEvict == nil {
		return
	}
	c.safeOnEvict(ev.key, ev.value, ev.reason)
}

func (c *Cache[K, V]) fireCallbacks(events []evictEvent[K, V]) {
	if len(events) == 0 || c.onEvict == nil {
		return
	}
	for i := range events {
		c.safeOnEvict(events[i].key, events[i].value, events[i].reason)
	}
}

// safeOnEvict invokes the eviction callback under [github.com/aasyanov/urx/panix]
// so user panics cannot corrupt the cache or crash the sweeper goroutine.
func (c *Cache[K, V]) safeOnEvict(key K, value V, reason EvictionReason) {
	_ = panix.SafeVoid(opOnEvict, func() error {
		c.onEvict(key, value, reason)
		return nil
	})
}

// --- Background cleanup ---

// cleanupTicker drives periodic expired-entry sweeps.
type cleanupTicker struct {
	ticker   *time.Ticker
	stop     chan struct{}
	stopOnce sync.Once
}

// Stop halts the ticker. It is safe to call multiple times.
func (ct *cleanupTicker) Stop() {
	ct.stopOnce.Do(func() {
		ct.ticker.Stop()
		close(ct.stop)
	})
}

func (c *Cache[K, V]) startCleanup(interval time.Duration) {
	ct := &cleanupTicker{
		ticker: time.NewTicker(interval),
		stop:   make(chan struct{}),
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
