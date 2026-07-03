package fallx

import "context"

// Strategy selects how a [Fallback] produces an alternative result when the
// primary operation fails. A Fallback is built for exactly one strategy, fixed
// at construction by the corresponding option.
type Strategy uint8

const (
	// StrategyStatic returns a fixed value supplied by [WithStatic]. The
	// fallback can never itself fail.
	StrategyStatic Strategy = iota
	// StrategyFunc calls the function supplied by [WithFunc], passing the
	// original error so the callback can compute a degraded result.
	StrategyFunc
	// StrategyCached replays the most recent successful primary result for the
	// request's key, caching successes automatically. Configured by [WithCached].
	StrategyCached
)

// String returns the lowercase strategy name ("static", "function", "cached").
func (s Strategy) String() string {
	switch s {
	case StrategyStatic:
		return labelStatic
	case StrategyFunc:
		return labelFunc
	case StrategyCached:
		return labelCached
	default:
		return labelUnknown
	}
}

// Strategy label strings, kept out of [Strategy.String] so there are no string
// literals in logic.
const (
	labelStatic  = "static"
	labelFunc    = "function"
	labelCached  = "cached"
	labelUnknown = "unknown"
)

// FallController exposes the execution context to the [Execute] callback and,
// on the fallback path, to the function registered with [WithFunc]. The
// implementation is private; callers interact only through this interface. A
// FallController is bound to a single [Execute] call and must not be retained
// after the callback returns.
//
// On the first invocation the callback runs the primary path: [OnFallback]
// reports false and [Error] is nil. If the primary fails under [StrategyFunc],
// the same controller is handed to the fallback function with [OnFallback] true
// and [Error] carrying the primary failure, so one closure can serve both paths
// and branch on [OnFallback].
type FallController interface {
	// Strategy returns the fallback strategy this [Fallback] was built with.
	Strategy() Strategy

	// Key returns the cache key resolved for this call. It is the explicit key
	// passed to [ExecuteWithKey], the value returned by [WithKeyFunc], or
	// [DefaultKey] when neither is configured. Meaningful mainly under
	// [StrategyCached].
	Key() string

	// OnFallback reports whether the current invocation is the fallback path
	// (true) rather than the primary attempt (false). It is only ever true for
	// the [WithFunc] callback; the primary callback passed to [Execute] always
	// observes false.
	OnFallback() bool

	// Error returns the primary failure that triggered the fallback, or nil on
	// the primary path. Available to the [WithFunc] callback so it can inspect
	// the cause before producing a degraded result.
	Error() error
}

// PrimaryFunc is the primary unit of work run by [Execute] and
// [ExecuteWithKey]. It receives the call context and a [FallController] whose
// [FallController.OnFallback] is always false. It runs under panic recovery.
type PrimaryFunc[T any] func(ctx context.Context, fc FallController) (T, error)

// FallbackFunc is the fallback unit of work registered with [WithFunc]. It runs
// only when the primary fails, receives a [FallController] whose
// [FallController.OnFallback] is true and whose [FallController.Error] carries
// the primary failure, and runs under panic recovery.
type FallbackFunc[T any] func(ctx context.Context, fc FallController) (T, error)

// execution is the private implementation of [FallController]. One instance is
// created per [Execute] call and accessed only from the goroutine running the
// callback, so it needs no synchronization. The onFallback flag and err are
// flipped in place before the fallback function is invoked.
type execution struct {
	strategy   Strategy
	key        string
	err        error
	onFallback bool
}

// Strategy implements [FallController].
func (e *execution) Strategy() Strategy { return e.strategy }

// Key implements [FallController].
func (e *execution) Key() string { return e.key }

// OnFallback implements [FallController].
func (e *execution) OnFallback() bool { return e.onFallback }

// Error implements [FallController].
func (e *execution) Error() error { return e.err }

// compile-time assertion that execution satisfies the public interface.
var _ FallController = (*execution)(nil)

// Stats holds a point-in-time snapshot of fallback counters. All fields are
// cumulative since construction or the last [Fallback.ResetStats].
type Stats struct {
	// Calls is the total number of [Execute]/[ExecuteWithKey] invocations.
	Calls int64 `json:"calls"`
	// PrimarySuccess is the number of calls whose primary attempt succeeded.
	PrimarySuccess int64 `json:"primary_success"`
	// FallbackUsed is the number of calls that entered the fallback path
	// because the primary failed.
	FallbackUsed int64 `json:"fallback_used"`
	// FallbackSuccess is the number of fallback paths that produced a result.
	FallbackSuccess int64 `json:"fallback_success"`
	// FallbackFailed is the number of fallback paths that could not produce a
	// result (fallback function errored, or no cached value existed).
	FallbackFailed int64 `json:"fallback_failed"`
	// CacheHits is the number of fallback paths served from the cache.
	CacheHits int64 `json:"cache_hits"`
	// CacheMisses is the number of fallback paths that found no cached value.
	CacheMisses int64 `json:"cache_misses"`
	// CacheSize is the number of live entries in the cache at snapshot time.
	CacheSize int `json:"cache_size"`
	// CacheEvictions is the number of entries removed by TTL expiry or capacity
	// eviction.
	CacheEvictions int64 `json:"cache_evictions"`
}
