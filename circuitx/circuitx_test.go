package circuitx

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

var errBoom = errors.New("boom")

// fail returns a CircuitFunc that always errors with errBoom.
func fail[T any]() CircuitFunc[T] {
	return func(context.Context, CircuitController) (T, error) {
		var zero T
		return zero, errBoom
	}
}

// ok returns a CircuitFunc that returns v with no error.
func ok[T any](v T) CircuitFunc[T] {
	return func(context.Context, CircuitController) (T, error) {
		return v, nil
	}
}

// --- State ---

func TestState_String(t *testing.T) {
	tests := []struct {
		name  string
		state State
		want  string
	}{
		{"closed", Closed, labelClosed},
		{"open", Open, labelOpen},
		{"half_open", HalfOpen, labelHalfOpen},
		{"unknown", State(99), labelUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.state.String())
		})
	}
}

// --- New / defaults ---

func TestNew_Defaults(t *testing.T) {
	b := New()
	assert.Equal(t, DefaultMaxFailures, b.cfg.maxFailures)
	assert.Equal(t, DefaultResetTimeout, b.cfg.resetTimeout)
	assert.Equal(t, DefaultHalfOpenMax, b.cfg.halfOpenMax)
	assert.Equal(t, Closed, b.State())
	assert.Equal(t, 0, b.Failures())
}

func TestNew_NilOptionIgnored(t *testing.T) {
	opts := []Option{nil, WithMaxFailures(3), nil}
	b := New(opts...)
	assert.Equal(t, 3, b.cfg.maxFailures)
}

// --- Options ---

func TestWithMaxFailures(t *testing.T) {
	tests := []struct {
		name string
		n    int
		want int
	}{
		{"default", 0, DefaultMaxFailures},
		{"custom", 7, 7},
		{"negative floored", -3, DefaultMaxFailures},
		{"one", 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b *Breaker
			if tt.n == 0 {
				b = New()
			} else {
				b = New(WithMaxFailures(tt.n))
			}
			assert.Equal(t, tt.want, b.cfg.maxFailures)
		})
	}
}

func TestWithResetTimeout(t *testing.T) {
	assert.Equal(t, 2*time.Second, New(WithResetTimeout(2*time.Second)).cfg.resetTimeout)
	assert.Equal(t, DefaultResetTimeout, New(WithResetTimeout(0)).cfg.resetTimeout)
	assert.Equal(t, DefaultResetTimeout, New(WithResetTimeout(-time.Second)).cfg.resetTimeout)
}

func TestWithHalfOpenMax(t *testing.T) {
	assert.Equal(t, 3, New(WithHalfOpenMax(3)).cfg.halfOpenMax)
	assert.Equal(t, DefaultHalfOpenMax, New(WithHalfOpenMax(0)).cfg.halfOpenMax)
	assert.Equal(t, DefaultHalfOpenMax, New(WithHalfOpenMax(-1)).cfg.halfOpenMax)
}

func TestWithOp(t *testing.T) {
	assert.Equal(t, "api.charge", New(WithOp("api.charge")).cfg.opOrDefault())
	assert.Equal(t, opExecute, New(WithOp("")).cfg.opOrDefault())
	assert.Equal(t, opExecute, New().cfg.opOrDefault())
}

func TestWithOp_TryDefault(t *testing.T) {
	assert.Equal(t, opTryExecute, New().cfg.opOrDefaultTry())
	assert.Equal(t, "api.charge", New(WithOp("api.charge")).cfg.opOrDefaultTry())
	assert.Equal(t, opTryExecute, New(WithOp("")).cfg.opOrDefaultTry())
}

// TestNewConfig_FloorsInvalidValues exercises the defensive floors in newConfig
// directly. The public WithXxx options already reject invalid values, so these
// floors are a second belt reachable only by an option that writes a raw
// invalid value into the config.
func TestNewConfig_FloorsInvalidValues(t *testing.T) {
	bad := func(c *config) {
		c.maxFailures = -5
		c.resetTimeout = -time.Second
		c.halfOpenMax = 0
	}
	cfg := newConfig([]Option{bad})
	assert.Equal(t, minMaxFailures, cfg.maxFailures)
	assert.Equal(t, DefaultResetTimeout, cfg.resetTimeout)
	assert.Equal(t, minHalfOpenMax, cfg.halfOpenMax)
}

