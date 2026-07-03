package fallx

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/aasyanov/urx/panix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errPrimary = errors.New("primary failed")

func okFn[T any](v T) PrimaryFunc[T] {
	return func(context.Context, FallController) (T, error) { return v, nil }
}

func failFn[T any](err error) PrimaryFunc[T] {
	return func(context.Context, FallController) (T, error) {
		var zero T
		return zero, err
	}
}

// assertCacheSizeConsistent verifies that Stats().CacheSize equals the number
// of live entries across all cache shards.
func assertCacheSizeConsistent[T any](t *testing.T, f *Fallback[T]) {
	t.Helper()
	var n int
	for _, shard := range f.shards {
		shard.mu.RLock()
		n += len(shard.entries)
		shard.mu.RUnlock()
	}
	assert.Equal(t, n, f.Stats().CacheSize, "cacheSize counter must match shard maps")
}

// --- Strategy selection ---

func TestStrategy_String(t *testing.T) {
	tests := []struct {
		name string
		s    Strategy
		want string
	}{
		{name: "static", s: StrategyStatic, want: "static"},
		{name: "function", s: StrategyFunc, want: "function"},
		{name: "cached", s: StrategyCached, want: "cached"},
		{name: "unknown", s: Strategy(99), want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.s.String())
		})
	}
}

func TestNew_DefaultsToStatic(t *testing.T) {
	fb := New[int]()
	defer func() { _ = fb.Close() }()
	assert.Equal(t, StrategyStatic, fb.Strategy())
}

// --- Static strategy ---

func TestExecute_StaticPrimarySuccess(t *testing.T) {
	fb := New(WithStatic("backup"))
	defer func() { _ = fb.Close() }()

	got, err := Execute(fb, context.Background(), okFn("fresh"))
	require.NoError(t, err)
	assert.Equal(t, "fresh", got)

	st := fb.Stats()
	assert.Equal(t, int64(1), st.Calls)
	assert.Equal(t, int64(1), st.PrimarySuccess)
	assert.Equal(t, int64(0), st.FallbackUsed)
}

func TestExecute_StaticPrimaryFailure(t *testing.T) {
	fb := New(WithStatic("backup"))
	defer func() { _ = fb.Close() }()

	got, err := Execute(fb, context.Background(), failFn[string](errPrimary))
	require.NoError(t, err)
	assert.Equal(t, "backup", got)

	st := fb.Stats()
	assert.Equal(t, int64(1), st.FallbackUsed)
	assert.Equal(t, int64(1), st.FallbackSuccess)
}

func TestExecute_StaticZeroValueDefault(t *testing.T) {
	fb := New[int]()
	defer func() { _ = fb.Close() }()

	got, err := Execute(fb, context.Background(), failFn[int](errPrimary))
	require.NoError(t, err)
	assert.Equal(t, 0, got)
}

// --- Func strategy ---

func TestExecute_FuncFallbackProducesResult(t *testing.T) {
	fb := New(WithFunc(func(_ context.Context, fc FallController) (string, error) {
		assert.True(t, fc.OnFallback())
		assert.ErrorIs(t, fc.Error(), errPrimary)
		assert.Equal(t, StrategyFunc, fc.Strategy())
		return "computed", nil
	}))
	defer func() { _ = fb.Close() }()

	got, err := Execute(fb, context.Background(), failFn[string](errPrimary))
	require.NoError(t, err)
	assert.Equal(t, "computed", got)
	assert.Equal(t, int64(1), fb.Stats().FallbackSuccess)
}

func TestExecute_FuncFallbackErrorWrapped(t *testing.T) {
	fbErr := errors.New("fallback also failed")
	fb := New(WithFunc(func(context.Context, FallController) (string, error) {
		return "", fbErr
	}))
	defer func() { _ = fb.Close() }()

	_, err := Execute(fb, context.Background(), failFn[string](errPrimary))
	require.ErrorIs(t, err, ErrFallbackFailed)
	require.ErrorIs(t, err, fbErr)
	assert.Equal(t, int64(1), fb.Stats().FallbackFailed)
}

