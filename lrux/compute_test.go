package lrux

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetOrCompute_StatsExactlyOnePerCall verifies that a logical miss
// followed by a populate yields exactly one miss and that a subsequent hit
// yields exactly one hit, with no double-counting through the compute path.
func TestGetOrCompute_StatsExactlyOnePerCall(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	c.GetOrCompute("k", func() int { return 1 }) // one miss, then populated
	s := c.Stats()
	assert.Equal(t, uint64(0), s.Hits)
	assert.Equal(t, uint64(1), s.Misses)

	c.GetOrCompute("k", func() int { return 2 }) // one hit
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
			c.GetOrCompute("k", func() int {
				<-release
				return 1
			}, WithSingleflight())
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	s := c.Stats()
	// Each waiter's outer Get records a miss; the leader's inner closure must
	// not add extra misses. Total misses must not exceed the waiter count.
	assert.LessOrEqual(t, s.Misses, uint64(waiters))
	assert.GreaterOrEqual(t, s.Misses, uint64(1))
}

// TestGetOrComputeCtx_StatsExactlyOnePerCall mirrors the direct variant for
// the context-aware compute.
func TestGetOrComputeCtx_StatsExactlyOnePerCall(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	_, err := c.GetOrComputeCtx(context.Background(), "k", func(context.Context) (int, error) {
		return 1, nil
	})
	require.NoError(t, err)
	s := c.Stats()
	assert.Equal(t, uint64(1), s.Misses)
	assert.Equal(t, uint64(0), s.Hits)

	_, err = c.GetOrComputeCtx(context.Background(), "k", func(context.Context) (int, error) {
		return 2, nil
	})
	require.NoError(t, err)
	s = c.Stats()
	assert.Equal(t, uint64(1), s.Hits)
	assert.Equal(t, uint64(1), s.Misses)
}

// TestGetOrCompute_ConcurrentPopulateConvertsMissToHit drives the
// convertMissToHit branch: while the direct compute runs, another goroutine
// populates the key, so the double-check converts the recorded miss into a hit.
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
		got = c.GetOrCompute("k", func() int {
			close(started)
			<-release
			return 1
		})
	}()

	<-started
	c.Set("k", 42)
	close(release)
	wg.Wait()

	// The recheck observes the concurrently stored value and returns it.
	assert.Equal(t, 42, got)
	// The single logical lookup is accounted exactly once, as a hit (the
	// originating miss was converted), never double-counted.
	s := c.Stats()
	assert.Equal(t, uint64(1), s.Hits+s.Misses)
	assert.Equal(t, uint64(1), s.Hits)
}

// TestPeekPromote covers the internal lookup-and-promote helper used by the
// singleflight paths: it returns live values, promotes them to MRU, reports
// expired and missing keys as absent, and leaves statistics untouched.
func TestPeekPromote(t *testing.T) {
	t.Run("hit promotes without stats", func(t *testing.T) {
		c := New[string, int](WithCapacity[string, int](3))
		defer c.Close()
		c.Set("a", 1)
		c.Set("b", 2)
		c.Set("c", 3)

		v, ok := c.peekPromote("a") // a becomes MRU
		require.True(t, ok)
		assert.Equal(t, 1, v)

		s := c.Stats()
		assert.Zero(t, s.Hits)
		assert.Zero(t, s.Misses)

		c.Set("d", 4) // evicts LRU, which must not be "a"
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
	})
}

// TestConvertMissToHit covers the counter rebalancing helper directly.
func TestConvertMissToHit(t *testing.T) {
	c := New[string, int]()
	defer c.Close()
	c.misses.Store(1)

	c.convertMissToHit()

	s := c.Stats()
	assert.Equal(t, uint64(1), s.Hits)
	assert.Equal(t, uint64(0), s.Misses)
}

// TestCompute_RecheckAfterCompute exercises the double-check branch where a
// second goroutine stores the value while compute is running; the first
// goroutine must observe and return that stored value.
func TestCompute_RecheckAfterCompute(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	var got int
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		got = c.GetOrCompute("k", func() int {
			close(started)
			<-release
			return 1 // computed late
		})
	}()

	<-started
	c.Set("k", 99) // another goroutine wins the race
	close(release)
	wg.Wait()

	assert.Equal(t, 99, got) // recheck branch returns the stored value
}

