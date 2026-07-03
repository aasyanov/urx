package syncx

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/aasyanov/urx/panix"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Lazy ---

func TestNewLazy_NilInit(t *testing.T) {
	l, err := NewLazy[int](nil)
	require.ErrorIs(t, err, ErrNilInit)
	assert.Nil(t, l)
}

func TestLazy_GetInitializesOnce(t *testing.T) {
	var calls atomic.Int64
	l, err := NewLazy(func() (int, error) {
		calls.Add(1)
		return 42, nil
	})
	require.NoError(t, err)

	assert.False(t, l.Done())

	for range 5 {
		v, err := l.Get()
		require.NoError(t, err)
		assert.Equal(t, 42, v)
	}
	assert.Equal(t, int64(1), calls.Load())
	assert.True(t, l.Done())
}

func TestLazy_GetWrapsInitError(t *testing.T) {
	cause := errors.New("boom")
	l, err := NewLazy(func() (int, error) {
		return 0, cause
	})
	require.NoError(t, err)

	v, err := l.Get()
	assert.Zero(t, v)
	require.ErrorIs(t, err, ErrInitFailed)
	require.ErrorIs(t, err, cause)
	assert.False(t, l.Done())
}

func TestLazy_GetRetriesAfterFailure(t *testing.T) {
	var calls atomic.Int64
	l, err := NewLazy(func() (int, error) {
		n := calls.Add(1)
		if n < 3 {
			return 0, errors.New("transient")
		}
		return 7, nil
	})
	require.NoError(t, err)

	_, err1 := l.Get()
	require.ErrorIs(t, err1, ErrInitFailed)
	_, err2 := l.Get()
	require.ErrorIs(t, err2, ErrInitFailed)

	v, err3 := l.Get()
	require.NoError(t, err3)
	assert.Equal(t, 7, v)
	assert.Equal(t, int64(3), calls.Load())
}

func TestLazy_Reset(t *testing.T) {
	var calls atomic.Int64
	l, err := NewLazy(func() (int64, error) {
		return calls.Add(1), nil
	})
	require.NoError(t, err)

	v1, _ := l.Get()
	assert.Equal(t, int64(1), v1)

	l.Reset()
	assert.False(t, l.Done())

	v2, _ := l.Get()
	assert.Equal(t, int64(2), v2)
}

func TestLazy_ResetIdempotent(t *testing.T) {
	l, err := NewLazy(func() (int, error) { return 1, nil })
	require.NoError(t, err)
	assert.NotPanics(t, func() {
		l.Reset()
		l.Reset()
	})
}

func TestLazy_GetRecoversInitPanic(t *testing.T) {
	l, err := NewLazy(func() (int, error) {
		panic("init crashed")
	})
	require.NoError(t, err)

	v, err := l.Get()
	assert.Zero(t, v)
	pe := testx.RequirePanicError(t, err, opLazy)
	assert.Equal(t, "init crashed", pe.Value)
	assert.False(t, l.Done())
}

func TestLazy_GetRetriesAfterInitPanic(t *testing.T) {
	var calls atomic.Int64
	l, err := NewLazy(func() (int, error) {
		if calls.Add(1) < 2 {
			panic("transient")
		}
		return 9, nil
	})
	require.NoError(t, err)

	_, err1 := l.Get()
	testx.RequirePanicError(t, err1, opLazy)

	v, err2 := l.Get()
	require.NoError(t, err2)
	assert.Equal(t, 9, v)
	assert.Equal(t, int64(2), calls.Load())
}

func TestLazy_ConcurrentGet(t *testing.T) {
	var calls atomic.Int64
	l, err := NewLazy(func() (int, error) {
		calls.Add(1)
		time.Sleep(time.Millisecond)
		return 99, nil
	})
	require.NoError(t, err)

	testx.HammerNoError(t, 50, 100, func() error {
		v, err := l.Get()
		if err != nil {
			return err
		}
		if v != 99 {
			return errors.New("unexpected value")
		}
		return nil
	})
	assert.Equal(t, int64(1), calls.Load())
}

