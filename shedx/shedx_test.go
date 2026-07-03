package shedx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fill admits n requests at the given priority and returns their tokens so the
// caller can hold the shedder at a chosen load. It fails the test if any
// admission is rejected.
func fill(t *testing.T, s *Shedder, priority Priority, n int) []*Token {
	t.Helper()
	tokens := make([]*Token, 0, n)
	for range n {
		tok, err := s.Acquire(priority)
		require.NoError(t, err)
		tokens = append(tokens, tok)
	}
	return tokens
}

func release(tokens []*Token) {
	for _, tok := range tokens {
		tok.Release()
	}
}

// --- Construction & defaults ---

func TestNew_AppliesDefaults(t *testing.T) {
	s := New()
	assert.Equal(t, DefaultCapacity, s.Capacity())
	assert.InEpsilon(t, DefaultThreshold, s.Threshold(), 1e-9)
}

func TestNew_OptionValidation(t *testing.T) {
	tests := []struct {
		name          string
		opts          []Option
		wantCapacity  int
		wantThreshold float64
	}{
		{"defaults", nil, DefaultCapacity, DefaultThreshold},
		{"custom capacity", []Option{WithCapacity(50)}, 50, DefaultThreshold},
		{"zero capacity ignored then floored", []Option{WithCapacity(0)}, DefaultCapacity, DefaultThreshold},
		{"negative capacity ignored", []Option{WithCapacity(-5)}, DefaultCapacity, DefaultThreshold},
		{"custom threshold", []Option{WithThreshold(0.5)}, DefaultCapacity, 0.5},
		{"threshold at ceil", []Option{WithThreshold(1.0)}, DefaultCapacity, 1.0},
		{"threshold zero ignored", []Option{WithThreshold(0)}, DefaultCapacity, DefaultThreshold},
		{"threshold above one ignored", []Option{WithThreshold(1.5)}, DefaultCapacity, DefaultThreshold},
		{"threshold negative ignored", []Option{WithThreshold(-0.1)}, DefaultCapacity, DefaultThreshold},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := New(tt.opts...)
			assert.Equal(t, tt.wantCapacity, s.Capacity())
			assert.InEpsilon(t, tt.wantThreshold, s.Threshold(), 1e-9)
		})
	}
}

func TestNew_CapacityFlooredToMin(t *testing.T) {
	// WithCapacity ignores n<=0, but a direct out-of-range config still floors.
	cfg := newConfig([]Option{func(c *config) { c.capacity = -10 }})
	assert.Equal(t, minCapacity, cfg.capacity)
}

func TestNewConfig_ThresholdResetToDefault(t *testing.T) {
	// WithThreshold ignores out-of-range input, but a config mutated past the
	// bounds is reset to the default rather than left invalid.
	tests := []struct {
		name string
		set  float64
	}{
		{"zero", 0},
		{"negative", -0.5},
		{"above ceil", 1.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newConfig([]Option{func(c *config) { c.threshold = tt.set }})
			assert.InEpsilon(t, DefaultThreshold, cfg.threshold, 1e-9)
		})
	}
}

func TestWithOp_OverridesDefault(t *testing.T) {
	assert.Equal(t, opExecute, newConfig(nil).opOrDefault())
	assert.Equal(t, opTryExecute, newConfig(nil).opOrDefaultTry())
	assert.Equal(t, "api.search", newConfig([]Option{WithOp("api.search")}).opOrDefault())
	assert.Equal(t, "api.search", newConfig([]Option{WithOp("api.search")}).opOrDefaultTry())
	assert.Equal(t, opExecute, newConfig([]Option{WithOp("")}).opOrDefault())
	assert.Equal(t, opTryExecute, newConfig([]Option{WithOp("")}).opOrDefaultTry())
}

// --- Priority ---

func TestPriority_String(t *testing.T) {
	tests := []struct {
		p    Priority
		want string
	}{
		{PriorityLow, labelLow},
		{PriorityNormal, labelNormal},
		{PriorityHigh, labelHigh},
		{PriorityCritical, labelCritical},
		{Priority(99), labelUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.p.String())
		})
	}
}

// --- Execute: happy path ---

