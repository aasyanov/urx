// Package quotax provides per-key rate limiting for production Go services.
//
// A [Quota] maintains an independent token-bucket rate limiter for each key
// (user ID, IP address, API key, tenant, ...). Keys are distributed across
// shards to spread lock contention, and inactive keys are evicted automatically
// by a background sweeper so memory does not grow without bound. The package
// offers three layers, from lowest to highest level:
//
//   - [Quota.Allow] / [Quota.AllowN]: non-blocking checks that consume tokens
//     from a key's bucket when available.
//   - [Quota.Wait] / [Quota.WaitN]: block until a key's tokens are available or
//     the context is done.
//   - [Execute] / [TryExecute]: run a function under a key's bucket with panic
//     recovery, handing the callback a [QuotaController].
//
//	q := quotax.New(
//	    quotax.WithRate(100),
//	    quotax.WithBurst(20),
//	    quotax.WithMaxKeys(100_000),
//	)
//	defer q.Close()
//
//	resp, err := quotax.Execute(q, ctx, "user:123",
//	    func(ctx context.Context, qc quotax.QuotaController) (*Response, error) {
//	        if qc.Tokens() < 5 {
//	            return cheapResponse(ctx) // degrade while this key is near its limit
//	        }
//	        return handler.Serve(ctx, req)
//	    })
//
// The callback receives a [QuotaController] exposing the key, its remaining
// tokens, the configured rate and burst, whether the call waited, and a
// [QuotaController.SkipToken] method to refund the token for no-op calls.
//
// Each callback is wrapped with [github.com/aasyanov/urx/panix]: a panicking
// function becomes a [*panix.PanicError] rather than crashing the process.
//
// # Dependencies
//
// quotax depends only on the Go standard library and the urx ratex and panix
// packages.
package quotax

import (
	"context"
	"errors"
	"hash/maphash"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/panix"
	"github.com/aasyanov/urx/ratex"
)

const (
	// minWaitDelay is the shortest backoff [Quota.WaitN] sleeps between token
	// availability checks, preventing a busy-spin when the computed delay
	// rounds down to zero.
	minWaitDelay = time.Millisecond

	// nanosPerSecond converts a fractional token deficit divided by the rate
	// (in seconds) into a [time.Duration].
	nanosPerSecond = float64(time.Second)

	// opExecute labels panics recovered while running an [Execute] callback.
	opExecute = "quotax.Execute"

	// opTryExecute labels panics recovered while running a [TryExecute]
	// callback.
	opTryExecute = "quotax.TryExecute"
)

// hashSeed is process-global so every shard lookup hashes keys consistently.
// maphash is collision-resistant and faster than fnv for string keys, and it
// allocates nothing per call.
var hashSeed = maphash.MakeSeed()

// Quota provides per-key rate limiting backed by an independent [ratex.Limiter]
// per key. Create one with [New]; check with [Quota.Allow] or [Quota.AllowN],
// block with [Quota.Wait] or [Quota.WaitN], run functions with [Execute] or
// [TryExecute], and release resources with [Quota.Close].
//
// A Quota is safe for concurrent use from multiple goroutines. Buckets for
// inactive keys are evicted by a background sweeper started in [New] and stopped
// by [Quota.Close].
type Quota struct {
	cfg config

	shards   []shard
	keyCount atomic.Int64

	allowed atomic.Int64
	limited atomic.Int64

	stopEviction chan struct{}
	evictionDone chan struct{}
	closed       atomic.Bool
}

// shard owns a disjoint subset of keys behind its own lock, so concurrent
// access to different shards never contends.
type shard struct {
	mu      sync.RWMutex
	buckets map[string]*bucket
}

// bucket pairs a key's token-bucket limiter with the last-access timestamp the
// sweeper reads to decide eviction.
type bucket struct {
	limiter    *ratex.Limiter
	lastAccess atomic.Int64
}

func (b *bucket) touch() { b.lastAccess.Store(time.Now().UnixNano()) }

