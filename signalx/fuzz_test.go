package signalx

import (
	"context"
	"testing"
	"time"
)

// FuzzWaitWith runs [WaitWith] on an already-cancelled context so the fuzz
// target exercises hook collection and panic recovery without blocking.
func FuzzWaitWith(f *testing.F) {
	f.Add(int64(0), true)
	f.Add(int64(50), false)
	f.Add(int64(-1), true)

	f.Fuzz(func(t *testing.T, timeoutMs int64, panicHook bool) {
		t.Cleanup(ResetHooks)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		timeout := time.Duration(timeoutMs) * time.Millisecond
		err := WaitWith(ctx, []Option{WithTimeout(timeout)}, func(context.Context) {
			if panicHook {
				panic("fuzz hook panic")
			}
		})
		_ = err // may be nil, ErrHookPanic, ErrShutdownTimeout, or joined
	})
}
