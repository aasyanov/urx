// Package adaptx provides adaptive concurrency limiting for industrial Go
// services.
//
// A [Limiter] discovers a backend's safe concurrency on its own. It starts at a
// configured limit and continuously moves that limit up or down from latency
// and error feedback, using one of three control laws — [AIMD], [Vegas], or
// [Gradient]. Where a static bulkhead (see bulkx) must be sized by hand to a
// fixed guess, an adaptive limiter tracks capacity as it changes: it opens up
// when the backend is fast and healthy, and clamps down the moment latency
// climbs or errors appear, so callers wait (or are turned away) instead of
// piling onto a struggling backend.
//
// # Quick Start
//
//	l := adaptx.New(
//	    adaptx.WithAlgorithm(adaptx.Gradient),
//	    adaptx.WithInitialLimit(10),
//	)
//	defer l.Close()
//
//	rows, err := adaptx.Execute(l, ctx,
//	    func(ctx context.Context, ac adaptx.AdaptController) (*sql.Rows, error) {
//	        if ac.InFlight() > ac.Limit()/2 {
//	            return db.QueryContext(ctx, simpleSQL) // shed load near saturation
//	        }
//	        return db.QueryContext(ctx, complexSQL)
//	    })
//
// The callback receives an [AdaptController] exposing the limit and in-flight
// count at admission and an [AdaptController.SkipSample] method to keep outlier
// latencies out of the feedback signal. For tracked admission without a
// callback, use [Limiter.Acquire] and call the returned release function.
//
// Each callback is wrapped with [github.com/aasyanov/urx/panix] for panic
// recovery; a panicking function yields a [*panix.PanicError] instead of
// crashing the process, and the in-flight slot is always released.
//
// # Dependencies
//
// adaptx depends only on the Go standard library and the urx panix package.
package adaptx

import (
	"cmp"
	"context"
	"math"
	"math/rand/v2"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aasyanov/urx/panix"
)

// Limiter is a thread-safe adaptive concurrency limiter. Create one with [New],
// run work with [Execute] or admit it manually with [Limiter.Acquire], inspect
// counters with [Limiter.Stats], and release resources with [Limiter.Close].
//
// It is safe for concurrent use from multiple goroutines. Admission rides a
// buffered-channel semaphore; only the periodic adaptation step and the
// percentile snapshot take the mutex.
type Limiter struct {
	cfg config

	// sem is the permit semaphore. Its buffer is the maximum limit; the number
	// of buffered values is the count of permits currently available to acquire.
	sem chan struct{}

	inFlight atomic.Int32
	closed   atomic.Bool
	closeOnce sync.Once
	closedCh  chan struct{}

	total    atomic.Int64
	success  atomic.Int64
	fail     atomic.Int64
	rejected atomic.Int64
	incr     atomic.Int64
	decr     atomic.Int64

	// mu guards every field below: the live limit, the shrink debt, the latency
	// estimators, and the sample ring buffer.
	mu     sync.Mutex
	limit  int
	debt   int
	avgLat float64
	minLat float64

	samples []sample
	head    int
	count   int
	seen    int
	ringCap int
}

// New creates a [Limiter] with the given options applied on top of the package
// defaults ([AIMD], initial limit [DefaultInitialLimit], bounds
// [DefaultMinLimit]–[DefaultMaxLimit]). Invalid options are ignored and
// cross-field invariants are enforced, so New never returns an unusable limiter.
func New(opts ...Option) *Limiter {
	cfg := newConfig(opts)
	ringCap := cfg.ringCapacity()

	l := &Limiter{
		cfg:      cfg,
		sem:      make(chan struct{}, cfg.maxLimit),
		closedCh: make(chan struct{}),
		limit:    cfg.initialLimit,
		minLat:   math.MaxFloat64,
		samples:  make([]sample, ringCap),
		ringCap:  ringCap,
	}
	for range cfg.initialLimit {
		l.sem <- struct{}{}
	}
	return l
}

// --- Admission ---

