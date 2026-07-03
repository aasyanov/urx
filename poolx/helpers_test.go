package poolx

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func closePool(t *testing.T, wp *WorkerPool) {
	t.Helper()
	require.NoError(t, wp.Close())
}

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

func newTestBatch[T any](t *testing.T, flush func(context.Context, []T) error, opts ...BatchOption) *Batch[T] {
	t.Helper()
	b, err := NewBatch(flush, opts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = b.Close() })
	return b
}