// --- Closed-state behavior ---

func TestExecute_ClosedSuccess(t *testing.T) {
	b := New()
	got, err := Execute(b, context.Background(), ok(42))
	require.NoError(t, err)
	assert.Equal(t, 42, got)
	assert.Equal(t, Closed, b.State())
	assert.Equal(t, uint64(1), b.Stats().Successes)
}

func TestExecute_FailuresTripToOpen(t *testing.T) {
	b := New(WithMaxFailures(3))
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		_, err := Execute(b, ctx, fail[int]())
		require.ErrorIs(t, err, errBoom)
		assert.Equal(t, Closed, b.State(), "still closed before threshold")
	}

	_, err := Execute(b, ctx, fail[int]())
	require.ErrorIs(t, err, errBoom)
	assert.Equal(t, Open, b.State(), "tripped at threshold")
	assert.Equal(t, uint64(1), b.Stats().Trips)
	assert.Equal(t, uint64(3), b.Stats().TotalFail)
}

func TestExecute_SuccessResetsFailureCount(t *testing.T) {
	b := New(WithMaxFailures(3))
	ctx := context.Background()

	_, _ = Execute(b, ctx, fail[int]())
	_, _ = Execute(b, ctx, fail[int]())
	assert.Equal(t, 2, b.Failures())

	_, err := Execute(b, ctx, ok(1))
	require.NoError(t, err)
	assert.Equal(t, 0, b.Failures(), "success clears consecutive count")
	assert.Equal(t, Closed, b.State())
}

// --- Open-state behavior ---

func TestExecute_OpenRejectsImmediately(t *testing.T) {
	b := New(WithMaxFailures(1))
	ctx := context.Background()

	_, _ = Execute(b, ctx, fail[int]())
	require.Equal(t, Open, b.State())

	called := false
	_, err := Execute(b, ctx, func(context.Context, CircuitController) (int, error) {
		called = true
		return 0, nil
	})
	require.ErrorIs(t, err, ErrOpen)
	assert.False(t, called, "fn must not run while Open")
	assert.Equal(t, uint64(1), b.Stats().Rejected)
}

// --- HalfOpen transitions ---

func TestExecute_HalfOpenProbeSuccessCloses(t *testing.T) {
	b := New(WithMaxFailures(1), WithResetTimeout(20*time.Millisecond))
	ctx := context.Background()

	_, _ = Execute(b, ctx, fail[int]())
	require.Equal(t, Open, b.State())

	testx.Eventually(t, func() bool { return b.State() == HalfOpen }, time.Second)

	got, err := Execute(b, ctx, ok(99))
	require.NoError(t, err)
	assert.Equal(t, 99, got)
	assert.Equal(t, Closed, b.State(), "probe success closes the circuit")
	assert.Equal(t, 0, b.Failures())
}

func TestExecute_HalfOpenProbeFailureReopens(t *testing.T) {
	b := New(WithMaxFailures(3), WithResetTimeout(20*time.Millisecond))
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_, _ = Execute(b, ctx, fail[int]())
	}
	require.Equal(t, Open, b.State())

	testx.Eventually(t, func() bool { return b.State() == HalfOpen }, time.Second)

	_, err := Execute(b, ctx, fail[int]())
	require.ErrorIs(t, err, errBoom)
	assert.Equal(t, Open, b.State(), "a single probe failure re-opens immediately")
	assert.Equal(t, uint64(2), b.Stats().Trips)
}

