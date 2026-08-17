package fallx

import (
	"container/heap"
	"sync"
	"time"
)

// FNV-1a 32-bit constants. The hash is computed inline (rather than via
// hash/fnv) so shard selection on the cache hot path takes no interface boxing,
// no constructor call, and no []byte conversion of the key.
const (
	fnvOffset32 = 2166136261
	fnvPrime32  = 16777619
)

// cacheShard is one independently-locked partition of the result cache. Spreading
// keys across shards lets concurrent callers cache results without serializing on
// a single mutex. The lru heap orders live entries by last access so the coldest
// entry can be evicted in O(log n) when the cache is over capacity.
type cacheShard[T any] struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry[T]
	lru     lruHeap[T]
}

// initCache allocates the sharded result cache. It runs only under
// [StrategyCached], from [New] before any concurrent use.
func (f *Fallback[T]) initCache() {
	if len(f.shards) > 0 {
		return
	}
	f.shards = make([]*cacheShard[T], f.cfg.shardCount)
	for i := range f.shards {
		f.shards[i] = newCacheShard[T]()
	}
}

// newCacheShard returns an empty, ready-to-use shard.
func newCacheShard[T any]() *cacheShard[T] {
	return &cacheShard[T]{
		entries: make(map[string]*cacheEntry[T]),
		lru:     make(lruHeap[T], 0),
	}
}

// clear drops all entries in the shard.
func (s *cacheShard[T]) clear() {
	s.mu.Lock()
	s.entries = make(map[string]*cacheEntry[T])
	s.lru = make(lruHeap[T], 0)
	s.mu.Unlock()
}

// cacheEntry is one cached primary result with its expiry and LRU bookkeeping.
// heapIndex is maintained by the heap so an entry can be removed in place.
type cacheEntry[T any] struct {
	key        string
	value      T
	expiresAt  time.Time
	lastAccess time.Time
	heapIndex  int
}

// getShard maps a key to its shard via an inline FNV-1a hash, giving a stable,
// well-distributed assignment with zero allocation and no interface dispatch.
func (f *Fallback[T]) getShard(key string) *cacheShard[T] {
	var h uint32 = fnvOffset32
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= fnvPrime32
	}
	return f.shards[h%uint32(len(f.shards))]
}

// cacheResult stores or refreshes the value for key. An existing entry is
// updated in place and its access time bumped; a new entry is inserted and, if
// the cache is now over capacity, eviction is triggered outside the shard lock.
func (f *Fallback[T]) cacheResult(key string, result T, ttl time.Duration) {
	if f.cfg.clone != nil {
		result = f.cfg.clone(result)
	}
	shard := f.getShard(key)
	shard.mu.Lock()

	now := time.Now()
	if existing, ok := shard.entries[key]; ok {
		existing.value = result
		existing.expiresAt = now.Add(ttl)
		shard.lru.touch(existing, now)
		shard.mu.Unlock()
		return
	}

	entry := &cacheEntry[T]{
		key:        key,
		value:      result,
		expiresAt:  now.Add(ttl),
		lastAccess: now,
		heapIndex:  -1,
	}
	shard.entries[key] = entry
	heap.Push(&shard.lru, entry)
	f.cacheSize.Add(1)
	shard.mu.Unlock()

	if f.cfg.maxCacheSize > 0 && f.cacheSize.Load() > int64(f.cfg.maxCacheSize) {
		f.evictIfNeeded()
	}
}

// getCachedResult returns the live value for key, if any. An expired entry is
// removed and reported as a miss so a stale value is never replayed.
func (f *Fallback[T]) getCachedResult(key string) (T, bool) {
	var zero T
	shard := f.getShard(key)
	shard.mu.Lock()

	entry, ok := shard.entries[key]
	if !ok {
		shard.mu.Unlock()
		return zero, false
	}
	now := time.Now()
	if now.After(entry.expiresAt) {
		shard.remove(entry)
		f.cacheSize.Add(-1)
		f.cacheEvictions.Add(1)
		shard.mu.Unlock()
		return zero, false
	}
	shard.lru.touch(entry, now)
	val := entry.value
	clone := f.cfg.clone
	shard.mu.Unlock()
	if clone != nil {
		val = clone(val)
	}
	return val, true
}

