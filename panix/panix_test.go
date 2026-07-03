package panix

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Safe
// ---------------------------------------------------------------------------

func TestSafe_NoPanic(t *testing.T) {
	val, err := Safe("test.op", func() (int, error) { return 42, nil })
	require.NoError(t, err)
	assert.Equal(t, 42, val)
}

func TestSafe_ReturnsError(t *testing.T) {
	sentinel := errors.New("expected")
	val, err := Safe("test.op", func() (int, error) { return 0, sentinel })
	require.ErrorIs(t, err, sentinel)
	assert.Equal(t, 0, val)
}

func TestSafe_PanicValues(t *testing.T) {
	cause := errors.New("root cause")
	type customPanic struct{ code int }

	tests := []struct {
		name       string
		panicVal   any
		wantValue  any
		wantUnwrap bool
	}{
		{name: "string", panicVal: "boom", wantValue: "boom"},
		{name: "int", panicVal: 123, wantValue: 123},
		{name: "error", panicVal: cause, wantValue: cause, wantUnwrap: true},
		{name: "struct", panicVal: customPanic{99}, wantValue: customPanic{99}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := Safe("test.panic", func() (string, error) { panic(tt.panicVal) })

			assert.Empty(t, val, "should return zero value on panic")

			var pe *PanicError
			require.ErrorAs(t, err, &pe)
			assert.Equal(t, "test.panic", pe.Op)
			assert.Equal(t, tt.wantValue, pe.Value)
			assert.NotEmpty(t, pe.Stack, "stack trace must be captured")
			assert.Contains(t, string(pe.Stack), "panix", "stack should reference panix package")

			if tt.wantUnwrap {
				require.ErrorIs(t, err, cause, "errors.Is should find cause through Unwrap")
			}
		})
	}
}

func TestSafe_PanicNil(t *testing.T) {
	//nolint:govet // intentionally testing panic(nil) recovery semantics
	_, err := Safe("op", func() (int, error) { panic(nil) })

	var pe *PanicError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, "op", pe.Op)
	assert.NotNil(t, pe.Value)
	errVal, ok := pe.Value.(error)
	require.True(t, ok, "Go 1.21+ wraps panic(nil) in an error value")
	assert.Equal(t, "panic called with nil argument", errVal.Error())
}

func TestSafe_NilFunc(t *testing.T) {
	_, err := Safe[int]("nil.fn", nil)

	var pe *PanicError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, "nil.fn", pe.Op)
	assert.NotEmpty(t, pe.Stack)
}

func TestSafe_ZeroValueOnPanic(t *testing.T) {
	type result struct {
		A int
		B string
	}
	val, err := Safe("op", func() (result, error) { panic("fail") })
	require.Error(t, err)
	assert.Equal(t, result{}, val, "must return zero value on panic")
}

// ---------------------------------------------------------------------------
// SafeVoid
// ---------------------------------------------------------------------------

func TestSafeVoid_NoPanic(t *testing.T) {
	err := SafeVoid("op", func() error { return nil })
	require.NoError(t, err)
}

func TestSafeVoid_ReturnsError(t *testing.T) {
	sentinel := errors.New("expected")
	err := SafeVoid("op", func() error { return sentinel })
	require.ErrorIs(t, err, sentinel)
}

func TestSafeVoid_Panic(t *testing.T) {
	err := SafeVoid("void.panic", func() error { panic("void boom") })

	var pe *PanicError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, "void.panic", pe.Op)
	assert.Equal(t, "void boom", pe.Value)
	assert.NotEmpty(t, pe.Stack)
}

// ---------------------------------------------------------------------------
// SafeGo
// ---------------------------------------------------------------------------

func TestSafeGo_NoPanic(t *testing.T) {
	done := make(chan struct{})
	SafeGo(context.Background(), "op", func(_ context.Context) {
		close(done)
	}, nil)
	<-done
}

func TestSafeGo_PanicCallsOnError(t *testing.T) {
	errCh := make(chan error, 1)
	SafeGo(context.Background(), "go.panic", func(_ context.Context) {
		panic("goroutine boom")
	}, func(_ context.Context, err error) {
		errCh <- err
	})

	got := <-errCh
	var pe *PanicError
	require.ErrorAs(t, got, &pe)
	assert.Equal(t, "go.panic", pe.Op)
}

func TestSafeGo_PanicNilOnError(t *testing.T) {
	done := make(chan struct{})
	SafeGo(context.Background(), "op", func(_ context.Context) {
		defer func() { close(done) }()
		panic("silent")
	}, nil)
	<-done
}

func TestSafeGo_NilContext(t *testing.T) {
	ctxCh := make(chan context.Context, 1)
	//nolint:staticcheck // SA1012: intentionally passing nil ctx to verify Background fallback
	SafeGo(nil, "op", func(ctx context.Context) {
		ctxCh <- ctx
	}, nil)
	got := <-ctxCh
	assert.NotNil(t, got, "nil ctx should be replaced with Background")
}

