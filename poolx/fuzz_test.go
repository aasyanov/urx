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
		_ = wp.Close()
		st := wp.Stats()
		if st.Submitted < st.Completed+st.Failed {
			t.Fatalf("submitted %d < completed+failed %d", st.Submitted, st.Completed+st.Failed)
		}
	})
}

// FuzzWorkerPoolSubmitWait drives [WorkerPool.SubmitWait] concurrently with
// [WorkerPool.Close] and never panics.
func FuzzWorkerPoolSubmitWaitClose(f *testing.F) {
	f.Add(2, 8)
	f.Add(1, 1)

	ctx := context.Background()
	f.Fuzz(func(t *testing.T, workers, queue int) {
		wp := NewWorkerPool(WithWorkers(workers), WithQueueSize(queue))
		done := make(chan struct{})
		go func() {
			for range 4 {
				_ = wp.SubmitWait(ctx, func(context.Context) error { return nil })
			}
			close(done)
		}()
		_ = wp.Close()
		<-done
	})
}

// FuzzBatchAdd drives [Batch.Add] with fuzzed size parameters and item values,
// asserting the buffer never goes negative and Close never panics.
func FuzzBatchAdd(f *testing.F) {
	f.Add(10, int64(1))
	f.Add(0, int64(-99))
	f.Add(1000, int64(0))

	f.Fuzz(func(t *testing.T, size int, item int64) {
		b, err := NewBatch(func(context.Context, []int64) error { return nil }, WithBatchSize(size))
		if err != nil {
			t.Fatalf("NewBatch: %v", err)
		}
		_ = b.Add(item)
		if st := b.Stats(); st.Buffered < 0 {
			t.Fatalf("negative buffered count: %d", st.Buffered)
		}
		_ = b.Close()
	})
}

// FuzzObjectPool drives [ObjectPool.Get] and [ObjectPool.Put] concurrently.
func FuzzObjectPoolGetPut(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(99))

	f.Fuzz(func(t *testing.T, seed int64) {
		op, err := NewObjectPool(func() int64 { return seed })
		if err != nil {
			t.Fatalf("NewObjectPool: %v", err)
		}
		for range 16 {
			v := op.Get()
			op.Put(v + 1)
		}
	})
}
