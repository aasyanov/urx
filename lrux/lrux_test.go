package lrux

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_Defaults(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	assert.Equal(t, 0, c.capacity)
	assert.Equal(t, time.Duration(0), c.ttl)
	assert.Nil(t, c.onEvict)
	assert.Nil(t, c.cleanup)
}

func TestCache_SetGet(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)

	v, ok := c.Get("a")
	require.True(t, ok)
	assert.Equal(t, 1, v)

	v, ok = c.Get("b")
	require.True(t, ok)
	assert.Equal(t, 2, v)

	_, ok = c.Get("missing")
	assert.False(t, ok)
}

func TestCache_Update_OverwritesValue(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	c.Set("k", 1)
	c.Set("k", 2)
	c.Set("k", 3)

	v, ok := c.Get("k")
	require.True(t, ok)
	assert.Equal(t, 3, v)
	assert.Equal(t, 1, c.Len())
}

func TestCache_CapacityEviction(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](3))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	c.Set("d", 4) // evicts "a" (LRU)

	assert.Equal(t, 3, c.Len())
	_, ok := c.Get("a")
	assert.False(t, ok)
	_, ok = c.Get("d")
	assert.True(t, ok)
}

func TestCache_GetPromotesRecency(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](3))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	c.Get("a")    // "a" now most recently used
	c.Set("d", 4) // evicts "b", not "a"

	_, ok := c.Get("a")
	assert.True(t, ok)
	_, ok = c.Get("b")
	assert.False(t, ok)
}

func TestCache_GetFast_DoesNotPromote(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](3))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	v, ok := c.GetFast("a")
	require.True(t, ok)
	assert.Equal(t, 1, v)

	c.Set("d", 4) // "a" was not promoted, so it is evicted

	_, ok = c.Get("a")
	assert.False(t, ok)
}

func TestCache_Peek_NoStatsNoPromotion(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.Set("a", 1)

	v, ok := c.Peek("a")
	require.True(t, ok)
	assert.Equal(t, 1, v)

	stats := c.Stats()
	assert.Zero(t, stats.Hits)
	assert.Zero(t, stats.Misses)
}

func TestCache_Has(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.Set("a", 1)

	assert.True(t, c.Has("a"))
	assert.False(t, c.Has("b"))
}

func TestCache_Delete(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.Set("a", 1)

	assert.True(t, c.Delete("a"))
	assert.False(t, c.Delete("a"))
	assert.False(t, c.Has("a"))
}

func TestCache_Clear(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.Set("a", 1)
	c.Set("b", 2)

	c.Clear()
	assert.Equal(t, 0, c.Len())
}

func TestCache_TTL_Expiration(t *testing.T) {
	c := New[string, int](WithTTL[string, int](20 * time.Millisecond))
	defer c.Close()
	c.Set("a", 1)

	v, ok := c.Get("a")
	require.True(t, ok)
	assert.Equal(t, 1, v)

	require.Eventually(t, func() bool {
		_, ok := c.Get("a")
		return !ok
	}, time.Second, 5*time.Millisecond)
}

func TestCache_SetWithTTL_PerEntry(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.SetWithTTL("a", 1, 20*time.Millisecond)
	c.Set("b", 2) // no TTL

	require.Eventually(t, func() bool {
		_, ok := c.Get("a")
		return !ok
	}, time.Second, 5*time.Millisecond)

	_, ok := c.Get("b")
	assert.True(t, ok)
}

func TestCache_TTL_RemainingValues(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	c.SetWithTTL("ttl", 1, time.Hour)
	c.Set("forever", 2)

	assert.Greater(t, c.TTL("ttl"), time.Duration(0))
	assert.Equal(t, time.Duration(-1), c.TTL("forever"))
	assert.Equal(t, time.Duration(0), c.TTL("missing"))
}

func TestCache_Touch(t *testing.T) {
	c := New[string, int](WithTTL[string, int](50 * time.Millisecond))
	defer c.Close()
	c.Set("a", 1)

	time.Sleep(30 * time.Millisecond)
	assert.True(t, c.Touch("a"))
	assert.False(t, c.Touch("missing"))

	time.Sleep(30 * time.Millisecond) // total 60ms but TTL refreshed at 30ms
	_, ok := c.Get("a")
	assert.True(t, ok)
}

func TestCache_Touch_PerEntryTTL(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.SetWithTTL("session", 1, time.Hour)

	time.Sleep(30 * time.Millisecond)
	assert.True(t, c.Touch("session"))

	rem := c.TTL("session")
	assert.Greater(t, rem, 59*time.Minute)
}

