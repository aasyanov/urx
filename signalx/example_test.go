package signalx_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aasyanov/urx/signalx"
)

// Example demonstrates the typical graceful-shutdown flow: trap signals into
// a context, then drain resources with Wait. Here the context is cancelled
// directly instead of by a real signal so the example is deterministic.
func Example() {
	ctx, cancel := signalx.Trap(context.Background())
	defer cancel()

	// In production the cancel below is triggered by SIGINT/SIGTERM.
	cancel()

	err := signalx.Wait(ctx,
		func(context.Context) { fmt.Println("http server stopped") },
		func(context.Context) { fmt.Println("database closed") },
	)
	fmt.Println("shutdown error:", err)
	// Output:
	// http server stopped
	// database closed
	// shutdown error: <nil>
}

// ExampleOnShutdown shows how process-global hooks registered anywhere in the
// codebase run before hooks passed directly to Wait.
func ExampleOnShutdown() {
	signalx.ResetHooks()

	signalx.OnShutdown(func(context.Context) { fmt.Println("global hook") })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_ = signalx.Wait(ctx, func(context.Context) { fmt.Println("local hook") })
	// Output:
	// global hook
	// local hook
}

// ExampleWaitWith demonstrates bounding shutdown with a custom timeout. A hook
// that exceeds the timeout causes Wait to return ErrShutdownTimeout.
func ExampleWaitWith() {
	signalx.ResetHooks()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := signalx.WaitWith(ctx, []signalx.Option{signalx.WithTimeout(10 * time.Millisecond)},
		func(context.Context) { time.Sleep(50 * time.Millisecond) },
	)
	fmt.Println(errors.Is(err, signalx.ErrShutdownTimeout))
	// Output:
	// true
}