func TestExecute_FuncNilIgnored_StaysStatic(t *testing.T) {
	// WithFunc(nil) must not switch the strategy away from the default static.
	fb := New(WithFunc[string](nil))
	defer func() { _ = fb.Close() }()
	assert.Equal(t, StrategyStatic, fb.Strategy())
}

func TestExecute_FuncStrategyWithoutFuncReturnsErrNoFunc(t *testing.T) {
	// Force StrategyFunc with no function by hand to exercise the guard.
	fb := New(WithStatic(""))
	fb.cfg.strategy = StrategyFunc
	fb.cfg.fallbackFn = nil
	defer func() { _ = fb.Close() }()

	_, err := Execute(fb, context.Background(), failFn[string](errPrimary))
	require.ErrorIs(t, err, ErrNoFunc)
}

func TestExecute_UnknownStrategyReturnsErrNoFunc(t *testing.T) {
	fb := New(WithStatic(0))
	fb.cfg.strategy = Strategy(42)
	defer func() { _ = fb.Close() }()

	_, err := Execute(fb, context.Background(), failFn[int](errPrimary))
	require.ErrorIs(t, err, ErrNoFunc)
}

// --- Cached strategy ---

func TestExecute_CachedReplaysLastSuccess(t *testing.T) {
	fb := New(WithCached[int](time.Minute, 16))
	defer func() { _ = fb.Close() }()

	got, err := Execute(fb, context.Background(), okFn(7))
	require.NoError(t, err)
	assert.Equal(t, 7, got)

	got, err = Execute(fb, context.Background(), failFn[int](errPrimary))
	require.NoError(t, err)
	assert.Equal(t, 7, got)

	st := fb.Stats()
	assert.Equal(t, int64(1), st.CacheHits)
	assert.Equal(t, int64(1), st.PrimarySuccess)
}

func TestExecute_CachedMissReturnsErrNoCached(t *testing.T) {
	fb := New(WithCached[int](time.Minute, 16))
	defer func() { _ = fb.Close() }()

	_, err := Execute(fb, context.Background(), failFn[int](errPrimary))
	require.ErrorIs(t, err, ErrNoCached)
	assert.Equal(t, int64(1), fb.Stats().CacheMisses)
}

func TestExecute_CachedExpiredIsMiss(t *testing.T) {
	fb := New(WithCached[int](20*time.Millisecond, 16))
	defer func() { _ = fb.Close() }()

	_, err := Execute(fb, context.Background(), okFn(99))
	require.NoError(t, err)

	time.Sleep(40 * time.Millisecond)

	_, err = Execute(fb, context.Background(), failFn[int](errPrimary))
	require.ErrorIs(t, err, ErrNoCached)
	assert.GreaterOrEqual(t, fb.Stats().CacheEvictions, int64(1))
}

func TestExecuteWithKey_IsolatesKeys(t *testing.T) {
	fb := New(WithCached[string](time.Minute, 16))
	defer func() { _ = fb.Close() }()

	_, err := ExecuteWithKey(fb, context.Background(), "user-a", okFn("a-value"))
	require.NoError(t, err)

	// user-b has no cached value yet.
	_, err = ExecuteWithKey(fb, context.Background(), "user-b", failFn[string](errPrimary))
	require.ErrorIs(t, err, ErrNoCached)

	// user-a replays.
	got, err := ExecuteWithKey(fb, context.Background(), "user-a", failFn[string](errPrimary))
	require.NoError(t, err)
	assert.Equal(t, "a-value", got)
}