func TestExecute_HalfOpenBudgetRejectsExtraProbes(t *testing.T) {
	b := New(WithMaxFailures(1), WithResetTimeout(20*time.Millisecond), WithHalfOpenMax(1))
	ctx := context.Background()

	_, _ = Execute(b, ctx, fail[int]())
	testx.Eventually(t, func() bool { return b.State() == HalfOpen }, time.Second)

	release := make(chan struct{})
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := Execute(b, ctx, func(context.Context, CircuitController) (int, error) {
			close(entered)
			<-release
			return 1, nil
		})
		done <- err
	}()

	<-entered // first probe is now occupying the single slot
	_, err := Execute(b, ctx, ok(2))
	require.ErrorIs(t, err, ErrOpen, "second concurrent probe is rejected")

	close(release)
	require.NoError(t, <-done)
	assert.Equal(t, Closed, b.State())
}

func TestExecute_HalfOpenMultiProbeBudget(t *testing.T) {
	b := New(WithMaxFailures(1), WithResetTimeout(20*time.Millisecond), WithHalfOpenMax(2))
	ctx := context.Background()

	_, _ = Execute(b, ctx, fail[int]())
	testx.Eventually(t, func() bool { return b.State() == HalfOpen }, time.Second)

	var inflight atomic.Int32
	var maxSeen atomic.Int32
	release := make(chan struct{})
	ready := make(chan struct{}, 2)

	probe := func() {
		_, _ = Execute(b, ctx, func(context.Context, CircuitController) (int, error) {
			n := inflight.Add(1)
			for {
				m := maxSeen.Load()
				if n <= m || maxSeen.CompareAndSwap(m, n) {
					break
				}
			}
			ready <- struct{}{}
			<-release
			inflight.Add(-1)
			return 1, nil
		})
	}
	go probe()
	go probe()
	<-ready
	<-ready
	close(release)

	assert.Equal(t, int32(2), maxSeen.Load(), "both probe slots admitted concurrently")
}

// --- Controller: SkipFailure ---

func TestExecute_SkipFailureNotCounted(t *testing.T) {
	b := New(WithMaxFailures(2))
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		_, err := Execute(b, ctx, func(_ context.Context, cc CircuitController) (int, error) {
			cc.SkipFailure()
			return 0, errBoom
		})
		require.ErrorIs(t, err, errBoom, "the business error still reaches the caller")
	}
	assert.Equal(t, Closed, b.State(), "skipped failures never trip the breaker")
	assert.Equal(t, 0, b.Failures())
	assert.Equal(t, uint64(0), b.Stats().TotalFail)
}

func TestExecute_SkipFailureReturnsValue(t *testing.T) {
	b := New(WithMaxFailures(2))
	ctx := context.Background()

	got, err := Execute(b, ctx, func(_ context.Context, cc CircuitController) (int, error) {
		cc.SkipFailure()
		return 42, errBoom
	})
	require.ErrorIs(t, err, errBoom)
	assert.Equal(t, 42, got)
	assert.Equal(t, Closed, b.State())
	assert.Equal(t, uint64(0), b.Stats().TotalFail)
}

// --- Controller: Trip ---

func TestExecute_TripForcesOpenOnSuccess(t *testing.T) {
	b := New(WithMaxFailures(100))
	ctx := context.Background()

	got, err := Execute(b, ctx, func(_ context.Context, cc CircuitController) (int, error) {
		cc.Trip()
		return 7, nil
	})
	require.NoError(t, err, "Trip does not synthesize an error")
	assert.Equal(t, 7, got)
	assert.Equal(t, Open, b.State(), "Trip forces the breaker open")
	assert.Equal(t, uint64(1), b.Stats().Trips)
}

func TestExecute_TripWithSkippedFailureStillOpens(t *testing.T) {
	b := New(WithMaxFailures(100))
	ctx := context.Background()

	_, err := Execute(b, ctx, func(_ context.Context, cc CircuitController) (int, error) {
		cc.SkipFailure()
		cc.Trip()
		return 0, errBoom
	})
	require.ErrorIs(t, err, errBoom)
	assert.Equal(t, Open, b.State())
	assert.Equal(t, uint64(0), b.Stats().TotalFail, "SkipFailure keeps it out of the count")
	assert.Equal(t, uint64(1), b.Stats().Trips)
}