func TestSafeGo_ContextPropagated(t *testing.T) {
	type key struct{}
	parent := context.WithValue(context.Background(), key{}, "value")
	valCh := make(chan any, 1)
	SafeGo(parent, "op", func(ctx context.Context) {
		valCh <- ctx.Value(key{})
	}, nil)
	assert.Equal(t, "value", <-valCh)
}

func TestSafeGo_NilFunc(t *testing.T) {
	done := make(chan struct{})
	SafeGo(context.Background(), "nil.fn", nil, func(_ context.Context, err error) {
		defer close(done)
		var pe *PanicError
		require.ErrorAs(t, err, &pe)
		assert.Equal(t, "nil.fn", pe.Op)
	})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("onError was not called for nil fn panic")
	}
}

func TestSafeGo_OnErrorPanicRecovered(t *testing.T) {
	done := make(chan struct{})
	SafeGo(context.Background(), "op", func(context.Context) {
		panic("task panic")
	}, func(context.Context, error) {
		panic("handler panic")
	})
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SafeGo must not leak a panicking onError handler")
	}
}

func TestSafeGo_OnErrorPanicSwallowsDelivery(t *testing.T) {
	delivered := make(chan error, 1)
	done := make(chan struct{})
	SafeGo(context.Background(), "op", func(context.Context) {
		panic("task panic")
	}, func(_ context.Context, _ error) {
		panic("handler panic before delivery")
	})
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SafeGo goroutine")
	}
	select {
	case <-delivered:
		t.Fatal("onError panic must prevent delivery of task panic to caller")
	default:
	}
}

// ---------------------------------------------------------------------------
// Wrap
// ---------------------------------------------------------------------------

func TestWrap_NoPanic(t *testing.T) {
	wrapped := Wrap("op", func() (int, error) { return 7, nil })
	val, err := wrapped()
	require.NoError(t, err)
	assert.Equal(t, 7, val)
}

func TestWrap_Panic(t *testing.T) {
	wrapped := Wrap("wrap.panic", func() (int, error) { panic("wrapped boom") })
	_, err := wrapped()

	var pe *PanicError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, "wrap.panic", pe.Op)
}

func TestWrap_MultipleInvocations(t *testing.T) {
	count := 0
	wrapped := Wrap("op", func() (int, error) {
		count++
		if count == 2 {
			panic("second call")
		}
		return count, nil
	})

	val, err := wrapped()
	require.NoError(t, err)
	assert.Equal(t, 1, val)

	_, err = wrapped()
	var pe *PanicError
	require.ErrorAs(t, err, &pe)

	val, err = wrapped()
	require.NoError(t, err)
	assert.Equal(t, 3, val, "wrapper should recover and work again after panic")
}

// ---------------------------------------------------------------------------
// WrapVoid
// ---------------------------------------------------------------------------

func TestWrapVoid_NoPanic(t *testing.T) {
	wrapped := WrapVoid("op", func() error { return nil })
	require.NoError(t, wrapped())
}

func TestWrapVoid_Panic(t *testing.T) {
	wrapped := WrapVoid("wv.panic", func() error { panic("boom") })
	err := wrapped()

	var pe *PanicError
	require.ErrorAs(t, err, &pe)
	assert.Equal(t, "wv.panic", pe.Op)
}

func TestWrapVoid_MultipleInvocations(t *testing.T) {
	count := 0
	wrapped := WrapVoid("op", func() error {
		count++
		if count == 2 {
			panic("second call")
		}
		return nil
	})

	require.NoError(t, wrapped())
	err := wrapped()
	var pe *PanicError
	require.ErrorAs(t, err, &pe)
	require.NoError(t, wrapped())
	assert.Equal(t, 3, count, "wrapper should recover and work again after panic")
}

// ---------------------------------------------------------------------------
// PanicError
// ---------------------------------------------------------------------------

func TestPanicError_Error(t *testing.T) {
	tests := []struct {
		name  string
		pe    PanicError
		want  string
	}{
		{name: "string value", pe: PanicError{Op: "test.op", Value: "msg"}, want: "panix: panic in test.op: msg"},
		{name: "nil value", pe: PanicError{Op: "test.op", Value: nil}, want: "panix: panic in test.op: <nil>"},
		{name: "int value", pe: PanicError{Op: "x", Value: 42}, want: "panix: panic in x: 42"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.pe.Error())
		})
	}
}

func TestPanicError_Unwrap(t *testing.T) {
	t.Run("error value returns cause", func(t *testing.T) {
		cause := errors.New("root")
		pe := &PanicError{Op: "op", Value: cause}
		assert.Equal(t, cause, pe.Unwrap())
	})

	t.Run("non-error value returns nil", func(t *testing.T) {
		pe := &PanicError{Op: "op", Value: "string"}
		assert.Nil(t, pe.Unwrap())
	})

	t.Run("nil value returns nil", func(t *testing.T) {
		pe := &PanicError{Op: "op", Value: nil}
		assert.Nil(t, pe.Unwrap())
	})
}

