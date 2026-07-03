package circuitx_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/aasyanov/urx/circuitx"
)

// ExampleTryExecute shows the non-blocking variant rejecting a call when the
// circuit is open, returning (false, zero, nil) instead of ErrOpen.
func ExampleTryExecute() {
	cb := circuitx.New(circuitx.WithMaxFailures(1))
	ctx := context.Background()
	boom := errors.New("downstream down")

	_, _ = circuitx.Execute(cb, ctx,
		func(context.Context, circuitx.CircuitController) (int, error) {
			return 0, boom
		})

	ok, _, err := circuitx.TryExecute(cb, ctx,
		func(context.Context, circuitx.CircuitController) (int, error) {
			return 1, nil
		})
	fmt.Println(ok, err == nil)
	// Output: false true
}

// ExampleExecute demonstrates a successful call through a closed breaker.
func ExampleExecute() {
	cb := circuitx.New(circuitx.WithMaxFailures(3))

	got, err := circuitx.Execute(cb, context.Background(),
		func(context.Context, circuitx.CircuitController) (int, error) {
			return 21 * 2, nil
		})
	fmt.Println(got, err)
	// Output: 42 <nil>
}

// ExampleExecute_trip shows the breaker tripping open after the configured
// number of consecutive failures, then rejecting further calls with ErrOpen.
func ExampleExecute_trip() {
	cb := circuitx.New(circuitx.WithMaxFailures(2))
	ctx := context.Background()
	boom := errors.New("downstream down")

	call := func() error {
		_, err := circuitx.Execute(cb, ctx,
			func(context.Context, circuitx.CircuitController) (int, error) {
				return 0, boom
			})
		return err
	}

	_ = call() // failure 1
	_ = call() // failure 2 -> trips Open

	_, err := circuitx.Execute(cb, ctx,
		func(context.Context, circuitx.CircuitController) (int, error) {
			return 1, nil // never runs while Open
		})
	fmt.Println(circuitx.ErrOpen == err, cb.State())
	// Output: true open
}

// ExampleCircuitController_skipFailure shows excluding a business error from the
// failure count so it never trips the breaker.
func ExampleCircuitController_skipFailure() {
	cb := circuitx.New(circuitx.WithMaxFailures(1))
	ctx := context.Background()
	notFound := errors.New("not found")

	for range 5 {
		_, _ = circuitx.Execute(cb, ctx,
			func(_ context.Context, cc circuitx.CircuitController) (int, error) {
				cc.SkipFailure()
				return 0, notFound
			})
	}
	fmt.Println(cb.State())
	// Output: closed
}

// ExampleCircuitController_trip shows a callback forcing the breaker open after
// detecting an unrecoverable condition, regardless of the failure threshold.
func ExampleCircuitController_trip() {
	cb := circuitx.New(circuitx.WithMaxFailures(100))

	_, _ = circuitx.Execute(cb, context.Background(),
		func(_ context.Context, cc circuitx.CircuitController) (int, error) {
			cc.Trip() // e.g. credentials were revoked
			return 0, nil
		})
	fmt.Println(cb.State())
	// Output: open
}
