package lrux

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClosed_AllReadPathsNoOp covers the closed guards on every read and
// mutate path that returns early.
func TestClosed_AllReadPathsNoOp(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	c.Set("a", 1)
	c.Close()

	_, ok := c.GetFast("a")
	assert.False(t, ok)
	_, ok = c.Peek("a")
	assert.False(t, ok)
	assert.Equal(t, time.Duration(0), c.TTL("a"))
	assert.False(t, c.Touch("a"))
	assert.NotPanics(t, c.Clear)
	assert.NotPanics(t, func() { c.Resize(1) })
	assert.NotPanics(t, func() { c.SetMulti(map[string]int{"x": 1}) })
	assert.Equal(t, 0, c.DeleteMulti([]string{"a"}))
	assert.NotPanics(t, func() { c.Range(func(string, int) bool { return true }) })
	_, err := c.GetOrCompute(context.Background(), "a", func(context.Context) (int, error) {
		return 1, nil
	})
	assert.ErrorIs(t, err, ErrClosed)
}

// TestIteration_SweepsExpiredWithCallback covers the expired-entry branch in
// Keys/Values/Snapshot/Range when an eviction callback is configured.
func TestIteration_SweepsExpiredWithCallback(t *testing.T) {
	newCache := func() *Cache[string, int] {
		evicted := 0
		c := New[string, int](
			WithOnEvict[string, int](func(string, int, EvictionReason) { evicted++ }),
		)
		c.SetWithTTL("exp1", 1, time.Millisecond)
		c.SetWithTTL("exp2", 2, time.Millisecond)
		c.Set("live", 3)
		time.Sleep(10 * time.Millisecond)
		return c
	}

	t.Run("Keys", func(t *testing.T) {
		c := newCache()
		defer c.Close()
		assert.Equal(t, []string{"live"}, c.Keys())
	})
	t.Run("Values", func(t *testing.T) {
		c := newCache()
		defer c.Close()
		assert.Equal(t, []int{3}, c.Values())
	})
	t.Run("Snapshot", func(t *testing.T) {
		c := newCache()
		defer c.Close()
		snap := c.Snapshot()
		assert.Len(t, snap, 1)
		assert.Equal(t, "live", snap[0].Key)
	})
	t.Run("Range", func(t *testing.T) {
		c := newCache()
		defer c.Close()
		var keys []string
		c.Range(func(k string, _ int) bool {
			keys = append(keys, k)
			return true
		})
		assert.Equal(t, []string{"live"}, keys)
	})
}

// TestGetOrCompute_CapacityEvictionDuringInsert covers the capacity branch inside
// insertLocked and removeTailLocked reached via GetOrCompute.
func TestGetOrCompute_CapacityEvictionDuringInsert(t *testing.T) {
	var evicted int
	c := New[int, int](
		WithCapacity[int, int](2),
		WithOnEvict[int, int](func(int, int, EvictionReason) { evicted++ }),
	)
	defer c.Close()

	for i := range 3 {
		_, err := c.GetOrCompute(context.Background(), i, func(context.Context) (int, error) {
			return i, nil
		})
		require.NoError(t, err)
	}

	assert.Equal(t, 2, c.Len())
	assert.Equal(t, 1, evicted)
}

// TestResize_EvictsWithCallback covers Resize shrinking with an eviction
// callback so the events branch is exercised.
func TestResize_EvictsWithCallback(t *testing.T) {
	var evicted int
	c := New[int, int](
		WithCapacity[int, int](5),
		WithOnEvict[int, int](func(int, int, EvictionReason) { evicted++ }),
	)
	defer c.Close()
	for i := range 5 {
		c.Set(i, i)
	}

	c.Resize(2)
	assert.Equal(t, 2, c.Len())
	assert.Equal(t, 3, evicted)
}