// Acquire blocks until a permit is available, the context is cancelled, or the
// limiter is closed. It returns a release function that MUST be called exactly
// once with the operation outcome and measured latency; the release function is
// idempotent, so extra calls are no-ops.
//
// Returns [ErrClosed] if the limiter has been closed, [ErrTimeout] if the
// context deadline is exceeded while waiting, or [ErrCancelled] if the context
// is cancelled. Acquire is the building block for code that cannot use the
// callback form of [Execute]; the caller owns the returned release function and
// must invoke it to free the permit and feed the adaptive algorithm.
func (l *Limiter) Acquire(ctx context.Context) (release func(success bool, latency time.Duration), err error) {
	if l.closed.Load() {
		return nil, ErrClosed
	}
	// Context takes priority: a pre-cancelled context never consumes a permit,
	// even when one is immediately available (select would pick randomly).
	if err := ctx.Err(); err != nil {
		l.rejected.Add(1)
		return nil, l.ctxErr(err)
	}
	select {
	case <-ctx.Done():
		l.rejected.Add(1)
		return nil, l.ctxErr(ctx.Err())
	case <-l.closedCh:
		l.rejected.Add(1)
		return nil, ErrClosed
	case <-l.sem:
	}
	if l.closed.Load() {
		l.returnPermit()
		l.rejected.Add(1)
		return nil, ErrClosed
	}
	return l.admit(), nil
}

// TryAcquire attempts to take a permit without blocking. It returns the release
// function and true on success, or (nil, false) when no permit is immediately
// available or the limiter is closed. The release function MUST be called
// exactly once on success and is idempotent.
func (l *Limiter) TryAcquire() (release func(success bool, latency time.Duration), ok bool) {
	if l.closed.Load() {
		return nil, false
	}
	select {
	case <-l.sem:
		if l.closed.Load() {
			l.returnPermit()
			l.rejected.Add(1)
			return nil, false
		}
		return l.admit(), true
	default:
		l.rejected.Add(1)
		return nil, false
	}
}

// returnPermit puts one permit back into the semaphore without touching debt
// or counters. Used when a permit was taken but admission is aborted.
func (l *Limiter) returnPermit() {
	select {
	case l.sem <- struct{}{}:
	default:
	}
}

// admit finalizes a successful permit grab: it bumps the in-flight and total
// counters and returns an idempotent release closure that records the outcome
// and returns the permit (paying down shrink debt first).
func (l *Limiter) admit() func(success bool, latency time.Duration) {
	l.inFlight.Add(1)
	l.total.Add(1)
	var released atomic.Bool
	return func(success bool, latency time.Duration) {
		if !released.CompareAndSwap(false, true) {
			return
		}
		l.record(success, latency)
		l.inFlight.Add(-1)
		l.releasePermit()
	}
}

// releasePermit returns one permit to the semaphore unless the limiter has
// shrunk and owes a permit drop, in which case the debt is paid by retiring
// the permit instead of returning it. This is how a multiplicative decrease
// actually removes capacity: held permits are reclaimed as they are released.
//
// The debt check and the permit send happen under the same lock that
// [Limiter.adjust] takes, so a release racing a concurrent shrink cannot return
// a permit the shrink already accounted for — the live permit pool never
// transiently exceeds the limit. The send is non-blocking because the buffer is
// the max limit, which the live count never reaches.
func (l *Limiter) releasePermit() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.debt > 0 {
		l.debt--
		return
	}
	select {
	case l.sem <- struct{}{}:
	default:
	}
}

// --- Execution ---

// Execute admits one operation and runs fn under panic recovery. Because Go
// methods cannot have type parameters, Execute is a package-level generic
// function taking the [Limiter] as its first argument; it is the recommended
// way to use the limiter.
//
// Execute blocks for a permit exactly as [Limiter.Acquire] does and reports the
// same admission errors: [ErrClosed], [ErrTimeout], or [ErrCancelled]. It
// returns [ErrNilFunc] if fn is nil. On admission the permit is held for the
// duration of fn and released even if fn panics — the callback runs under
// [panix.Safe], so a panic becomes a [*panix.PanicError]. The call's latency
// and outcome feed the adaptive algorithm unless the callback invokes
// [AdaptController.SkipSample].
func Execute[T any](l *Limiter, ctx context.Context, fn func(ctx context.Context, ac AdaptController) (T, error)) (T, error) {
	var zero T
	if fn == nil {
		return zero, ErrNilFunc
	}
	release, err := l.Acquire(ctx)
	if err != nil {
		return zero, err
	}

	ac := &execution{
		limit:     l.Limit(),
		inFlight:  l.InFlight() - 1,
		algorithm: l.cfg.algorithm,
	}

	start := time.Now()
	val, err := panix.Safe(l.cfg.opOrDefault(), func() (T, error) {
		return fn(ctx, ac)
	})

	if ac.isSkipped() {
		release(true, 0)
	} else {
		release(err == nil, time.Since(start))
	}
	return val, err
}