// New creates a [Quota] with the given options applied on top of sensible
// defaults ([DefaultRate] req/s, burst [DefaultBurst], [DefaultShards] shards)
// and starts a background eviction goroutine. Call [Quota.Close] to stop it.
func New(opts ...Option) *Quota {
	cfg := newConfig(opts)

	q := &Quota{
		cfg:          cfg,
		shards:       make([]shard, cfg.shards),
		stopEviction: make(chan struct{}),
		evictionDone: make(chan struct{}),
	}
	for i := range q.shards {
		q.shards[i].buckets = make(map[string]*bucket)
	}
	go q.evictLoop()
	return q
}

// --- Non-blocking checks ---

// Allow reports whether one request for the given key is admitted right now,
// consuming one token from the key's bucket on success. It returns false once
// the limiter is closed or the [WithMaxKeys] cap blocks a new key.
func (q *Quota) Allow(key string) bool {
	return q.AllowN(key, 1)
}

// AllowN reports whether n requests for the given key are admitted right now. It
// consumes n tokens from the key's bucket on success; on failure no tokens are
// consumed. It returns false once the limiter is closed or the [WithMaxKeys]
// cap blocks a new key. Values of n < 1 are treated as 1 by the underlying
// bucket.
func (q *Quota) AllowN(key string, n int) bool {
	if q.closed.Load() {
		return false
	}

	s := q.shardFor(key)
	if b := q.lookup(s, key); b != nil {
		return q.record(b.limiter.AllowN(n))
	}

	b := q.getOrCreate(s, key)
	if b == nil {
		q.limited.Add(1)
		return false
	}
	return q.record(b.limiter.AllowN(n))
}

// AllowOrError is like [Quota.Allow] but returns an error on rejection: it
// returns [ErrClosed] if the limiter is closed, [ErrMaxKeys] if the
// [WithMaxKeys] cap blocks a new key, or [ErrLimited] if the key's bucket is
// empty. It returns nil when the request is admitted.
func (q *Quota) AllowOrError(key string) error {
	if q.closed.Load() {
		return ErrClosed
	}

	s := q.shardFor(key)
	b := q.lookup(s, key)
	if b == nil {
		b = q.getOrCreate(s, key)
		if b == nil {
			q.limited.Add(1)
			return errMaxKeys(key)
		}
	}
	if q.record(b.limiter.Allow()) {
		return nil
	}
	return errLimited(key)
}

// --- Blocking checks ---

// Wait blocks until one token for the given key is available or ctx is done. It
// returns nil once a token has been consumed, [ErrClosed] if the limiter is
// closed, [ErrMaxKeys] if the [WithMaxKeys] cap blocks a new key, or
// [ErrCancelled] wrapping ctx.Err() if the context is cancelled first.
func (q *Quota) Wait(ctx context.Context, key string) error {
	return q.WaitN(ctx, key, 1)
}

// WaitN blocks until n tokens for the given key are available or ctx is done. It
// returns nil once the tokens have been consumed, [ErrClosed] if the limiter is
// closed, [ErrMaxKeys] if the [WithMaxKeys] cap blocks a new key, or
// [ErrCancelled] wrapping ctx.Err() if the context is cancelled first.
//
// A request for more than the per-key burst can never be satisfied; WaitN
// blocks until ctx is cancelled in that case.
func (q *Quota) WaitN(ctx context.Context, key string, n int) error {
	if q.closed.Load() {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return errCancelled(err)
	}

	b, err := q.bucketForWait(key)
	if err != nil {
		return err
	}

	for {
		if err := ctx.Err(); err != nil {
			q.limited.Add(1)
			return errCancelled(err)
		}

		// Refresh the access stamp each iteration so a key blocked for longer
		// than the eviction TTL is never swept out from under an active waiter.
		b.touch()
		if b.limiter.AllowN(n) {
			if err := ctx.Err(); err != nil {
				b.limiter.Release(float64(n))
				q.limited.Add(1)
				return errCancelled(err)
			}
			q.allowed.Add(1)
			return nil
		}

		timer := time.NewTimer(q.waitDelay(b, n))
		select {
		case <-ctx.Done():
			stopTimer(timer)
			q.limited.Add(1)
			return errCancelled(ctx.Err())
		case <-timer.C:
			if err := ctx.Err(); err != nil {
				q.limited.Add(1)
				return errCancelled(err)
			}
		}
	}
}