func TestExecute_AdmitsUnderThreshold(t *testing.T) {
	s := New(WithCapacity(10))
	defer func() { require.NoError(t, s.Close()) }()

	got, err := Execute(s, context.Background(), PriorityNormal,
		func(context.Context, ShedController) (int, error) {
			return 42, nil
		})
	require.NoError(t, err)
	assert.Equal(t, 42, got)
	assert.Equal(t, int64(1), s.Stats().Admitted)
}

func TestExecute_ReleasesSlotAfterReturn(t *testing.T) {
	s := New(WithCapacity(10))
	defer func() { require.NoError(t, s.Close()) }()

	_, err := Execute(s, context.Background(), PriorityNormal,
		func(_ context.Context, sc ShedController) (int, error) {
			assert.Equal(t, int64(1), s.InFlight())
			assert.Equal(t, int64(0), sc.InFlight()) // snapshot excludes self
			assert.Equal(t, 10, sc.Capacity())
			return 1, nil
		})
	require.NoError(t, err)
	assert.Equal(t, int64(0), s.InFlight())
}

func TestExecute_PropagatesError(t *testing.T) {
	s := New()
	defer func() { require.NoError(t, s.Close()) }()

	sentinel := errors.New("boom")
	_, err := Execute(s, context.Background(), PriorityNormal,
		func(context.Context, ShedController) (int, error) {
			return 0, sentinel
		})
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, int64(0), s.InFlight())
}

// --- Execute: error paths ---

func TestExecute_ReturnsErrClosedAfterClose(t *testing.T) {
	s := New()
	require.NoError(t, s.Close())
	testx.AssertOpAfterClose(t, func() error {
		_, err := Execute(s, context.Background(), PriorityCritical,
			func(context.Context, ShedController) (int, error) { return 1, nil })
		return err
	}, ErrClosed, "Execute")
}

func TestExecute_ReturnsErrNilFunc(t *testing.T) {
	s := New()
	defer func() { require.NoError(t, s.Close()) }()

	_, err := Execute[int](s, context.Background(), PriorityNormal, nil)
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestExecute_ReturnsErrCancelledOnCancelledContext(t *testing.T) {
	s := New()
	defer func() { require.NoError(t, s.Close()) }()

	called := false
	_, err := Execute(s, testx.CancelledCtx(), PriorityCritical,
		func(context.Context, ShedController) (int, error) {
			called = true
			return 1, nil
		})
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, called, "fn must not run for a cancelled context")
	assert.Equal(t, int64(0), s.InFlight(), "cancelled request must not consume a slot")
	assert.Equal(t, int64(0), s.Stats().Admitted)
}

func TestExecute_ReturnsErrCancelledOnExpiredDeadline(t *testing.T) {
	s := New()
	defer func() { require.NoError(t, s.Close()) }()

	_, err := Execute(s, testx.ExpiredCtx(), PriorityCritical,
		func(context.Context, ShedController) (int, error) { return 1, nil })
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestExecute_ShedRollsBackReservation(t *testing.T) {
	s := New(WithCapacity(4), WithThreshold(0.5))
	defer func() { require.NoError(t, s.Close()) }()

	tokens := fill(t, s, PriorityCritical, 4)
	defer release(tokens)

	// Several shed attempts must not accumulate phantom in-flight slots.
	for range 10 {
		_, err := Execute(s, context.Background(), PriorityLow,
			func(context.Context, ShedController) (int, error) { return 1, nil })
		require.ErrorIs(t, err, ErrRejected)
	}
	assert.Equal(t, int64(4), s.InFlight(), "shed must roll back its reserved slot")
	assert.Equal(t, int64(10), s.Stats().Shed)
}

func TestExecute_RejectsWhenOverloaded(t *testing.T) {
	s := New(WithCapacity(10), WithThreshold(0.5))
	defer func() { require.NoError(t, s.Close()) }()

	// Hold 10/10 = full load; only Critical survives.
	tokens := fill(t, s, PriorityCritical, 10)
	defer release(tokens)

	_, err := Execute(s, context.Background(), PriorityLow,
		func(context.Context, ShedController) (int, error) { return 1, nil })
	require.ErrorIs(t, err, ErrRejected)
	assert.ErrorContains(t, err, "low")
	assert.Equal(t, int64(1), s.Stats().Shed)
}

func TestExecute_RecoversPanic(t *testing.T) {
	s := New(WithOp("shedx.test"))
	defer func() { require.NoError(t, s.Close()) }()

	_, err := Execute(s, context.Background(), PriorityNormal,
		func(context.Context, ShedController) (int, error) {
			panic("kaboom")
		})
	testx.RequirePanicError(t, err, "shedx.test")
	assert.Equal(t, int64(0), s.InFlight(), "slot released even on panic")
}

// --- TryExecute ---

func TestTryExecute_RunsWhenAdmitted(t *testing.T) {
	s := New(WithCapacity(10))
	defer func() { require.NoError(t, s.Close()) }()

	ok, got, err := TryExecute(s, context.Background(), PriorityNormal,
		func(context.Context, ShedController) (int, error) { return 42, nil })
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 42, got)
	assert.Equal(t, int64(1), s.Stats().Admitted)
}

