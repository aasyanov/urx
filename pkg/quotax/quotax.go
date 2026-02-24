// Package quotax provides per-key rate limiting for industrial Go services.
//
// A [Limiter] maintains independent token-bucket rate limiters for each key
// (user ID, IP address, API key, etc.). Keys are distributed across shards
// to reduce lock contention. Inactive keys are evicted automatically by a
// background goroutine.
//
//	ql := quotax.New(
//	    quotax.WithRate(100),
//	    quotax.WithBurst(20),
//	    quotax.WithMaxKeys(100_000),
//	)
//	defer ql.Close()
//
//	if ql.Allow("user:123") {
//	    // handle request
//	}
package quotax

import (
	"context"
	"hash"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/pkg/ratex"
)

var fnvPool = sync.Pool{
	New: func() any { return fnv.New32a() },
}

// --- Configuration ---

type config struct {
	rate  float64
	burst int

	shards          int
	maxKeys         int64
	evictionTTL     time.Duration
	evictionInterval time.Duration

	onMaxKeys func(key string)
}

func defaultConfig() config {
	return config{
		rate:             10,
		burst:            20,
		shards:           64,
		maxKeys:          0,
		evictionTTL:      15 * time.Minute,
		evictionInterval: 1 * time.Minute,
	}
}

// --- Options ---

// Option configures [New] behavior.
type Option func(*config)

// WithRate sets the sustained rate in requests per second for each key.
// Values <= 0 are ignored. Default: 10.
func WithRate(r float64) Option {
	return func(c *config) {
		if r > 0 {
			c.rate = r
		}
	}
}

// WithBurst sets the token bucket capacity (burst size) for each key.
// Values <= 0 are ignored. Default: 20.
func WithBurst(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.burst = n
		}
	}
}

// WithShards sets the number of internal shards to reduce lock contention.
// Values <= 0 are ignored. Default: 64.
func WithShards(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.shards = n
		}
	}
}

// WithMaxKeys sets the maximum number of tracked keys. When reached, new keys
// are rejected and [WithOnMaxKeys] is called. 0 means unlimited. Default: 0.
func WithMaxKeys(n int64) Option {
	return func(c *config) {
		if n >= 0 {
			c.maxKeys = n
		}
	}
}

// WithEvictionTTL sets how long an inactive key is kept before eviction.
// Values <= 0 are ignored. Default: 15 minutes.
func WithEvictionTTL(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.evictionTTL = d
		}
	}
}

// WithEvictionInterval sets how often the background eviction runs.
// Values <= 0 are ignored. Default: 1 minute.
func WithEvictionInterval(d time.Duration) Option {
	return func(c *config) {
		if d > 0 {
			c.evictionInterval = d
		}
	}
}

// WithOnMaxKeys registers a callback invoked when a new key is rejected
// because [WithMaxKeys] limit is reached.
func WithOnMaxKeys(fn func(key string)) Option {
	return func(c *config) {
		c.onMaxKeys = fn
	}
}

// --- Limiter ---

// Limiter provides per-key rate limiting backed by individual token-bucket
// instances from [ratex]. Create with [New]; call [Limiter.Close] when done
// to stop background eviction.
type Limiter struct {
	cfg config

	shards   []shard
	keyCount atomic.Int64

	totalAllowed atomic.Int64
	totalLimited atomic.Int64

	stopEviction chan struct{}
	evictionDone chan struct{}
	closed       atomic.Bool
}

type shard struct {
	mu      sync.RWMutex
	buckets map[string]*bucket
}

type bucket struct {
	limiter    *ratex.Limiter
	lastAccess atomic.Int64
}

func (b *bucket) touch() { b.lastAccess.Store(time.Now().UnixNano()) }

// New creates a [Limiter] and starts a background eviction goroutine.
func New(opts ...Option) *Limiter {
	cfg := defaultConfig()
	for _, o := range opts {
		o(&cfg)
	}

	l := &Limiter{
		cfg:          cfg,
		shards:       make([]shard, cfg.shards),
		stopEviction: make(chan struct{}),
		evictionDone: make(chan struct{}),
	}
	for i := range l.shards {
		l.shards[i].buckets = make(map[string]*bucket)
	}
	go l.evictLoop()
	return l
}

// --- Public API ---

// Allow reports whether one request for the given key is allowed right now.
func (l *Limiter) Allow(key string) bool {
	return l.AllowN(key, 1)
}

// AllowN reports whether n requests for the given key are allowed.
func (l *Limiter) AllowN(key string, n int) bool {
	if l.closed.Load() {
		return false
	}

	s := l.shardFor(key)

	s.mu.RLock()
	b, exists := s.buckets[key]
	s.mu.RUnlock()

	if exists {
		b.touch()
		ok := b.limiter.AllowN(n)
		if ok {
			l.totalAllowed.Add(1)
		} else {
			l.totalLimited.Add(1)
		}
		return ok
	}

	return l.slowAllowN(s, key, n)
}

// AllowOrError is like [Allow] but returns a structured [errx.Error] on rejection.
func (l *Limiter) AllowOrError(key string) error {
	if l.closed.Load() {
		return errClosed()
	}
	if !l.Allow(key) {
		return errLimited(key)
	}
	return nil
}

// Wait blocks until one token for the given key is available or ctx is done.
func (l *Limiter) Wait(ctx context.Context, key string) error {
	return l.WaitN(ctx, key, 1)
}

