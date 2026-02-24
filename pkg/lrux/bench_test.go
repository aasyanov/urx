package lrux

import (
	"strconv"
	"testing"
	"time"
)

// --- LRU Set ---

func BenchmarkLRU_Set(b *testing.B) {
	c := New[string, int](WithCapacity[string, int](8192))
	defer c.Close()

	keys := make([]string, 8192)
	for i := range keys {
		keys[i] = strconv.Itoa(i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.Set(keys[i%8192], i)
		i++
	}
}

// --- LRU Get (hit) ---

func BenchmarkLRU_Get_Hit(b *testing.B) {
	c := New[string, int](WithCapacity[string, int](8192))
	defer c.Close()

	for i := 0; i < 8192; i++ {
		c.Set(strconv.Itoa(i), i)
	}

	keys := make([]string, 8192)
	for i := range keys {
		keys[i] = strconv.Itoa(i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.Get(keys[i%8192])
		i++
	}
}

// --- LRU Get (miss) ---

func BenchmarkLRU_Get_Miss(b *testing.B) {
	c := New[string, int](WithCapacity[string, int](8192))
	defer c.Close()

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.Get(strconv.Itoa(i))
		i++
	}
}

// --- LRU Peek ---

func BenchmarkLRU_Peek(b *testing.B) {
	c := New[string, int](WithCapacity[string, int](8192))
	defer c.Close()

	for i := 0; i < 8192; i++ {
		c.Set(strconv.Itoa(i), i)
	}

	keys := make([]string, 8192)
	for i := range keys {
		keys[i] = strconv.Itoa(i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.Peek(keys[i%8192])
		i++
	}
}

// --- LRU SetWithTTL ---

func BenchmarkLRU_SetWithTTL(b *testing.B) {
	c := New[string, int](WithCapacity[string, int](8192))
	defer c.Close()

	keys := make([]string, 8192)
	for i := range keys {
		keys[i] = strconv.Itoa(i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.SetWithTTL(keys[i%8192], i, time.Hour)
		i++
	}
}

// --- LRU GetOrCompute (miss) ---

func BenchmarkLRU_GetOrCompute_Miss(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](8192))
	defer c.Close()

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.GetOrCompute(i%8192, func() int { return i })
		i++
	}
}

// --- LRU GetOrCompute (hit) ---

func BenchmarkLRU_GetOrCompute_Hit(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](8192))
	defer c.Close()

	for i := 0; i < 8192; i++ {
		c.Set(i, i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.GetOrCompute(i%8192, func() int { return i })
		i++
	}
}

// --- LRU GetOrCompute with singleflight ---

func BenchmarkLRU_GetOrCompute_Singleflight(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](8192))
	defer c.Close()

	for i := 0; i < 8192; i++ {
		c.Set(i, i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.GetOrCompute(i%8192, func() int { return i }, WithSingleflight())
		i++
	}
}

// --- LRU Delete ---

func BenchmarkLRU_Delete(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](1 << 20))
	defer c.Close()

	for i := 0; i < 1<<20; i++ {
		c.Set(i, i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.Delete(i % (1 << 20))
		i++
	}
}

// --- LRU Stats ---

func BenchmarkLRU_Stats(b *testing.B) {
	c := New[string, int](WithCapacity[string, int](1000))
	defer c.Close()

	for i := 0; i < 1000; i++ {
		c.Set(strconv.Itoa(i), i)
	}
	c.Get("0")
	c.Get("missing")

	b.ResetTimer()
	for b.Loop() {
		c.Stats()
	}
}

// --- Concurrent Get ---

func BenchmarkLRU_Concurrent_Get(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](8192))
	defer c.Close()

	for i := 0; i < 8192; i++ {
		c.Set(i, i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			c.Get(i % 8192)
			i++
		}
	})
}

// --- Concurrent Set ---

func BenchmarkLRU_Concurrent_Set(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](8192))
	defer c.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			c.Set(i%8192, i)
			i++
		}
	})
}

// --- Concurrent mixed ---

func BenchmarkLRU_Concurrent_Mixed(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](8192))
	defer c.Close()

	for i := 0; i < 8192; i++ {
		c.Set(i, i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%3 == 0 {
				c.Set(i%8192, i)
			} else {
				c.Get(i % 8192)
			}
			i++
		}
	})
}

// --- Sharded Set ---

func BenchmarkSharded_Set(b *testing.B) {
	c := NewSharded[int, int](WithShardCount[int, int](16), WithShardCapacity[int, int](1024))
	defer c.Close()

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.Set(i%16384, i)
		i++
	}
}

// --- Sharded Get (hit) ---

func BenchmarkSharded_Get_Hit(b *testing.B) {
	c := NewSharded[int, int](WithShardCount[int, int](16), WithShardCapacity[int, int](1024))
	defer c.Close()

	for i := 0; i < 16384; i++ {
		c.Set(i, i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.Get(i % 16384)
		i++
	}
}

// --- Sharded Concurrent Get ---

func BenchmarkSharded_Concurrent_Get(b *testing.B) {
	c := NewSharded[int, int](WithShardCount[int, int](16), WithShardCapacity[int, int](1024))
	defer c.Close()

	for i := 0; i < 16384; i++ {
		c.Set(i, i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			c.Get(i % 16384)
			i++
		}
	})
}

// --- Sharded Concurrent Set ---

