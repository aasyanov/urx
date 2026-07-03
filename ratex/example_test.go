package ratex_test

import (
	"context"
	"fmt"

	"github.com/aasyanov/urx/ratex"
)

// ExampleLimiter_Allow demonstrates the non-blocking admission check.
func ExampleLimiter_Allow() {
	rl := ratex.New(ratex.WithRate(100), ratex.WithBurst(2))

	for range 3 {
		fmt.Println(rl.Allow())
	}
	// Output:
	// true
	// true
	// false
}

// ExampleExecute runs work under the limiter, receiving a RateController.
func ExampleExecute() {
	rl := ratex.New(ratex.WithRate(100), ratex.WithBurst(5))

	got, err := ratex.Execute(rl, context.Background(),
		func(_ context.Context, rc ratex.RateController) (int, error) {
			if rc.Tokens() < 1 {
				return 0, nil // degrade near the limit
			}
			return 21 * 2, nil
		})
	fmt.Println(got, err)
	// Output: 42 <nil>
}

// ExampleTryExecute shows the non-blocking execution variant.
func ExampleTryExecute() {
	rl := ratex.New(ratex.WithRate(1), ratex.WithBurst(1))

	ok, _, _ := ratex.TryExecute(rl, context.Background(),
		func(context.Context, ratex.RateController) (string, error) {
			return "first", nil
		})
	fmt.Println("first admitted:", ok)

	ok, _, _ = ratex.TryExecute(rl, context.Background(),
		func(context.Context, ratex.RateController) (string, error) {
			return "second", nil
		})
	fmt.Println("second admitted:", ok)
	// Output:
	// first admitted: true
	// second admitted: false
}

// ExampleRateController_SkipToken refunds the token for a no-op call so it does
// not count against the budget.
func ExampleRateController_SkipToken() {
	rl := ratex.New(ratex.WithRate(1), ratex.WithBurst(1))

	_, _ = ratex.Execute(rl, context.Background(),
		func(_ context.Context, rc ratex.RateController) (int, error) {
			rc.SkipToken() // cache hit: no downstream work performed
			return 0, nil
		})

	// The single token was refunded, so the next call is still admitted.
	fmt.Println(rl.Allow())
	// Output: true
}