// --- Controller: admission snapshot ---

func TestExecute_ControllerSnapshot(t *testing.T) {
	b := New(WithMaxFailures(5))
	ctx := context.Background()

	_, _ = Execute(b, ctx, fail[int]())
	_, _ = Execute(b, ctx, fail[int]())

	_, err := Execute(b, ctx, func(_ context.Context, cc CircuitController) (int, error) {
		assert.Equal(t, Closed, cc.State())
		assert.Equal(t, 2, cc.Failures())
		assert.Equal(t, 5, cc.MaxFailures())
		return 0, nil
	})
	require.NoError(t, err)
}

// --- Panic handling ---

func TestExecute_PanicBecomesCountedFailure(t *testing.T) {
	b := New(WithMaxFailures(1), WithOp("test.op"))
	ctx := context.Background()

	_, err := Execute(b, ctx, func(context.Context, CircuitController) (int, error) {
		panic("kaboom")
	})
	require.Error(t, err)
	var pe *panix.PanicError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, "test.op", pe.Op)
	assert.Equal(t, Open, b.State(), "a panic counts as a failure and trips the breaker")
}

// --- TryExecute ---

func TestTryExecute_RunsWhenClosed(t *testing.T) {
	b := New()
	ok, got, err := TryExecute(b, context.Background(), ok(42))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 42, got)
}

func TestTryExecute_RejectsWhenOpen(t *testing.T) {
	b := New(WithMaxFailures(1))
	ctx := context.Background()

	_, _ = Execute(b, ctx, fail[int]())
	require.Equal(t, Open, b.State())

	called := false
	ok, _, err := TryExecute(b, ctx, func(context.Context, CircuitController) (int, error) {
		called = true
		return 0, nil
	})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.False(t, called, "fn must not run while Open")
	assert.Equal(t, uint64(1), b.Stats().Rejected)
}

func TestTryExecute_HalfOpenBudgetRejectsExtraProbes(t *testing.T) {
	b := New(WithMaxFailures(1), WithResetTimeout(20*time.Millisecond), WithHalfOpenMax(1))
	ctx := context.Background()

	_, _ = Execute(b, ctx, fail[int]())
	testx.Eventually(t, func() bool { return b.State() == HalfOpen }, time.Second)

	release := make(chan struct{})
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := Execute(b, ctx, func(context.Context, CircuitController) (int, error) {
			close(entered)
			<-release
			return 1, nil
		})
		done <- err
	}()

	<-entered
	ok, _, err := TryExecute(b, ctx, ok(2))
	require.NoError(t, err)
	assert.False(t, ok, "second concurrent probe is rejected")

	close(release)
	require.NoError(t, <-done)
}

func TestTryExecute_NilFunc(t *testing.T) {
	b := New()
	ok, _, err := TryExecute[int](b, context.Background(), nil)
	require.ErrorIs(t, err, ErrNilFunc)
	assert.False(t, ok)
}

func TestTryExecute_Cancelled(t *testing.T) {
	b := New()
	ok, _, err := TryExecute(b, testx.CancelledCtx(), ok(1))
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, ok)
}

func TestTryExecute_ReturnsErrCancelledOnExpiredDeadline(t *testing.T) {
	b := New()
	ok, _, err := TryExecute(b, testx.ExpiredCtx(), ok(1))
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, ok)
}

func TestTryExecute_PropagatesError(t *testing.T) {
	b := New()
	ok, _, err := TryExecute(b, context.Background(), fail[int]())
	require.True(t, ok)
	require.ErrorIs(t, err, errBoom)
}

func TestTryExecute_SkipFailureNotCounted(t *testing.T) {
	b := New(WithMaxFailures(2))
	ctx := context.Background()

	for range 5 {
		ok, _, err := TryExecute(b, ctx, func(_ context.Context, cc CircuitController) (int, error) {
			cc.SkipFailure()
			return 0, errBoom
		})
		require.True(t, ok)
		require.ErrorIs(t, err, errBoom)
	}
	assert.Equal(t, Closed, b.State())
	assert.Equal(t, uint64(0), b.Stats().TotalFail)
}

