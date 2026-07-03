package poolx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBatch_NilFlushReturnsErrNilFlush(t *testing.T) {
	_, err := NewBatch[int](nil)
	require.ErrorIs(t, err, ErrNilFlush)
}

func TestBatchOptions(t *testing.T) {
	tests := []struct {
		name      string
		opts      []BatchOption
		wantSize  int
		wantFlush time.Duration
	}{
		{name: "defaults", opts: nil, wantSize: DefaultBatchSize, wantFlush: DefaultFlushInterval},
		{name: "custom", opts: []BatchOption{WithBatchSize(10), WithFlushInterval(2 * time.Second)}, wantSize: 10, wantFlush: 2 * time.Second},
		{name: "zero size ignored", opts: []BatchOption{WithBatchSize(0)}, wantSize: DefaultBatchSize, wantFlush: DefaultFlushInterval},
		{name: "negative interval ignored", opts: []BatchOption{WithFlushInterval(-time.Second)}, wantSize: DefaultBatchSize, wantFlush: DefaultFlushInterval},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newTestBatch(t, func(context.Context, []int) error { return nil }, tt.opts...)
			st := b.Stats()
			assert.Equal(t, tt.wantSize, st.BatchSize)
			assert.Equal(t, tt.wantFlush.String(), st.FlushInterval)
		})
	}
}

func TestWithBatchOp_OverridesDefault(t *testing.T) {
	assert.Equal(t, opBatchFlush, newBatchConfig(nil).opOrDefault())
	assert.Equal(t, "db.insert", newBatchConfig([]BatchOption{WithBatchOp("db.insert")}).opOrDefault())
	assert.Equal(t, opBatchFlush, newBatchConfig([]BatchOption{WithBatchOp("")}).opOrDefault())
}

func TestBatch_FlushOnSizeThreshold(t *testing.T) {
	var got [][]int
	var mu sync.Mutex
	b := newTestBatch(t, collectingFlush(&got, &mu), WithBatchSize(3), WithFlushInterval(time.Hour))

	for i := range 3 {
		require.NoError(t, b.Add(i))
	}

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, got, 1)
	assert.Equal(t, []int{0, 1, 2}, got[0])
}

func TestBatch_ManualFlush(t *testing.T) {
	var got [][]int
	var mu sync.Mutex
	b := newTestBatch(t, collectingFlush(&got, &mu), WithBatchSize(100), WithFlushInterval(time.Hour))

	require.NoError(t, b.Add(1))
	require.NoError(t, b.Add(2))
	require.NoError(t, b.Flush(context.Background()))

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, [][]int{{1, 2}}, got)
}

func TestBatch_FlushEmptyIsNoop(t *testing.T) {
	var flushes atomic.Int64
	b := newTestBatch(t, func(context.Context, []int) error {
		flushes.Add(1)
		return nil
	}, WithFlushInterval(time.Hour))

	require.NoError(t, b.Flush(context.Background()))
	assert.Equal(t, int64(0), flushes.Load())
}

func TestBatch_PeriodicFlush(t *testing.T) {
	var got [][]int
	var mu sync.Mutex
	b := newTestBatch(t, collectingFlush(&got, &mu), WithBatchSize(1000), WithFlushInterval(20*time.Millisecond))

	require.NoError(t, b.Add(42))

	testx.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 1
	}, 2*time.Second)
}

func TestBatch_FlushErrorJoinsErrFlushFailed(t *testing.T) {
	sentinel := errors.New("db down")
	b, err := NewBatch(func(context.Context, []int) error { return sentinel }, WithBatchSize(1), WithFlushInterval(time.Hour))
	require.NoError(t, err)
	defer func() { _ = b.Close() }()

	err = b.Add(1)
	require.ErrorIs(t, err, ErrFlushFailed)
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, uint64(1), b.Stats().Errors)
	assert.Equal(t, 1, b.Stats().Buffered, "failed flush must restore items")
}

func TestBatch_FlushErrorRequeuesForRetry(t *testing.T) {
	var attempts atomic.Int64
	b, err := NewBatch(func(context.Context, []int) error {
		if attempts.Add(1) == 1 {
			return errors.New("temporary")
		}
		return nil
	}, WithBatchSize(1), WithFlushInterval(time.Hour))
	require.NoError(t, err)
	defer func() { _ = b.Close() }()

	require.Error(t, b.Add(42))
	assert.Equal(t, int64(1), attempts.Load())
	assert.Equal(t, uint64(0), b.Stats().Flushed)

	require.NoError(t, b.Flush(context.Background()))
	assert.Equal(t, int64(2), attempts.Load())
	assert.Equal(t, uint64(1), b.Stats().Flushed)
	assert.Equal(t, uint64(1), b.Stats().Items)
}

func TestBatch_FlushPanicRecovered(t *testing.T) {
	b, err := NewBatch(func(context.Context, []int) error { panic("flush boom") }, WithBatchSize(1), WithFlushInterval(time.Hour))
	require.NoError(t, err)
	defer func() { _ = b.Close() }()

	err = b.Add(1)
	require.ErrorIs(t, err, ErrFlushFailed)
	assert.Equal(t, uint64(1), b.Stats().Errors)
	assert.Equal(t, 1, b.Stats().Buffered)
}

func TestBatch_FlushPanicUsesCustomOp(t *testing.T) {
	b, err := NewBatch(
		func(context.Context, []int) error { panic("flush boom") },
		WithBatchSize(1),
		WithFlushInterval(time.Hour),
		WithBatchOp("db.batch_insert"),
	)
	require.NoError(t, err)
	defer func() { _ = b.Close() }()

	err = b.Add(1)
	require.ErrorIs(t, err, ErrFlushFailed)
	pe := testx.RequirePanicError(t, err, "db.batch_insert")
	assert.Equal(t, "flush boom", pe.Value)
}

