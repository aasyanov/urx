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

func collectingFlush[T any](dst *[][]T, mu *sync.Mutex) func(context.Context, []T) error {
	return func(_ context.Context, items []T) error {
		mu.Lock()
		cp := make([]T, len(items))
		copy(cp, items)
		*dst = append(*dst, cp)
		mu.Unlock()
		return nil
	}
}

func TestNewBatch_NilFlushPanics(t *testing.T) {
	assert.Panics(t, func() {
		NewBatch[int](nil)
	})
}

func TestBatchOptions(t *testing.T) {
	tests := []struct {
		name      string
		opts      []BatchOption
		wantSize  int
		wantFlush time.Duration
	}{
		{name: "defaults", opts: nil, wantSize: defaultBatchSize, wantFlush: defaultFlushInterval},
		{name: "custom", opts: []BatchOption{WithBatchSize(10), WithFlushInterval(2 * time.Second)}, wantSize: 10, wantFlush: 2 * time.Second},
		{name: "zero size ignored", opts: []BatchOption{WithBatchSize(0)}, wantSize: defaultBatchSize, wantFlush: defaultFlushInterval},
		{name: "negative interval ignored", opts: []BatchOption{WithFlushInterval(-time.Second)}, wantSize: defaultBatchSize, wantFlush: defaultFlushInterval},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBatch(func(context.Context, []int) error { return nil }, tt.opts...)
			defer b.Close()
			st := b.Stats()
			assert.Equal(t, tt.wantSize, st.BatchSize)
			assert.Equal(t, tt.wantFlush.String(), st.FlushInterval)
		})
	}
}

func TestBatch_FlushOnSizeThreshold(t *testing.T) {
	var got [][]int
	var mu sync.Mutex
	b := NewBatch(collectingFlush(&got, &mu), WithBatchSize(3), WithFlushInterval(time.Hour))
	defer b.Close()

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
	b := NewBatch(collectingFlush(&got, &mu), WithBatchSize(100), WithFlushInterval(time.Hour))
	defer b.Close()

	require.NoError(t, b.Add(1))
	require.NoError(t, b.Add(2))
	require.NoError(t, b.Flush(context.Background()))

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, [][]int{{1, 2}}, got)
}

func TestBatch_FlushEmptyIsNoop(t *testing.T) {
	var flushes atomic.Int64
	b := NewBatch(func(context.Context, []int) error {
		flushes.Add(1)
		return nil
	}, WithFlushInterval(time.Hour))
	defer b.Close()

	require.NoError(t, b.Flush(context.Background()))
	assert.Equal(t, int64(0), flushes.Load())
}

func TestBatch_PeriodicFlush(t *testing.T) {
	var got [][]int
	var mu sync.Mutex
	b := NewBatch(collectingFlush(&got, &mu), WithBatchSize(1000), WithFlushInterval(20*time.Millisecond))
	defer b.Close()

	require.NoError(t, b.Add(42))

	testx.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == 1
	}, 2*time.Second)
}

func TestBatch_FlushErrorJoinsErrFlushFailed(t *testing.T) {
	sentinel := errors.New("db down")
	b := NewBatch(func(context.Context, []int) error { return sentinel }, WithBatchSize(1), WithFlushInterval(time.Hour))
	defer b.Close()

	err := b.Add(1)
	require.ErrorIs(t, err, ErrFlushFailed)
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, uint64(1), b.Stats().Errors)
}

func TestBatch_FlushPanicRecovered(t *testing.T) {
	b := NewBatch(func(context.Context, []int) error { panic("flush boom") }, WithBatchSize(1), WithFlushInterval(time.Hour))
	defer b.Close()

	err := b.Add(1)
	require.ErrorIs(t, err, ErrFlushFailed)
	assert.Equal(t, uint64(1), b.Stats().Errors)
}

func TestBatch_WithErrorHandlerReceivesTickerErrors(t *testing.T) {
	sentinel := errors.New("periodic fail")
	var captured atomic.Pointer[error]
	b := NewBatch(
		func(context.Context, []int) error { return sentinel },
		WithBatchSize(1000),
		WithFlushInterval(20*time.Millisecond),
		WithErrorHandler(func(err error) { captured.Store(&err) }),
	)
	defer b.Close()

	require.NoError(t, b.Add(1))

	testx.Eventually(t, func() bool {
		return captured.Load() != nil
	}, 2*time.Second)
	require.ErrorIs(t, *captured.Load(), sentinel)
}

func TestBatch_FlushReceivesContext(t *testing.T) {
	var sawDeadline atomic.Bool
	b := NewBatch(func(ctx context.Context, _ []int) error {
		_, ok := ctx.Deadline()
		sawDeadline.Store(ok)
		return nil
	}, WithBatchSize(1000), WithFlushInterval(time.Hour))
	defer b.Close()

	require.NoError(t, b.Add(1))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, b.Flush(ctx))
	assert.True(t, sawDeadline.Load())
}

func TestBatch_AddAfterCloseReturnsErrClosed(t *testing.T) {
	b := NewBatch(func(context.Context, []int) error { return nil })
	require.NoError(t, b.Close())

	testx.AssertOpAfterClose(t, func() error { return b.Add(1) }, ErrClosed, "Add")
	assert.True(t, b.IsClosed())
}

func TestBatch_CloseFlushesRemaining(t *testing.T) {
	var got [][]int
	var mu sync.Mutex
	b := NewBatch(collectingFlush(&got, &mu), WithBatchSize(1000), WithFlushInterval(time.Hour))

	require.NoError(t, b.Add(1))
	require.NoError(t, b.Add(2))
	require.NoError(t, b.Close())

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, [][]int{{1, 2}}, got)
}

func TestBatch_CloseIdempotent(t *testing.T) {
	b := NewBatch(func(context.Context, []int) error { return nil })
	testx.AssertCloseIdempotent(t, b)
}

func TestBatch_ResetStats(t *testing.T) {
	b := NewBatch(func(context.Context, []int) error { return nil }, WithBatchSize(1), WithFlushInterval(time.Hour))
	defer b.Close()

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
	b := NewBatch(func(_ context.Context, items []int) error {
		total.Add(int64(len(items)))
		return nil
	}, WithBatchSize(10), WithFlushInterval(time.Hour))

	testx.HammerVoid(20, 100, func() {
		_ = b.Add(1)
	})
	require.NoError(t, b.Close())

	assert.Equal(t, int64(20*100), total.Load())
}