// TryExecute runs fn only if a permit is immediately available, without
// blocking. It returns (true, val, err) when fn ran and (false, zero, nil) when
// no permit was free. Returns (false, zero, [ErrClosed]) if the limiter is
// closed and (false, zero, [ErrNilFunc]) if fn is nil. The permit is released
// when fn returns or panics.
func TryExecute[T any](l *Limiter, ctx context.Context, fn func(ctx context.Context, ac AdaptController) (T, error)) (bool, T, error) {
	var zero T
	if fn == nil {
		return false, zero, ErrNilFunc
	}
	release, ok := l.TryAcquire()
	if !ok {
		if l.closed.Load() {
			return false, zero, ErrClosed
		}
		return false, zero, nil
	}

	ac := &execution{
		limit:     l.Limit(),
		inFlight:  l.InFlight() - 1,
		algorithm: l.cfg.algorithm,
	}

	start := time.Now()
	val, err := panix.Safe(l.cfg.opOrDefault(), func() (T, error) {
		return fn(ctx, ac)
	})

	if ac.isSkipped() {
		release(true, 0)
	} else {
		release(err == nil, time.Since(start))
	}
	return true, val, err
}

// --- Queries ---

// Limit returns the current adaptive concurrency limit.
func (l *Limiter) Limit() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.limit
}

// InFlight returns the number of operations currently admitted and running.
func (l *Limiter) InFlight() int {
	return int(l.inFlight.Load())
}

// --- Lifecycle ---

// CloseWithTimeout shuts the limiter down, waiting up to timeout for in-flight
// operations to drain before returning. Subsequent [Limiter.Acquire],
// [Limiter.TryAcquire], [Execute], and [TryExecute] calls return [ErrClosed].
// A zero or negative timeout returns immediately without waiting. Close is
// idempotent: the second and later calls return [ErrClosed].
func (l *Limiter) CloseWithTimeout(timeout time.Duration) error {
	if !l.closed.CompareAndSwap(false, true) {
		return ErrClosed
	}
	l.closeOnce.Do(func() { close(l.closedCh) })
	if timeout > 0 {
		deadline := time.Now().Add(timeout)
		for l.inFlight.Load() > 0 && time.Now().Before(deadline) {
			time.Sleep(drainPollInterval)
		}
	}
	for {
		select {
		case <-l.sem:
		default:
			return nil
		}
	}
}

// Close shuts the limiter down, waiting up to [DefaultCloseTimeout] for
// in-flight operations to drain. For a custom timeout use
// [Limiter.CloseWithTimeout]. Close is idempotent.
func (l *Limiter) Close() error {
	return l.CloseWithTimeout(DefaultCloseTimeout)
}

// IsClosed reports whether [Limiter.Close] has been called.
func (l *Limiter) IsClosed() bool {
	return l.closed.Load()
}

const (
	// DefaultCloseTimeout is how long [Limiter.Close] waits for in-flight work
	// to drain before retiring the remaining permits.
	DefaultCloseTimeout = 30 * time.Second

	// drainPollInterval is how often [Limiter.CloseWithTimeout] re-checks the
	// in-flight count while waiting for a drain.
	drainPollInterval = 10 * time.Millisecond
)

// --- Statistics ---

// Stats holds a point-in-time snapshot of limiter counters and latency
// percentiles computed over the configured sample window.
type Stats struct {
	Algorithm string        `json:"algorithm"`
	Limit     int           `json:"limit"`
	MinLimit  int           `json:"min_limit"`
	MaxLimit  int           `json:"max_limit"`
	InFlight  int           `json:"in_flight"`
	Total     int64         `json:"total"`
	Success   int64         `json:"success"`
	Failures  int64         `json:"failures"`
	Rejected  int64         `json:"rejected"`
	Increases int64         `json:"increases"`
	Decreases int64         `json:"decreases"`
	AvgLat    time.Duration `json:"avg_latency"`
	MinLat    time.Duration `json:"min_latency"`
	MaxLat    time.Duration `json:"max_latency"`
	P50Lat    time.Duration `json:"p50_latency"`
	P99Lat    time.Duration `json:"p99_latency"`
}

const (
	// p50 and p99 are the percentile ranks reported in [Stats].
	p50 = 0.50
	p99 = 0.99
)