func TestTryExecute_SkipsWhenShed(t *testing.T) {
	s := New(WithCapacity(4), WithThreshold(0.5))
	defer func() { require.NoError(t, s.Close()) }()

	tokens := fill(t, s, PriorityCritical, 4)
	defer release(tokens)

	called := false
	ok, _, err := TryExecute(s, context.Background(), PriorityLow,
		func(context.Context, ShedController) (int, error) {
			called = true
			return 1, nil
		})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.False(t, called, "fn must not run when the request is shed")
	assert.Equal(t, int64(1), s.Stats().Shed)
	assert.Equal(t, int64(4), s.InFlight(), "shed must roll back its reserved slot")
}

func TestTryExecute_ReturnsErrClosedAfterClose(t *testing.T) {
	s := New()
	require.NoError(t, s.Close())
	testx.AssertOpAfterClose(t, func() error {
		ok, _, err := TryExecute(s, context.Background(), PriorityCritical,
			func(context.Context, ShedController) (int, error) { return 1, nil })
		if ok {
			return errors.New("expected ok=false")
		}
		return err
	}, ErrClosed, "TryExecute")
}

func TestTryExecute_ReturnsErrNilFunc(t *testing.T) {
	s := New()
	defer func() { require.NoError(t, s.Close()) }()

	ok, _, err := TryExecute[int](s, context.Background(), PriorityNormal, nil)
	require.ErrorIs(t, err, ErrNilFunc)
	assert.False(t, ok)
}

func TestTryExecute_ReturnsErrCancelledOnCancelledContext(t *testing.T) {
	s := New()
	defer func() { require.NoError(t, s.Close()) }()

	called := false
	ok, _, err := TryExecute(s, testx.CancelledCtx(), PriorityCritical,
		func(context.Context, ShedController) (int, error) {
			called = true
			return 1, nil
		})
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, ok)
	assert.False(t, called, "fn must not run for a cancelled context")
	assert.Equal(t, int64(0), s.InFlight(), "cancelled request must not consume a slot")
}

func TestTryExecute_ReturnsErrCancelledOnExpiredDeadline(t *testing.T) {
	s := New()
	defer func() { require.NoError(t, s.Close()) }()

	ok, _, err := TryExecute(s, testx.ExpiredCtx(), PriorityCritical,
		func(context.Context, ShedController) (int, error) { return 1, nil })
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, ok)
}

func TestTryExecute_ReleasesSlotAfterReturn(t *testing.T) {
	s := New(WithCapacity(10))
	defer func() { require.NoError(t, s.Close()) }()

	ok, _, err := TryExecute(s, context.Background(), PriorityNormal,
		func(_ context.Context, sc ShedController) (int, error) {
			assert.Equal(t, int64(1), s.InFlight())
			assert.Equal(t, int64(0), sc.InFlight())
			return 1, nil
		})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, int64(0), s.InFlight())
}

