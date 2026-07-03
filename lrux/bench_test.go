package lrux

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
)

func BenchmarkCache_Set(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](1024))
	defer c.Close()

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.Set(i&1023, i)
		i++
	}
}

func BenchmarkCache_Get_Hit(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](1024))
	defer c.Close()
	for i := range 1024 {
		c.Set(i, i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.Get(i & 1023)
		i++
	}
}

func BenchmarkCache_Get_Miss(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](1024))
	defer c.Close()

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.Get(i + 1<<20)
		i++
	}
}

func BenchmarkCache_GetFast_Hit(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](1024))
	defer c.Close()
	for i := range 1024 {
		c.Set(i, i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.GetFast(i & 1023)
		i++
	}
}

func BenchmarkCache_Mixed(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](1024))
	defer c.Close()
	for i := range 1024 {
		c.Set(i, i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		if i%10 == 0 {
			c.Set(i&1023, i)
		} else {
			c.Get(i & 1023)
		}
		i++
	}
}

func BenchmarkCache_Get_Parallel(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](1024))
	defer c.Close()
	for i := range 1024 {
		c.Set(i, i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var i int
		for pb.Next() {
			c.Get(i & 1023)
			i++
		}
	})
}

func BenchmarkShardedCache_Set_Parallel(b *testing.B) {
	c := NewSharded[int, int](
		WithShardCount[int, int](16),
		WithShardCapacity[int, int](1024),
	)
	defer c.Close()

	b.ResetTimer()
	var counter atomic.Int64
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			k := int(counter.Add(1))
			c.Set(k&16383, k)
		}
	})
}

func BenchmarkShardedCache_Get_Parallel(b *testing.B) {
	c := NewSharded[int, int](
		WithShardCount[int, int](16),
		WithShardCapacity[int, int](1024),
	)
	defer c.Close()
	for i := range 16384 {
		c.Set(i, i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var i int
		for pb.Next() {
			c.Get(i & 16383)
			i++
		}
	})
}

func BenchmarkCache_GetOrCompute_Hit(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](1024))
	defer c.Close()
	for i := range 1024 {
		c.Set(i, i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		_, _ = c.GetOrCompute(context.Background(), i&1023, func(context.Context) (int, error) {
			return 0, nil
		})
		i++
	}
}

func BenchmarkCache_GetOrCompute_Miss(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](1<<20))
	defer c.Close()

	b.ResetTimer()
	i := 0
	for b.Loop() {
		_, _ = c.GetOrCompute(context.Background(), i+1<<20, func(context.Context) (int, error) {
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
