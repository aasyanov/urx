package poolx

import (
	"context"
	"sync"
	"testing"
	"time"
)

func BenchmarkWorkerPool_Submit(b *testing.B) {
	wp := NewWorkerPool(WithWorkers(4), WithQueueSize(256))
	defer wp.Close()
	b.ReportAllocs()
	var wg sync.WaitGroup
	for b.Loop() {
		wg.Add(1)
		wp.Submit(context.Background(), func(ctx context.Context) error {
			wg.Done()
			return nil
		})
	}
	wg.Wait()
}

func BenchmarkObjectPool_GetPut(b *testing.B) {
	op := NewObjectPool(func() []byte { return make([]byte, 0, 1024) })
	b.ReportAllocs()
	for b.Loop() {
		buf := op.Get()
		op.Put(buf[:0])
	}
}

func BenchmarkBatch_Add(b *testing.B) {
	bt := NewBatch(func(items []int) error { return nil },
		WithBatchSize(1000), WithFlushInterval(time.Hour))
	defer bt.Close()
	b.ReportAllocs()
	for b.Loop() {
		bt.Add(1)
	}
}