// stopTimer stops t and drains its channel when the timer already fired, so
// the runtime can reclaim the timer goroutine promptly.
func stopTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}

// bucketForWait resolves the key's bucket for a blocking call, creating it if
// absent. Both [Quota.lookup] (on hit) and [Quota.getOrCreate] (on create)
// already refresh the access timestamp, so no extra touch is needed here. It
// returns [ErrMaxKeys] if a new key cannot be admitted under the [WithMaxKeys]
// cap.
func (q *Quota) bucketForWait(key string) (*bucket, error) {
	s := q.shardFor(key)
	b := q.lookup(s, key)
	if b == nil {
		b = q.getOrCreate(s, key)
		if b == nil {
			q.limited.Add(1)
			return nil, errMaxKeys(key)
		}
	}
	return b, nil
}

// --- Execution ---

// Execute admits one request for the given key, blocking until a token is
// available or ctx is done, then runs fn under panic recovery. Because Go
// methods cannot have type parameters, Execute is a package-level generic
// function taking the [Quota] as its first argument.
//
// On admission fn receives the original ctx and a [QuotaController] exposing the
// key, its remaining tokens, the configured rate and burst, whether the call
// waited, and a [QuotaController.SkipToken] hook to refund the token for no-op
// calls. The callback is wrapped with [github.com/aasyanov/urx/panix]; a panic
// becomes a [*panix.PanicError].
//
// Execute returns [ErrNilFunc] if fn is nil, [ErrClosed] if the limiter is
// closed, [ErrMaxKeys] if the [WithMaxKeys] cap blocks a new key, or
// [ErrCancelled] wrapping ctx.Err() if the context is cancelled before a token
// is acquired.
func Execute[T any](q *Quota, ctx context.Context, key string, fn func(ctx context.Context, qc QuotaController) (T, error)) (T, error) {
	var zero T
	if fn == nil {
		return zero, ErrNilFunc
	}
	if q.closed.Load() {
		return zero, ErrClosed
	}

	b, err := q.bucketForWait(key)
	if err != nil {
		return zero, err
	}

	val, err := ratex.Execute(b.limiter, ctx, adapt(key, fn))
	q.recordExecute(err)
	return val, normalizeErr(ctx, err, opExecute)
}

// TryExecute attempts to run fn for the given key without blocking. If a token
// is immediately available the function executes and TryExecute returns
// (true, val, err). If no token is available it returns (false, zero, nil)
// without executing fn.
//
// It returns (false, zero, [ErrNilFunc]) if fn is nil, (false, zero,
// [ErrClosed]) if the limiter is closed, and (false, zero, [ErrMaxKeys]) if the
// [WithMaxKeys] cap blocks a new key.
func TryExecute[T any](q *Quota, ctx context.Context, key string, fn func(ctx context.Context, qc QuotaController) (T, error)) (bool, T, error) {
	var zero T
	if fn == nil {
		return false, zero, ErrNilFunc
	}
	if q.closed.Load() {
		return false, zero, ErrClosed
	}

	b, err := q.bucketForWait(key)
	if err != nil {
		return false, zero, err
	}

	ok, val, err := ratex.TryExecute(b.limiter, ctx, adapt(key, fn))
	if !ok {
		q.limited.Add(1)
		return false, zero, normalizeErr(ctx, err, opTryExecute)
	}
	q.recordExecute(err)
	return true, val, normalizeErr(ctx, err, opTryExecute)
}

