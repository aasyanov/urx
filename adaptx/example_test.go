package adaptx_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/aasyanov/urx/adaptx"
)

// ExampleExecute shows the recommended callback form: the limiter admits the
// call, the callback adapts its work to the admission snapshot, and the result
// feeds the adaptive algorithm.
func ExampleExecute() {
	l := adaptx.New(
		adaptx.WithAlgorithm(adaptx.Gradient),
		adaptx.WithInitialLimit(10),
	)
	defer l.Close()

	result, err := adaptx.Execute(l, context.Background(),
		func(ctx context.Context, ac adaptx.AdaptController) (string, error) {
			if ac.InFlight() > ac.Limit()/2 {
				return "cheap", nil // shed load near saturation
			}
			return "full", nil
		})

	switch {
	case errors.Is(err, adaptx.ErrClosed):
		fmt.Println("closed")
	case err != nil:
		fmt.Println("failed:", err)
	default:
		fmt.Println("ok:", result)
	}
	// Output: ok: full
}

// ExampleLimiter_Acquire shows manual admission for code that cannot use a
// single callback. The release function must be called exactly once with the
// outcome and measured latency.
func ExampleLimiter_Acquire() {
	l := adaptx.New(adaptx.WithInitialLimit(5))
	defer l.Close()

	release, err := l.Acquire(context.Background())
	if err != nil {
		fmt.Println("acquire failed:", err)
		return
	}
	// ... do work, measure latency ...
	release(true, 0)

	fmt.Println("in-flight:", l.InFlight())
	// Output: in-flight: 0
}

// ExampleAdaptController_SkipSample shows excluding an outlier call (a cache
// miss here) from the adaptive feedback so its latency does not mislead the
// controller.
func ExampleAdaptController_SkipSample() {
	l := adaptx.New(adaptx.WithAlgorithm(adaptx.Vegas))
	defer l.Close()

	_, _ = adaptx.Execute(l, context.Background(),
		func(ctx context.Context, ac adaptx.AdaptController) (int, error) {
			cacheMiss := true
			if cacheMiss {
				ac.SkipSample() // cold-start latency is not representative
			}
			return 1, nil
		})

	fmt.Println("samples in latency window:", l.Stats().AvgLat)
	// Output: samples in latency window: 0s
}

// ExampleTryExecute shows the non-blocking variant: when no permit is free the
// call is skipped rather than queued.
func ExampleTryExecute() {
	l := adaptx.New(adaptx.WithInitialLimit(1), adaptx.WithMaxLimit(1))
	defer l.Close()

	ran, val, err := adaptx.TryExecute(l, context.Background(),
		func(ctx context.Context, ac adaptx.AdaptController) (int, error) {
			return 7, nil
		})
	fmt.Printf("ran=%v val=%d err=%v\n", ran, val, err)
	// Output: ran=true val=7 err=<nil>
}