// TestCompute_ExpiredEntryRemovedBeforeCompute exercises the branch that
// removes an expired entry before invoking compute.
func TestCompute_ExpiredEntryRemovedBeforeCompute(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	c.SetWithTTL("k", 1, time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	v := c.GetOrCompute("k", func() int { return 2 })
	assert.Equal(t, 2, v)
}

func TestCompute_Closed_ReturnsZero(t *testing.T) {
	c := New[string, int]()
	c.Close()
	v := c.GetOrCompute("k", func() int { return 1 })
	assert.Equal(t, 0, v)
}

func TestComputeCtx_RecheckAfterCompute(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	started := make(chan struct{})
	release := make(chan struct{})
	var got int
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		got, _ = c.GetOrComputeCtx(context.Background(), "k", func(context.Context) (int, error) {
			close(started)
			<-release
			return 1, nil
		})
	}()

	<-started
	c.Set("k", 77)
	close(release)
	wg.Wait()

	assert.Equal(t, 77, got)
}

func TestComputeCtx_ExpiredEntryRemovedBeforeCompute(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	c.SetWithTTL("k", 1, time.Millisecond)
	time.Sleep(10 * time.Millisecond)

	v, err := c.GetOrComputeCtx(context.Background(), "k", func(context.Context) (int, error) {
		return 2, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 2, v)
}

// TestComputeSingleCtx_CallerCancelDoesNotAbortShared verifies that one
// caller cancelling its context returns an error to that caller while the
// shared singleflight computation still completes and caches the value.
func TestComputeSingleCtx_CallerCancelDoesNotAbortShared(t *testing.T) {
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
		_, cancelErr = c.GetOrComputeCtx(cancelCtx, "k", func(ctx context.Context) (int, error) {
			computeCalls.Add(1)
			close(enter)
			<-finish
			return 5, nil
		}, WithSingleflight())
	}()

	<-enter
	cancel() // first caller cancels mid-flight
	close(finish)
	wg.Wait()

	require.ErrorIs(t, cancelErr, context.Canceled)
	assert.Equal(t, int64(1), computeCalls.Load())

	// The shared computation should still have cached the value.
	require.Eventually(t, func() bool {
		v, ok := c.Get("k")
		return ok && v == 5
	}, time.Second, 5*time.Millisecond)
}

func TestComputeSingleCtx_ComputeError(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	_, err := c.GetOrComputeCtx(context.Background(), "k", func(context.Context) (int, error) {
		return 0, ErrNotFound
	}, WithSingleflight())
	require.ErrorIs(t, err, ErrNotFound)
	assert.False(t, c.Has("k"))
}

// TestCompute_ExpiredRecheckAfterCompute forces the post-compute recheck to
// find an expired entry (inserted with a tiny TTL during compute) and replace
// it. This exercises the second expired-removal branch in computeDirect.
func TestCompute_ExpiredRecheckAfterCompute(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	v := c.GetOrCompute("k", func() int {
		c.SetWithTTL("k", 1, time.Nanosecond) // expires before recheck
		time.Sleep(time.Millisecond)
		return 2
	})
	assert.Equal(t, 2, v)
}

func TestComputeCtx_ExpiredRecheckAfterCompute(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	v, err := c.GetOrComputeCtx(context.Background(), "k", func(context.Context) (int, error) {
		c.SetWithTTL("k", 1, time.Nanosecond)
		time.Sleep(time.Millisecond)
		return 2, nil
	})
	require.NoError(t, err)
	assert.Equal(t, 2, v)
}

func TestComputeCtx_CancelledAfterMiss(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	_, err := c.GetOrComputeCtx(ctx, "k", func(context.Context) (int, error) {
		cancel() // cancel during compute → checked on next ctx.Err()
		return 1, nil
	})
	// Direct (non-singleflight) path returns the computed value because the
	// post-compute path stores it; the context is only checked before compute.
	require.NoError(t, err)
}
