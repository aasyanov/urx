package testx

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// FailMode.String
// ---------------------------------------------------------------------------

func TestFailMode_String(t *testing.T) {
	tests := []struct {
		mode FailMode
		want string
	}{
		{FailNever, "never"},
		{FailAlways, "always"},
		{FailPattern, "pattern"},
		{FailAfterN, "after_n"},
		{FailUntilN, "until_n"},
		{FailEveryN, "every_n"},
		{FailMode(99), "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.mode.String())
		})
	}
}

// ---------------------------------------------------------------------------
// Simulator: FailNever
// ---------------------------------------------------------------------------

func TestSimulator_NeverFail(t *testing.T) {
	sim := NeverFail()
	for range 10 {
		require.NoError(t, sim.Call())
	}
	assert.Equal(t, int64(10), sim.Calls())
	assert.Equal(t, int64(0), sim.Failures())
}

// ---------------------------------------------------------------------------
// Simulator: FailAlways
// ---------------------------------------------------------------------------

func TestSimulator_AlwaysFail(t *testing.T) {
	sim := AlwaysFail()
	for range 5 {
		err := sim.Call()
		require.Error(t, err)
		require.ErrorIs(t, err, ErrSimulated)
	}
	assert.Equal(t, int64(5), sim.Calls())
	assert.Equal(t, int64(5), sim.Failures())
}

// ---------------------------------------------------------------------------
// Simulator: FailPattern
// ---------------------------------------------------------------------------