// adapt converts a quotax callback into a ratex callback, wrapping the inner
// [ratex.RateController] in a [QuotaController] that adds the key. Token refund
// via [QuotaController.SkipToken] is delegated straight to ratex, so a skipped
// call is correctly returned to the key's bucket. It is a package-level generic
// function because Go methods cannot have type parameters.
func adapt[T any](key string, fn func(ctx context.Context, qc QuotaController) (T, error)) func(ctx context.Context, rc ratex.RateController) (T, error) {
	return func(ctx context.Context, rc ratex.RateController) (T, error) {
		return fn(ctx, &execution{
			key:    key,
			tokens: rc.Tokens(),
			rate:   rc.Rate(),
			burst:  rc.Burst(),
			waited: rc.Waited(),
			inner:  rc,
		})
	}
}

// normalizeErr maps a delegated ratex error back into quotax's own vocabulary so
// callers compare against a single package's sentinels and see correct panic
// attribution:
//
//   - [ratex.ErrCancelled] becomes quotax's [ErrCancelled], preserving the
//     wrapped cause (ctx.Err()).
//   - a [*panix.PanicError] raised by the callback is re-tagged from ratex's op
//     ("ratex.Execute") to the quotax op so the panic points at this package.
//
// Any other error (including a callback-originated one) passes through
// unchanged.
func normalizeErr(ctx context.Context, err error, op string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ratex.ErrCancelled) {
		cause := ctx.Err()
		if cause == nil {
			cause = context.Canceled
		}
		return errCancelled(cause)
	}
	var pe *panix.PanicError
	if errors.As(err, &pe) {
		pe.Op = op
	}
	return err
}

// recordExecute attributes the outcome of a ratex execution to the aggregate
// counters. A context-cancellation error counts as limited; anything else
// (success or a callback-originated error) counts as allowed, mirroring ratex's
// own outcome accounting at the per-key level.
func (q *Quota) recordExecute(err error) {
	if errors.Is(err, ratex.ErrCancelled) {
		q.limited.Add(1)
		return
	}
	q.allowed.Add(1)
}

// --- Key management ---

// Remove deletes the bucket for a key, discarding its accrued tokens. It
// reports whether the key existed.
func (q *Quota) Remove(key string) bool {
	s := q.shardFor(key)
	s.mu.Lock()
	_, exists := s.buckets[key]
	if exists {
		delete(s.buckets, key)
		q.keyCount.Add(-1)
	}
	s.mu.Unlock()
	return exists
}

// Exists reports whether a bucket is currently tracked for the given key.
func (q *Quota) Exists(key string) bool {
	s := q.shardFor(key)
	s.mu.RLock()
	_, exists := s.buckets[key]
	s.mu.RUnlock()
	return exists
}

// KeyCount returns the number of currently tracked keys.
func (q *Quota) KeyCount() int64 {
	return q.keyCount.Load()
}

// Reset removes all tracked keys, discarding every bucket. Counters are left
// untouched; use [Quota.ResetStats] for those.
func (q *Quota) Reset() {
	for i := range q.shards {
		q.shards[i].mu.Lock()
		q.shards[i].buckets = make(map[string]*bucket)
		q.shards[i].mu.Unlock()
	}
	q.keyCount.Store(0)
}

// --- Statistics ---

// Stats returns a snapshot of aggregate limiter statistics across all keys.
func (q *Quota) Stats() Stats {
	return Stats{
		Keys:    q.keyCount.Load(),
		Allowed: q.allowed.Load(),
		Limited: q.limited.Load(),
	}
}

// ResetStats zeroes the allowed and limited counters. The key count is not
// affected.
func (q *Quota) ResetStats() {
	q.allowed.Store(0)
	q.limited.Store(0)
}

// --- Lifecycle ---

// Close stops the background eviction goroutine and marks the limiter closed:
// subsequent admission calls return false or [ErrClosed]. Close blocks until
// the sweeper has exited. It is idempotent and always returns nil; the error
// return satisfies the common closer contract used across urx.
func (q *Quota) Close() error {
	if q.closed.Swap(true) {
		return nil
	}
	close(q.stopEviction)
	<-q.evictionDone
	return nil
}

