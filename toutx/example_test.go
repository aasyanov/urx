package toutx_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aasyanov/urx/toutx"
)

// ExampleExecute demonstrates the basic timeout wrapper around a fast call.
func ExampleExecute() {
	got, err := toutx.Execute(context.Background(), time.Second,
		func(context.Context, toutx.TimeoutController) (int, error) {
			return 21 * 2, nil
		})
	fmt.Println(got, err)
	// Output: 42 <nil>
}

// ExampleExecute_deadline shows that a function exceeding its budget returns
// ErrDeadlineExceeded.
func ExampleExecute_deadline() {
	_, err := toutx.Execute(context.Background(), 5*time.Millisecond,
		func(ctx context.Context, _ toutx.TimeoutController) (int, error) {
			select {
			case <-time.After(time.Second):
				return 1, nil
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		})
	fmt.Println(errors.Is(err, toutx.ErrDeadlineExceeded))
	// Output: true
}

// ExampleExecute_controller shows a callback that self-throttles using the
// remaining budget reported by the TimeoutController.
func ExampleExecute_controller() {
	_, _ = toutx.Execute(context.Background(), 100*time.Millisecond,
		func(_ context.Context, tc toutx.TimeoutController) (string, error) {
			if tc.Remaining() < 10*time.Millisecond {
				return "cheap", nil // not enough budget for the full path
			}
			return "full", nil
		})
	fmt.Println("done")
	// Output: done
}

// ExampleTimer demonstrates reusing a pre-configured Timer across calls.
func ExampleTimer() {
	timer := toutx.New(
		toutx.WithTimeout(3*time.Second),
		toutx.WithOp("db.query"),
	)

	got, err := toutx.Execute(context.Background(), 0,
		func(context.Context, toutx.TimeoutController) (string, error) {
			return "rows", nil
		}, toutx.WithTimer(timer))
	fmt.Println(got, err)
	// Output: rows <nil>
}