func TestCache_GetFast_ExpiredNotRemoved(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.SetWithTTL("a", 1, time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	_, ok := c.GetFast("a")
	assert.False(t, ok)
	assert.Equal(t, 1, c.Len())
	assert.Equal(t, 0, c.LenValid())
}

func TestCache_ResetStats(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.Get("missing")
	c.ResetStats()
	s := c.Stats()
	assert.Equal(t, uint64(0), s.Hits)
	assert.Equal(t, uint64(0), s.Misses)
	assert.Equal(t, uint64(0), s.Evictions)
}

func TestRemoveTailLocked_EmptyCache(t *testing.T) {
	c := New[int, int]()
	defer c.Close()

	c.mu.Lock()
	ev := c.removeTailLocked()
	c.mu.Unlock()
	assert.Nil(t, ev)
}

func TestCache_GetEntry(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.Set("a", 1)

	e := c.GetEntry("a")
	require.NotNil(t, e)
	assert.Equal(t, "a", e.Key)
	assert.Equal(t, 1, e.Value)
	assert.False(t, e.CreatedAt.IsZero())

	assert.Nil(t, c.GetEntry("missing"))
}

func TestCache_Resize_Shrinks(t *testing.T) {
	c := New[int, int](WithCapacity[int, int](5))
	defer c.Close()
	for i := range 5 {
		c.Set(i, i)
	}

	c.Resize(2)
	assert.Equal(t, 2, c.Len())
}

func TestCache_Resize_Grow(t *testing.T) {
	c := New[int, int](WithCapacity[int, int](2))
	defer c.Close()
	c.Set(1, 1)
	c.Set(2, 2)

	c.Resize(10)
	c.Set(3, 3)
	assert.Equal(t, 3, c.Len())
}

func TestCache_LenValid_ExcludesExpired(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.SetWithTTL("exp", 1, time.Millisecond)
	c.Set("live", 2)

	time.Sleep(10 * time.Millisecond)
	assert.Equal(t, 1, c.LenValid())
	assert.Equal(t, 2, c.Len()) // expired entry still counted until swept
}

func TestCache_Keys_Values_Snapshot(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.Set("a", 1)
	c.Set("b", 2)

	assert.Len(t, c.Keys(), 2)
	assert.Len(t, c.Values(), 2)
	assert.Len(t, c.Snapshot(), 2)
}

func TestCache_Range_StopsEarly(t *testing.T) {
	c := New[int, int]()
	defer c.Close()
	for i := range 10 {
		c.Set(i, i)
	}

	count := 0
	c.Range(func(_ int, _ int) bool {
		count++
		return count < 3
	})
	assert.Equal(t, 3, count)
}

func TestCache_ExpireOld(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.SetWithTTL("a", 1, time.Millisecond)
	c.SetWithTTL("b", 2, time.Millisecond)
	c.Set("c", 3)

	time.Sleep(10 * time.Millisecond)
	removed := c.ExpireOld()
	assert.Equal(t, 2, removed)
	assert.Equal(t, 1, c.Len())
}

func TestCache_BatchOperations(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	c.SetMulti(map[string]int{"a": 1, "b": 2, "c": 3})
	assert.Equal(t, 3, c.Len())

	got := c.GetMulti([]string{"a", "b", "missing"})
	assert.Len(t, got, 2)

	n := c.DeleteMulti([]string{"a", "b", "missing"})
	assert.Equal(t, 2, n)
}

func TestCache_Stats(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()
	c.Set("a", 1)
	c.Get("a")       // hit
	c.Get("missing") // miss

	stats := c.Stats()
	assert.Equal(t, uint64(1), stats.Hits)
	assert.Equal(t, uint64(1), stats.Misses)
	assert.Equal(t, 0.5, stats.HitRate)
	assert.Equal(t, 10, stats.Capacity)

	c.ResetStats()
	stats = c.Stats()
	assert.Zero(t, stats.Hits)
	assert.Zero(t, stats.Misses)
}

func TestCache_Close_Idempotent(t *testing.T) {
	c := New[string, int]()
	c.Set("a", 1)

	testx.AssertCloseIdempotent(t, c)
	assert.True(t, c.IsClosed())
}

func TestCache_ClosedOperations_AreNoOps(t *testing.T) {
	c := New[string, int]()
	c.Set("a", 1)
	c.Close()

	c.Set("b", 2)
	assert.False(t, c.Has("a"))
	assert.False(t, c.Has("b"))
	_, ok := c.Get("a")
	assert.False(t, ok)
	assert.False(t, c.Delete("a"))
	assert.False(t, c.Touch("a"))
	assert.Equal(t, 0, c.Len())
	assert.Equal(t, 0, c.LenValid())
	assert.Nil(t, c.Keys())
	assert.Nil(t, c.Values())
	assert.Nil(t, c.Snapshot())
	assert.Nil(t, c.GetEntry("a"))
	assert.Equal(t, time.Duration(0), c.TTL("a"))
	assert.Equal(t, 0, c.ExpireOld())
}

func TestCache_OnEvict_AllReasons(t *testing.T) {
	tests := []struct {
		name   string
		setup  func(c *Cache[string, int])
		action func(c *Cache[string, int])
		want   EvictionReason
		key    string
	}{
		{
			name:   "capacity",
			setup:  func(c *Cache[string, int]) { c.Set("a", 1) },
			action: func(c *Cache[string, int]) { c.Set("b", 2) },
			want:   EvictionCapacity,
			key:    "a",
		},
		{
			name:   "deleted",
			setup:  func(c *Cache[string, int]) { c.Set("a", 1) },
			action: func(c *Cache[string, int]) { c.Delete("a") },
			want:   EvictionDeleted,
			key:    "a",
		},
		{
			name:   "replaced",
			setup:  func(c *Cache[string, int]) { c.Set("a", 1) },
			action: func(c *Cache[string, int]) { c.Set("a", 2) },
			want:   EvictionReplaced,
			key:    "a",
		},
		{
			name:   "cleared",
			setup:  func(c *Cache[string, int]) { c.Set("a", 1) },
			action: func(c *Cache[string, int]) { c.Clear() },
			want:   EvictionCleared,
			key:    "a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotKey string
			var gotReason EvictionReason
			capacity := 1
			c := New[string, int](
				WithCapacity[string, int](capacity),
				WithOnEvict[string, int](func(k string, _ int, r EvictionReason) {
					gotKey = k
					gotReason = r
				}),
			)
			defer c.Close()

			tt.setup(c)
			tt.action(c)

			assert.Equal(t, tt.key, gotKey)
			assert.Equal(t, tt.want, gotReason)
		})
	}
}

