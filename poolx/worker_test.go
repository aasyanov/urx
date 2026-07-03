package poolx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/aasyanov/urx/panix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWorkerPool_Defaults(t *testing.T) {
	wp := NewWorkerPool()
	defer closePool(t, wp)

	st := wp.Stats()
	assert.Equal(t, DefaultWorkers, st.Workers)
	assert.Equal(t, DefaultQueueSize, st.QueueSize)
}

func TestWorkerOptions(t *testing.T) {
	tests := []struct {
		name          string
		opts          []WorkerOption
		wantWorkers   int
		wantQueueSize int
	}{
		{name: "defaults", opts: nil, wantWorkers: DefaultWorkers, wantQueueSize: DefaultQueueSize},
		{name: "custom", opts: []WorkerOption{WithWorkers(8), WithQueueSize(256)}, wantWorkers: 8, wantQueueSize: 256},
		{name: "zero workers ignored", opts: []WorkerOption{WithWorkers(0)}, wantWorkers: DefaultWorkers, wantQueueSize: DefaultQueueSize},
		{name: "negative workers ignored", opts: []WorkerOption{WithWorkers(-3)}, wantWorkers: DefaultWorkers, wantQueueSize: DefaultQueueSize},
		{name: "zero queue ignored", opts: []WorkerOption{WithQueueSize(0)}, wantWorkers: DefaultWorkers, wantQueueSize: DefaultQueueSize},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wp := NewWorkerPool(tt.opts...)
			defer closePool(t, wp)
			st := wp.Stats()
			assert.Equal(t, tt.wantWorkers, st.Workers)
			assert.Equal(t, tt.wantQueueSize, st.QueueSize)
		})
	}
}

func TestWithWorkerOp_OverridesDefault(t *testing.T) {
	assert.Equal(t, opWorker, newWorkerConfig(nil).opOrDefault())
	assert.Equal(t, "api.ingest", newWorkerConfig([]WorkerOption{WithWorkerOp("api.ingest")}).opOrDefault())
	assert.Equal(t, opWorker, newWorkerConfig([]WorkerOption{WithWorkerOp("")}).opOrDefault())
}

func TestWorkerPool_SubmitRunsTask(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2))
	defer closePool(t, wp)

	var ran atomic.Bool
	require.NoError(t, wp.Submit(context.Background(), func(context.Context) error {
		ran.Store(true)
		return nil
	}))

	testx.Eventually(t, ran.Load, time.Second)
}

func TestWorkerPool_SubmitCountsCompleted(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(4))
	defer closePool(t, wp)

	const n = 100
	for range n {
		require.NoError(t, wp.Submit(context.Background(), func(context.Context) error { return nil }))
	}

	testx.Eventually(t, func() bool {
		return wp.Stats().Completed == n
	}, 2*time.Second)
	assert.Equal(t, uint64(n), wp.Stats().Submitted)
}

func TestWorkerPool_SubmitCountsFailed(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2))
	defer closePool(t, wp)

	sentinel := errors.New("task failed")
	require.NoError(t, wp.Submit(context.Background(), func(context.Context) error { return sentinel }))

	testx.Eventually(t, func() bool {
		return wp.Stats().Failed == 1
	}, time.Second)
	assert.Equal(t, uint64(0), wp.Stats().Panics)
}

func TestWorkerPool_SubmitCountsPanicSeparately(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2))
	defer closePool(t, wp)

	require.NoError(t, wp.Submit(context.Background(), func(context.Context) error { panic("boom") }))

	testx.Eventually(t, func() bool {
		st := wp.Stats()
		return st.Panics == 1 && st.Failed == 1
	}, time.Second)
}

func TestWorkerPool_SubmitReturnsErrNilFunc(t *testing.T) {
	wp := NewWorkerPool()
	defer closePool(t, wp)

	err := wp.Submit(context.Background(), nil)
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestWorkerPool_SubmitAfterCloseReturnsErrClosed(t *testing.T) {
	wp := NewWorkerPool()
	require.NoError(t, wp.Close())

	testx.AssertOpAfterClose(t, func() error {
		return wp.Submit(context.Background(), func(context.Context) error { return nil })
	}, ErrClosed, "Submit")
	assert.True(t, wp.IsClosed())
}

func TestWorkerPool_SubmitCancelledContextWhenQueueFull(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(1), WithQueueSize(1))
	defer closePool(t, wp)

	block := make(chan struct{})
	require.NoError(t, wp.Submit(context.Background(), func(context.Context) error {
		<-block
		return nil
	}))
	require.NoError(t, wp.Submit(context.Background(), func(context.Context) error { return nil }))

	err := wp.Submit(testx.CancelledCtx(), func(context.Context) error { return nil })
	require.ErrorIs(t, err, ErrCancelled)
	close(block)
}

func TestWorkerPool_SubmitCancelledContextWhenQueueHasSpace(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2), WithQueueSize(8))
	defer closePool(t, wp)

	err := wp.Submit(testx.CancelledCtx(), func(context.Context) error { return nil })
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
}

