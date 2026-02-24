// Package fallx provides fallback patterns for graceful degradation in
// industrial Go services.
//
// A [Fallback] wraps a primary operation and returns an alternative result
// when the primary fails. Three strategies are available:
//
//   - [StrategyStatic] — returns a fixed value on failure.
//   - [StrategyFunc] — calls a user-supplied function on failure.
//   - [StrategyCached] — caches successful results and replays them on failure.
//
// Static fallback:
//
//	fb := fallx.New[string](
//	    fallx.WithStatic("service unavailable"),
//	)
//	val, err := fb.Do(ctx, func(ctx context.Context) (string, error) {
//	    return callAPI(ctx)
//	})
//
// Cached fallback with TTL:
//
//	fb := fallx.New[Response](
//	    fallx.WithCached(5*time.Minute, 1000),
//	    fallx.WithKeyFunc(func(ctx context.Context) string {
//	        return ctxx.TraceID(ctx)
//	    }),
//	)
//	defer fb.Close()
//
package fallx

import (
	"container/heap"
	"context"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// --- Strategy ---

// Strategy defines which fallback approach to use.
type Strategy uint8

const (
	// StrategyStatic returns a predefined value on primary failure.
	StrategyStatic Strategy = iota
	// StrategyFunc calls a fallback function on primary failure.
	StrategyFunc
	// StrategyCached replays a previously cached successful result.
	StrategyCached
)

func (s Strategy) String() string {
	switch s {
	case StrategyStatic:
		return "static"
	case StrategyFunc:
		return "function"
	case StrategyCached:
		return "cached"
	default:
		return "unknown"
	}
}

// --- Configuration ---

type config[T any] struct {
	strategy    Strategy
	staticValue T
	fallbackFn  func(ctx context.Context, err error) (T, error)
	keyFn       func(ctx context.Context) string

	cacheTTL        time.Duration
	maxCacheSize    int
	shardCount      int
	cleanupInterval time.Duration

	onFallback func(err error, strategy Strategy)
}

func defaultConfig[T any]() config[T] {
	return config[T]{
		strategy:        StrategyStatic,
		cacheTTL:        5 * time.Minute,
		maxCacheSize:    100,
		shardCount:      16,
		cleanupInterval: 1 * time.Minute,
	}
}

// --- Options ---

// Option configures [New] behavior.
type Option[T any] func(*config[T])

// WithStatic configures [StrategyStatic] with the given value.
func WithStatic[T any](value T) Option[T] {
	return func(c *config[T]) {
		c.strategy = StrategyStatic
		c.staticValue = value
	}
}

// WithFunc configures [StrategyFunc] with the given fallback function.
// The function receives the original error and may return an alternative result.
func WithFunc[T any](fn func(ctx context.Context, err error) (T, error)) Option[T] {
	return func(c *config[T]) {
		if fn != nil {
			c.strategy = StrategyFunc
			c.fallbackFn = fn
		}
	}
}

// WithCached configures [StrategyCached] with the given TTL and max cache size.
// The cache stores successful primary results and replays them on failure.
// Call [Fallback.Close] when done to stop the background cleanup goroutine.
func WithCached[T any](ttl time.Duration, maxSize int) Option[T] {
	return func(c *config[T]) {
		c.strategy = StrategyCached
		if ttl > 0 {
			c.cacheTTL = ttl
			c.cleanupInterval = ttl / 2
		}
		if maxSize > 0 {
			c.maxCacheSize = maxSize
		}
	}
}

// WithKeyFunc sets a function that extracts a cache key from context.
// Only used with [StrategyCached]. If nil, all calls share a single "default" key.
func WithKeyFunc[T any](fn func(ctx context.Context) string) Option[T] {
	return func(c *config[T]) {
		c.keyFn = fn
	}
}

// WithShards sets the number of cache shards for reduced lock contention.
// Default: 16. Only used with [StrategyCached].
func WithShards[T any](n int) Option[T] {
	return func(c *config[T]) {
		if n > 0 {
			c.shardCount = n
		}
	}
}

// WithOnFallback registers a callback invoked each time the fallback path is taken.
func WithOnFallback[T any](fn func(err error, strategy Strategy)) Option[T] {
	return func(c *config[T]) {
		c.onFallback = fn
	}
}

// --- Fallback ---

// Fallback wraps a primary operation with a graceful degradation strategy.
// Use [New] to create.
type Fallback[T any] struct {
	cfg config[T]

	shards []*cacheShard[T]

	totalCalls      atomic.Int64
	primarySuccess  atomic.Int64
	fallbackUsed    atomic.Int64
	fallbackSuccess atomic.Int64
	fallbackFailed  atomic.Int64
	cacheHits       atomic.Int64
	cacheMisses     atomic.Int64
	cacheEvictions  atomic.Int64
	cacheSize       atomic.Int64

	evictMu sync.Mutex

	stopCleanup chan struct{}
	cleanupDone chan struct{}
	closed      atomic.Bool
}

// New creates a [Fallback] with the given options.
func New[T any](opts ...Option[T]) *Fallback[T] {
	cfg := defaultConfig[T]()
	for _, o := range opts {
		o(&cfg)
	}

	if cfg.shardCount > cfg.maxCacheSize && cfg.maxCacheSize > 0 {
		cfg.shardCount = max(1, cfg.maxCacheSize/4)
	}

	f := &Fallback[T]{
		cfg:    cfg,
		shards: make([]*cacheShard[T], cfg.shardCount),
	}

	for i := range f.shards {
		f.shards[i] = &cacheShard[T]{
			entries: make(map[string]*cacheEntry[T]),
			lru:     make(lruHeap[T], 0),
		}
	}

	if cfg.strategy == StrategyCached && cfg.cleanupInterval > 0 {
		f.stopCleanup = make(chan struct{})
		f.cleanupDone = make(chan struct{})
		go f.cleanupLoop()
	}

	return f
}

// Do runs primaryFn and, on failure, returns a fallback result.
//
// For [StrategyCached], successful primary results are cached automatically
// for later fallback use.
func (f *Fallback[T]) Do(ctx context.Context, primaryFn func(ctx context.Context) (T, error)) (T, error) {
	if f.closed.Load() {
		var zero T
		return zero, errClosed()
	}

	f.totalCalls.Add(1)

	key := "default"
	if f.cfg.keyFn != nil {
		key = f.cfg.keyFn(ctx)
	}

	var (
		result T
		err    error
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = errx.NewPanicError("fallx.Do", r)
			}
		}()
		result, err = primaryFn(ctx)
	}()

	if err == nil {
		f.primarySuccess.Add(1)
		if f.cfg.strategy == StrategyCached {
			f.cacheResult(key, result, f.cfg.cacheTTL)
		}
		return result, nil
	}

	return f.fallback(ctx, key, err)
}