func TestWithKeyFunc_DerivesKeyFromContext(t *testing.T) {
	type ctxKey struct{}
	keyOf := func(ctx context.Context) string {
		v, _ := ctx.Value(ctxKey{}).(string)
		return v
	}
	fb := New(WithCached[string](time.Minute, 16), WithKeyFunc[string](keyOf))
	defer func() { _ = fb.Close() }()

	ctxA := context.WithValue(context.Background(), ctxKey{}, "alpha")
	_, err := Execute(fb, ctxA, okFn("A"))
	require.NoError(t, err)

	got, err := Execute(fb, ctxA, failFn[string](errPrimary))
	require.NoError(t, err)
	assert.Equal(t, "A", got)
}

func TestExecute_ControllerKeyIsDefaultWhenUnset(t *testing.T) {
	var seen string
	fb := New(WithFunc(func(_ context.Context, fc FallController) (int, error) {
		seen = fc.Key()
		return 0, nil
	}))
	defer func() { _ = fb.Close() }()

	_, err := Execute(fb, context.Background(), failFn[int](errPrimary))
	require.NoError(t, err)
	assert.Equal(t, DefaultKey, seen)
}

// --- Seed ---

func TestSeed_WarmsCacheBeforeFirstFailure(t *testing.T) {
	fb := New(WithCached[string](time.Minute, 16))
	defer func() { _ = fb.Close() }()

	fb.Seed("k", "seeded")
	got, err := ExecuteWithKey(fb, context.Background(), "k", failFn[string](errPrimary))
	require.NoError(t, err)
	assert.Equal(t, "seeded", got)
}

func TestSeedWithTTL_NonPositiveUsesDefault(t *testing.T) {
	fb := New(WithCached[string](time.Minute, 16))
	defer func() { _ = fb.Close() }()

	fb.SeedWithTTL("k", "v", 0)
	got, err := ExecuteWithKey(fb, context.Background(), "k", failFn[string](errPrimary))
	require.NoError(t, err)
	assert.Equal(t, "v", got)
}

func TestSeedWithTTL_ExpiresAfterTTL(t *testing.T) {
	fb := New(WithCached[string](time.Minute, 16))
	defer func() { _ = fb.Close() }()

	fb.SeedWithTTL("k", "v", 20*time.Millisecond)
	time.Sleep(40 * time.Millisecond)
	_, err := ExecuteWithKey(fb, context.Background(), "k", failFn[string](errPrimary))
	require.ErrorIs(t, err, ErrNoCached)
}

func TestClearCache_RemovesEntries(t *testing.T) {
	fb := New(WithCached[int](time.Minute, 16))
	defer func() { _ = fb.Close() }()

	_, _ = Execute(fb, context.Background(), okFn(1))
	require.Equal(t, 1, fb.Stats().CacheSize)

	fb.ClearCache()
	assert.Equal(t, 0, fb.Stats().CacheSize)
	assertCacheSizeConsistent(t, fb)

	_, err := Execute(fb, context.Background(), failFn[int](errPrimary))
	require.ErrorIs(t, err, ErrNoCached)
}

func TestCacheSize_MatchesShardMaps(t *testing.T) {
	fb := New(WithCached[int](time.Minute, 32), WithShards[int](4))
	defer func() { _ = fb.Close() }()

	for i := 0; i < 20; i++ {
		fb.Seed(fmt.Sprintf("k%d", i), i)
	}
	assertCacheSizeConsistent(t, fb)
}

func TestClearCache_ConcurrentWithExecute(t *testing.T) {
	fb := New(WithCached[int](time.Minute, 64), WithShards[int](8))
	defer func() { _ = fb.Close() }()

	var counter atomic.Int64
	testx.HammerVoid(32, 200, func() {
		if counter.Add(1)%2 == 0 {
			fb.ClearCache()
			return
		}
		key := fmt.Sprintf("k%d", counter.Load()%32)
		_, _ = ExecuteWithKey(fb, context.Background(), key,
			func(context.Context, FallController) (int, error) { return 1, nil })
	})
	assertCacheSizeConsistent(t, fb)
	assert.LessOrEqual(t, fb.Stats().CacheSize, 64)
}