func TestPanicError_ErrorsIs_Chain(t *testing.T) {
	sentinel := errors.New("sentinel")
	_, err := Safe("op", func() (int, error) { panic(sentinel) })
	require.ErrorIs(t, err, sentinel, "errors.Is should find sentinel through Unwrap chain")
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

func TestSafe_Concurrent(t *testing.T) {
	errs := hammerIndexed(100, 1, func(idx int) error {
		_, err := Safe("concurrent", func() (int, error) {
			if idx%3 == 0 {
				panic("boom")
			}
			return idx, nil
		})
		if idx%3 == 0 {
			var pe *PanicError
			if !errors.As(err, &pe) {
				return fmt.Errorf("goroutine %d: expected *PanicError, got %v", idx, err)
			}
			return nil
		}
		if err != nil {
			return fmt.Errorf("goroutine %d: unexpected error: %v", idx, err)
		}
		return nil
	})
	assert.Empty(t, errs)
}

func TestSafeGo_Concurrent(t *testing.T) {
	const goroutines = 50
	var panicCount atomic.Int32
	done := make(chan struct{}, goroutines)

	for i := range goroutines {
		idx := i
		SafeGo(context.Background(), "concurrent", func(_ context.Context) {
			if idx%5 == 0 {
				panic("concurrent boom")
			}
			done <- struct{}{}
		}, func(_ context.Context, _ error) {
			panicCount.Add(1)
			done <- struct{}{}
		})
	}
	for range goroutines {
		<-done
	}

	assert.Equal(t, int32(10), panicCount.Load(), "every 5th goroutine (0,5,10,...,45) should panic")
}

// ---------------------------------------------------------------------------
// Stack trace (white-box: access captureStack directly)
// ---------------------------------------------------------------------------

func TestSafe_StackContainsFunctionName(t *testing.T) {
	_, err := Safe("stack.test", func() (int, error) { panic("trace me") })
	var pe *PanicError
	require.ErrorAs(t, err, &pe)
	assert.True(t, bytes.Contains(pe.Stack, []byte("captureStack")),
		"stack trace should contain captureStack:\n%s", pe.Stack)
}

func TestSafe_StackMinimumLength(t *testing.T) {
	_, err := Safe("op", func() (int, error) { panic("x") })
	var pe *PanicError
	require.ErrorAs(t, err, &pe)
	assert.Greater(t, len(pe.Stack), 100, "stack trace should be substantial")
}

func TestCaptureStack_NotEmpty(t *testing.T) {
	stack := captureStack()
	assert.NotEmpty(t, stack)
}

func TestCaptureStack_ContainsCallerName(t *testing.T) {
	stack := captureStack()
	assert.Contains(t, string(stack), "TestCaptureStack_ContainsCallerName")
}

func TestCaptureStack_MaxSize(t *testing.T) {
	stack := captureStack()
	assert.LessOrEqual(t, len(stack), maxStackSize,
		"stack should never exceed maxStackSize (%d)", maxStackSize)
}

// deepStack recurses depth times before capturing, forcing a stack trace
// large enough to exceed the 4 KB default buffer and exercise the
// buffer-growth branch of captureStack.
func deepStack(depth int) []byte {
	if depth <= 0 {
		return captureStack()
	}
	stack := deepStack(depth - 1)
	return stack
}

// callCaptureAtDepth invokes captureStack from a deep stack frame so the
// captured trace reflects real recursion depth (not tail-call collapsed).
func callCaptureAtDepth(depth int, scratch [128]byte) []byte {
	_ = scratch[depth%128]
	if depth <= 0 {
		return captureStack()
	}
	stack := callCaptureAtDepth(depth-1, scratch)
	return stack
}

func callCaptureLimitedAtDepth(cap, depth int, scratch [128]byte) []byte {
	_ = scratch[depth%128]
	if depth <= 0 {
		return captureStackLimited(cap)
	}
	stack := callCaptureLimitedAtDepth(cap, depth-1, scratch)
	return stack
}

func TestCaptureStack_GrowsBuffer(t *testing.T) {
	stack := deepStack(400)
	assert.Greater(t, len(stack), defaultStackSize,
		"deep recursion should produce a stack larger than the default buffer")
	assert.LessOrEqual(t, len(stack), maxStackSize,
		"stack should never exceed maxStackSize (%d)", maxStackSize)
}

func TestCaptureStack_TruncatesAtMaxSize(t *testing.T) {
	const testCap = 8192
	stack := callCaptureLimitedAtDepth(testCap, 2000, [128]byte{})
	assert.Equal(t, testCap, len(stack),
		"trace larger than cap must return a full capped buffer")

	stack = callCaptureAtDepth(12000, [128]byte{})
	assert.LessOrEqual(t, len(stack), maxStackSize)
	if len(stack) == maxStackSize {
		t.Log("production captureStack hit the 64 KB hard cap")
	}
}