// Stats returns a snapshot of limiter statistics. Latency percentiles are
// computed over the samples recorded within the configured sample window; with
// no recent samples the latency fields are zero.
func (l *Limiter) Stats() Stats {
	l.mu.Lock()
	lim := l.limit
	lats := make([]time.Duration, 0, l.count)
	cutoff := time.Now().Add(-l.cfg.sampleWindow)
	for i := 0; i < l.count; i++ {
		idx := (l.head - l.count + i + l.ringCap) % l.ringCap
		s := l.samples[idx]
		if s.ts.After(cutoff) {
			lats = append(lats, s.latency)
		}
	}
	l.mu.Unlock()

	var totalLat, minL, maxL, avgL, q50, q99 time.Duration
	if len(lats) > 0 {
		minL = lats[0]
		for _, d := range lats {
			totalLat += d
			if d < minL {
				minL = d
			}
			if d > maxL {
				maxL = d
			}
		}
		avgL = totalLat / time.Duration(len(lats))
		slices.SortFunc(lats, cmp.Compare)
		q50 = lats[int(float64(len(lats)-1)*p50)]
		q99 = lats[int(float64(len(lats)-1)*p99)]
	}

	return Stats{
		Algorithm: l.cfg.algorithm.String(),
		Limit:     lim,
		MinLimit:  l.cfg.minLimit,
		MaxLimit:  l.cfg.maxLimit,
		InFlight:  int(l.inFlight.Load()),
		Total:     l.total.Load(),
		Success:   l.success.Load(),
		Failures:  l.fail.Load(),
		Rejected:  l.rejected.Load(),
		Increases: l.incr.Load(),
		Decreases: l.decr.Load(),
		AvgLat:    avgL,
		MinLat:    minL,
		MaxLat:    maxL,
		P50Lat:    q50,
		P99Lat:    q99,
	}
}

// ResetStats zeroes the cumulative counters and resets the adaptive state back
// to the initial limit, clearing the latency estimators and sample history. It
// does not affect the in-flight count or the closed state. When in-flight work
// exceeds the configured initial limit the live limit is raised to that count
// so permits never go negative; the permit pool is reconciled immediately.
func (l *Limiter) ResetStats() {
	l.mu.Lock()
	resetLimit := l.cfg.initialLimit
	if inFlight := int(l.inFlight.Load()); resetLimit < inFlight {
		resetLimit = inFlight
	}
	l.limit = resetLimit
	l.debt = 0
	l.avgLat = 0
	l.minLat = math.MaxFloat64
	l.head = 0
	l.count = 0
	l.seen = 0
	for i := range l.samples {
		l.samples[i] = sample{}
	}
	l.reconcilePermitsLocked()
	l.mu.Unlock()

	l.total.Store(0)
	l.success.Store(0)
	l.fail.Store(0)
	l.rejected.Store(0)
	l.incr.Store(0)
	l.decr.Store(0)
}

// --- Internal ---

// ctxErr maps a context error to the corresponding adaptx sentinel.
func (l *Limiter) ctxErr(err error) error {
	if err == context.DeadlineExceeded {
		return errTimeout(err)
	}
	return errCancelled(err)
}