// DoWithKey is like [Do] but uses an explicit cache key instead of extracting
// one from context via [WithKeyFunc]. Useful with [StrategyCached].
func (f *Fallback[T]) DoWithKey(ctx context.Context, key string, primaryFn func(ctx context.Context) (T, error)) (T, error) {
	if f.closed.Load() {
		var zero T
		return zero, errClosed()
	}

	f.totalCalls.Add(1)

	var (
		result T
		err    error
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				err = errx.NewPanicError("fallx.DoWithKey", r)
			}
		}()
		result, err = primaryFn(ctx)
	}()

	if err == nil {
		f.primarySuccess.Add(1)
		if f.cfg.strategy == StrategyCached {
			f.cacheResult(key, result, f.cfg.cacheTTL)
		}
		return result, nil
	}

	return f.fallback(ctx, key, err)
}

// Seed pre-populates the cache with a value for the given key.
// Only meaningful with [StrategyCached].
func (f *Fallback[T]) Seed(key string, value T) {
	f.cacheResult(key, value, f.cfg.cacheTTL)
}

// SeedWithTTL pre-populates the cache with a value and custom TTL.
func (f *Fallback[T]) SeedWithTTL(key string, value T, ttl time.Duration) {
	f.cacheResult(key, value, ttl)
}

