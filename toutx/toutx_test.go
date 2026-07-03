package toutx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aasyanov/urx/internal/testx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Execute: happy path ---

func TestExecute_ReturnsValueWhenFast(t *testing.T) {
	got, err := Execute(context.Background(), time.Second,
		func(context.Context, TimeoutController) (int, error) {
			return 42, nil
		})
	require.NoError(t, err)
	assert.Equal(t, 42, got)
}

func TestExecute_PropagatesFunctionError(t *testing.T) {
	sentinel := errors.New("boom")
	_, err := Execute(context.Background(), time.Second,
		func(context.Context, TimeoutController) (int, error) {
			return 0, sentinel
		})
	require.ErrorIs(t, err, sentinel)
	// A function error is not a toutx error.
	assert.NotErrorIs(t, err, ErrDeadlineExceeded)
	assert.NotErrorIs(t, err, ErrCancelled)
}

// --- Execute: error paths ---

func TestExecute_NilFunc(t *testing.T) {
	_, err := Execute[int](context.Background(), time.Second, nil)
	require.ErrorIs(t, err, ErrNilFunc)
}

func TestExecute_DeadlineExceeded(t *testing.T) {
	sim := testx.SlowCall(50 * time.Millisecond)
	_, err := Execute(context.Background(), 5*time.Millisecond,
		func(ctx context.Context, _ TimeoutController) (int, error) {
			return 0, sim.Call(ctx)
		}, WithOp("slow.op"))
	require.ErrorIs(t, err, ErrDeadlineExceeded)
	assert.ErrorContains(t, err, "slow.op")
	assert.ErrorContains(t, err, "timeout=")
}

func TestExecute_AlreadyCancelledContext(t *testing.T) {
	_, err := Execute(testx.CancelledCtx(), time.Second,
		func(context.Context, TimeoutController) (int, error) {
			return 1, nil
		})
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
}

func TestExecute_ExpiredContext(t *testing.T) {
	_, err := Execute(testx.ExpiredCtx(), time.Second,
		func(context.Context, TimeoutController) (int, error) {
			return 1, nil
		})
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestExecute_ParentCancelledMidFlight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	go func() {
		<-started
		cancel()
	}()

	_, err := Execute(ctx, time.Minute,
		func(ctx context.Context, _ TimeoutController) (int, error) {
			close(started)
			<-ctx.Done()
			return 0, ctx.Err()
		})
	require.ErrorIs(t, err, ErrCancelled)
	require.ErrorIs(t, err, context.Canceled)
}

func TestExecute_RecoversPanic(t *testing.T) {
	_, err := Execute(context.Background(), time.Second,
		func(context.Context, TimeoutController) (int, error) {
			panic("handler exploded")
		}, WithOp("panicky"))
	pe := testx.RequirePanicError(t, err, "panicky")
	assert.Equal(t, "handler exploded", pe.Value)
}

func TestExecute_DeadlineReturnsWhileFnStillRunning(t *testing.T) {
	// The deadline must win immediately even if fn ignores ctx and keeps
	// running; the buffered result channel guarantees the late goroutine never
	// blocks on send and is reclaimed once fn finally returns.
	release := make(chan struct{})
	finished := make(chan struct{})
	_, err := Execute(context.Background(), 5*time.Millisecond,
		func(context.Context, TimeoutController) (int, error) {
			defer close(finished)
			<-release // fn deliberately ignores its context
			return 1, nil
		})
	require.ErrorIs(t, err, ErrDeadlineExceeded)

	close(release)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("fn goroutine did not finish after release")
	}
}

// --- TimeoutController ---

func TestExecute_ControllerExposesBudget(t *testing.T) {
	const budget = 200 * time.Millisecond
	_, err := Execute(context.Background(), budget,
		func(_ context.Context, tc TimeoutController) (int, error) {
			assert.Equal(t, "ctl.op", tc.Op())
			assert.Equal(t, budget, tc.Timeout())
			assert.WithinDuration(t, time.Now().Add(budget), tc.Deadline(), 50*time.Millisecond)
			assert.GreaterOrEqual(t, tc.Elapsed(), time.Duration(0))
			assert.LessOrEqual(t, tc.Remaining(), budget)
			assert.Greater(t, tc.Remaining(), time.Duration(0))
			return 0, nil
		}, WithOp("ctl.op"))
	require.NoError(t, err)
}