func BenchmarkSharded_Concurrent_Set(b *testing.B) {
	c := NewSharded[int, int](WithShardCount[int, int](16), WithShardCapacity[int, int](1024))
	defer c.Close()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			c.Set(i%16384, i)
			i++
		}
	})
}

// --- Sharded Concurrent Mixed ---

func BenchmarkSharded_Concurrent_Mixed(b *testing.B) {
	c := NewSharded[int, int](WithShardCount[int, int](16), WithShardCapacity[int, int](1024))
	defer c.Close()

	for i := 0; i < 16384; i++ {
		c.Set(i, i)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%3 == 0 {
				c.Set(i%16384, i)
			} else {
				c.Get(i % 16384)
			}
			i++
		}
	})
}

// --- Sharded Concurrent Contention (all goroutines hit same key) ---

func BenchmarkSharded_Concurrent_Contention(b *testing.B) {
	c := NewSharded[string, int](WithShardCount[string, int](16), WithShardCapacity[string, int](1024))
	defer c.Close()

	c.Set("hot", 1)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			c.Get("hot")
		}
	})
}

// --- Sharded SetMulti (parallel threshold) ---

func BenchmarkSharded_SetMulti(b *testing.B) {
	c := NewSharded[int, int](WithShardCount[int, int](16), WithShardCapacity[int, int](10000))
	defer c.Close()

	items := make(map[int]int, 100)
	for i := 0; i < 100; i++ {
		items[i] = i
	}

	b.ResetTimer()
	for b.Loop() {
		c.SetMulti(items)
	}
}

// --- LRU with eviction callback overhead ---

func BenchmarkLRU_Set_WithEviction(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](1024), WithOnEvict[int, int](func(_ int, _ int, _ EvictionReason) {}))
	defer c.Close()

	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.Set(i, i)
		i++
	}
}

// --- LRU ExpireOld ---

func BenchmarkLRU_ExpireOld(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](8192), WithTTL[int, int](time.Nanosecond))
	defer c.Close()

	for i := 0; i < 8192; i++ {
		c.Set(i, i)
	}

	b.ResetTimer()
	for b.Loop() {
		c.ExpireOld()
		for i := 0; i < 8192; i++ {
			c.Set(i, i)
		}
	}
}

// --- Hasher ---

func BenchmarkHasher_String(b *testing.B) {
	h := newHasher[string]()
	b.ResetTimer()
	for b.Loop() {
		h("benchmark-key-12345")
	}
}

func BenchmarkHasher_Int(b *testing.B) {
	h := newHasher[int]()
	b.ResetTimer()
	for b.Loop() {
		h(42)
	}
}

// --- LRU vs Sharded: single goroutine comparison ---

func BenchmarkComparison_SingleGoroutine_LRU(b *testing.B) {
	c := New[int, int](WithCapacity[int, int](8192))
	defer c.Close()

	for i := 0; i < 8192; i++ {
		c.Set(i, i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		if i%2 == 0 {
			c.Set(i%8192, i)
		} else {
			c.Get(i % 8192)
		}
		i++
	}
}

func BenchmarkComparison_SingleGoroutine_Sharded(b *testing.B) {
	c := NewSharded[int, int](WithShardCount[int, int](16), WithShardCapacity[int, int](512))
	defer c.Close()

	for i := 0; i < 8192; i++ {
		c.Set(i, i)
	}

	b.ResetTimer()
	i := 0
	for b.Loop() {
		if i%2 == 0 {
			c.Set(i%8192, i)
		} else {
			c.Get(i % 8192)
		}
		i++
	}
}

// --- Parallel scaling: 1, 4, 8, 16 goroutines ---

func BenchmarkLRU_ParallelScaling(b *testing.B) {
	for _, procs := range []int{1, 4, 8, 16} {
		b.Run(strconv.Itoa(procs), func(b *testing.B) {
			c := New[int, int](WithCapacity[int, int](8192))
			defer c.Close()

			for i := 0; i < 8192; i++ {
				c.Set(i, i)
			}

			b.SetParallelism(procs)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					if i%3 == 0 {
						c.Set(i%8192, i)
					} else {
						c.Get(i % 8192)
					}
					i++
				}
			})
		})
	}
}

func BenchmarkSharded_ParallelScaling(b *testing.B) {
	for _, procs := range []int{1, 4, 8, 16} {
		b.Run(strconv.Itoa(procs), func(b *testing.B) {
			c := NewSharded[int, int](WithShardCount[int, int](16), WithShardCapacity[int, int](512))
			defer c.Close()

			for i := 0; i < 8192; i++ {
				c.Set(i, i)
			}

			b.SetParallelism(procs)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					if i%3 == 0 {
						c.Set(i%8192, i)
					} else {
						c.Get(i % 8192)
					}
					i++
				}
			})
		})
	}
}

// --- Allocation check: Set should be 1 alloc (node) ---

func BenchmarkLRU_Set_Allocs(b *testing.B) {
	c := New[string, int](WithCapacity[string, int](1 << 20))
	defer c.Close()

	keys := make([]string, 8192)
	for i := range keys {
		keys[i] = strconv.Itoa(i)
	}

	b.ReportAllocs()
	b.ResetTimer()
	i := 0
	for b.Loop() {
		c.Set(keys[i%8192], i)
		i++
	}
}
