// Package fallx provides fallback strategies for graceful degradation in
// production Go services.
//
// A [Fallback] wraps a primary operation and produces an alternative result
// when the primary fails, turning a hard failure into a degraded-but-useful
// response. One of three strategies is fixed at construction:
//
//   - [StrategyStatic] — return a fixed value (configured by [WithStatic]).
//   - [StrategyFunc] — call a fallback function (configured by [WithFunc]).
//   - [StrategyCached] — replay the last successful result (configured by
//     [WithCached]).
//
//	fb := fallx.New(fallx.WithStatic("service unavailable"))
//
//	val, err := fallx.Execute(fb, ctx,
//	    func(ctx context.Context, fc fallx.FallController) (string, error) {
//	        return callAPI(ctx)
//	    })
//
// Because Go methods cannot have type parameters, the primary entry points are
// the package-level generic functions [Execute] and [ExecuteWithKey], each
// taking the [Fallback] as their first argument. The callback receives a
// [FallController] exposing the active strategy, the resolved cache key, and —
// on the fallback path under [StrategyFunc] — the primary error.
//
// Each callback runs under [github.com/aasyanov/urx/panix]: a panicking
// function becomes a [*panix.PanicError] instead of crashing the process and is
// treated as a primary (or fallback) failure.
//
// # Dependencies
//
// fallx depends only on the Go standard library and the urx panix package.
package fallx

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/panix"
)

