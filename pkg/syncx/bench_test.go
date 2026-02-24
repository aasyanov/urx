package syncx

import (
	"context"
	"testing"
)

func BenchmarkLazy_Get(b *testing.B) {
	l := NewLazy(func() (int, error) { return 42, nil })
	l.Get()
	b.ReportAllocs()
	for b.Loop() {
		l.Get()
	}
}

func BenchmarkGroup_Go(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		g, _ := NewGroup(context.Background())
		g.Go(func(ctx context.Context) error { return nil })
		g.Wait()
	}
}

func BenchmarkMap_StoreLoad(b *testing.B) {
	m := NewMap[int, int]()
	b.ReportAllocs()
	for b.Loop() {
		m.Store(1, 1)
		m.Load(1)
	}
}
