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

func TestNewSharded_RoundsShardCount(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](10))
	defer c.Close()
	assert.Len(t, c.shards, 16) // 10 rounded up to 16
}

func TestNewSharded_Defaults(t *testing.T) {
	c := NewSharded[string, int]()
	defer c.Close()
	assert.Len(t, c.shards, defaultShardCount)
}

func TestShardedCache_SetGet(t *testing.T) {
	c := NewSharded[string, int]()
	defer c.Close()

	c.Set("a", 1)
	v, ok := c.Get("a")
	require.True(t, ok)
	assert.Equal(t, 1, v)

	_, ok = c.Get("missing")
	assert.False(t, ok)
}

func TestShardedCache_DistributesKeys(t *testing.T) {
	c := NewSharded[int, int](WithShardCount[int, int](16))
	defer c.Close()

	for i := range 1000 {
		c.Set(i, i)
	}
	assert.Equal(t, 1000, c.Len())

	used := 0
	for _, s := range c.shards {
		if s.Len() > 0 {
			used++
		}
	}
	assert.Greater(t, used, 1, "keys should spread across multiple shards")
}

func TestShardedCache_CapacityPerShard(t *testing.T) {
	c := NewSharded[int, int](
		WithShardCount[int, int](2),
		WithShardCapacity[int, int](10),
	)
	defer c.Close()

	for i := range 1000 {
		c.Set(i, i)
	}
	// total capacity = 2 shards * 10 = 20
	assert.LessOrEqual(t, c.Len(), 20)
}

func TestShardedCache_AllAccessors(t *testing.T) {
	c := NewSharded[string, int](WithShardTTL[string, int](time.Hour))
	defer c.Close()

	c.Set("a", 1)
	c.SetWithTTL("b", 2, time.Hour)

	v, ok := c.GetFast("a")
	require.True(t, ok)
	assert.Equal(t, 1, v)

	v, ok = c.Peek("b")
	require.True(t, ok)
	assert.Equal(t, 2, v)

	assert.True(t, c.Has("a"))
	assert.True(t, c.Touch("a"))
	assert.Greater(t, c.TTL("a"), time.Duration(0))

	e := c.GetEntry("a")
	require.NotNil(t, e)
	assert.Equal(t, 1, e.Value)

	assert.True(t, c.Delete("a"))
}

func TestShardedCache_BatchOperations(t *testing.T) {
	c := NewSharded[int, int]()
	defer c.Close()

	items := make(map[int]int, 200)
	keys := make([]int, 0, 200)
	for i := range 200 {
		items[i] = i * 10
		keys = append(keys, i)
	}

	c.SetMulti(items) // exceeds parallelBatchThreshold → parallel path
	assert.Equal(t, 200, c.Len())

	got := c.GetMulti(keys)
	assert.Len(t, got, 200)
	assert.Equal(t, 50, got[5])

	n := c.DeleteMulti(keys)
	assert.Equal(t, 200, n)
	assert.Equal(t, 0, c.Len())
}

func TestShardedCache_BatchOperations_Sequential(t *testing.T) {
	c := NewSharded[int, int]()
	defer c.Close()

	items := map[int]int{1: 10, 2: 20, 3: 30} // below threshold → sequential
	c.SetMulti(items)
	assert.Equal(t, 3, c.Len())

	got := c.GetMulti([]int{1, 2, 99})
	assert.Len(t, got, 2)

	assert.Equal(t, 2, c.DeleteMulti([]int{1, 2, 99}))
}

func TestShardedCache_EmptyBatches(t *testing.T) {
	c := NewSharded[int, int]()
	defer c.Close()

	c.SetMulti(nil)
	assert.Empty(t, c.GetMulti(nil))
	assert.Equal(t, 0, c.DeleteMulti(nil))
}

func TestShardedCache_Iteration(t *testing.T) {
	c := NewSharded[int, int]()
	defer c.Close()
	for i := range 20 {
		c.Set(i, i)
	}

	assert.Len(t, c.Keys(), 20)
	assert.Len(t, c.Values(), 20)
	assert.Len(t, c.Snapshot(), 20)

	count := 0
	c.Range(func(int, int) bool {
		count++
		return true
	})
	assert.Equal(t, 20, count)
}

func TestShardedCache_Range_StopsEarly(t *testing.T) {
	c := NewSharded[int, int]()
	defer c.Close()
	for i := range 100 {
		c.Set(i, i)
	}

	count := 0
	c.Range(func(int, int) bool {
		count++
		return count < 5
	})
	assert.Equal(t, 5, count)
}

func TestShardedCache_StatsAggregated(t *testing.T) {
	c := NewSharded[int, int]()
	defer c.Close()
	for i := range 10 {
		c.Set(i, i)
		c.Get(i)
	}
	c.Get(999) // miss

	stats := c.Stats()
	assert.Equal(t, 10, stats.Size)
	assert.Equal(t, uint64(10), stats.Hits)
	assert.Equal(t, uint64(1), stats.Misses)

	c.ResetStats()
	assert.Zero(t, c.Stats().Hits)
}

func TestShardedCache_ClearExpireResize(t *testing.T) {
	c := NewSharded[int, int](WithShardCapacity[int, int](100))
	defer c.Close()
	for i := range 50 {
		c.Set(i, i)
	}

	c.Resize(1)
	assert.LessOrEqual(t, c.Len(), len(c.shards))

	c.SetMulti(map[int]int{100: 1, 101: 2})
	assert.GreaterOrEqual(t, c.ExpireOld(), 0)

	c.Clear()
	assert.Equal(t, 0, c.Len())
	assert.Equal(t, 0, c.LenValid())
}

func TestShardedCache_Compute(t *testing.T) {
	c := NewSharded[string, int]()
	defer c.Close()

	v, err := c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
		return 5, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 5, v)

	v2, err := c.GetOrCompute(context.Background(), "k2", func(context.Context) (int, error) {
		return 6, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 6, v2)
}

func TestShardedCache_Close(t *testing.T) {
	c := NewSharded[string, int]()
	c.Set("a", 1)

	testx.AssertCloseIdempotent(t, c)
	assert.True(t, c.IsClosed())
}

func TestShardedCache_OnEvict(t *testing.T) {
	var evictions atomic.Int64
	c := NewSharded[int, int](
		WithShardCount[int, int](1),
		WithShardCapacity[int, int](1),
		WithShardOnEvict[int, int](func(int, int, EvictionReason) {
			evictions.Add(1)
		}),
	)
	defer c.Close()

	c.Set(1, 1)
	c.Set(2, 2) // evicts key 1
	assert.Equal(t, int64(1), evictions.Load())
}

func TestShardedCache_RaceSafe(t *testing.T) {
	c := NewSharded[int, int](WithShardCapacity[int, int](64))
	defer c.Close()

	var ctr atomic.Int64
	testx.HammerNoError(t, 50, 500, func() error {
		k := int(ctr.Add(1) % 1024)
		c.Set(k, k)
		c.Get(k)
		c.GetFast(k)
		return nil
	})
}