// IsClosed reports whether [Quota.Close] has been called.
func (q *Quota) IsClosed() bool {
	return q.closed.Load()
}

// --- Internal ---

// shardFor returns the shard owning the given key.
func (q *Quota) shardFor(key string) *shard {
	h := maphash.String(hashSeed, key)
	return &q.shards[h%uint64(len(q.shards))]
}

// lookup returns the existing bucket for key under a read lock, refreshing its
// access timestamp, or nil if the key is not tracked.
func (q *Quota) lookup(s *shard, key string) *bucket {
	s.mu.RLock()
	b := s.buckets[key]
	s.mu.RUnlock()
	if b != nil {
		b.touch()
	}
	return b
}

// record attributes a single admission outcome to the aggregate counters and
// returns the outcome unchanged for fluent use.
func (q *Quota) record(ok bool) bool {
	if ok {
		q.allowed.Add(1)
	} else {
		q.limited.Add(1)
	}
	return ok
}

// getOrCreate returns the bucket for key, creating it under the write lock if
// absent. It returns nil when a new key would exceed the [WithMaxKeys] cap,
// invoking the [WithOnMaxKeys] callback in that case.
func (q *Quota) getOrCreate(s *shard, key string) *bucket {
	s.mu.Lock()

	if b, exists := s.buckets[key]; exists {
		s.mu.Unlock()
		b.touch()
		return b
	}

	if !q.reserveKey() {
		s.mu.Unlock()
		if q.cfg.onMaxKeys != nil {
			q.cfg.onMaxKeys(key)
		}
		return nil
	}

	b := &bucket{
		limiter: ratex.New(ratex.WithRate(q.cfg.rate), ratex.WithBurst(q.cfg.burst)),
	}
	b.touch()
	s.buckets[key] = b
	s.mu.Unlock()
	return b
}

// reserveKey atomically claims one slot in the key budget, reporting whether
// the claim succeeded. Unlimited budgets always succeed; bounded budgets use a
// compare-and-swap loop so the cap is never exceeded under concurrency.
func (q *Quota) reserveKey() bool {
	if q.cfg.maxKeys == unlimitedKeys {
		q.keyCount.Add(1)
		return true
	}
	for {
		cur := q.keyCount.Load()
		if cur >= q.cfg.maxKeys {
			return false
		}
		if q.keyCount.CompareAndSwap(cur, cur+1) {
			return true
		}
	}
}

// waitDelay returns the estimated time until n tokens accrue in the bucket,
// with a [minWaitDelay] floor so the wait loop never busy-spins.
func (q *Quota) waitDelay(b *bucket, n int) time.Duration {
	deficit := float64(n) - b.limiter.Tokens()
	if deficit <= 0 {
		return minWaitDelay
	}
	d := time.Duration(deficit / q.cfg.rate * nanosPerSecond)
	if d < minWaitDelay {
		return minWaitDelay
	}
	return d
}

// --- Eviction ---

// evictLoop runs the background sweeper until [Quota.Close] signals it to stop.
func (q *Quota) evictLoop() {
	defer close(q.evictionDone)
	ticker := time.NewTicker(q.cfg.evictionInterval)
	defer ticker.Stop()

	for {
		select {
		case <-q.stopEviction:
			return
		case <-ticker.C:
			q.evict()
		}
	}
}

// ForceEviction runs one eviction pass immediately, removing every key inactive
// for longer than the configured TTL. It is primarily a testing hook; the
// background sweeper handles eviction in normal operation.
func (q *Quota) ForceEviction() {
	q.evict()
}

// evict removes buckets whose last access predates the TTL cutoff. It scans each
// shard under a read lock to collect stale keys, then re-checks each under the
// write lock before deleting, so a key touched between the two phases survives.
func (q *Quota) evict() {
	cutoff := time.Now().Add(-q.cfg.evictionTTL).UnixNano()

	for i := range q.shards {
		s := &q.shards[i]

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
				q.keyCount.Add(-1)
			}
		}
		s.mu.Unlock()
	}
}