func TestWorkerPool_SubmitExpiredContext(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2), WithQueueSize(8))
	defer closePool(t, wp)

	err := wp.Submit(testx.ExpiredCtx(), func(context.Context) error { return nil })
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestWorkerPool_TrySubmitExpiredContext(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2), WithQueueSize(8))
	defer closePool(t, wp)

	called := false
	err := wp.TrySubmit(testx.ExpiredCtx(), func(context.Context) error {
		called = true
		return nil
	})
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, called)
}

func TestWorkerPool_TrySubmitQueueFull(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(1), WithQueueSize(1))
	defer closePool(t, wp)

	block := make(chan struct{})
	require.NoError(t, wp.Submit(context.Background(), func(context.Context) error {
		<-block
		return nil
	}))
	require.NoError(t, wp.Submit(context.Background(), func(context.Context) error { return nil }))

	err := wp.TrySubmit(context.Background(), func(context.Context) error { return nil })
	require.ErrorIs(t, err, ErrQueueFull)
	close(block)
}

func TestWorkerPool_TrySubmitCancelledContext(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(4), WithQueueSize(8))
	defer closePool(t, wp)

	called := false
	err := wp.TrySubmit(testx.CancelledCtx(), func(context.Context) error {
		called = true
		return nil
	})
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, called)
}

func TestWorkerPool_TrySubmitReturnsErrNilFunc(t *testing.T) {
	wp := NewWorkerPool()
	defer closePool(t, wp)

	err := wp.TrySubmit(context.Background(), nil)
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestWorkerPool_TrySubmitAfterCloseReturnsErrClosed(t *testing.T) {
	wp := NewWorkerPool()
	require.NoError(t, wp.Close())

	err := wp.TrySubmit(context.Background(), func(context.Context) error { return nil })
	require.ErrorIs(t, err, ErrClosed)
}

func TestWorkerPool_SubmitWaitReturnsResult(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2))
	defer closePool(t, wp)

	sentinel := errors.New("boom")
	err := wp.SubmitWait(context.Background(), func(context.Context) error { return sentinel })
	require.ErrorIs(t, err, sentinel)

	require.NoError(t, wp.SubmitWait(context.Background(), func(context.Context) error { return nil }))
}

func TestWorkerPool_SubmitWaitPanicReturnsPanicError(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2))
	defer closePool(t, wp)

	err := wp.SubmitWait(context.Background(), func(context.Context) error { panic("kaboom") })
	pe := testx.RequirePanicError(t, err, opWorker)
	assert.Equal(t, "kaboom", pe.Value)
}

func TestWorkerPool_SubmitWaitPanicUsesCustomOp(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2), WithWorkerOp("api.worker"))
	defer closePool(t, wp)

	err := wp.SubmitWait(context.Background(), func(context.Context) error { panic("kaboom") })
	testx.RequirePanicError(t, err, "api.worker")
}

func TestWorkerPool_SubmitWaitReturnsErrNilFunc(t *testing.T) {
	wp := NewWorkerPool()
	defer closePool(t, wp)

	err := wp.SubmitWait(context.Background(), nil)
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestWorkerPool_SubmitWaitAfterCloseReturnsErrClosed(t *testing.T) {
	wp := NewWorkerPool()
	require.NoError(t, wp.Close())

	err := wp.SubmitWait(context.Background(), func(context.Context) error { return nil })
	require.ErrorIs(t, err, ErrClosed)
}

func TestWorkerPool_SubmitWaitCancelBeforeEnqueueWhenQueueFull(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(1), WithQueueSize(1))
	defer closePool(t, wp)

	block := make(chan struct{})
	require.NoError(t, wp.Submit(context.Background(), func(context.Context) error {
		<-block
		return nil
	}))
	require.NoError(t, wp.Submit(context.Background(), func(context.Context) error { return nil }))

	err := wp.SubmitWait(testx.CancelledCtx(), func(context.Context) error { return nil })
	require.ErrorIs(t, err, ErrCancelled)
	close(block)
}

func TestWorkerPool_SubmitWaitCancelDuringExecution(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(1))
	defer closePool(t, wp)

	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})

	go func() {
		_ = wp.SubmitWait(ctx, func(context.Context) error {
			close(started)
			<-release
			return nil
		})
	}()

	<-started
	cancel()

	err := wp.SubmitWait(ctx, func(context.Context) error { return nil })
	require.ErrorIs(t, err, ErrCancelled)
	close(release)
}