func TestBatch_FlushAfterCloseReturnsErrClosed(t *testing.T) {
	b, err := NewBatch(func(context.Context, []int) error { return nil })
	require.NoError(t, err)
	require.NoError(t, b.Close())

	err = b.Flush(context.Background())
	require.ErrorIs(t, err, ErrClosed)
}

func TestBatch_AddConcurrentWithCloseNoLostItems(t *testing.T) {
	var (
		mu        sync.Mutex
		flushed   []int
		accepted  atomic.Int64
	)
	b, err := NewBatch(func(_ context.Context, items []int) error {
		mu.Lock()
		flushed = append(flushed, items...)
		mu.Unlock()
		return nil
	}, WithBatchSize(1000), WithFlushInterval(time.Hour))
	require.NoError(t, err)

	const n = 200
	var wg sync.WaitGroup
	wg.Add(n + 1)
	for range n {
		go func() {
			defer wg.Done()
			if err := b.Add(1); err == nil {
				accepted.Add(1)
			}
		}()
	}
	go func() {
		defer wg.Done()
		_ = b.Close()
	}()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, int(accepted.Load()), len(flushed))
	assert.Equal(t, 0, b.Stats().Buffered)
}

func TestBatch_WithErrorHandlerReceivesTickerErrors(t *testing.T) {
	sentinel := errors.New("periodic fail")
	var captured atomic.Pointer[error]
	b := newTestBatch(t,
		func(context.Context, []int) error { return sentinel },
		WithBatchSize(1000),
		WithFlushInterval(20*time.Millisecond),
		WithErrorHandler(func(err error) { captured.Store(&err) }),
	)

	require.NoError(t, b.Add(1))

	testx.Eventually(t, func() bool {
		return captured.Load() != nil
	}, 2*time.Second)
	require.ErrorIs(t, *captured.Load(), sentinel)
}

func TestBatch_FlushReceivesContext(t *testing.T) {
	var sawDeadline atomic.Bool
	b := newTestBatch(t, func(ctx context.Context, _ []int) error {
		_, ok := ctx.Deadline()
		sawDeadline.Store(ok)
		return nil
	}, WithBatchSize(1000), WithFlushInterval(time.Hour))

	require.NoError(t, b.Add(1))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, b.Flush(ctx))
	assert.True(t, sawDeadline.Load())
}

func TestBatch_LifecycleContextCancelledOnClose(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	b, err := NewBatch(func(ctx context.Context, _ []int) error {
		close(started)
		<-ctx.Done()
		<-release
		return ctx.Err()
	}, WithBatchSize(1000), WithFlushInterval(time.Hour))
	require.NoError(t, err)

	require.NoError(t, b.Add(1))
	go func() {
		_ = b.Flush(b.lifecycleCtx())
	}()
	<-started

	go func() { _ = b.Close() }()

	select {
	case <-b.lifecycleCtx().Done():
	case <-time.After(2 * time.Second):
		t.Fatal("lifecycle context not cancelled on Close")
	}
	close(release)
}

func TestBatch_AddAfterCloseReturnsErrClosed(t *testing.T) {
	b, err := NewBatch(func(context.Context, []int) error { return nil })
	require.NoError(t, err)
	require.NoError(t, b.Close())

	testx.AssertOpAfterClose(t, func() error { return b.Add(1) }, ErrClosed, "Add")
	assert.True(t, b.IsClosed())
}

func TestBatch_CloseFlushesRemaining(t *testing.T) {
	var got [][]int
	var mu sync.Mutex
	b, err := NewBatch(collectingFlush(&got, &mu), WithBatchSize(1000), WithFlushInterval(time.Hour))
	require.NoError(t, err)

	require.NoError(t, b.Add(1))
	require.NoError(t, b.Add(2))
	require.NoError(t, b.Close())

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, [][]int{{1, 2}}, got)
}

func TestBatch_CloseReturnsFlushError(t *testing.T) {
	sentinel := errors.New("close flush failed")
	b, err := NewBatch(func(context.Context, []int) error { return sentinel })
	require.NoError(t, err)

	require.NoError(t, b.Add(1))
	err = b.Close()
	require.ErrorIs(t, err, ErrFlushFailed)
	require.ErrorIs(t, err, sentinel)
}

func TestBatch_CloseIdempotent(t *testing.T) {
	b, err := NewBatch(func(context.Context, []int) error { return nil })
	require.NoError(t, err)
	testx.AssertCloseIdempotent(t, b)
}

func TestBatch_ResetStats(t *testing.T) {
	b := newTestBatch(t, func(context.Context, []int) error { return nil }, WithBatchSize(1), WithFlushInterval(time.Hour))

	require.NoError(t, b.Add(1))
	require.Positive(t, b.Stats().Flushed)

	b.ResetStats()
	st := b.Stats()
	assert.Equal(t, uint64(0), st.Flushed)
	assert.Equal(t, uint64(0), st.Items)
	assert.Equal(t, uint64(0), st.Errors)
}

func TestBatch_ConcurrentAdd(t *testing.T) {
	var total atomic.Int64
	b, err := NewBatch(func(_ context.Context, items []int) error {
		total.Add(int64(len(items)))
		return nil
	}, WithBatchSize(10), WithFlushInterval(time.Hour))
	require.NoError(t, err)

	testx.HammerVoid(20, 100, func() {
		_ = b.Add(1)
	})
	require.NoError(t, b.Close())

	assert.Equal(t, int64(20*100), total.Load())
}