// evictIfNeeded brings the cache back under capacity. It first sweeps expired
// entries across all shards, then repeatedly removes the globally coldest live
// entry until the size is within bounds. The evictMu lock serializes concurrent
// evictions so two callers do not over-evict past the target.
func (f *Fallback[T]) evictIfNeeded() {
	f.evictMu.Lock()
	defer f.evictMu.Unlock()

	if f.cfg.maxCacheSize <= 0 || f.cacheSize.Load() <= int64(f.cfg.maxCacheSize) {
		return
	}

	now := time.Now()
	for _, shard := range f.shards {
		shard.mu.Lock()
		f.evictExpired(shard, now)
		shard.mu.Unlock()
	}

	for f.cacheSize.Load() > int64(f.cfg.maxCacheSize) {
		oldest, shard := f.coldestEntry()
		if shard == nil {
			f.syncCacheSize()
			break
		}
		shard.mu.Lock()
		evicted := false
		if cur, ok := shard.entries[oldest.key]; ok && cur == oldest {
			shard.remove(oldest)
			f.cacheSize.Add(-1)
			f.cacheEvictions.Add(1)
			evicted = true
		}
		shard.mu.Unlock()
		if !evicted {
			// Stale LRU heap entry or counter drift; resync rather than spin.
			f.syncCacheSize()
			break
		}
	}
}

// coldestEntry finds the least-recently-accessed live entry across all shards by
// peeking each shard's LRU root. It returns the entry and its shard, or nil when
// the cache is empty.
func (f *Fallback[T]) coldestEntry() (*cacheEntry[T], *cacheShard[T]) {
	var (
		oldest *cacheEntry[T]
		owner  *cacheShard[T]
	)
	for _, shard := range f.shards {
		shard.mu.RLock()
		if e := shard.lru.peek(); e != nil {
			if oldest == nil || e.lastAccess.Before(oldest.lastAccess) {
				oldest, owner = e, shard
			}
		}
		shard.mu.RUnlock()
	}
	return oldest, owner
}

// evictExpired removes every expired entry from shard. The caller holds the
// shard lock. It must collect keys before deleting so the map is not mutated
// while ranging.
func (f *Fallback[T]) evictExpired(shard *cacheShard[T], now time.Time) {
	var expired []*cacheEntry[T]
	for _, e := range shard.entries {
		if now.After(e.expiresAt) {
			expired = append(expired, e)
		}
	}
	for _, e := range expired {
		shard.remove(e)
		f.cacheSize.Add(-1)
		f.cacheEvictions.Add(1)
	}
}

// remove deletes entry from the shard map and the LRU heap. The caller holds the
// shard lock.
func (s *cacheShard[T]) remove(entry *cacheEntry[T]) {
	delete(s.entries, entry.key)
	if entry.heapIndex >= 0 && entry.heapIndex < len(s.lru) {
		heap.Remove(&s.lru, entry.heapIndex)
	}
}

// syncCacheSize recomputes the live entry count from the shard maps and stores
// it in cacheSize. Used after ClearCache and as a safety net when the counter
// and maps could have diverged.
func (f *Fallback[T]) syncCacheSize() int64 {
	var n int64
	for _, shard := range f.shards {
		shard.mu.RLock()
		n += int64(len(shard.entries))
		shard.mu.RUnlock()
	}
	f.cacheSize.Store(n)
	return n
}

// cleanupLoop periodically sweeps expired entries from every shard until Close
// signals it to stop. It runs only under [StrategyCached] when
// [WithCleanupInterval] is positive.
func (f *Fallback[T]) cleanupLoop() {
	defer close(f.cleanupDone)
	ticker := time.NewTicker(f.cfg.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-f.stopCleanup:
			return
		case <-ticker.C:
			now := time.Now()
			for _, shard := range f.shards {
				shard.mu.Lock()
				f.evictExpired(shard, now)
				shard.mu.Unlock()
			}
		}
	}
}

// lruHeap is a min-heap of cache entries ordered by last access time; the root
// is the coldest entry. It implements [heap.Interface].
type lruHeap[T any] []*cacheEntry[T]

func (h lruHeap[T]) Len() int           { return len(h) }
func (h lruHeap[T]) Less(i, j int) bool { return h[i].lastAccess.Before(h[j].lastAccess) }

func (h lruHeap[T]) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].heapIndex = i
	h[j].heapIndex = j
}

// Push implements [heap.Interface].
func (h *lruHeap[T]) Push(x any) {
	e := x.(*cacheEntry[T])
	e.heapIndex = len(*h)
	*h = append(*h, e)
}

// Pop implements [heap.Interface].
func (h *lruHeap[T]) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	e.heapIndex = -1
	*h = old[:n-1]
	return e
}

// peek returns the coldest entry without removing it, or nil when empty.
func (h lruHeap[T]) peek() *cacheEntry[T] {
	if len(h) == 0 {
		return nil
	}
	return h[0]
}

// touch updates an entry's access time and re-establishes the heap order.
func (h *lruHeap[T]) touch(entry *cacheEntry[T], t time.Time) {
	entry.lastAccess = t
	heap.Fix(h, entry.heapIndex)
}