func TestSeed_AfterCloseIsNoOp(t *testing.T) {
	fb := New(WithCached[string](time.Minute, 16))
	fb.Seed("warm", "before")
	require.NoError(t, fb.Close())

	fb.Seed("ignored", "after")
	fb.SeedWithTTL("also-ignored", "after", time.Minute)
	assert.Equal(t, 1, fb.Stats().CacheSize)
	assertCacheSizeConsistent(t, fb)

	_, err := ExecuteWithKey(fb, context.Background(), "ignored", failFn[string](errPrimary))
	require.ErrorIs(t, err, ErrClosed)
}

func TestClearCache_AfterCloseIsNoOp(t *testing.T) {
	fb := New(WithCached[int](time.Minute, 16))
	fb.Seed("k", 42)
	require.NoError(t, fb.Close())

	fb.ClearCache()
	assert.Equal(t, 1, fb.Stats().CacheSize)
	assertCacheSizeConsistent(t, fb)
}

func TestSeed_RefreshExistingEntry(t *testing.T) {
	fb := New(WithCached[string](time.Minute, 16))
	defer func() { _ = fb.Close() }()

	fb.Seed("k", "first")
	fb.Seed("k", "second")
	assert.Equal(t, 1, fb.Stats().CacheSize)
	assertCacheSizeConsistent(t, fb)

	got, err := ExecuteWithKey(fb, context.Background(), "k", failFn[string](errPrimary))
	require.NoError(t, err)
	assert.Equal(t, "second", got)
}

// --- Eviction ---

func TestCache_EvictsOverCapacity(t *testing.T) {
	const cap = 8
	fb := New(WithCached[int](time.Minute, cap), WithShards[int](2))
	defer func() { _ = fb.Close() }()

	for i := 0; i < cap*4; i++ {
		fb.Seed(fmt.Sprintf("k%d", i), i)
	}
	assert.LessOrEqual(t, fb.Stats().CacheSize, cap)
	assert.Positive(t, fb.Stats().CacheEvictions)
	assertCacheSizeConsistent(t, fb)
}

// --- Error / lifecycle paths ---

