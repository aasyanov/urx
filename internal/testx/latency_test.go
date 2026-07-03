package testx

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLatencySim_ZeroDelay(t *testing.T) {
	sim := NewLatencySim()
	err := sim.Call(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(1), sim.Calls())
}

func TestLatencySim_WithDelay(t *testing.T) {
	sim := NewLatencySim(WithLatency(50 * time.Millisecond))

	start := time.Now()
	err := sim.Call(context.Background())
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, elapsed, 40*time.Millisecond, "should sleep ~50ms")
}

func TestLatencySim_ContextCancellation(t *testing.T) {
	sim := NewLatencySim(WithLatency(5 * time.Second))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := sim.Call(ctx)
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, time.Second, "should exit early on ctx cancel")
}

func TestLatencySim_WithError(t *testing.T) {
	sim := NewLatencySim(
		WithLatency(10*time.Millisecond),
		WithLatencyError(func() error { return ErrSimulated }),
	)

	err := sim.Call(context.Background())
	require.ErrorIs(t, err, ErrSimulated)
}

func TestLatencySim_ZeroDelayWithError(t *testing.T) {
	sim := NewLatencySim(
		WithLatencyError(func() error { return ErrSimulated }),
	)
	err := sim.Call(context.Background())
	require.ErrorIs(t, err, ErrSimulated)
}

func TestSlowCall(t *testing.T) {
	sim := SlowCall(10 * time.Millisecond)
	err := sim.Call(context.Background())
	require.NoError(t, err)
}

func TestSlowThenFail(t *testing.T) {
	sim := SlowThenFail(10 * time.Millisecond)
	err := sim.Call(context.Background())
	require.ErrorIs(t, err, ErrSimulated)
}

func TestLatencySim_Concurrent(t *testing.T) {
	sim := NewLatencySim(WithLatency(5 * time.Millisecond))

	errs := Hammer(10, 5, func() error {
		return sim.Call(context.Background())
	})
	assert.Empty(t, errs)
	assert.Equal(t, int64(50), sim.Calls())
}