// Fallback wraps a primary operation with a graceful-degradation strategy fixed
// at construction. Create one with [New], run work with the package-level
// [Execute] or [ExecuteWithKey], inspect counters with [Fallback.Stats], and —
// under [StrategyCached] — release the background cleanup goroutine with
// [Fallback.Close].
//
// A Fallback is safe for concurrent use from any number of goroutines: counters
// are lock-free atomics and the cache is sharded under per-shard mutexes.
type Fallback[T any] struct {
	cfg config[T]

	shards []*cacheShard[T]

	calls           atomic.Int64
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

// New creates a [Fallback] with the given options applied on top of the package
// defaults. With no strategy option it defaults to [StrategyStatic] returning
// the zero value of T. Invalid options are clamped, so New never returns an
// unusable fallback. Under [StrategyCached] it starts a background goroutine
// that sweeps expired entries; call [Fallback.Close] to stop it.
func New[T any](opts ...Option[T]) *Fallback[T] {
	cfg := newConfig(opts)

	f := &Fallback[T]{
		cfg:    cfg,
		shards: make([]*cacheShard[T], cfg.shardCount),
	}
	for i := range f.shards {
		f.shards[i] = newCacheShard[T]()
	}

	if cfg.strategy == StrategyCached && cfg.cleanupInterval > 0 {
		f.stopCleanup = make(chan struct{})
		f.cleanupDone = make(chan struct{})
		go f.cleanupLoop()
	}
	return f
}

// Strategy returns the strategy this fallback was built with.
func (f *Fallback[T]) Strategy() Strategy { return f.cfg.strategy }

// Execute runs primaryFn and, if it fails, produces a fallback result according
// to the configured [Strategy]. Because Go methods cannot be generic, Execute
// is a package-level function taking the [Fallback] as its first argument. The
// cache key is resolved from [WithKeyFunc] (or [DefaultKey] when unset).
//
// Execute returns [ErrClosed] if the fallback is closed and [ErrNilFunc] if
// primaryFn is nil. When the primary succeeds its value is returned and, under
// [StrategyCached], cached for the key. When it fails the result depends on the
// strategy: [StrategyStatic] returns the configured value; [StrategyFunc] runs
// the fallback function (returning [ErrFallbackFailed] on its error, or
// [ErrNoFunc] if none is configured); [StrategyCached] returns the cached value
// or [ErrNoCached]. Each callback runs under [panix.Safe].
func Execute[T any](f *Fallback[T], ctx context.Context, primaryFn PrimaryFunc[T]) (T, error) {
	return ExecuteWithKey(f, ctx, f.resolveKey(ctx), primaryFn)
}

// ExecuteWithKey is [Execute] with an explicit cache key, bypassing
// [WithKeyFunc]. Use it under [StrategyCached] when the key is known at the call
// site (a user ID, a query hash) rather than derivable from the context. The
// key is otherwise ignored by [StrategyStatic] and [StrategyFunc].
func ExecuteWithKey[T any](f *Fallback[T], ctx context.Context, key string, primaryFn PrimaryFunc[T]) (T, error) {
	var zero T
	if f.closed.Load() {
		return zero, ErrClosed
	}
	if primaryFn == nil {
		return zero, ErrNilFunc
	}

	f.calls.Add(1)

	fc := &execution{strategy: f.cfg.strategy, key: key}
	result, err := panix.Safe(f.cfg.opOrDefault(), func() (T, error) {
		return primaryFn(ctx, fc)
	})
	if err == nil {
		f.primarySuccess.Add(1)
		if f.cfg.strategy == StrategyCached {
			f.cacheResult(key, result, f.cfg.cacheTTL)
		}
		return result, nil
	}

	return f.fallback(ctx, fc, err)
}

// fallback produces the degraded result for a failed primary attempt. It runs
// the [WithOnFallback] hook, then dispatches on the configured strategy. The
// controller fc is reused for the fallback path with its onFallback flag set
// and err populated so the [WithFunc] callback can inspect the cause.
func (f *Fallback[T]) fallback(ctx context.Context, fc *execution, primaryErr error) (T, error) {
	var zero T
	f.fallbackUsed.Add(1)

	if f.cfg.onFallback != nil {
		f.cfg.onFallback(primaryErr, f.cfg.strategy)
	}

	fc.onFallback = true
	fc.err = primaryErr

	switch f.cfg.strategy {
	case StrategyStatic:
		f.fallbackSuccess.Add(1)
		return f.cfg.staticValue, nil

	case StrategyFunc:
		if f.cfg.fallbackFn == nil {
			f.fallbackFailed.Add(1)
			return zero, ErrNoFunc
		}
		result, err := panix.Safe(opFallback, func() (T, error) {
			return f.cfg.fallbackFn(ctx, fc)
		})
		if err != nil {
			f.fallbackFailed.Add(1)
			return zero, errFallbackFailed(err)
		}
		f.fallbackSuccess.Add(1)
		return result, nil

	case StrategyCached:
		if cached, found := f.getCachedResult(fc.key); found {
			f.cacheHits.Add(1)
			f.fallbackSuccess.Add(1)
			return cached, nil
		}
		f.cacheMisses.Add(1)
		f.fallbackFailed.Add(1)
		return zero, errNoCached(fc.key)

	default:
		f.fallbackFailed.Add(1)
		return zero, ErrNoFunc
	}
}

// resolveKey derives the cache key for a call: the [WithKeyFunc] result when
// configured, otherwise [DefaultKey].
func (f *Fallback[T]) resolveKey(ctx context.Context) string {
	if f.cfg.keyFn != nil {
		return f.cfg.keyFn(ctx)
	}
	return DefaultKey
}

// Seed pre-populates the cache with value for key using the configured TTL.
// Meaningful only under [StrategyCached]; use it to warm the cache so the very
// first failure already has a fallback to replay. After [Fallback.Close], Seed
// is a no-op so the cache snapshot remains inspectable via [Fallback.Stats].
func (f *Fallback[T]) Seed(key string, value T) {
	if f.closed.Load() {
		return
	}
	f.cacheResult(key, value, f.cfg.cacheTTL)
}

// SeedWithTTL is [Fallback.Seed] with an explicit entry lifetime. After
// [Fallback.Close] it is a no-op.
func (f *Fallback[T]) SeedWithTTL(key string, value T, ttl time.Duration) {
	if f.closed.Load() {
		return
	}
	if ttl <= 0 {
		ttl = f.cfg.cacheTTL
	}
	f.cacheResult(key, value, ttl)
}

// ClearCache removes every cached entry and resets the cache size to zero. It
// does not affect counters or the closed state. After [Fallback.Close] it is a
// no-op so cached entries remain readable via [Fallback.Stats].
func (f *Fallback[T]) ClearCache() {
	if f.closed.Load() {
		return
	}
	f.evictMu.Lock()
	defer f.evictMu.Unlock()
	for _, shard := range f.shards {
		shard.clear()
	}
	f.syncCacheSize()
}

// Stats returns a snapshot of fallback statistics. It is safe to call
// concurrently with [Execute]; counters are read independently and may reflect
// a call in progress.
func (f *Fallback[T]) Stats() Stats {
	return Stats{
		Calls:           f.calls.Load(),
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

// ResetStats zeroes the cumulative counters. It does not affect the cache
// contents, the reported cache size, or the closed state.
func (f *Fallback[T]) ResetStats() {
	f.calls.Store(0)
	f.primarySuccess.Store(0)
	f.fallbackUsed.Store(0)
	f.fallbackSuccess.Store(0)
	f.fallbackFailed.Store(0)
	f.cacheHits.Store(0)
	f.cacheMisses.Store(0)
	f.cacheEvictions.Store(0)
}

// Close stops the background cleanup goroutine started for [StrategyCached] and
// marks the fallback closed: subsequent [Execute] and [ExecuteWithKey] calls
// return [ErrClosed], and [Fallback.Seed], [Fallback.SeedWithTTL], and
// [Fallback.ClearCache] become no-ops. Close is idempotent and always returns
// nil; the cache contents are left intact for inspection via [Fallback.Stats].
// It is safe to call on non-cached strategies (a no-op beyond setting the
// closed flag).
func (f *Fallback[T]) Close() error {
	if f.closed.Swap(true) {
		return nil
	}
	if f.stopCleanup != nil {
		close(f.stopCleanup)
		<-f.cleanupDone
	}
	return nil
}

// IsClosed reports whether [Fallback.Close] has been called.
func (f *Fallback[T]) IsClosed() bool {
	return f.closed.Load()
}