func TestTryExecute_PropagatesError(t *testing.T) {
	s := New()
	defer func() { require.NoError(t, s.Close()) }()

	sentinel := errors.New("boom")
	ok, _, err := TryExecute(s, context.Background(), PriorityNormal,
		func(context.Context, ShedController) (int, error) {
			return 0, sentinel
		})
	require.ErrorIs(t, err, sentinel)
	assert.True(t, ok)
	assert.Equal(t, int64(0), s.InFlight())
}

func TestTryExecute_RecoversPanic(t *testing.T) {
	s := New()
	defer func() { require.NoError(t, s.Close()) }()

	ok, _, err := TryExecute(s, context.Background(), PriorityNormal,
		func(context.Context, ShedController) (int, error) {
			panic("kaboom")
		})
	assert.True(t, ok)
	testx.RequirePanicError(t, err, opTryExecute)
	assert.Equal(t, int64(0), s.InFlight(), "slot released even on panic")
}

func TestTryExecute_RecoversPanic_WithCustomOp(t *testing.T) {
	s := New(WithOp("api.search"))
	defer func() { require.NoError(t, s.Close()) }()

	ok, _, err := TryExecute(s, context.Background(), PriorityNormal,
		func(context.Context, ShedController) (int, error) {
			panic("kaboom")
		})
	assert.True(t, ok)
	testx.RequirePanicError(t, err, "api.search")
}

func TestTryExecute_ShedRecordsDegradation(t *testing.T) {
	s := New()
	defer func() { require.NoError(t, s.Close()) }()

	ok, _, err := TryExecute(s, context.Background(), PriorityNormal,
		func(_ context.Context, sc ShedController) (string, error) {
			sc.Shed()
			return "cached", nil
		})
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, int64(1), s.Stats().Degraded)
}

func TestTryExecute_ReturnsErrClosedWhenClosedDuringReserve(t *testing.T) {
	s := New(WithCapacity(1000), WithThreshold(0.9))
	defer func() { require.NoError(t, s.Close()) }()

	var wg sync.WaitGroup
	start := make(chan struct{})
	type result struct {
		ok  bool
		err error
	}
	results := make(chan result, 64)

	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, _, err := TryExecute(s, context.Background(), PriorityNormal,
				func(context.Context, ShedController) (int, error) { return 1, nil })
			results <- result{ok: ok, err: err}
		}()
	}

	require.NoError(t, s.Close())
	close(start)
	wg.Wait()
	close(results)

	for r := range results {
		require.ErrorIs(t, r.err, ErrClosed)
		assert.False(t, r.ok)
	}
	assert.Equal(t, int64(0), s.Stats().Shed)
}

// --- ShedController.Shed ---

func TestExecute_ShedRecordsDegradation(t *testing.T) {
	s := New()
	defer func() { require.NoError(t, s.Close()) }()

	_, err := Execute(s, context.Background(), PriorityNormal,
		func(_ context.Context, sc ShedController) (string, error) {
			sc.Shed()
			sc.Shed() // idempotent for counting purposes
			return "cached", nil
		})
	require.NoError(t, err)
	assert.Equal(t, int64(1), s.Stats().Degraded)
}

func TestExecute_NoDegradationByDefault(t *testing.T) {
	s := New()
	defer func() { require.NoError(t, s.Close()) }()

	_, err := Execute(s, context.Background(), PriorityNormal,
		func(context.Context, ShedController) (string, error) { return "fresh", nil })
	require.NoError(t, err)
	assert.Equal(t, int64(0), s.Stats().Degraded)
}

func TestExecute_ControllerLoadSnapshot(t *testing.T) {
	s := New(WithCapacity(10), WithThreshold(0.9))
	defer func() { require.NoError(t, s.Close()) }()

	tokens := fill(t, s, PriorityCritical, 4) // load 0.4 before the call
	defer release(tokens)

	_, err := Execute(s, context.Background(), PriorityHigh,
		func(_ context.Context, sc ShedController) (int, error) {
			assert.InEpsilon(t, 0.4, sc.Load(), 1e-9)
			assert.Equal(t, int64(4), sc.InFlight())
			assert.Equal(t, PriorityHigh, sc.Priority())
			return 1, nil
		})
	require.NoError(t, err)
}

// --- Admission matrix ---