func TestExecution_RemainingClampsToZero(t *testing.T) {
	// start far in the past with a tiny timeout => deadline already passed.
	e := &execution{start: time.Now().Add(-time.Hour), timeout: time.Second}
	assert.Equal(t, time.Duration(0), e.Remaining())
}

// --- Options ---

func TestNewConfig_Resolution(t *testing.T) {
	tests := []struct {
		name        string
		timeout     time.Duration
		opts        []Option
		wantTimeout time.Duration
		wantOp      string
	}{
		{name: "defaults", wantTimeout: DefaultTimeout, wantOp: opExecute},
		{name: "positional only", timeout: time.Second, wantTimeout: time.Second, wantOp: opExecute},
		{name: "option only", opts: []Option{WithTimeout(2 * time.Second)}, wantTimeout: 2 * time.Second, wantOp: opExecute},
		{name: "positional wins over option", timeout: 3 * time.Second, opts: []Option{WithTimeout(time.Second)}, wantTimeout: 3 * time.Second, wantOp: opExecute},
		{name: "zero positional ignored", timeout: 0, opts: []Option{WithTimeout(time.Second)}, wantTimeout: time.Second, wantOp: opExecute},
		{name: "negative timeout ignored", opts: []Option{WithTimeout(-5)}, wantTimeout: DefaultTimeout, wantOp: opExecute},
		{name: "op set", opts: []Option{WithOp("x")}, wantTimeout: DefaultTimeout, wantOp: "x"},
		{name: "empty op ignored", opts: []Option{WithOp("")}, wantTimeout: DefaultTimeout, wantOp: opExecute},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newConfig(tt.timeout, tt.opts)
			assert.Equal(t, tt.wantTimeout, cfg.timeout)
			assert.Equal(t, tt.wantOp, cfg.opOrDefault())
		})
	}
}

// --- Timer ---

func TestNew_AppliesDefaults(t *testing.T) {
	timer := New()
	assert.Equal(t, DefaultTimeout, timer.Timeout())
	assert.Empty(t, timer.Op())
}

func TestNew_AppliesOptions(t *testing.T) {
	timer := New(WithTimeout(7*time.Second), WithOp("db.query"))
	assert.Equal(t, 7*time.Second, timer.Timeout())
	assert.Equal(t, "db.query", timer.Op())
}

func TestWithTimer_SeedsConfig(t *testing.T) {
	timer := New(WithTimeout(7*time.Second), WithOp("db.query"))
	cfg := newConfig(0, []Option{WithTimer(timer)})
	assert.Equal(t, 7*time.Second, cfg.timeout)
	assert.Equal(t, "db.query", cfg.op)
}

func TestWithTimer_OverriddenByLaterOption(t *testing.T) {
	timer := New(WithTimeout(7 * time.Second))
	cfg := newConfig(0, []Option{WithTimer(timer), WithOp("override")})
	assert.Equal(t, 7*time.Second, cfg.timeout)
	assert.Equal(t, "override", cfg.op)
}

func TestWithTimer_PositionalTimeoutWins(t *testing.T) {
	timer := New(WithTimeout(7 * time.Second))
	cfg := newConfig(2*time.Second, []Option{WithTimer(timer)})
	assert.Equal(t, 2*time.Second, cfg.timeout)
}

func TestWithTimer_NilIgnored(t *testing.T) {
	cfg := newConfig(0, []Option{WithTimer(nil)})
	assert.Equal(t, DefaultTimeout, cfg.timeout)
}

func TestExecute_WithTimerEndToEnd(t *testing.T) {
	timer := New(WithTimeout(5*time.Millisecond), WithOp("timed"))
	sim := testx.SlowCall(50 * time.Millisecond)
	_, err := Execute(context.Background(), 0,
		func(ctx context.Context, _ TimeoutController) (int, error) {
			return 0, sim.Call(ctx)
		}, WithTimer(timer))
	require.ErrorIs(t, err, ErrDeadlineExceeded)
	assert.ErrorContains(t, err, "timed")
}

// --- Concurrency ---

func TestExecute_ConcurrentRaceSafe(t *testing.T) {
	timer := New(WithTimeout(time.Second))
	testx.HammerNoError(t, 50, 200, func() error {
		v, err := Execute(context.Background(), 0,
			func(context.Context, TimeoutController) (int, error) {
				return 1, nil
			}, WithTimer(timer))
		if err != nil {
			return err
		}
		if v != 1 {
			return errors.New("unexpected value")
		}
		return nil
	})
}
