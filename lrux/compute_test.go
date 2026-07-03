package lrux

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetOrCompute_StatsExactlyOnePerCall verifies that a logical miss
// followed by a populate yields exactly one miss and that a subsequent hit
// yields exactly one hit, with no double-counting through the compute path.
func TestGetOrCompute_StatsExactlyOnePerCall(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	_, err := c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
		return 1, nil
	})
	require.NoError(t, err)
	s := c.Stats()
	assert.Equal(t, uint64(0), s.Hits)
	assert.Equal(t, uint64(1), s.Misses)

	_, err = c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
		return 2, nil
	})
	require.NoError(t, err)
	s = c.Stats()
	assert.Equal(t, uint64(1), s.Hits)
	assert.Equal(t, uint64(1), s.Misses)
}

// TestGetOrCompute_Singleflight_StatsNoDoubleCount verifies that a single
// deduplicated compute records exactly one miss for the leader, not one per
// waiter, so the leader's compute closure never double-counts.
func TestGetOrCompute_Singleflight_StatsNoDoubleCount(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	const waiters = 16
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(waiters)
	for range waiters {
		go func() {
			defer wg.Done()
			_, _ = c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
				<-release
				return 1, nil
			}, WithSingleflight())
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	s := c.Stats()
	assert.Equal(t, uint64(1), s.Misses)
}

// TestGetOrCompute_ConcurrentPopulateConvertsMissToHit drives the
// convertMissToHit branch: while compute runs, another goroutine populates
// the key, so the double-check converts the recorded miss into a hit.
func TestGetOrCompute_ConcurrentPopulateConvertsMissToHit(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	var got int
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		got, err = c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
			close(started)
			<-release
			return 1, nil
		})
		require.NoError(t, err)
	}()

	<-started
	c.Set("k", 42)
	close(release)
	wg.Wait()

	assert.Equal(t, 42, got)
	s := c.Stats()
	assert.Equal(t, uint64(1), s.Hits+s.Misses)
	assert.Equal(t, uint64(1), s.Hits)
}

func TestPeekPromote(t *testing.T) {
	t.Run("hit promotes without stats", func(t *testing.T) {
		c := New[string, int](WithCapacity[string, int](3))
		defer c.Close()
		c.Set("a", 1)
		c.Set("b", 2)
		c.Set("c", 3)

		v, ok := c.peekPromote("a")
		require.True(t, ok)
		assert.Equal(t, 1, v)

		s := c.Stats()
		assert.Zero(t, s.Hits)
		assert.Zero(t, s.Misses)

		c.Set("d", 4)
		_, ok = c.Get("a")
		assert.True(t, ok)
	})

	t.Run("missing key", func(t *testing.T) {
		c := New[string, int]()
		defer c.Close()
		_, ok := c.peekPromote("nope")
		assert.False(t, ok)
	})

	t.Run("expired key", func(t *testing.T) {
		c := New[string, int]()
		defer c.Close()
		c.SetWithTTL("a", 1, time.Millisecond)
		time.Sleep(10 * time.Millisecond)
		_, ok := c.peekPromote("a")
		assert.False(t, ok)
		assert.Equal(t, 0, c.Len())
	})
}

func TestConvertMissToHit(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.misses.Store(1)

	c.convertMissToHit()

	s := c.Stats()
	assert.Equal(t, uint64(1), s.Hits)
	assert.Equal(t, uint64(0), s.Misses)
}

func TestGetOrCompute_RecheckAfterCompute(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	var got int
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var err error
		got, err = c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
			close(started)
			<-release
			return 1, nil
		})
		require.NoError(t, err)
	}()

	<-started
	c.Set("k", 99)
	close(release)
	wg.Wait()

	assert.Equal(t, 99, got)
}

func TestGetOrCompute_ExpiredEntryRemovedBeforeCompute(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	c.SetWithTTL("k", 1, time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	v, err := c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
		return 2, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 2, v)
}

func TestGetOrCompute_ClosedReturnsErrClosed(t *testing.T) {
	c := New[string, int]()
	c.Close()

	_, err := c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
		return 1, nil
	})
	require.ErrorIs(t, err, ErrClosed)
}

func TestGetOrCompute_Singleflight_CallerCancelDoesNotAbortShared(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	enter := make(chan struct{})
	finish := make(chan struct{})
	var computeCalls atomic.Int64

	cancelCtx, cancel := context.WithCancel(context.Background())
	var cancelErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, cancelErr = c.GetOrCompute(cancelCtx, "k", func(ctx context.Context) (int, error) {
			computeCalls.Add(1)
			close(enter)
			<-finish
			return 5, nil
		}, WithSingleflight())
	}()

	<-enter
	cancel()
	close(finish)
	wg.Wait()

	require.ErrorIs(t, cancelErr, context.Canceled)
	assert.Equal(t, int64(1), computeCalls.Load())

	require.Eventually(t, func() bool {
		v, ok := c.Get("k")
		return ok && v == 5
	}, time.Second, 5*time.Millisecond)
}

