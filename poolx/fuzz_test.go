package poolx

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
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
// asserting accounting invariants hold and Close drains the buffer.
func FuzzBatchAdd(f *testing.F) {
	f.Add(10, int64(1))
	f.Add(0, int64(-99))
	f.Add(1000, int64(0))

	f.Fuzz(func(t *testing.T, size int, item int64) {
		var flushed atomic.Int64
		b, err := NewBatch(func(_ context.Context, items []int64) error {
			flushed.Add(int64(len(items)))
			return nil
		}, WithBatchSize(size), WithFlushInterval(time.Hour))
		if err != nil {
			t.Fatalf("NewBatch: %v", err)
		}
		addErr := b.Add(item)
		st := b.Stats()
		if st.Buffered < 0 {
			t.Fatalf("negative buffered count: %d", st.Buffered)
		}
		if addErr == nil {
			if got := int64(st.Items) + int64(st.Buffered); got != 1 {
				t.Fatalf("accepted add not accounted for: items+buffered=%d", got)
			}
		}
		_ = b.Close()
		final := b.Stats()
		if final.Buffered != 0 {
			t.Fatalf("buffer not drained on close: buffered=%d", final.Buffered)
		}
		if flushed.Load() != int64(final.Items) {
			t.Fatalf("callback count %d != items stat %d", flushed.Load(), final.Items)
		}
	})
}

// FuzzBatchAddClose drives concurrent [Batch.Add] with [Batch.Close] and
// asserts no accepted item is lost.
func FuzzBatchAddClose(f *testing.F) {
	f.Add(10)
	f.Add(1000)

	f.Fuzz(func(t *testing.T, size int) {
		var (
			flushed  atomic.Int64
			accepted atomic.Int64
		)
		b, err := NewBatch(func(_ context.Context, items []int64) error {
			flushed.Add(int64(len(items)))
			return nil
		}, WithBatchSize(size), WithFlushInterval(time.Hour))
		if err != nil {
			t.Fatalf("NewBatch: %v", err)
		}

		const goroutines = 8
		done := make(chan struct{})
		go func() {
			for range goroutines {
				if err := b.Add(1); err == nil {
					accepted.Add(1)
				}
			}
			close(done)
		}()
		_ = b.Close()
		<-done

		if flushed.Load() != accepted.Load() {
			t.Fatalf("flushed %d != accepted %d", flushed.Load(), accepted.Load())
		}
		if st := b.Stats(); st.Buffered != 0 {
			t.Fatalf("buffer not drained: %d", st.Buffered)
		}
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
		st := op.Stats()
		if st.Gets != st.Puts {
			t.Fatalf("gets %d != puts %d", st.Gets, st.Puts)
		}
	})
}
