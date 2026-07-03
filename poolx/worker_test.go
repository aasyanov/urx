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
	defer wp.Close()

	st := wp.Stats()
	assert.Equal(t, defaultWorkers, st.Workers)
	assert.Equal(t, defaultQueueSize, st.QueueSize)
}

func TestWorkerOptions(t *testing.T) {
	tests := []struct {
		name          string
		opts          []WorkerOption
		wantWorkers   int
		wantQueueSize int
	}{
		{name: "defaults", opts: nil, wantWorkers: defaultWorkers, wantQueueSize: defaultQueueSize},
		{name: "custom", opts: []WorkerOption{WithWorkers(8), WithQueueSize(256)}, wantWorkers: 8, wantQueueSize: 256},
		{name: "zero workers ignored", opts: []WorkerOption{WithWorkers(0)}, wantWorkers: defaultWorkers, wantQueueSize: defaultQueueSize},
		{name: "negative workers ignored", opts: []WorkerOption{WithWorkers(-3)}, wantWorkers: defaultWorkers, wantQueueSize: defaultQueueSize},
		{name: "zero queue ignored", opts: []WorkerOption{WithQueueSize(0)}, wantWorkers: defaultWorkers, wantQueueSize: defaultQueueSize},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wp := NewWorkerPool(tt.opts...)
			defer wp.Close()
			st := wp.Stats()
			assert.Equal(t, tt.wantWorkers, st.Workers)
			assert.Equal(t, tt.wantQueueSize, st.QueueSize)
		})
	}
}

func TestWorkerPool_SubmitRunsTask(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2))
	defer wp.Close()

	var ran atomic.Bool
	require.NoError(t, wp.Submit(context.Background(), func(context.Context) error {
		ran.Store(true)
		return nil
	}))

	testx.Eventually(t, ran.Load, time.Second)
}

func TestWorkerPool_SubmitCountsCompleted(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(4))
	defer wp.Close()

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
	defer wp.Close()

	sentinel := errors.New("task failed")
	require.NoError(t, wp.Submit(context.Background(), func(context.Context) error { return sentinel }))

	testx.Eventually(t, func() bool {
		return wp.Stats().Failed == 1
	}, time.Second)
	assert.Equal(t, uint64(0), wp.Stats().Panics)
}

func TestWorkerPool_SubmitCountsPanicSeparately(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2))
	defer wp.Close()

	require.NoError(t, wp.Submit(context.Background(), func(context.Context) error { panic("boom") }))

	testx.Eventually(t, func() bool {
		st := wp.Stats()
		return st.Panics == 1 && st.Failed == 1
	}, time.Second)
}

func TestWorkerPool_SubmitAfterCloseReturnsErrClosed(t *testing.T) {
	wp := NewWorkerPool()
	wp.Close()

	testx.AssertOpAfterClose(t, func() error {
		return wp.Submit(context.Background(), func(context.Context) error { return nil })
	}, ErrClosed, "Submit")
	assert.True(t, wp.IsClosed())
}

func TestWorkerPool_SubmitCancelledContext(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(1), WithQueueSize(1))
	defer wp.Close()

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

func TestWorkerPool_TrySubmitQueueFull(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(1), WithQueueSize(1))
	defer wp.Close()

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

func TestWorkerPool_TrySubmitAfterCloseReturnsErrClosed(t *testing.T) {
	wp := NewWorkerPool()
	wp.Close()

	err := wp.TrySubmit(context.Background(), func(context.Context) error { return nil })
	require.ErrorIs(t, err, ErrClosed)
}

func TestWorkerPool_SubmitWaitReturnsResult(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2))
	defer wp.Close()

	sentinel := errors.New("boom")
	err := wp.SubmitWait(context.Background(), func(context.Context) error { return sentinel })
	require.ErrorIs(t, err, sentinel)

	require.NoError(t, wp.SubmitWait(context.Background(), func(context.Context) error { return nil }))
}

func TestWorkerPool_SubmitWaitPanicReturnsPanicError(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2))
	defer wp.Close()

	err := wp.SubmitWait(context.Background(), func(context.Context) error { panic("kaboom") })
	pe := testx.RequirePanicError(t, err, opWorker)
	assert.Equal(t, "kaboom", pe.Value)
}

func TestWorkerPool_SubmitWaitAfterCloseReturnsErrClosed(t *testing.T) {
	wp := NewWorkerPool()
	wp.Close()

	err := wp.SubmitWait(context.Background(), func(context.Context) error { return nil })
	require.ErrorIs(t, err, ErrClosed)
}

func TestWorkerPool_SubmitWaitCancelDuringExecution(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(1))
	defer wp.Close()

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
	wp.Close()

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

	wp.Close()
	assert.Equal(t, int64(n), done.Load(), "Close must drain all queued tasks")
}

func TestWorkerPool_CloseIdempotent(t *testing.T) {
	wp := NewWorkerPool()
	assert.NotPanics(t, func() {
		wp.Close()
		wp.Close()
	})
}

func TestWorkerPool_ResetStats(t *testing.T) {
	wp := NewWorkerPool(WithWorkers(2))
	defer wp.Close()

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
	defer wp.Close()

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
	wp.Close()

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

	wp.Close()
	wg.Wait()

	require.NoError(t, waitErr)
	assert.Equal(t, int64(queued+1), ran.Load())
}

func TestIsPanic(t *testing.T) {
	assert.False(t, isPanic(nil))
	assert.False(t, isPanic(errors.New("regular")))
	assert.True(t, isPanic(&panix.PanicError{Op: "x", Value: "v"}))
}
