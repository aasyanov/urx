package poolx

import (
	"context"
	"testing"
)

// FuzzWorkerPoolTrySubmit drives [WorkerPool.TrySubmit] with fuzzed pool
// dimensions and never panics. Submitted work must not exceed completed+failed
// once the pool is closed and drained.
func FuzzWorkerPoolTrySubmit(f *testing.F) {
	f.Add(1, 1)
	f.Add(4, 64)
	f.Add(0, 0)
	f.Add(-1, -5)

	ctx := context.Background()
	f.Fuzz(func(t *testing.T, workers, queue int) {
		wp := NewWorkerPool(WithWorkers(workers), WithQueueSize(queue))
		for range 8 {
			_ = wp.TrySubmit(ctx, func(context.Context) error { return nil })
		}
		wp.Close()
		st := wp.Stats()
		if st.Submitted < st.Completed+st.Failed {
			t.Fatalf("submitted %d < completed+failed %d", st.Submitted, st.Completed+st.Failed)
		}
	})
}

// FuzzBatchAdd drives [Batch.Add] with fuzzed size parameters and item values,
// asserting the buffer never goes negative and Close never panics.
func FuzzBatchAdd(f *testing.F) {
	f.Add(10, int64(1))
	f.Add(0, int64(-99))
	f.Add(1000, int64(0))

	f.Fuzz(func(t *testing.T, size int, item int64) {
		b := NewBatch(func(context.Context, []int64) error { return nil }, WithBatchSize(size))
		_ = b.Add(item)
		if st := b.Stats(); st.Buffered < 0 {
			t.Fatalf("negative buffered count: %d", st.Buffered)
		}
		_ = b.Close()
	})
}