// admits is evaluated against the post-reservation in-flight count (inclusive
// of the candidate), so a candidate joining with `inflight` total includes
// itself. With cap=100, threshold=0.8: load=inflight/100, overload=(load-0.8)/0.2.
func TestAdmits_ProgressiveByPriority(t *testing.T) {
	const capacity = 100
	s := New(WithCapacity(capacity), WithThreshold(0.8))
	defer func() { require.NoError(t, s.Close()) }()

	tests := []struct {
		name     string
		inflight int64
		priority Priority
		want     bool
	}{
		{"below threshold low admitted", 70, PriorityLow, true},
		{"at threshold low admitted (overload 0)", 80, PriorityLow, true},
		{"mild overload low shed", 90, PriorityLow, false},        // overload 0.5 >= 0.25
		{"mild overload normal admit", 88, PriorityNormal, true},  // overload 0.4 < 0.6
		{"high overload normal shed", 95, PriorityNormal, false},  // overload 0.75 >= 0.6
		{"high overload high admit", 95, PriorityHigh, true},      // overload 0.75 < 0.9
		{"full load high shed", 100, PriorityHigh, false},         // overload 1.0 >= 0.9
		{"full load critical admit", 100, PriorityCritical, true}, // never shed
		{"over capacity critical admit", 120, PriorityCritical, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, s.admits(tt.priority, tt.inflight))
		})
	}
}

func TestAdmits_ThresholdAtCeil(t *testing.T) {
	s := New(WithCapacity(100), WithThreshold(1.0))
	defer func() { require.NoError(t, s.Close()) }()

	assert.True(t, s.admits(PriorityLow, 99), "below full capacity")
	assert.False(t, s.admits(PriorityLow, 100), "at capacity non-critical shed")
	assert.False(t, s.admits(PriorityHigh, 100))
	assert.True(t, s.admits(PriorityCritical, 100))
}

func TestAdmits_UnknownPriorityTreatedAsHigh(t *testing.T) {
	s := New(WithCapacity(100), WithThreshold(0.8))
	defer func() { require.NoError(t, s.Close()) }()

	const inflight int64 = 100 // overload 1.0 — high is shed, critical is not
	assert.False(t, s.admits(PriorityHigh, inflight))
	assert.False(t, s.admits(Priority(50), inflight), "unknown priority must use high cutoff, not critical bypass")
	assert.True(t, s.admits(PriorityCritical, inflight))
}

func TestAllow_MatchesAdmission(t *testing.T) {
	s := New(WithCapacity(10), WithThreshold(0.5))
	tokens := fill(t, s, PriorityCritical, 10)
	defer release(tokens)

	assert.False(t, s.Allow(PriorityLow))
	assert.True(t, s.Allow(PriorityCritical))

	require.NoError(t, s.Close())
	assert.False(t, s.Allow(PriorityCritical), "closed shedder allows nothing")
}

func TestAllow_DoesNotConsumeSlot(t *testing.T) {
	s := New(WithCapacity(10))
	defer func() { require.NoError(t, s.Close()) }()

	for range 100 {
		s.Allow(PriorityNormal)
	}
	assert.Equal(t, int64(0), s.InFlight(), "Allow must not reserve a slot")
}

// --- Acquire / Token ---

func TestLoad_ReflectsInFlight(t *testing.T) {
	s := New(WithCapacity(10))
	defer func() { require.NoError(t, s.Close()) }()

	assert.Equal(t, 0.0, s.Load())
	tokens := fill(t, s, PriorityCritical, 3)
	defer release(tokens)
	assert.InEpsilon(t, 0.3, s.Load(), 1e-9)
}

func TestAcquire_TracksInFlight(t *testing.T) {
	s := New(WithCapacity(10))
	defer func() { require.NoError(t, s.Close()) }()

	tok, err := s.Acquire(PriorityNormal)
	require.NoError(t, err)
	assert.Equal(t, int64(1), s.InFlight())

	tok.Release()
	assert.Equal(t, int64(0), s.InFlight())
}