// ClearCache removes all cached entries.
func (f *Fallback[T]) ClearCache() {
	for _, shard := range f.shards {
		shard.mu.Lock()
		shard.entries = make(map[string]*cacheEntry[T])
		shard.lru = make(lruHeap[T], 0)
		shard.mu.Unlock()
	}
	f.cacheSize.Store(0)
}

// Stats returns a snapshot of current statistics.
func (f *Fallback[T]) Stats() Stats {
	return Stats{
		TotalCalls:      f.totalCalls.Load(),
		PrimarySuccess:  f.primarySuccess.Load(),
		FallbackUsed:    f.fallbackUsed.Load(),
		FallbackSuccess: f.fallbackSuccess.Load(),
		FallbackFailed:  f.fallbackFailed.Load(),
		CacheHits:       f.cacheHits.Load(),
		CacheMisses:     f.cacheMisses.Load(),
		CacheSize:       int(f.cacheSize.Load()),
		CacheEvictions:  f.cacheEvictions.Load(),
	}
}

// ResetStats zeroes all counters. CacheSize is not affected.
func (f *Fallback[T]) ResetStats() {
	f.totalCalls.Store(0)
	f.primarySuccess.Store(0)
	f.fallbackUsed.Store(0)
	f.fallbackSuccess.Store(0)
	f.fallbackFailed.Store(0)
	f.cacheHits.Store(0)
	f.cacheMisses.Store(0)
	f.cacheEvictions.Store(0)
}

// Close stops the background cleanup goroutine for [StrategyCached].
// Safe to call multiple times or on non-cached strategies.
func (f *Fallback[T]) Close() {
	if f.closed.Swap(true) {
		return
	}
	if f.stopCleanup != nil {
		close(f.stopCleanup)
		<-f.cleanupDone
	}
}

// Stats holds fallback statistics.
type Stats struct {
	TotalCalls      int64 `json:"total_calls"`
	PrimarySuccess  int64 `json:"primary_success"`
	FallbackUsed    int64 `json:"fallback_used"`
	FallbackSuccess int64 `json:"fallback_success"`
	FallbackFailed  int64 `json:"fallback_failed"`
	CacheHits       int64 `json:"cache_hits"`
	CacheMisses     int64 `json:"cache_misses"`
	CacheSize       int   `json:"cache_size"`
	CacheEvictions  int64 `json:"cache_evictions"`
}

// --- Internal ---

func (f *Fallback[T]) fallback(ctx context.Context, key string, origErr error) (T, error) {
	var zero T

	f.fallbackUsed.Add(1)

	if f.cfg.onFallback != nil {
		f.cfg.onFallback(origErr, f.cfg.strategy)
	}

	switch f.cfg.strategy {
	case StrategyStatic:
		f.fallbackSuccess.Add(1)
		return f.cfg.staticValue, nil

	case StrategyFunc:
		if f.cfg.fallbackFn == nil {
			f.fallbackFailed.Add(1)
			return zero, errNoFunc()
		}

		var (
			result T
			err    error
		)
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = errx.NewPanicError("fallx.fallbackFn", r)
				}
			}()
			result, err = f.cfg.fallbackFn(ctx, origErr)
		}()
		if err != nil {
			f.fallbackFailed.Add(1)
			return zero, errFuncFailed(err)
		}
		f.fallbackSuccess.Add(1)
		return result, nil

	case StrategyCached:
		cached, found := f.getCachedResult(key)
		if found {
			f.cacheHits.Add(1)
			f.fallbackSuccess.Add(1)
			return cached, nil
		}
		f.cacheMisses.Add(1)
		f.fallbackFailed.Add(1)
		return zero, errNoCached(key)

	default:
		f.fallbackFailed.Add(1)
		return zero, errNoFunc()
	}
}

// --- Cache ---

type cacheShard[T any] struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry[T]
	lru     lruHeap[T]
}

