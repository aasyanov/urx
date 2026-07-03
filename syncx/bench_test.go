package syncx

import (
	"context"
	"testing"
)

func BenchmarkLazy_Get(b *testing.B) {
	l, _ := NewLazy(func() (int, error) { return 42, nil })
	_, _ = l.Get() // warm the cache
	b.ResetTimer()
	for b.Loop() {
		_, _ = l.Get()
	}
}

func BenchmarkLazy_Get_Parallel(b *testing.B) {
	l, _ := NewLazy(func() (int, error) { return 42, nil })
	_, _ = l.Get()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = l.Get()
		}
	})
}

func BenchmarkMap_Load_Hit(b *testing.B) {
	m := NewMap[int, int]()
	m.Store(1, 1)
	b.ResetTimer()
	for b.Loop() {
		_, _ = m.Load(1)
	}
}

func BenchmarkMap_Load_Miss(b *testing.B) {
	m := NewMap[int, int]()
	b.ResetTimer()
	for b.Loop() {
		_, _ = m.Load(1)
	}
}

func BenchmarkMap_Store(b *testing.B) {
	m := NewMap[int, int]()
	b.ResetTimer()
	for b.Loop() {
		m.Store(1, 1)
	}
}

func BenchmarkMap_Store_Parallel(b *testing.B) {
	m := NewMap[int, int]()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			m.Store(i, i)
			i++
		}
	})
}

func BenchmarkMap_LoadOrStore(b *testing.B) {
	m := NewMap[int, int]()
	m.Store(1, 1)
	b.ResetTimer()
	for b.Loop() {
		_, _ = m.LoadOrStore(1, 1)
	}
}

func BenchmarkMap_Load_Parallel(b *testing.B) {
	m := NewMap[int, int]()
	m.Store(1, 1)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = m.Load(1)
		}
	})
}

func BenchmarkGroup_Go(b *testing.B) {
	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		g, _ := NewGroup(ctx)
		g.Go(func(context.Context) error { return nil })
		_ = g.Wait()
	}
}

func BenchmarkGroup_Go_Parallel(b *testing.B) {
	ctx := context.Background()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			g, _ := NewGroup(ctx)
			g.Go(func(context.Context) error { return nil })
			_ = g.Wait()
		}
	})
}

func BenchmarkGroup_Go_Limited(b *testing.B) {
	ctx := context.Background()
	b.ResetTimer()
	for b.Loop() {
		g, _ := NewGroup(ctx, WithLimit(4))
		for range 4 {
			g.Go(func(context.Context) error { return nil })
		}
		_ = g.Wait()
	}
}