func TestLazy_ConcurrentGetAndReset(t *testing.T) {
	l, err := NewLazy(func() (int, error) { return 1, nil })
	require.NoError(t, err)

	var stop atomic.Bool
	done := make(chan struct{})
	go func() {
		for !stop.Load() {
			l.Reset()
		}
		close(done)
	}()

	testx.HammerVoid(20, 200, func() { _, _ = l.Get() })
	stop.Store(true)
	<-done
}

// --- Map ---

func TestMap_StoreLoadDelete(t *testing.T) {
	m := NewMap[string, int]()

	_, ok := m.Load("missing")
	assert.False(t, ok)
	assert.Equal(t, 0, m.Len())

	m.Store("a", 1)
	m.Store("b", 2)
	assert.Equal(t, 2, m.Len())

	v, ok := m.Load("a")
	assert.True(t, ok)
	assert.Equal(t, 1, v)

	m.Store("a", 10) // overwrite does not change len
	assert.Equal(t, 2, m.Len())
	v, _ = m.Load("a")
	assert.Equal(t, 10, v)

	m.Delete("a")
	assert.Equal(t, 1, m.Len())
	_, ok = m.Load("a")
	assert.False(t, ok)

	m.Delete("missing") // no-op
	assert.Equal(t, 1, m.Len())
}

func TestMap_Swap(t *testing.T) {
	m := NewMap[string, int]()

	prev, loaded := m.Swap("k", 1)
	assert.False(t, loaded)
	assert.Zero(t, prev)
	assert.Equal(t, 1, m.Len())

	prev, loaded = m.Swap("k", 2)
	assert.True(t, loaded)
	assert.Equal(t, 1, prev)
	assert.Equal(t, 1, m.Len())
}

func TestMap_LoadAndDelete(t *testing.T) {
	m := NewMap[string, int]()
	m.Store("k", 5)

	v, loaded := m.LoadAndDelete("k")
	assert.True(t, loaded)
	assert.Equal(t, 5, v)
	assert.Equal(t, 0, m.Len())

	v, loaded = m.LoadAndDelete("k")
	assert.False(t, loaded)
	assert.Zero(t, v)
	assert.Equal(t, 0, m.Len())
}

func TestMap_LoadOrStore(t *testing.T) {
	m := NewMap[string, int]()

	actual, loaded := m.LoadOrStore("k", 1)
	assert.False(t, loaded)
	assert.Equal(t, 1, actual)
	assert.Equal(t, 1, m.Len())

	actual, loaded = m.LoadOrStore("k", 2)
	assert.True(t, loaded)
	assert.Equal(t, 1, actual)
	assert.Equal(t, 1, m.Len())
}

func TestMap_Range(t *testing.T) {
	m := NewMap[int, int]()
	for i := range 10 {
		m.Store(i, i*i)
	}

	seen := map[int]int{}
	m.Range(func(k, v int) bool {
		seen[k] = v
		return true
	})
	assert.Len(t, seen, 10)
	assert.Equal(t, 81, seen[9])
}

func TestMap_RangeEarlyStop(t *testing.T) {
	m := NewMap[int, int]()
	for i := range 100 {
		m.Store(i, i)
	}

	count := 0
	m.Range(func(int, int) bool {
		count++
		return count < 3
	})
	assert.Equal(t, 3, count)
}

func TestMap_Clear(t *testing.T) {
	m := NewMap[int, int]()
	for i := range 50 {
		m.Store(i, i)
	}
	assert.Equal(t, 50, m.Len())

	m.Clear()
	assert.Equal(t, 0, m.Len())

	_, ok := m.Load(0)
	assert.False(t, ok)

	m.Clear() // idempotent on empty map
	assert.Equal(t, 0, m.Len())
}