type cacheEntry[T any] struct {
	key        string
	value      T
	expiresAt  time.Time
	lastAccess time.Time
	heapIndex  int
}

func (f *Fallback[T]) getShard(key string) *cacheShard[T] {
	h := fnv.New32a()
	h.Write([]byte(key))
	return f.shards[h.Sum32()%uint32(len(f.shards))]
}

func (f *Fallback[T]) cacheResult(key string, result T, ttl time.Duration) {
	shard := f.getShard(key)
	shard.mu.Lock()

	now := time.Now()

	if existing, exists := shard.entries[key]; exists {
		existing.value = result
		existing.expiresAt = now.Add(ttl)
		shard.lru.update(existing, now)
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
	shard.mu.Unlock()

	f.cacheSize.Add(1)

	if f.cfg.maxCacheSize > 0 && f.cacheSize.Load() > int64(f.cfg.maxCacheSize) {
		f.evictIfNeeded()
	}
}

func (f *Fallback[T]) getCachedResult(key string) (T, bool) {
	var zero T

	shard := f.getShard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	entry, exists := shard.entries[key]
	if !exists {
		return zero, false
	}

	now := time.Now()
	if now.After(entry.expiresAt) {
		delete(shard.entries, key)
		heap.Remove(&shard.lru, entry.heapIndex)
		f.cacheSize.Add(-1)
		f.cacheEvictions.Add(1)
		return zero, false
	}

	shard.lru.update(entry, now)
	return entry.value, true
}

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
		var (
			oldestEntry *cacheEntry[T]
			oldestShard *cacheShard[T]
		)

		for _, shard := range f.shards {
			shard.mu.RLock()
			if entry := shard.lru.peek(); entry != nil {
				if oldestEntry == nil || entry.lastAccess.Before(oldestEntry.lastAccess) {
					oldestEntry = entry
					oldestShard = shard
				}
			}
			shard.mu.RUnlock()
		}

		if oldestShard == nil {
			break
		}

		oldestShard.mu.Lock()
		if existing, exists := oldestShard.entries[oldestEntry.key]; exists && existing == oldestEntry {
			delete(oldestShard.entries, oldestEntry.key)
			heap.Remove(&oldestShard.lru, oldestEntry.heapIndex)
			f.cacheSize.Add(-1)
			f.cacheEvictions.Add(1)
		}
		oldestShard.mu.Unlock()
	}
}

func (f *Fallback[T]) evictExpired(shard *cacheShard[T], now time.Time) {
	var expired []string
	for k, v := range shard.entries {
		if now.After(v.expiresAt) {
			expired = append(expired, k)
		}
	}
	for _, k := range expired {
		if entry, exists := shard.entries[k]; exists {
			delete(shard.entries, k)
			heap.Remove(&shard.lru, entry.heapIndex)
			f.cacheSize.Add(-1)
			f.cacheEvictions.Add(1)
		}
	}
}

// --- LRU heap ---

type lruHeap[T any] []*cacheEntry[T]

func (h lruHeap[T]) Len() int            { return len(h) }
func (h lruHeap[T]) Less(i, j int) bool  { return h[i].lastAccess.Before(h[j].lastAccess) }
func (h lruHeap[T]) Swap(i, j int)       { h[i], h[j] = h[j], h[i]; h[i].heapIndex = i; h[j].heapIndex = j }
// Push implements [heap.Interface].
func (h *lruHeap[T]) Push(x any) { e := x.(*cacheEntry[T]); e.heapIndex = len(*h); *h = append(*h, e) }

// Pop implements [heap.Interface].
func (h *lruHeap[T]) Pop() any { old := *h; n := len(old); e := old[n-1]; old[n-1] = nil; e.heapIndex = -1; *h = old[:n-1]; return e }
func (h lruHeap[T]) peek() *cacheEntry[T] { if len(h) == 0 { return nil }; return h[0] }

func (h *lruHeap[T]) update(entry *cacheEntry[T], t time.Time) {
	entry.lastAccess = t
	heap.Fix(h, entry.heapIndex)
}

// --- Background cleanup ---

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
