package shedx_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/aasyanov/urx/shedx"
)

// ExampleExecute demonstrates admitting a request under normal load.
func ExampleExecute() {
	s := shedx.New(shedx.WithCapacity(100))
	defer func() { _ = s.Close() }()

	got, err := shedx.Execute(s, context.Background(), shedx.PriorityNormal,
		func(context.Context, shedx.ShedController) (int, error) {
			return 21 * 2, nil
		})
	fmt.Println(got, err)
	// Output: 42 <nil>
}

// ExampleExecute_shed shows a low-priority request being shed once the shedder
// is saturated, while a critical request is always admitted.
func ExampleExecute_shed() {
	s := shedx.New(shedx.WithCapacity(1), shedx.WithThreshold(0.5))
	defer func() { _ = s.Close() }()

	// Occupy the single slot with a critical request to drive load to 1.0.
	tok, _ := s.Acquire(shedx.PriorityCritical)
	defer tok.Release()

	_, err := shedx.Execute(s, context.Background(), shedx.PriorityLow,
		func(context.Context, shedx.ShedController) (int, error) {
			return 1, nil
		})
	fmt.Println(errors.Is(err, shedx.ErrRejected))
	// Output: true
}

// ExampleExecute_degrade shows a callback that serves a cheaper response under
// load and records the degradation via the controller.
func ExampleExecute_degrade() {
	s := shedx.New(shedx.WithCapacity(10), shedx.WithThreshold(0.5))
	defer func() { _ = s.Close() }()

	resp, _ := shedx.Execute(s, context.Background(), shedx.PriorityNormal,
		func(_ context.Context, sc shedx.ShedController) (string, error) {
			if sc.Load() >= 0.5 {
				sc.Shed()
				return "cached", nil
			}
			return "fresh", nil
		})
	fmt.Println(resp)
	// Output: fresh
}

// ExampleTryExecute shows the non-blocking variant: when the request would be
// shed TryExecute returns ok=false without an error.
func ExampleTryExecute() {
	s := shedx.New(shedx.WithCapacity(1), shedx.WithThreshold(0.5))
	defer func() { _ = s.Close() }()

	tok, _ := s.Acquire(shedx.PriorityCritical)
	defer tok.Release()

	ok, _, err := shedx.TryExecute(s, context.Background(), shedx.PriorityLow,
		func(context.Context, shedx.ShedController) (int, error) { return 1, nil })
	fmt.Println(ok, err == nil)
	// Output: false true
}

// ExampleShedder_Acquire demonstrates manual admission with a Token for code
// that cannot use the callback form.
func ExampleShedder_Acquire() {
	s := shedx.New(shedx.WithCapacity(100))
	defer func() { _ = s.Close() }()

	tok, err := s.Acquire(shedx.PriorityHigh)
	if err != nil {
		fmt.Println("shed:", err)
		return
	}
	defer tok.Release()

	fmt.Println("admitted, in-flight:", s.InFlight())
	// Output: admitted, in-flight: 1
}

// ExampleShedController_Shed shows a callback recording graceful degradation
// when the shedder is already under load.
func ExampleShedController_Shed() {
	s := shedx.New(shedx.WithCapacity(4), shedx.WithThreshold(0.5))
	defer func() { _ = s.Close() }()

	tok1, _ := s.Acquire(shedx.PriorityCritical)
	tok2, _ := s.Acquire(shedx.PriorityCritical)
	defer tok1.Release()
	defer tok2.Release()

	val, err := shedx.Execute(s, context.Background(), shedx.PriorityNormal,
		func(_ context.Context, sc shedx.ShedController) (string, error) {
			if sc.Load() >= 0.5 {
				sc.Shed()
				return "cached", nil
			}
			return "fresh", nil
		})
	fmt.Println(val, err)
	// Output:
	// cached <nil>
}
