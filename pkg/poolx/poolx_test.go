package poolx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/pkg/errx"
)

// ============================================================
// WorkerPool
// ============================================================

func TestWorkerPool_Submit(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2), WithQueueSize(8))
	defer wp.Close()

	var sum atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		wp.Submit(context.Background(), func(ctx context.Context) error {
			sum.Add(1)
			wg.Done()
			return nil
		})
	}
	wg.Wait()
	if sum.Load() != 10 {
		t.Fatalf("expected 10, got %d", sum.Load())
	}
}

func TestNewObjectPool_NilFactory_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil factory")
		}
	}()
	NewObjectPool[int](nil)
}

func TestNewBatch_NilFlush_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil flush")
		}
	}()
	NewBatch[int](nil)
}

func TestWorkerPool_SubmitClosed(t *testing.T) {
	wp := NewWorkerPool()
	wp.Close()

	err := wp.Submit(context.Background(), func(ctx context.Context) error { return nil })
	if err == nil {
		t.Fatal("expected error on closed pool")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeClosed {
		t.Fatalf("expected CLOSED error, got %v", err)
	}
}

func TestWorkerPool_TrySubmit_QueueFull(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(1), WithQueueSize(1))
	defer wp.Close()

	started := make(chan struct{})
	blocker := make(chan struct{})

	wp.Submit(context.Background(), func(ctx context.Context) error {
		close(started)
		<-blocker
		return nil
	})

	<-started

	wp.TrySubmit(context.Background(), func(ctx context.Context) error {
		<-blocker
		return nil
	})

	err := wp.TrySubmit(context.Background(), func(ctx context.Context) error { return nil })
	close(blocker)

	if err == nil {
		t.Fatal("expected queue full error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeQueueFull {
		t.Fatalf("expected QUEUE_FULL, got %v", err)
	}
}

func TestWorkerPool_PanicRecovery(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(1), WithQueueSize(4))
	defer wp.Close()

	done := make(chan struct{})
	wp.Submit(context.Background(), func(ctx context.Context) error {
		defer func() { close(done) }()
		panic("boom")
	})
	<-done
	time.Sleep(10 * time.Millisecond)

	s := wp.Stats()
	if s.Panics != 1 {
		t.Fatalf("expected 1 panic, got %d", s.Panics)
	}
}

func TestWorkerPool_Stats(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2), WithQueueSize(8))
	defer wp.Close()

	var wg sync.WaitGroup
	wg.Add(3)
	for i := 0; i < 3; i++ {
		wp.Submit(context.Background(), func(ctx context.Context) error {
			wg.Done()
			return nil
		})
	}
	wg.Wait()
	time.Sleep(10 * time.Millisecond)

	s := wp.Stats()
	if s.Submitted != 3 {
		t.Fatalf("expected submitted=3, got %d", s.Submitted)
	}
	if s.Completed != 3 {
		t.Fatalf("expected completed=3, got %d", s.Completed)
	}
	if s.Workers != 2 {
		t.Fatalf("expected workers=2, got %d", s.Workers)
	}
}

func TestWorkerPool_ResetStats(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(1), WithQueueSize(4))
	defer wp.Close()

	done := make(chan struct{})
	wp.Submit(context.Background(), func(ctx context.Context) error {
		close(done)
		return nil
	})
	<-done
	time.Sleep(10 * time.Millisecond)

	wp.ResetStats()
	s := wp.Stats()
	if s.Submitted != 0 || s.Completed != 0 {
		t.Fatalf("expected zeroed stats, got %+v", s)
	}
}

func TestWorkerPool_CloseIdempotent(t *testing.T) {
	wp := NewWorkerPool()
	wp.Close()
	wp.Close()
	if !wp.IsClosed() {
		t.Fatal("expected closed")
	}
}

// ============================================================
// ObjectPool
// ============================================================

func TestObjectPool_GetPut(t *testing.T) {
	op := NewObjectPool(func() []byte { return make([]byte, 0, 1024) })
	buf := op.Get()
	if cap(buf) != 1024 {
		t.Fatalf("expected cap 1024, got %d", cap(buf))
	}
	op.Put(buf[:0])

	s := op.Stats()
	if s.Gets != 1 || s.Puts != 1 {
		t.Fatalf("expected gets=1 puts=1, got %+v", s)
	}
}

func TestObjectPool_Reuse(t *testing.T) {
	var created atomic.Int32
	op := NewObjectPool(func() *int {
		created.Add(1)
		v := 0
		return &v
	})

	p := op.Get()
	op.Put(p)
	_ = op.Get()

	if created.Load() > 2 {
		t.Logf("created %d (pool may not reuse in test conditions)", created.Load())
	}
}

func TestObjectPool_Stats(t *testing.T) {
	op := NewObjectPool(func() int { return 0 })
	op.Get()
	op.Get()
	op.Put(0)

	s := op.Stats()
	if s.Gets != 2 || s.Puts != 1 {
		t.Fatalf("expected gets=2 puts=1, got %+v", s)
	}
}

func TestObjectPool_ResetStats(t *testing.T) {
	op := NewObjectPool(func() int { return 0 })
	op.Get()
	op.ResetStats()
	s := op.Stats()
	if s.Gets != 0 || s.Puts != 0 || s.Creates != 0 {
		t.Fatalf("expected zeroed, got %+v", s)
	}
}

// ============================================================
// Batch
// ============================================================