func TestMap_Clear_Concurrent(t *testing.T) {
	m := NewMap[int, int]()
	for i := range 100 {
		m.Store(i, i)
	}

	testx.HammerVoid(20, 200, func() {
		m.Clear()
		m.Store(999, 999)
		m.Delete(999)
	})

	seen := 0
	m.Range(func(int, int) bool {
		seen++
		return true
	})
	assert.Equal(t, m.Len(), seen)
}

func TestMap_ClearLenMatchesRangeUnderConcurrency(t *testing.T) {
	m := NewMap[int, int]()
	for i := range 100 {
		m.Store(i, i)
	}

	testx.HammerVoid(50, 200, func() {
		m.Clear()
		m.Store(999, 999)
		m.Delete(999)
	})

	seen := 0
	m.Range(func(int, int) bool {
		seen++
		return true
	})
	assert.Equal(t, m.Len(), seen, "Len must match the number of entries Range visits")
}

func TestMap_ConcurrentDisjointKeys(t *testing.T) {
	m := NewMap[int, int]()
	testx.HammerVoid(50, 1000, func() {
		k := int(time.Now().UnixNano())
		m.Store(k, k)
		m.Load(k)
		m.Delete(k)
	})
	assert.Equal(t, 0, m.Len())
}

func TestMap_ConcurrentLenConsistency(t *testing.T) {
	const keys = 100
	m := NewMap[int, int]()

	testx.HammerIndexed(keys, 200, func(idx int) error {
		m.Store(idx, idx)
		m.Delete(idx)
		return nil
	})
	assert.Equal(t, 0, m.Len())
}

// --- Group ---

func TestNewGroup_DerivedContextCancelledOnWait(t *testing.T) {
	g, ctx := NewGroup(context.Background())
	g.Go(func(context.Context) error { return nil })
	require.NoError(t, g.Wait())

	select {
	case <-ctx.Done():
	default:
		t.Fatal("derived context should be cancelled after Wait")
	}
}

func TestGroup_CollectsFirstError(t *testing.T) {
	g, _ := NewGroup(context.Background())
	sentinel := errors.New("first")

	g.Go(func(context.Context) error { return sentinel })
	g.Go(func(context.Context) error {
		time.Sleep(10 * time.Millisecond)
		return nil
	})

	err := g.Wait()
	require.ErrorIs(t, err, sentinel)
}

func TestGroup_ParentContextCancelled(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	g, ctx := NewGroup(parent)
	cancel()

	observed := make(chan struct{})
	g.Go(func(c context.Context) error {
		<-c.Done()
		close(observed)
		return c.Err()
	})

	_ = g.Wait()
	select {
	case <-observed:
	default:
		t.Fatal("task should observe parent cancellation")
	}
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestGroup_ReuseAfterWait(t *testing.T) {
	g, ctx := NewGroup(context.Background())
	g.Go(func(context.Context) error { return nil })
	require.NoError(t, g.Wait())

	select {
	case <-ctx.Done():
	default:
		t.Fatal("derived context should be cancelled after Wait")
	}

	errCh := make(chan error, 1)
	g.Go(func(c context.Context) error {
		errCh <- c.Err()
		return c.Err()
	})
	err := g.Wait()
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, <-errCh, context.Canceled)
}

func TestGroup_AllSucceed(t *testing.T) {
	g, _ := NewGroup(context.Background())
	var done atomic.Int64
	for range 20 {
		g.Go(func(context.Context) error {
			done.Add(1)
			return nil
		})
	}
	require.NoError(t, g.Wait())
	assert.Equal(t, int64(20), done.Load())

	st := g.Stats()
	assert.Equal(t, int64(20), st.Started)
	assert.Equal(t, int64(20), st.Succeeded)
	assert.Zero(t, st.Failed)
}

func TestGroup_CancelsSiblingsOnError(t *testing.T) {
	g, _ := NewGroup(context.Background())
	g.Go(func(context.Context) error { return errors.New("fail") })

	cancelled := make(chan struct{})
	g.Go(func(ctx context.Context) error {
		<-ctx.Done()
		close(cancelled)
		return nil
	})

	require.Error(t, g.Wait())
	select {
	case <-cancelled:
	default:
		t.Fatal("sibling was not cancelled")
	}
}