// TestSetMulti_EvictsWithCallback covers the capacity loop in SetMulti.
func TestSetMulti_EvictsWithCallback(t *testing.T) {
	var evicted int
	c := New[int, int](
		WithCapacity[int, int](3),
		WithOnEvict[int, int](func(int, int, EvictionReason) { evicted++ }),
	)
	defer c.Close()

	c.SetMulti(map[int]int{1: 1, 2: 2, 3: 3, 4: 4, 5: 5})
	assert.Equal(t, 3, c.Len())
	assert.Equal(t, 2, evicted)
}

// TestSetMulti_UpdateExistingWithCallback covers the replace branch in
// SetMulti where the key already exists.
func TestSetMulti_UpdateExistingWithCallback(t *testing.T) {
	var replaced int
	c := New[int, int](
		WithOnEvict[int, int](func(_ int, _ int, r EvictionReason) {
			if r == EvictionReplaced {
				replaced++
			}
		}),
	)
	defer c.Close()

	c.Set(1, 10)
	c.SetMulti(map[int]int{1: 11, 2: 22})
	assert.Equal(t, 1, replaced)
	v, _ := c.Get(1)
	assert.Equal(t, 11, v)
}

func TestShardedCache_IsClosed_BeforeClose(t *testing.T) {
	c := NewSharded[string, int]()
	defer c.Close()
	assert.False(t, c.IsClosed())
}

// TestTouch_ExpiredRemovesWithCallback covers the expired branch in Touch
// where the entry is removed and the eviction callback fires.
func TestTouch_ExpiredRemovesWithCallback(t *testing.T) {
	var reason EvictionReason
	c := New[string, int](
		WithOnEvict[string, int](func(_ string, _ int, r EvictionReason) { reason = r }),
	)
	defer c.Close()

	c.SetWithTTL("a", 1, time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	assert.False(t, c.Touch("a"))
	assert.Equal(t, EvictionExpired, reason)
	assert.Equal(t, 0, c.Len())
}

// TestGet_ExpiredRemovesWithCallback covers the expired branch in Get.
func TestGet_ExpiredRemovesWithCallback(t *testing.T) {
	var reason EvictionReason
	c := New[string, int](
		WithOnEvict[string, int](func(_ string, _ int, r EvictionReason) { reason = r }),
	)
	defer c.Close()

	c.SetWithTTL("a", 1, time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	_, ok := c.Get("a")
	assert.False(t, ok)
	assert.Equal(t, EvictionExpired, reason)
}

// TestDeleteMulti_MissingKeys covers DeleteMulti with absent keys.
func TestDeleteMulti_MissingKeys(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.Set("a", 1)

	assert.Equal(t, 1, c.DeleteMulti([]string{"a", "missing1", "missing2"}))
}

func TestDeleteMulti_ClosedCache(t *testing.T) {
	c := New[string, int]()
	c.Set("a", 1)
	c.Close()
	assert.Equal(t, 0, c.DeleteMulti([]string{"a"}))
}

func TestResize_ClosedCache(t *testing.T) {
	c := New[string, int]()
	c.Set("a", 1)
	c.Close()
	assert.NotPanics(t, func() { c.Resize(1) })
	assert.Equal(t, 0, c.Len())
}

func TestSlideExpiration_GlobalTTL(t *testing.T) {
	c := New[string, int](WithTTL[string, int](time.Hour))
	defer c.Close()

	now := time.Now()
	c.mu.Lock()
	n := &node[string, int]{key: "session", value: 1, createdAt: now, accessedAt: now}
	c.items["session"] = n
	c.listPushFront(n)
	assert.True(t, n.expiresAt.IsZero())
	c.slideExpiration(n, now)
	c.mu.Unlock()
	assert.False(t, n.expiresAt.IsZero())
	assert.Greater(t, n.expiresAt, now)
}

func TestSlideExpiration_RemainingNonPositive(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.SetWithTTL("session", 1, time.Hour)

	c.mu.Lock()
	n := c.items["session"]
	n.accessedAt = n.expiresAt
	c.slideExpiration(n, time.Now())
	exp := n.expiresAt
	c.mu.Unlock()
	assert.Equal(t, exp, n.expiresAt)
}