func TestBatch_AutoFlush(t *testing.T) {
	var mu sync.Mutex
	var flushed [][]int

	b := NewBatch(func(items []int) error {
		mu.Lock()
		flushed = append(flushed, items)
		mu.Unlock()
		return nil
	}, WithBatchSize(3), WithFlushInterval(time.Hour))
	defer b.Close()

	b.Add(1)
	b.Add(2)
	b.Add(3)

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	n := len(flushed)
	mu.Unlock()
	if n < 1 {
		t.Fatal("expected at least 1 flush")
	}
}

func TestBatch_ManualFlush(t *testing.T) {
	var got []string
	b := NewBatch(func(items []string) error {
		got = append(got, items...)
		return nil
	}, WithBatchSize(100), WithFlushInterval(time.Hour))
	defer b.Close()

	b.Add("a")
	b.Add("b")
	b.Flush()

	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("expected [a b], got %v", got)
	}
}

func TestBatch_FlushEmpty(t *testing.T) {
	b := NewBatch(func(items []int) error {
		t.Fatal("should not flush empty")
		return nil
	}, WithBatchSize(10), WithFlushInterval(time.Hour))
	defer b.Close()

	if err := b.Flush(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestBatch_FlushError(t *testing.T) {
	sentinel := errors.New("write failed")
	b := NewBatch(func(items []int) error {
		return sentinel
	}, WithBatchSize(10), WithFlushInterval(time.Hour))
	defer b.Close()

	b.Add(1)
	err := b.Flush()
	if err == nil {
		t.Fatal("expected error")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeFlushFailed {
		t.Fatalf("expected FLUSH_FAILED, got %v", err)
	}
}

func TestBatch_AddClosed(t *testing.T) {
	b := NewBatch(func(items []int) error { return nil },
		WithBatchSize(10), WithFlushInterval(time.Hour))
	b.Close()

	err := b.Add(1)
	if err == nil {
		t.Fatal("expected error on closed batch")
	}
}

func TestBatch_CloseFlushes(t *testing.T) {
	var got []int
	b := NewBatch(func(items []int) error {
		got = append(got, items...)
		return nil
	}, WithBatchSize(100), WithFlushInterval(time.Hour))

	b.Add(1)
	b.Add(2)
	b.Close()

	if len(got) != 2 {
		t.Fatalf("expected 2 items flushed on close, got %d", len(got))
	}
}

func TestBatch_Stats(t *testing.T) {
	b := NewBatch(func(items []int) error { return nil },
		WithBatchSize(10), WithFlushInterval(time.Hour))
	defer b.Close()

	b.Add(1)
	b.Add(2)
	b.Flush()

	s := b.Stats()
	if s.Flushed != 1 || s.Items != 2 {
		t.Fatalf("expected flushed=1 items=2, got %+v", s)
	}
}

func TestBatch_ResetStats(t *testing.T) {
	b := NewBatch(func(items []int) error { return nil },
		WithBatchSize(10), WithFlushInterval(time.Hour))
	defer b.Close()

	b.Add(1)
	b.Flush()
	b.ResetStats()

	s := b.Stats()
	if s.Flushed != 0 || s.Items != 0 || s.Errors != 0 {
		t.Fatalf("expected zeroed, got %+v", s)
	}
}

func TestBatch_PeriodicFlush(t *testing.T) {
	var mu sync.Mutex
	var count int

	b := NewBatch(func(items []int) error {
		mu.Lock()
		count += len(items)
		mu.Unlock()
		return nil
	}, WithBatchSize(1000), WithFlushInterval(50*time.Millisecond))
	defer b.Close()

	b.Add(1)
	time.Sleep(150 * time.Millisecond)

	mu.Lock()
	n := count
	mu.Unlock()
	if n != 1 {
		t.Fatalf("expected 1 item flushed by ticker, got %d", n)
	}
}

func TestBatch_CloseIdempotent(t *testing.T) {
	b := NewBatch(func(items []int) error { return nil },
		WithBatchSize(10), WithFlushInterval(time.Hour))
	b.Close()
	b.Close()
	if !b.IsClosed() {
		t.Fatal("expected closed")
	}
}

func TestBatch_PanicRecovery(t *testing.T) {
	b := NewBatch(func(items []int) error {
		panic("flush panic")
	}, WithBatchSize(10), WithFlushInterval(time.Hour))
	defer b.Close()

	b.Add(1)
	err := b.Flush()
	if err == nil {
		t.Fatal("expected error from panic")
	}
	s := b.Stats()
	if s.Errors != 1 {
		t.Fatalf("expected 1 error, got %d", s.Errors)
	}
}

func TestWorkerPool_TrySubmitClosed(t *testing.T) {
	wp := NewWorkerPool()
	wp.Close()

	err := wp.TrySubmit(context.Background(), func(ctx context.Context) error { return nil })
	if err == nil {
		t.Fatal("expected error on closed pool")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) || xe.Code != CodeClosed {
		t.Fatalf("expected CLOSED error, got %v", err)
	}
}

func TestWorkerPool_SubmitContextCancelled(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(1), WithQueueSize(1))
	defer wp.Close()

	blocker := make(chan struct{})
	wp.Submit(context.Background(), func(ctx context.Context) error {
		<-blocker
		return nil
	})
	wp.Submit(context.Background(), func(ctx context.Context) error {
		<-blocker
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := wp.Submit(ctx, func(ctx context.Context) error {
		return nil
	})
	close(blocker)

	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if xe.Code != CodeCancelled {
		t.Fatalf("expected CANCELLED, got %s", xe.Code)
	}
}