func TestCache_OnEvict_TTLReason(t *testing.T) {
	var gotReason EvictionReason
	c := New[string, int](
		WithOnEvict[string, int](func(_ string, _ int, r EvictionReason) { gotReason = r }),
	)
	defer c.Close()

	c.SetWithTTL("a", 1, time.Millisecond)
	time.Sleep(10 * time.Millisecond)
	c.ExpireOld()

	assert.Equal(t, EvictionExpired, gotReason)
}

func TestCache_OnEvict_PanicRecovered(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](1),
		WithOnEvict[string, int](func(_ string, _ int, _ EvictionReason) {
			panic("callback boom")
		}),
	)
	defer c.Close()

	assert.NotPanics(t, func() {
		c.Set("a", 1)
		c.Set("b", 2) // triggers eviction → panic in callback
	})
}

func TestCache_CleanupTicker_RemovesExpired(t *testing.T) {
	c := New[string, int](
		WithTTL[string, int](10*time.Millisecond),
		WithCleanupInterval[string, int](5*time.Millisecond),
	)
	defer c.Close()
	c.Set("a", 1)

	require.Eventually(t, func() bool {
		return c.Len() == 0
	}, time.Second, 5*time.Millisecond)
}

func TestEvictionReason_String(t *testing.T) {
	tests := []struct {
		reason EvictionReason
		want   string
	}{
		{EvictionCapacity, "capacity"},
		{EvictionExpired, "expired"},
		{EvictionDeleted, "deleted"},
		{EvictionCleared, "cleared"},
		{EvictionReplaced, "replaced"},
		{EvictionReason(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.reason.String())
		})
	}
}

func TestCache_RaceSafe(t *testing.T) {
	c := New[int, int](WithCapacity[int, int](256))
	defer c.Close()

	var ctr atomic.Int64
	testx.HammerNoError(t, 50, 500, func() error {
		k := int(ctr.Add(1) % 512)
		c.Set(k, k)
		c.Get(k)
		c.GetFast(k)
		c.Has(k)
		return nil
	})
}

func TestCache_GetOrCompute_Basic(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	var calls atomic.Int64
	v, err := c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
		calls.Add(1)
		return 42, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 42, v)

	v, err = c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
		calls.Add(1)
		return 99, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 42, v)
	assert.Equal(t, int64(1), calls.Load())
}

func TestCache_GetOrCompute_WithTTL(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	_, err := c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
		return 1, nil
	}, WithComputeTTL(20*time.Millisecond))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		_, ok := c.Get("k")
		return !ok
	}, time.Second, 5*time.Millisecond)
}

func TestCache_GetOrCompute_Singleflight(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	var calls atomic.Int64
	errs := testx.Hammer(20, 1, func() error {
		_, err := c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
			calls.Add(1)
			time.Sleep(10 * time.Millisecond)
			return 1, nil
		}, WithSingleflight())
		return err
	})
	assert.Empty(t, errs)
	assert.Equal(t, int64(1), calls.Load())
}

func TestCache_GetOrCompute_ComputeError_NotCached(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	_, err := c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
		return 0, ErrNotFound
	})
	require.ErrorIs(t, err, ErrNotFound)
	assert.False(t, c.Has("k"))
}

func TestCache_GetOrCompute_CancelledContext(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	_, err := c.GetOrCompute(testx.CancelledCtx(), "k", func(context.Context) (int, error) {
		return 1, nil
	})
	require.ErrorIs(t, err, context.Canceled)
}

func TestCache_GetOrCompute_ClosedCache(t *testing.T) {
	c := New[string, int]()
	c.Close()

	_, err := c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
		return 1, nil
	})
	require.ErrorIs(t, err, ErrClosed)
}