func TestTryExecute_TripForcesOpenOnSuccess(t *testing.T) {
	b := New(WithMaxFailures(100))
	ctx := context.Background()

	ok, got, err := TryExecute(b, ctx, func(_ context.Context, cc CircuitController) (int, error) {
		cc.Trip()
		return 7, nil
	})
	require.True(t, ok)
	require.NoError(t, err)
	assert.Equal(t, 7, got)
	assert.Equal(t, Open, b.State())
}

func TestTryExecute_AfterClose(t *testing.T) {
	b := New()
	require.NoError(t, b.Close())

	called := false
	ok, _, err := TryExecute(b, context.Background(), func(context.Context, CircuitController) (int, error) {
		called = true
		return 0, nil
	})
	require.ErrorIs(t, err, ErrClosed)
	assert.False(t, ok)
	assert.False(t, called)
}

func TestTryExecute_PanicBecomesCountedFailure(t *testing.T) {
	b := New(WithMaxFailures(1), WithOp("test.try"))
	ctx := context.Background()

	ok, _, err := TryExecute(b, ctx, func(context.Context, CircuitController) (int, error) {
		panic("kaboom")
	})
	require.True(t, ok, "fn ran before panicking")
	testx.RequirePanicError(t, err, "test.try")
	assert.Equal(t, Open, b.State())
}

func TestTryExecute_DefaultOpPanicLabel(t *testing.T) {
	b := New(WithMaxFailures(1))
	ok, _, err := TryExecute(b, context.Background(), func(context.Context, CircuitController) (int, error) {
		panic("kaboom")
	})
	require.True(t, ok)
	testx.RequirePanicError(t, err, opTryExecute)
}

// --- Guard clauses ---