func TestToken_ReleaseIsIdempotent(t *testing.T) {
	s := New(WithCapacity(10))
	defer func() { require.NoError(t, s.Close()) }()

	tok, err := s.Acquire(PriorityNormal)
	require.NoError(t, err)
	tok.Release()
	tok.Release() // must not drive inflight negative
	assert.Equal(t, int64(0), s.InFlight())
}

func TestToken_NilReleaseIsNoop(t *testing.T) {
	var tok *Token
	assert.NotPanics(t, tok.Release)
}

func TestAcquire_ReturnsErrClosedAfterClose(t *testing.T) {
	s := New()
	require.NoError(t, s.Close())
	_, err := s.Acquire(PriorityCritical)
	require.ErrorIs(t, err, ErrClosed)
	assert.Equal(t, int64(0), s.Stats().Shed, "closed admission must not count as shed")
}

func TestExecute_ReturnsErrClosedWhenClosedDuringReserve(t *testing.T) {
	s := New(WithCapacity(1000), WithThreshold(0.9))
	defer func() { require.NoError(t, s.Close()) }()

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 64)

	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := Execute(s, context.Background(), PriorityNormal,
				func(context.Context, ShedController) (int, error) { return 1, nil })
			errs <- err
		}()
	}

	require.NoError(t, s.Close())
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		require.ErrorIs(t, err, ErrClosed)
	}
	assert.Equal(t, int64(0), s.Stats().Shed)
}

func TestAcquire_RejectsWhenOverloaded(t *testing.T) {
	s := New(WithCapacity(4), WithThreshold(0.5))
	defer func() { require.NoError(t, s.Close()) }()

	tokens := fill(t, s, PriorityCritical, 4)
	defer release(tokens)

	_, err := s.Acquire(PriorityLow)
	require.ErrorIs(t, err, ErrRejected)
}

// --- Stats & lifecycle ---

func TestStats_Snapshot(t *testing.T) {
	s := New(WithCapacity(100), WithThreshold(0.7))
	defer func() { require.NoError(t, s.Close()) }()

	tokens := fill(t, s, PriorityCritical, 3)
	defer release(tokens)

	st := s.Stats()
	assert.Equal(t, 100, st.Capacity)
	assert.InEpsilon(t, 0.7, st.Threshold, 1e-9)
	assert.Equal(t, int64(3), st.InFlight)
	assert.Equal(t, int64(3), st.Admitted)
}

func TestResetStats_ZeroesCounters(t *testing.T) {
	s := New(WithCapacity(2), WithThreshold(0.5))
	defer func() { require.NoError(t, s.Close()) }()

	tokens := fill(t, s, PriorityCritical, 2)
	_, _ = s.Acquire(PriorityLow) // rejected → shed++
	_, err := Execute(s, context.Background(), PriorityCritical,
		func(_ context.Context, sc ShedController) (int, error) { sc.Shed(); return 1, nil })
	require.NoError(t, err)

	s.ResetStats()
	st := s.Stats()
	assert.Equal(t, int64(0), st.Admitted)
	assert.Equal(t, int64(0), st.Shed)
	assert.Equal(t, int64(0), st.Degraded)
	release(tokens)
}

func TestClose_Idempotent(t *testing.T) {
	s := New()
	testx.AssertCloseIdempotent(t, s)
	assert.True(t, s.IsClosed())
}

func TestCommitReservation_RejectsAfterClose(t *testing.T) {
	s := New(WithCapacity(10))
	s.inflight.Store(3)
	require.NoError(t, s.Close())

	n, ok, closed := s.commitReservation(3)
	require.False(t, ok)
	require.True(t, closed)
	assert.Equal(t, int64(0), n)
	assert.Equal(t, int64(2), s.InFlight(), "rolled-back reservation must decrement inflight")
}

func TestCommitReservation_AdmitsWhenOpen(t *testing.T) {
	s := New(WithCapacity(10))
	defer func() { require.NoError(t, s.Close()) }()

	s.inflight.Store(2)
	n, ok, closed := s.commitReservation(3)
	require.True(t, ok)
	require.False(t, closed)
	assert.Equal(t, int64(3), n)
	assert.Equal(t, int64(2), s.InFlight())
}