func TestWorkerPool_CloseUnblocksPendingSubmit(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(1), WithQueueSize(1))

	block := make(chan struct{})
	require.NoError(t, wp.Submit(context.Background(), func(context.Context) error {
		<-block
		return nil
	}))
	require.NoError(t, wp.Submit(context.Background(), func(context.Context) error { return nil }))

	submitReturned := make(chan error, 1)
	go func() {
		submitReturned <- wp.Submit(context.Background(), func(context.Context) error { return nil })
	}()

	close(block)
	require.NoError(t, wp.Close())

	select {
	case err := <-submitReturned:
		if err != nil {
			require.ErrorIs(t, err, ErrClosed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Submit did not unblock after Close")
	}
}

func TestWorkerPool_CloseWaitsForQueuedTasks(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2), WithQueueSize(50))

	var done atomic.Int64
	const n = 20
	for range n {
		require.NoError(t, wp.Submit(context.Background(), func(context.Context) error {
			time.Sleep(5 * time.Millisecond)
			done.Add(1)
			return nil
		}))
	}

	require.NoError(t, wp.Close())
	assert.Equal(t, int64(n), done.Load(), "Close must drain all queued tasks")
}

func TestWorkerPool_CloseIdempotent(t *testing.T) {
	wp := NewWorkerPool()
	testx.AssertCloseIdempotent(t, wp)
}

func TestWorkerPool_ResetStats(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2))
	defer closePool(t, wp)

	require.NoError(t, wp.SubmitWait(context.Background(), func(context.Context) error { return nil }))
	require.Greater(t, wp.Stats().Completed, uint64(0))

	wp.ResetStats()
	st := wp.Stats()
	assert.Equal(t, uint64(0), st.Submitted)
	assert.Equal(t, uint64(0), st.Completed)
	assert.Equal(t, uint64(0), st.Failed)
	assert.Equal(t, uint64(0), st.Panics)
}

func TestWorkerPool_ConcurrentSubmit(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(8), WithQueueSize(256))
	defer closePool(t, wp)

	var executed atomic.Int64
	testx.HammerNoError(t, 50, 20, func() error {
		return wp.SubmitWait(context.Background(), func(context.Context) error {
			executed.Add(1)
			return nil
		})
	})
	assert.Equal(t, int64(50*20), executed.Load())
}

func TestWorkerPool_CloseDrainsTasksEnqueuedDuringShutdown(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(1), WithQueueSize(8))

	gate := make(chan struct{})
	require.NoError(t, wp.Submit(context.Background(), func(context.Context) error {
		<-gate
		return nil
	}))

	var executed atomic.Int64
	const queued = 5
	for range queued {
		require.NoError(t, wp.Submit(context.Background(), func(context.Context) error {
			executed.Add(1)
			return nil
		}))
	}

	close(gate)
	require.NoError(t, wp.Close())

	assert.Equal(t, int64(queued), executed.Load())
	st := wp.Stats()
	assert.Equal(t, st.Submitted, st.Completed+st.Failed, "every submitted task must finish before Close returns")
}

func TestWorkerPool_SubmitWaitCompletesWhenCloseDrainsQueue(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(1), WithQueueSize(4))

	var ran atomic.Int64
	const queued = 3
	for range queued {
		require.NoError(t, wp.Submit(context.Background(), func(context.Context) error {
			ran.Add(1)
			return nil
		}))
	}

	var wg sync.WaitGroup
	wg.Add(1)
	var waitErr error
	go func() {
		defer wg.Done()
		waitErr = wp.SubmitWait(context.Background(), func(context.Context) error {
			ran.Add(1)
			return nil
		})
	}()

	require.Eventually(t, func() bool {
		return wp.Stats().Submitted >= uint64(queued+1)
	}, time.Second, time.Millisecond)

	require.NoError(t, wp.Close())
	wg.Wait()

	require.NoError(t, waitErr)
	assert.Equal(t, int64(queued+1), ran.Load())
}

func TestWorkerPool_SubmitWaitReturnsResultDuringShutdown(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(1), WithQueueSize(4))

	block := make(chan struct{})
	require.NoError(t, wp.Submit(context.Background(), func(context.Context) error {
		<-block
		return nil
	}))

	var wg sync.WaitGroup
	wg.Add(1)
	var waitErr error
	go func() {
		defer wg.Done()
		waitErr = wp.SubmitWait(context.Background(), func(context.Context) error {
			return nil
		})
	}()

	require.Eventually(t, func() bool {
		return wp.Stats().Submitted >= 2
	}, time.Second, time.Millisecond)

	close(block)
	require.NoError(t, wp.Close())
	wg.Wait()

	require.NoError(t, waitErr)
}

func TestWorkerPool_drainOrphanedTasksRunsQueuedWork(t *testing.T) {
	wp := &WorkerPool{tasks: make(chan func(), 1)}

	var ran atomic.Bool
	wp.tasks <- func() { ran.Store(true) }
	wp.drainOrphanedTasks()
	assert.True(t, ran.Load())
}

func TestWorkerPool_rejectIfClosedReturnsErrClosed(t *testing.T) {
	wp := NewWorkerPool()
	require.NoError(t, wp.Close())
	require.ErrorIs(t, wp.rejectIfClosed(), ErrClosed)
}

func TestIsPanic(t *testing.T) {
	assert.False(t, isPanic(nil))
	assert.False(t, isPanic(errors.New("regular")))
	assert.True(t, isPanic(&panix.PanicError{Op: "x", Value: "v"}))
}