// WaitN blocks until n tokens for the given key are available or ctx is done.
func (l *Limiter) WaitN(ctx context.Context, key string, n int) error {
	if l.closed.Load() {
		return errClosed()
	}

	for {
		if err := ctx.Err(); err != nil {
			return errCancelled(err)
		}

		s := l.shardFor(key)

		s.mu.RLock()
		b, exists := s.buckets[key]
		s.mu.RUnlock()

		if !exists {
			b = l.getOrCreate(s, key)
			if b == nil {
				return errMaxKeys(key)
			}
		}

		b.touch()
		if b.limiter.AllowN(n) {
			l.totalAllowed.Add(1)
			return nil
		}

		delay := l.tokenDelay(b, n)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errCancelled(ctx.Err())
		case <-timer.C:
			timer.Stop()
		}
	}
}

// Remove deletes the bucket for a key. Returns true if the key existed.
func (l *Limiter) Remove(key string) bool {
	s := l.shardFor(key)
	s.mu.Lock()
	_, exists := s.buckets[key]
	if exists {
		delete(s.buckets, key)
		l.keyCount.Add(-1)
	}
	s.mu.Unlock()
	return exists
}

// Exists reports whether a bucket exists for the given key.
func (l *Limiter) Exists(key string) bool {
	s := l.shardFor(key)
	s.mu.RLock()
	_, exists := s.buckets[key]
	s.mu.RUnlock()
	return exists
}

// KeyCount returns the total number of tracked keys.
func (l *Limiter) KeyCount() int64 {
	return l.keyCount.Load()
}

// Reset removes all tracked keys.
func (l *Limiter) Reset() {
	for i := range l.shards {
		l.shards[i].mu.Lock()
		l.shards[i].buckets = make(map[string]*bucket)
		l.shards[i].mu.Unlock()
	}
	l.keyCount.Store(0)
}

// Stats returns a snapshot of limiter statistics.
func (l *Limiter) Stats() Stats {
	return Stats{
		Keys:    l.keyCount.Load(),
		Allowed: l.totalAllowed.Load(),
		Limited: l.totalLimited.Load(),
	}
}

// ResetStats zeroes counters. KeyCount is not affected.
func (l *Limiter) ResetStats() {
	l.totalAllowed.Store(0)
	l.totalLimited.Store(0)
}

// Close stops background eviction. Safe to call multiple times.
func (l *Limiter) Close() {
	if l.closed.Swap(true) {
		return
	}
	close(l.stopEviction)
	<-l.evictionDone
}

// ForceEviction runs one eviction pass immediately. Useful for testing.
func (l *Limiter) ForceEviction() {
	l.evict()
}

// Stats holds a point-in-time snapshot of per-key limiter counters.
type Stats struct {
	Keys    int64 `json:"keys"`
	Allowed int64 `json:"allowed"`
	Limited int64 `json:"limited"`
}

// --- Internal ---

func (l *Limiter) shardFor(key string) *shard {
	h := fnvPool.Get().(hash.Hash32)
	h.Reset()
	h.Write([]byte(key))
	idx := h.Sum32() % uint32(len(l.shards))
	fnvPool.Put(h)
	return &l.shards[idx]
}

func (l *Limiter) slowAllowN(s *shard, key string, n int) bool {
	b := l.getOrCreate(s, key)
	if b == nil {
		l.totalLimited.Add(1)
		return false
	}
	b.touch()
	ok := b.limiter.AllowN(n)
	if ok {
		l.totalAllowed.Add(1)
	} else {
		l.totalLimited.Add(1)
	}
	return ok
}

func (l *Limiter) getOrCreate(s *shard, key string) *bucket {
	s.mu.Lock()

	if b, exists := s.buckets[key]; exists {
		s.mu.Unlock()
		return b
	}

	if l.cfg.maxKeys > 0 {
		for {
			cur := l.keyCount.Load()
			if cur >= l.cfg.maxKeys {
				s.mu.Unlock()
				if l.cfg.onMaxKeys != nil {
					l.cfg.onMaxKeys(key)
				}
				return nil
			}
			if l.keyCount.CompareAndSwap(cur, cur+1) {
				break
			}
		}
	} else {
		l.keyCount.Add(1)
	}

	b := &bucket{
		limiter: ratex.New(ratex.WithRate(l.cfg.rate), ratex.WithBurst(l.cfg.burst)),
	}
	b.touch()
	s.buckets[key] = b
	s.mu.Unlock()
	return b
}

func (l *Limiter) tokenDelay(b *bucket, n int) time.Duration {
	tokens := b.limiter.Tokens()
	deficit := float64(n) - tokens
	if deficit <= 0 {
		return 0
	}
	d := time.Duration(deficit / l.cfg.rate * float64(time.Second))
	if d < time.Millisecond {
		d = time.Millisecond
	}
	return d
}

// --- Eviction ---

func (l *Limiter) evictLoop() {
	defer close(l.evictionDone)
	ticker := time.NewTicker(l.cfg.evictionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopEviction:
			return
		case <-ticker.C:
			l.evict()
		}
	}
}

func (l *Limiter) evict() {
	cutoff := time.Now().Add(-l.cfg.evictionTTL).UnixNano()

	for i := range l.shards {
		s := &l.shards[i]

		s.mu.RLock()
		var stale []string
		for k, b := range s.buckets {
			if b.lastAccess.Load() < cutoff {
				stale = append(stale, k)
			}
		}
		s.mu.RUnlock()

		if len(stale) == 0 {
			continue
		}

		s.mu.Lock()
		for _, k := range stale {
			if b, exists := s.buckets[k]; exists && b.lastAccess.Load() < cutoff {
				delete(s.buckets, k)
				l.keyCount.Add(-1)
			}
		}
		s.mu.Unlock()
	}
}