func TestAcquire_ReturnsErrClosedWhenClosedDuringReserve(t *testing.T) {
	s := New(WithCapacity(1000), WithThreshold(0.9))
	defer func() { require.NoError(t, s.Close()) }()

	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 64)

	for range 64 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := s.Acquire(PriorityNormal)
			errs <- err
		}()
	}

	require.NoError(t, s.Close())
	close(start)
	wg.Wait()
	close(errs)

	for err := range errs {
		require.ErrorIs(t, err, ErrClosed)
	}
	assert.Equal(t, int64(0), s.Stats().Shed)
}

// --- Concurrency ---

func TestExecute_RaceSafe(t *testing.T) {
	s := New(WithCapacity(1000), WithThreshold(0.9))
	defer func() { require.NoError(t, s.Close()) }()

	testx.HammerVoid(64, 500, func() {
		_, _ = Execute(s, context.Background(), PriorityNormal,
			func(_ context.Context, sc ShedController) (int, error) {
				if sc.Load() > 0.95 {
					sc.Shed()
				}
				return 1, nil
			})
		_, _, _ = TryExecute(s, context.Background(), PriorityLow,
			func(context.Context, ShedController) (int, error) { return 1, nil })
	})
	assert.GreaterOrEqual(t, s.InFlight(), int64(0))
}

func TestAcquire_RaceSafe(t *testing.T) {
	s := New(WithCapacity(100), WithThreshold(0.5))
	defer func() { require.NoError(t, s.Close()) }()

	testx.HammerVoid(32, 1000, func() {
		tok, err := s.Acquire(PriorityNormal)
		if err != nil {
			return
		}
		tok.Release()
	})
	assert.Equal(t, int64(0), s.InFlight())
}

// TestExecute_EnforcesCapacityUnderLoad is the contract that justifies the
// reserve-then-check admission: with many goroutines racing for a tiny
// capacity at a sheddable priority, the observed in-flight count must never
// exceed capacity. A non-atomic check-then-increment would overshoot here.
func TestExecute_EnforcesCapacityUnderLoad(t *testing.T) {
	const capacity = 8
	s := New(WithCapacity(capacity), WithThreshold(0.5))
	defer func() { require.NoError(t, s.Close()) }()

	var maxSeen atomic.Int64
	gate := make(chan struct{})

	testx.HammerVoid(64, 200, func() {
		_, _ = Execute(s, context.Background(), PriorityHigh,
			func(context.Context, ShedController) (int, error) {
				cur := s.InFlight()
				for {
					prev := maxSeen.Load()
					if cur <= prev || maxSeen.CompareAndSwap(prev, cur) {
						break
					}
				}
				select {
				case <-gate:
				default:
				}
				return 1, nil
			})
	})
	close(gate)
	assert.LessOrEqual(t, maxSeen.Load(), int64(capacity),
		"in-flight (%d) exceeded capacity (%d)", maxSeen.Load(), capacity)
	assert.Equal(t, int64(0), s.InFlight())
}

// TestAcquire_NeverExceedsCapacity holds every admitted token simultaneously
// to prove the hard ceiling: no acquire sequence can hold more than capacity
// non-critical tokens at once.
func TestAcquire_NeverExceedsCapacity(t *testing.T) {
	const capacity = 16
	s := New(WithCapacity(capacity), WithThreshold(0.5))
	defer func() { require.NoError(t, s.Close()) }()

	var (
		mu        sync.Mutex
		held      []*Token
		admitted  atomic.Int64
		maxInFlit atomic.Int64
	)
	testx.HammerVoid(32, 50, func() {
		tok, err := s.Acquire(PriorityHigh)
		if err != nil {
			return
		}
		admitted.Add(1)
		n := s.InFlight()
		for {
			prev := maxInFlit.Load()
			if n <= prev || maxInFlit.CompareAndSwap(prev, n) {
				break
			}
		}
		mu.Lock()
		held = append(held, tok)
		mu.Unlock()
	})

	assert.LessOrEqual(t, maxInFlit.Load(), int64(capacity))
	assert.Equal(t, admitted.Load(), s.InFlight(), "every admitted token still held")
	release(held)
	assert.Equal(t, int64(0), s.InFlight())
}