func TestGetOrCompute_Singleflight_ComputeError(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	_, err := c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
		return 0, ErrNotFound
	}, WithSingleflight())
	require.ErrorIs(t, err, ErrNotFound)
	assert.False(t, c.Has("k"))
}

func TestGetOrCompute_ExpiredRecheckAfterCompute(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	v, err := c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
		c.SetWithTTL("k", 1, time.Nanosecond)
		time.Sleep(time.Millisecond)
		return 2, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 2, v)
}

func TestGetOrCompute_CancelledAfterCompute(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	_, err := c.GetOrCompute(ctx, "k", func(context.Context) (int, error) {
		cancel()
		return 1, nil
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, c.Has("k"))
}

func TestGetOrCompute_ExpiredContext(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	_, err := c.GetOrCompute(testx.ExpiredCtx(), "k", func(context.Context) (int, error) {
		return 1, nil
	})
	require.Error(t, err)
	assert.False(t, c.Has("k"))
}

func TestGetOrCompute_ErrNotFound(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	_, err := c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
		return 0, ErrNotFound
	})
	require.ErrorIs(t, err, ErrNotFound)
	assert.False(t, c.Has("k"))
}

func TestComputeDirect_ExpiredRemovedBeforeCompute(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	c.SetWithTTL("k", 1, time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	c.misses.Add(1)
	got, err := c.computeDirect(context.Background(), "k", func(context.Context) (int, error) {
		return 2, nil
	}, 0)
	require.NoError(t, err)
	assert.Equal(t, 2, got)
	assert.True(t, c.Has("k"))
}

func TestComputeSingle_PeekPromoteHit(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	c.Set("k", 42)
	c.misses.Store(0)

	v, err := c.computeSingle(context.Background(), "k", func(context.Context) (int, error) {
		t.Fatal("compute must not run")
		return 0, nil
	}, 0)
	require.NoError(t, err)
	assert.Equal(t, 42, v)
	assert.Equal(t, uint64(0), c.Stats().Misses)
}

func TestComputeDirect_FirstCheckHit(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.Set("k", 42)
	c.misses.Add(1)

	got, err := c.computeDirect(context.Background(), "k", func(context.Context) (int, error) {
		t.Fatal("compute must not run")
		return 0, nil
	}, 0)
	require.NoError(t, err)
	assert.Equal(t, 42, got)
	s := c.Stats()
	assert.Equal(t, uint64(1), s.Hits)
	assert.Equal(t, uint64(0), s.Misses)
}

func TestComputeDirect_PostComputeCancel(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	_, err := c.computeDirect(ctx, "k", func(context.Context) (int, error) {
		cancel()
		return 1, nil
	}, 0)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, c.Has("k"))
}

func TestGetOrCompute_CloseDuringInsertReturnsErrClosed(t *testing.T) {
	c := New[string, int]()
	start := make(chan struct{})
	release := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	var got int
	var gotErr error
	go func() {
		defer wg.Done()
		got, gotErr = c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
			close(start)
			<-release
			return 42, nil
		})
	}()

	<-start
	c.Close()
	close(release)
	wg.Wait()

	require.ErrorIs(t, gotErr, ErrClosed)
	assert.Equal(t, 0, got)
	assert.Equal(t, 0, c.Stats().Size)
}

func TestGetOrCompute_Singleflight_CloseDuringComputeReturnsErrClosed(t *testing.T) {
	c := New[string, int]()
	start := make(chan struct{})
	release := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(1)
	var gotErr error
	go func() {
		defer wg.Done()
		_, gotErr = c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
			close(start)
			<-release
			return 42, nil
		}, WithSingleflight())
	}()

	<-start
	c.Close()
	close(release)
	wg.Wait()

	require.ErrorIs(t, gotErr, ErrClosed)
	assert.Equal(t, 0, c.Stats().Size)
}

func TestGetOrCompute_PanicReturnsPanicError(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	_, err := c.GetOrCompute(context.Background(), "k", func(context.Context) (int, error) {
		panic("compute boom")
	})
	require.Error(t, err)
	testx.RequirePanicError(t, err, opGetOrCompute)
	assert.False(t, c.Has("k"))
}