func TestExecute_NilPrimaryReturnsErrNilFunc(t *testing.T) {
	fb := New(WithStatic(0))
	defer func() { _ = fb.Close() }()

	_, err := Execute[int](fb, context.Background(), nil)
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestExecute_AfterCloseReturnsErrClosed(t *testing.T) {
	fb := New(WithCached[int](time.Minute, 16))
	require.NoError(t, fb.Close())

	_, err := Execute(fb, context.Background(), okFn(1))
	require.ErrorIs(t, err, ErrClosed)
}

func TestClose_Idempotent(t *testing.T) {
	fb := New(WithCached[int](time.Minute, 16))
	testx.AssertCloseIdempotent(t, fb)
	assert.True(t, fb.IsClosed())
}

// --- Context behavior ---
//
// fallx never adds a deadline of its own and never short-circuits on a
// cancelled context: a primary that observes cancellation and fails simply
// triggers the configured fallback, exactly like any other primary failure.

func TestExecute_CancelledCtxStillRunsFallback(t *testing.T) {
	fb := New(WithStatic("backup"))
	defer func() { _ = fb.Close() }()

	got, err := Execute(fb, testx.CancelledCtx(),
		func(ctx context.Context, _ FallController) (string, error) {
			return "", ctx.Err() // primary respects cancellation
		})
	require.NoError(t, err)
	assert.Equal(t, "backup", got)
	assert.Equal(t, int64(1), fb.Stats().FallbackUsed)
}

func TestExecute_CtxThreadedToFuncFallback(t *testing.T) {
	fb := New(WithFunc(func(ctx context.Context, fc FallController) (int, error) {
		// The same cancelled ctx is handed to the fallback unchanged.
		require.ErrorIs(t, ctx.Err(), context.Canceled)
		require.ErrorIs(t, fc.Error(), context.Canceled)
		return 7, nil
	}))
	defer func() { _ = fb.Close() }()

	got, err := Execute(fb, testx.CancelledCtx(),
		func(ctx context.Context, _ FallController) (int, error) {
			return 0, ctx.Err()
		})
	require.NoError(t, err)
	assert.Equal(t, 7, got)
}

func TestExecute_CtxPassedToPrimaryUnchanged(t *testing.T) {
	type ctxKey struct{}
	fb := New(WithStatic(0))
	defer func() { _ = fb.Close() }()

	ctx := context.WithValue(context.Background(), ctxKey{}, "v")
	_, err := Execute(fb, ctx, func(ctx context.Context, _ FallController) (int, error) {
		assert.Equal(t, "v", ctx.Value(ctxKey{}))
		return 1, nil
	})
	require.NoError(t, err)
}

func TestClose_NonCachedStrategy(t *testing.T) {
	fb := New(WithStatic(0))
	require.NoError(t, fb.Close())
	assert.True(t, fb.IsClosed())
}

func TestExecute_PrimaryPanicRecovered(t *testing.T) {
	fb := New(WithStatic("backup"))
	defer func() { _ = fb.Close() }()

	got, err := Execute(fb, context.Background(),
		func(context.Context, FallController) (string, error) {
			panic("boom")
		})
	// Primary panic is treated as a primary failure → static fallback.
	require.NoError(t, err)
	assert.Equal(t, "backup", got)
}

func TestExecute_FallbackPanicSurfaces(t *testing.T) {
	fb := New(WithFunc(func(context.Context, FallController) (int, error) {
		panic("fallback boom")
	}))
	defer func() { _ = fb.Close() }()

	_, err := Execute(fb, context.Background(), failFn[int](errPrimary))
	require.ErrorIs(t, err, ErrFallbackFailed)
	var pe *panix.PanicError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, opFallback, pe.Op)
	assert.Equal(t, "fallback boom", pe.Value)
}

func TestExecute_PrimaryControllerNotOnFallback(t *testing.T) {
	fb := New(WithStatic(0))
	defer func() { _ = fb.Close() }()

	_, err := Execute(fb, context.Background(),
		func(_ context.Context, fc FallController) (int, error) {
			assert.False(t, fc.OnFallback())
			assert.NoError(t, fc.Error())
			return 1, nil
		})
	require.NoError(t, err)
}

func TestWithOnFallback_FiresOnFailure(t *testing.T) {
	var fired atomic.Int64
	fb := New(
		WithStatic("backup"),
		WithOnFallback[string](func(err error, s Strategy) {
			assert.ErrorIs(t, err, errPrimary)
			assert.Equal(t, StrategyStatic, s)
			fired.Add(1)
		}),
	)
	defer func() { _ = fb.Close() }()

	_, _ = Execute(fb, context.Background(), failFn[string](errPrimary))
	assert.Equal(t, int64(1), fired.Load())
}

func TestWithOp_LabelsPrimaryPanic(t *testing.T) {
	fb := New(WithFunc(func(_ context.Context, fc FallController) (int, error) {
		// Surface the primary panic by re-failing; assert via Error path below.
		return 0, fc.Error()
	}), WithOp[int]("svc.fetch"))
	defer func() { _ = fb.Close() }()

	_, err := Execute(fb, context.Background(),
		func(context.Context, FallController) (int, error) { panic("p") })
	require.ErrorIs(t, err, ErrFallbackFailed)
	// The primary panic was captured under the custom op and threaded into the
	// fallback as the cause.
	var pe *panix.PanicError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, "svc.fetch", pe.Op)
	assert.Equal(t, "p", pe.Value)
}

// --- Stats ---