func TestGroup_RecoversPanic(t *testing.T) {
	g, _ := NewGroup(context.Background())
	g.Go(func(context.Context) error {
		panic("task crashed")
	})

	err := g.Wait()
	pe := testx.RequirePanicError(t, err, opGroup)
	assert.Equal(t, "task crashed", pe.Value)

	st := g.Stats()
	assert.Equal(t, int64(1), st.Panicked)
	assert.Equal(t, int64(1), st.Failed)
}

func TestGroup_NilFuncIgnored(t *testing.T) {
	g, _ := NewGroup(context.Background())
	g.Go(nil)
	assert.False(t, g.TryGo(nil))
	require.NoError(t, g.Wait())
	assert.Zero(t, g.Stats().Started)
}

func TestGroup_WithLimitBoundsConcurrency(t *testing.T) {
	const limit = 3
	g, _ := NewGroup(context.Background(), WithLimit(limit))

	var current, peak atomic.Int64
	for range 30 {
		g.Go(func(context.Context) error {
			c := current.Add(1)
			for {
				p := peak.Load()
				if c <= p || peak.CompareAndSwap(p, c) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			current.Add(-1)
			return nil
		})
	}
	require.NoError(t, g.Wait())
	assert.LessOrEqual(t, peak.Load(), int64(limit))
}

func TestGroup_TryGoRespectsLimit(t *testing.T) {
	g, _ := NewGroup(context.Background(), WithLimit(1))

	release := make(chan struct{})
	started := make(chan struct{})
	require.True(t, g.TryGo(func(context.Context) error {
		close(started)
		<-release
		return nil
	}))
	<-started

	// The single slot is occupied, so the next TryGo must fail fast.
	assert.False(t, g.TryGo(func(context.Context) error { return nil }))

	close(release)
	require.NoError(t, g.Wait())
}

func TestGroup_TryGoFromLimitedTaskAvoidsDeadlock(t *testing.T) {
	g, _ := NewGroup(context.Background(), WithLimit(1))
	ready := make(chan struct{})
	g.Go(func(context.Context) error {
		close(ready)
		assert.False(t, g.TryGo(func(context.Context) error { return nil }),
			"TryGo must fail while the single concurrency slot is held")
		return nil
	})
	<-ready
	require.NoError(t, g.Wait())
}

func TestGroup_TryGoUnlimitedAlwaysStarts(t *testing.T) {
	g, _ := NewGroup(context.Background())
	assert.True(t, g.TryGo(func(context.Context) error { return nil }))
	require.NoError(t, g.Wait())
}

func TestGroup_ConcurrentGo(t *testing.T) {
	g, _ := NewGroup(context.Background(), WithLimit(8))
	var ran atomic.Int64

	testx.HammerVoid(10, 50, func() {
		g.Go(func(context.Context) error {
			ran.Add(1)
			return nil
		})
	})
	require.NoError(t, g.Wait())
	assert.Equal(t, int64(500), ran.Load())
}

// --- Options ---

func TestWithLimit(t *testing.T) {
	tests := []struct {
		name string
		opt  GroupOption
		want int
	}{
		{name: "default", opt: nil, want: defaultLimit},
		{name: "custom", opt: WithLimit(10), want: 10},
		{name: "zero ignored", opt: WithLimit(0), want: defaultLimit},
		{name: "negative ignored", opt: WithLimit(-5), want: defaultLimit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var opts []GroupOption
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}
			cfg := newGroupConfig(opts)
			assert.Equal(t, tt.want, cfg.limit)
		})
	}
}

// --- helpers ---

func TestIsPanic(t *testing.T) {
	assert.False(t, isPanic(nil))
	assert.False(t, isPanic(errors.New("plain")))
	assert.True(t, isPanic(&panix.PanicError{Op: "x", Value: "boom"}))
}