func TestSimulator_Pattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		calls   int
		wantOK  []bool
	}{
		{
			name:    "SSFS",
			pattern: "SSFS",
			calls:   8,
			wantOK:  []bool{true, true, false, true, true, true, false, true},
		},
		{
			name:    "F",
			pattern: "F",
			calls:   3,
			wantOK:  []bool{false, false, false},
		},
		{
			name:    "S",
			pattern: "S",
			calls:   3,
			wantOK:  []bool{true, true, true},
		},
		{
			name:    "lowercase",
			pattern: "ssfS",
			calls:   4,
			wantOK:  []bool{true, true, false, true},
		},
		{
			name:    "empty pattern",
			pattern: "",
			calls:   3,
			wantOK:  []bool{true, true, true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sim := Pattern(tt.pattern)
			for i, wantOK := range tt.wantOK {
				err := sim.Call()
				if wantOK {
					assert.NoError(t, err, "call %d", i)
				} else {
					assert.Error(t, err, "call %d", i)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Simulator: FailAfterN
// ---------------------------------------------------------------------------

func TestSimulator_FailAfterN(t *testing.T) {
	sim := FailAfter(3)

	for i := range 3 {
		require.NoError(t, sim.Call(), "call %d should succeed", i)
	}
	for i := range 5 {
		require.Error(t, sim.Call(), "call %d after threshold should fail", i+3)
	}
}

// ---------------------------------------------------------------------------
// Simulator: FailUntilN
// ---------------------------------------------------------------------------

func TestSimulator_FailUntilN(t *testing.T) {
	sim := FailUntil(3)

	for i := range 3 {
		require.Error(t, sim.Call(), "call %d should fail", i)
	}
	for i := range 5 {
		require.NoError(t, sim.Call(), "call %d after threshold should succeed", i+3)
	}
}

// ---------------------------------------------------------------------------
// Simulator: FailEveryN
// ---------------------------------------------------------------------------

func TestSimulator_FailEveryN(t *testing.T) {
	sim := FailEvery(3)

	results := make([]bool, 9)
	for i := range 9 {
		results[i] = sim.Call() == nil
	}
	// Calls: 1(ok), 2(ok), 3(fail), 4(ok), 5(ok), 6(fail), 7(ok), 8(ok), 9(fail)
	assert.Equal(t, []bool{true, true, false, true, true, false, true, true, false}, results)
}

func TestSimulator_FailEveryN_ZeroN(t *testing.T) {
	sim := NewSimulator(WithFailEveryN(0))
	for range 5 {
		require.NoError(t, sim.Call())
	}
}

// ---------------------------------------------------------------------------
// Simulator: custom error
// ---------------------------------------------------------------------------

func TestSimulator_CustomError(t *testing.T) {
	custom := errors.New("custom")
	sim := NewSimulator(WithFailAlways(), WithErrorFunc(func() error {
		return custom
	}))

	err := sim.Call()
	require.ErrorIs(t, err, custom)
}

func TestSimulator_CustomMessage(t *testing.T) {
	sim := NewSimulator(WithFailAlways(), WithMessage("db timeout"))
	err := sim.Call()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db timeout")
}

// ---------------------------------------------------------------------------
// Simulator: Reset
// ---------------------------------------------------------------------------

func TestSimulator_Reset(t *testing.T) {
	sim := FailUntil(2)
	sim.Call()
	sim.Call()
	sim.Call()

	assert.Equal(t, int64(3), sim.Calls())
	assert.Equal(t, int64(2), sim.Failures())

	sim.Reset()

	assert.Equal(t, int64(0), sim.Calls())
	assert.Equal(t, int64(0), sim.Failures())

	// Pattern rewinds
	require.Error(t, sim.Call(), "after reset, should fail again")
}

func TestSimulator_Reset_PatternRewinds(t *testing.T) {
	sim := Pattern("FS")
	require.Error(t, sim.Call())
	require.NoError(t, sim.Call())

	sim.Reset()

	require.Error(t, sim.Call(), "pattern should restart from index 0")
	require.NoError(t, sim.Call())
}

// ---------------------------------------------------------------------------
// Simulator: Concurrency
// ---------------------------------------------------------------------------

func TestSimulator_Concurrent(t *testing.T) {
	sim := NewSimulator(WithFailEveryN(3))

	var wg sync.WaitGroup
	const goroutines = 50
	const iters = 100
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			for range iters {
				sim.Call()
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(goroutines*iters), sim.Calls())
	assert.Greater(t, sim.Failures(), int64(0))
}

// ---------------------------------------------------------------------------
// Simulator: Option edge cases
// ---------------------------------------------------------------------------

func TestSimulator_WithFailAfterN_NegativeN(t *testing.T) {
	sim := NewSimulator(WithFailAfterN(-5))
	// n stays 0, so FailAfterN with n=0 → fails from call 1
	require.Error(t, sim.Call())
}

func TestSimulator_WithFailUntilN_NegativeN(t *testing.T) {
	sim := NewSimulator(WithFailUntilN(-1))
	// n stays 0, so FailUntilN with n=0 → never fails (callNum > 0 always)
	require.NoError(t, sim.Call())
}

func TestSimulator_WithMessage_Empty(t *testing.T) {
	sim := NewSimulator(WithFailAlways(), WithMessage(""))
	err := sim.Call()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "simulated failure", "empty msg should keep default")
}

// ---------------------------------------------------------------------------
// Convenience constructors
// ---------------------------------------------------------------------------

func TestConvenienceConstructors(t *testing.T) {
	t.Run("AlwaysFail", func(t *testing.T) {
		assert.Error(t, AlwaysFail().Call())
	})
	t.Run("NeverFail", func(t *testing.T) {
		assert.NoError(t, NeverFail().Call())
	})
	t.Run("FailAfter", func(t *testing.T) {
		sim := FailAfter(1)
		assert.NoError(t, sim.Call())
		assert.Error(t, sim.Call())
	})
	t.Run("FailUntil", func(t *testing.T) {
		sim := FailUntil(1)
		assert.Error(t, sim.Call())
		assert.NoError(t, sim.Call())
	})
	t.Run("FailEvery", func(t *testing.T) {
		sim := FailEvery(2)
		assert.NoError(t, sim.Call())
		assert.Error(t, sim.Call())
	})
	t.Run("Pattern", func(t *testing.T) {
		sim := Pattern("SF")
		assert.NoError(t, sim.Call())
		assert.Error(t, sim.Call())
	})
}

// ---------------------------------------------------------------------------
// shouldFail: default branch
// ---------------------------------------------------------------------------

func TestSimulator_UnknownMode(t *testing.T) {
	sim := &Simulator{cfg: simConfig{mode: FailMode(99)}}
	require.NoError(t, sim.Call(), "unknown mode should never fail")
}