// record updates the counters, latency estimators, and ring buffer for one
// completed operation, then runs the adaptation step once warmup has elapsed.
// A zero latency marks a skipped sample: it counts toward success/failure
// totals but is excluded from the latency feedback and percentile history.
func (l *Limiter) record(success bool, latency time.Duration) {
	if success {
		l.success.Add(1)
	} else {
		l.fail.Add(1)
	}
	if latency <= 0 {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.samples[l.head] = sample{latency: latency, ts: time.Now(), success: success}
	l.head = (l.head + 1) % l.ringCap
	if l.count < l.ringCap {
		l.count++
	}
	l.seen++

	ns := float64(latency.Nanoseconds())
	l.avgLat = l.cfg.smoothing*ns + (1-l.cfg.smoothing)*l.avgLat
	if l.cfg.minLatDecay > 0 && l.minLat != math.MaxFloat64 {
		l.minLat += l.cfg.minLatDecay * (l.avgLat - l.minLat)
	}
	if ns > 0 && ns < l.minLat {
		l.minLat = ns
	}

	if l.seen < l.cfg.warmupSamples {
		return
	}
	l.adjust(success, latency)
}

// adjust runs the configured control law to compute the next limit, applies
// jitter and the [min, max] clamp, then reconciles the permit pool: a growth
// pushes new permits, a shrink records debt so released permits are retired.
// Callers hold l.mu.
func (l *Limiter) adjust(success bool, latency time.Duration) {
	old := l.limit
	var next int
	switch l.cfg.algorithm {
	case Vegas:
		next = l.vegas(latency)
	case Gradient:
		next = l.gradient(success, latency)
	default:
		next = l.aimd(success)
	}

	if next > old && l.cfg.jitter > 0 {
		withheld := int(float64(next-old) * l.cfg.jitter * rand.Float64())
		if rand.Float64() < jitterCoinFlip {
			next -= withheld
		}
	}
	if next < l.cfg.minLimit {
		next = l.cfg.minLimit
	}
	if next > l.cfg.maxLimit {
		next = l.cfg.maxLimit
	}
	if next == old {
		return
	}

	l.limit = next
	if next > old {
		l.incr.Add(1)
		grant := next - old
		if l.debt > 0 {
			paid := min(l.debt, grant)
			l.debt -= paid
			grant -= paid
		}
		for range grant {
			select {
			case l.sem <- struct{}{}:
			default:
			}
		}
	} else {
		l.decr.Add(1)
		l.shrink(old - next)
	}
	if l.cfg.onLimitChange != nil {
		go l.cfg.onLimitChange(old, next)
	}
}

// shrink removes drop permits from circulation: it first pulls any idle permits
// straight out of the semaphore, then records the remainder as debt to be paid
// by the next releases. Callers hold l.mu.
func (l *Limiter) shrink(drop int) {
	for i := 0; i < drop; i++ {
		select {
		case <-l.sem:
		default:
			l.debt += drop - i
			return
		}
	}
}

// aimd is the Additive Increase / Multiplicative Decrease control law.
// Callers hold l.mu.
func (l *Limiter) aimd(success bool) int {
	if success {
		return l.limit + int(l.cfg.increaseRate)
	}
	return int(float64(l.limit) * l.cfg.decreaseRatio)
}

// vegas estimates queued work from the gap between observed and minimum RTT and
// steers the limit to keep the estimated queue inside a tolerance band centered
// on [WithTargetLatency]. Callers hold l.mu.
func (l *Limiter) vegas(latency time.Duration) int {
	if l.minLat == math.MaxFloat64 {
		return l.limit
	}
	rtt := float64(latency.Nanoseconds())
	minLat := l.minLat
	if rtt <= 0 || minLat <= 0 {
		return l.limit
	}
	queue := float64(l.limit) * (rtt - minLat) / minLat

	target := float64(l.limit) * l.cfg.tolerance
	if targetNs := float64(l.cfg.targetLatency.Nanoseconds()); targetNs > minLat {
		target = float64(l.limit) * l.cfg.tolerance * (targetNs - minLat) / minLat
	}

	if queue < target {
		return l.limit + 1
	}
	if queue > target*vegasBackoffBand {
		return int(float64(l.limit) * l.cfg.decreaseRatio)
	}
	return l.limit
}

// reconcilePermitsLocked drains the permit channel and refills it so the number
// of available permits equals limit minus in-flight. Callers hold l.mu.
func (l *Limiter) reconcilePermitsLocked() {
	for {
		select {
		case <-l.sem:
		default:
			goto fill
		}
	}
fill:
	want := l.limit - int(l.inFlight.Load())
	if want < 0 {
		want = 0
	}
	for range want {
		select {
		case l.sem <- struct{}{}:
		default:
			return
		}
	}
}

// gradient backs off in proportion to how far the current latency sits above
// the smoothed average and grows while at or below it. Callers hold l.mu.
func (l *Limiter) gradient(success bool, latency time.Duration) int {
	if !success {
		return int(float64(l.limit) * l.cfg.decreaseRatio)
	}
	if l.avgLat <= 0 {
		return l.limit + 1
	}
	g := (float64(latency.Nanoseconds()) - l.avgLat) / l.avgLat
	if g < -l.cfg.tolerance {
		return l.limit + gradientFastStep
	}
	if g <= l.cfg.tolerance {
		return l.limit + 1
	}
	f := max(1-g*l.cfg.decreaseRatio, l.cfg.decreaseRatio)
	return int(float64(l.limit) * f)
}

const (
	// vegasBackoffBand is the multiple of the target queue above which Vegas
	// switches from holding the limit to multiplicatively decreasing it.
	vegasBackoffBand = 2.0

	// gradientFastStep is the additive step Gradient takes when latency is
	// comfortably below the average, ramping up faster than the unit step.
	gradientFastStep = 2
)