func TestExecute_NilFunc(t *testing.T) {
	b := New()
	_, err := Execute[int](b, context.Background(), nil)
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestExecute_Cancelled(t *testing.T) {
	b := New()
	_, err := Execute(b, testx.CancelledCtx(), ok(1))
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
}

func TestExecute_ExpiredDeadline(t *testing.T) {
	b := New()
	_, err := Execute(b, testx.ExpiredCtx(), ok(1))
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestExecute_AfterClose(t *testing.T) {
	b := New()
	require.NoError(t, b.Close())
	assert.True(t, b.IsClosed())

	called := false
	_, err := Execute(b, context.Background(), func(context.Context, CircuitController) (int, error) {
		called = true
		return 0, nil
	})
	require.ErrorIs(t, err, ErrClosed)
	assert.False(t, called)
}

func TestClose_Idempotent(t *testing.T) {
	b := New()
	require.NoError(t, b.Close())
	require.NoError(t, b.Close())
	assert.True(t, b.IsClosed())
}

// --- Reset ---

func TestReset_ForcesClosed(t *testing.T) {
	b := New(WithMaxFailures(1))
	ctx := context.Background()
	_, _ = Execute(b, ctx, fail[int]())
	require.Equal(t, Open, b.State())

	b.Reset()
	assert.Equal(t, Closed, b.State())
	assert.Equal(t, 0, b.Failures())

	got, err := Execute(b, ctx, ok(5))
	require.NoError(t, err)
	assert.Equal(t, 5, got)
}

func TestReset_NoopWhenAlreadyClosed(t *testing.T) {
	var changes int
	b := New(WithOnStateChange(func(State, State) { changes++ }))
	b.Reset()
	assert.Equal(t, 0, changes, "no transition fired when already closed")
}

// TestOpenFrom_IdempotentWhenAlreadyOpen covers the early-return branch in
// openFrom: a second trip from an already-Open circuit must not double-count or
// re-fire the hook.
func TestOpenFrom_IdempotentWhenAlreadyOpen(t *testing.T) {
	var edges int
	b := New(WithOnStateChange(func(State, State) { edges++ }))
	b.openFrom(Closed)
	require.Equal(t, Open, b.State())
	require.Equal(t, 1, edges)

	b.openFrom(Closed) // already Open
	assert.Equal(t, Open, b.State())
	assert.Equal(t, 1, edges, "no second hook fire")
	assert.Equal(t, uint64(1), b.Stats().Trips, "no second trip counted")
}

// TestOpenFrom_ReconcilesAdmissionState covers the prev != state branch: when
// the live state has already advanced past the admission state, the reported
// from-edge uses the live state.
func TestOpenFrom_ReconcilesAdmissionState(t *testing.T) {
	var from State
	b := New(WithOnStateChange(func(f, _ State) { from = f }))
	// Live state is HalfOpen but the call was admitted in Closed: openFrom must
	// report the live HalfOpen as the from-edge.
	b.state.Store(uint32(HalfOpen))
	b.openFrom(Closed)
	assert.Equal(t, HalfOpen, from)
}

// TestRecordSuccess_ClosedClearsFailures covers the Closed branch of
// recordSuccess: a success forgives accumulated failures without a transition.
func TestRecordSuccess_ClosedClearsFailures(t *testing.T) {
	var edges int
	b := New(WithOnStateChange(func(State, State) { edges++ }))
	b.failures.Store(3)
	b.recordSuccess()
	assert.Equal(t, 0, b.Failures())
	assert.Equal(t, Closed, b.State())
	assert.Equal(t, 0, edges, "no transition fired in Closed")
}

// TestRecordSuccess_HalfOpenHeals covers the HalfOpen branch of recordSuccess.
func TestRecordSuccess_HalfOpenHeals(t *testing.T) {
	var from, to State
	b := New(WithOnStateChange(func(f, t State) { from, to = f, t }))
	b.state.Store(uint32(HalfOpen))
	b.failures.Store(2)
	b.recordSuccess()
	assert.Equal(t, Closed, b.State())
	assert.Equal(t, 0, b.Failures())
	assert.Equal(t, HalfOpen, from)
	assert.Equal(t, Closed, to)
}

// TestRecordSuccess_DoesNotClobberConcurrentTrip covers the critical invariant:
// a success observed while the live state is Open must leave the breaker open.
func TestRecordSuccess_DoesNotClobberConcurrentTrip(t *testing.T) {
	var edges int
	b := New(WithOnStateChange(func(State, State) { edges++ }))
	// Simulate a fresh trip by a concurrent failure: Open with a recent
	// timestamp so the lazy promotion in State() does not fire.
	b.state.Store(uint32(Open))
	b.lastOpen.Store(time.Now().UnixNano())
	b.recordSuccess()
	assert.Equal(t, Open, State(b.state.Load()), "success must not re-close a tripped breaker")
	assert.Equal(t, 0, edges)
}

// TestState_ConcurrentPromotionLosesCAS drives many goroutines to read an
// expired-Open breaker at once. Exactly one wins the Open->HalfOpen CAS; the
// rest take the lost-CAS path and must still report the live HalfOpen state.
func TestState_ConcurrentPromotionLosesCAS(t *testing.T) {
	b := New(WithResetTimeout(time.Millisecond))

	// Repeatedly re-arm the breaker to expired-Open and have two goroutines race
	// to read it. Over many rounds the loser of the Open->HalfOpen CAS is
	// virtually certain to execute, and must always report the live HalfOpen.
	const rounds = 2000
	for range rounds {
		b.state.Store(uint32(Open))
		b.lastOpen.Store(time.Now().Add(-time.Hour).UnixNano())

		var got [2]State
		start := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(2)
		for i := range 2 {
			go func(idx int) {
				defer wg.Done()
				<-start
				got[idx] = b.State()
			}(i)
		}
		close(start)
		wg.Wait()

		require.Equal(t, HalfOpen, got[0])
		require.Equal(t, HalfOpen, got[1])
	}
}

// --- Stats ---

func TestStats_Snapshot(t *testing.T) {
	b := New(WithMaxFailures(2))
	ctx := context.Background()
	_, _ = Execute(b, ctx, ok(1))
	_, _ = Execute(b, ctx, fail[int]())
	_, _ = Execute(b, ctx, fail[int]()) // trips
	_, _ = Execute(b, ctx, ok(1))       // rejected (Open)

	s := b.Stats()
	assert.Equal(t, Open, s.State)
	assert.Equal(t, 2, s.MaxFailures)
	assert.Equal(t, uint64(1), s.Successes)
	assert.Equal(t, uint64(2), s.TotalFail)
	assert.Equal(t, uint64(1), s.Rejected)
	assert.Equal(t, uint64(1), s.Trips)
}

func TestResetStats_ZeroesCountersKeepsState(t *testing.T) {
	b := New(WithMaxFailures(1))
	ctx := context.Background()
	_, _ = Execute(b, ctx, fail[int]())
	require.Equal(t, Open, b.State())

	b.ResetStats()
	s := b.Stats()
	assert.Equal(t, uint64(0), s.Successes)
	assert.Equal(t, uint64(0), s.TotalFail)
	assert.Equal(t, uint64(0), s.Rejected)
	assert.Equal(t, uint64(0), s.Trips)
	assert.Equal(t, Open, s.State, "state is untouched by ResetStats")
}

// --- OnStateChange hook ---

func TestOnStateChange_FullCycle(t *testing.T) {
	type edge struct{ from, to State }
	var edges []edge
	b := New(
		WithMaxFailures(1),
		WithResetTimeout(20*time.Millisecond),
		WithOnStateChange(func(from, to State) { edges = append(edges, edge{from, to}) }),
	)
	ctx := context.Background()

	_, _ = Execute(b, ctx, fail[int]()) // Closed -> Open
	testx.Eventually(t, func() bool { return b.State() == HalfOpen }, time.Second)
	_, err := Execute(b, ctx, ok(1)) // HalfOpen -> Closed
	require.NoError(t, err)

	require.Len(t, edges, 3)
	assert.Equal(t, edge{Closed, Open}, edges[0])
	assert.Equal(t, edge{Open, HalfOpen}, edges[1])
	assert.Equal(t, edge{HalfOpen, Closed}, edges[2])
}

// --- Concurrency ---

func TestExecute_RaceSafe(t *testing.T) {
	b := New(WithMaxFailures(10), WithResetTimeout(time.Millisecond))
	ctx := context.Background()
	sim := testx.Pattern("SSFSFFSSF")

	testx.HammerVoid(50, 200, func() {
		_, _ = Execute(b, ctx, func(context.Context, CircuitController) (int, error) {
			return 1, sim.Call()
		})
		_, _, _ = TryExecute(b, ctx, func(context.Context, CircuitController) (int, error) {
			return 1, sim.Call()
		})
	})
	// The breaker must remain in a coherent state and counters must add up.
	s := b.Stats()
	assert.Equal(t, uint64(50*200*2), s.Successes+s.TotalFail+s.Rejected)
}

// TestExecute_SuccessNeverUnTripsUnderRace hammers the breaker with a heavy
// failure mix while interleaving successes, then asserts that once it is Open
// it is never silently re-closed by a racing success. The breaker uses a long
// reset timeout so the only path back to Closed would be the (buggy) success
// clobber; with the fix in place it must stay Open after tripping.
func TestExecute_SuccessNeverUnTripsUnderRace(t *testing.T) {
	b := New(WithMaxFailures(3), WithResetTimeout(time.Hour))
	ctx := context.Background()

	var sawOpen atomic.Bool
	// 1 failure for every success keeps tripping likely while successes race.
	sim := testx.Pattern("FS")

	testx.HammerVoid(40, 500, func() {
		_, _ = Execute(b, ctx, func(context.Context, CircuitController) (int, error) {
			return 1, sim.Call()
		})
		if b.State() == Open {
			sawOpen.Store(true)
		}
	})

	require.True(t, sawOpen.Load(), "breaker should have tripped during the storm")
	// With a 1-hour reset timeout the breaker cannot legitimately leave Open, so
	// the final state must be Open — a success clobber would show Closed.
	assert.Equal(t, Open, b.State())
}
