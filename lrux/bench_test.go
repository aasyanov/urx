package lrux

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
)

const (
	benchCacheCapacity = 1024
	benchShardedKeys   = 16384
	benchMissOffset    = 1 << 20
)

func BenchmarkCache_Set(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](benchCacheCapacity))
	defer c.Close()

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.Set(i&benchCacheCapacity-1, i)
		i++
	}
}

func BenchmarkCache_Get_Hit(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](benchCacheCapacity))
	defer c.Close()
	for i := range benchCacheCapacity {
		c.Set(i, i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.Get(i & (benchCacheCapacity - 1))
		i++
	}
}

func BenchmarkCache_Get_Miss(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](benchCacheCapacity))
	defer c.Close()

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.Get(i + benchMissOffset)
		i++
	}
}

func BenchmarkCache_GetFast_Hit(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](benchCacheCapacity))
	defer c.Close()
	for i := range benchCacheCapacity {
		c.Set(i, i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.GetFast(i & (benchCacheCapacity - 1))
		i++
	}
}

func BenchmarkCache_Mixed(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](benchCacheCapacity))
	defer c.Close()
	for i := range benchCacheCapacity {
		c.Set(i, i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		if i%10 == 0 {
			c.Set(i&(benchCacheCapacity-1), i)
		} else {
			c.Get(i & (benchCacheCapacity - 1))
		}
		i++
	}
}

func BenchmarkCache_Get_Parallel(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](benchCacheCapacity))
	defer c.Close()
	for i := range benchCacheCapacity {
		c.Set(i, i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var i int
		for pb.Next() {
			c.Get(i & (benchCacheCapacity - 1))
			i++
		}
	})
}

func BenchmarkShardedCache_Set_Parallel(b *testing.B) {
	c := NewSharded[int, int](
		WithShardCount[int, int](16),
		WithShardCapacity[int, int](benchCacheCapacity),
	)
	defer c.Close()

	b.ResetTimer()
	var counter atomic.Int64
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			k := int(counter.Add(1))
			c.Set(k&(benchShardedKeys-1), k)
		}
	})
}

func BenchmarkShardedCache_Get_Parallel(b *testing.B) {
	c := NewSharded[int, int](
		WithShardCount[int, int](16),
		WithShardCapacity[int, int](benchCacheCapacity),
	)
	defer c.Close()
	for i := range benchShardedKeys {
		c.Set(i, i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var i int
		for pb.Next() {
			c.Get(i & (benchShardedKeys - 1))
			i++
		}
	})
}

func BenchmarkCache_GetOrCompute_Hit(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](benchCacheCapacity))
	defer c.Close()
	for i := range benchCacheCapacity {
		c.Set(i, i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		_, _ = c.GetOrCompute(context.Background(), i&(benchCacheCapacity-1), func(context.Context) (int, error) {
			return 0, nil
		})
		i++
	}
}

func BenchmarkCache_GetOrCompute_Miss(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](benchMissOffset))
	defer c.Close()

	b.ResetTimer()
	i := 0
	for b.Loop() {
		_, _ = c.GetOrCompute(context.Background(), i+benchMissOffset, func(context.Context) (int, error) {
			return i, nil
		})
		i++
	}
}

func BenchmarkHasher_String(b *testing.B) {
	h := newHasher[string]()
	keys := make([]string, 256)
	for i := range keys {
		keys[i] = "key:" + strconv.Itoa(i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		_ = h(keys[i&255])
		i++
	}
}

func BenchmarkHasher_Int(b *testing.B) {
	h := newHasher[int]()

	b.ResetTimer()
	i := 0
	for b.Loop() {
		_ = h(i)
		i++
	}
}