func TestResetStats_ZeroesCounters(t *testing.T) {
	fb := New(WithStatic(0))
	defer func() { _ = fb.Close() }()

	_, _ = Execute(fb, context.Background(), okFn(1))
	_, _ = Execute(fb, context.Background(), failFn[int](errPrimary))
	require.NotZero(t, fb.Stats().Calls)

	fb.ResetStats()
	st := fb.Stats()
	assert.Zero(t, st.Calls)
	assert.Zero(t, st.PrimarySuccess)
	assert.Zero(t, st.FallbackUsed)
}

func TestResetStats_KeepsCacheSize(t *testing.T) {
	fb := New(WithCached[int](time.Minute, 16))
	defer func() { _ = fb.Close() }()

	_, _ = Execute(fb, context.Background(), okFn(1))
	fb.ResetStats()
	assert.Equal(t, 1, fb.Stats().CacheSize)
}

// --- Options edge cases ---

func TestOptions_Defaults(t *testing.T) {
	cfg := newConfig[int](nil)
	assert.Equal(t, StrategyStatic, cfg.strategy)
	assert.Equal(t, DefaultCacheTTL, cfg.cacheTTL)
	assert.Equal(t, DefaultMaxCacheSize, cfg.maxCacheSize)
	assert.Equal(t, DefaultShards, cfg.shardCount)
}

func TestOptions_ShardClamping(t *testing.T) {
	tests := []struct {
		name      string
		opts      []Option[int]
		minShards int
		maxShards int
	}{
		{name: "negative shards floored", opts: []Option[int]{WithShards[int](-3)}, minShards: DefaultShards, maxShards: DefaultShards},
		{name: "shards bounded by capacity", opts: []Option[int]{WithCached[int](time.Minute, 8), WithShards[int](100)}, minShards: 1, maxShards: 8},
		{name: "nil option skipped", opts: []Option[int]{nil}, minShards: DefaultShards, maxShards: DefaultShards},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newConfig(tt.opts)
			assert.GreaterOrEqual(t, cfg.shardCount, tt.minShards)
			assert.LessOrEqual(t, cfg.shardCount, tt.maxShards)
		})
	}
}

func TestWithCached_NonPositiveUsesDefaults(t *testing.T) {
	cfg := newConfig([]Option[int]{WithCached[int](0, 0)})
	assert.Equal(t, StrategyCached, cfg.strategy)
	assert.Equal(t, DefaultCacheTTL, cfg.cacheTTL)
	assert.Equal(t, DefaultMaxCacheSize, cfg.maxCacheSize)
}

func TestWithOp_EmptyIgnored(t *testing.T) {
	cfg := newConfig([]Option[int]{WithOp[int]("")})
	assert.Equal(t, opExecute, cfg.opOrDefault())
}

// --- Concurrency ---

func TestExecute_ConcurrentCachedAccess(t *testing.T) {
	fb := New(WithCached[int](time.Minute, 256), WithShards[int](8))
	defer func() { _ = fb.Close() }()

	var counter atomic.Int64
	testx.HammerNoError(t, 64, 500, func() error {
		key := fmt.Sprintf("k%d", counter.Add(1)%128)
		_, err := ExecuteWithKey(fb, context.Background(), key,
			func(context.Context, FallController) (int, error) { return 1, nil })
		return err
	})
	assert.LessOrEqual(t, fb.Stats().CacheSize, 256)
	assertCacheSizeConsistent(t, fb)
}

func TestExecute_ConcurrentStaticFallback(t *testing.T) {
	fb := New(WithStatic(-1))
	defer func() { _ = fb.Close() }()

	testx.HammerNoError(t, 32, 1000, func() error {
		_, err := Execute(fb, context.Background(), failFn[int](errPrimary))
		return err
	})
	st := fb.Stats()
	assert.Equal(t, st.Calls, st.FallbackUsed)
}
