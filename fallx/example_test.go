package fallx_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aasyanov/urx/fallx"
)

var errUnavailable = errors.New("service unavailable")

// ExampleExecute shows a static fallback: when the primary fails, the
// configured value is returned with a nil error.
func ExampleExecute() {
	fb := fallx.New(fallx.WithStatic("default"))
	defer func() { _ = fb.Close() }()

	got, err := fallx.Execute(fb, context.Background(),
		func(context.Context, fallx.FallController) (string, error) {
			return "", errUnavailable
		})
	fmt.Println(got, err)
	// Output: default <nil>
}

// ExampleExecute_func shows a function fallback that branches on the controller
// to inspect the primary error and compute a degraded result.
func ExampleExecute_func() {
	fb := fallx.New(fallx.WithFunc(
		func(_ context.Context, fc fallx.FallController) (string, error) {
			return "degraded after: " + fc.Error().Error(), nil
		}))
	defer func() { _ = fb.Close() }()

	got, _ := fallx.Execute(fb, context.Background(),
		func(context.Context, fallx.FallController) (string, error) {
			return "", errUnavailable
		})
	fmt.Println(got)
	// Output: degraded after: service unavailable
}

// ExampleExecute_cached shows the cached strategy replaying the last successful
// result after a later failure.
func ExampleExecute_cached() {
	fb := fallx.New(fallx.WithCached[int](time.Minute, 100))
	defer func() { _ = fb.Close() }()

	// First call succeeds and is cached under the default key.
	_, _ = fallx.Execute(fb, context.Background(),
		func(context.Context, fallx.FallController) (int, error) {
			return 200, nil
		})

	// Second call fails; the cached value is replayed.
	got, err := fallx.Execute(fb, context.Background(),
		func(context.Context, fallx.FallController) (int, error) {
			return 0, errUnavailable
		})
	fmt.Println(got, err)
	// Output: 200 <nil>
}

// ExampleExecuteWithKey shows per-key caching: each key replays only its own
// last success.
func ExampleExecuteWithKey() {
	fb := fallx.New(fallx.WithCached[string](time.Minute, 100))
	defer func() { _ = fb.Close() }()

	fb.Seed("user-1", "Alice")

	got, err := fallx.ExecuteWithKey(fb, context.Background(), "user-1",
		func(context.Context, fallx.FallController) (string, error) {
			return "", errUnavailable
		})
	fmt.Println(got, err)

	_, err = fallx.ExecuteWithKey(fb, context.Background(), "user-2",
		func(context.Context, fallx.FallController) (string, error) {
			return "", errUnavailable
		})
	fmt.Println(errors.Is(err, fallx.ErrNoCached))
	// Output:
	// Alice <nil>
	// true
}

// ExampleFallController_OnFallback shows a StrategyFunc callback that inspects
// the primary error via the FallController before producing a degraded result.
func ExampleFallController_OnFallback() {
	fb := fallx.New(
		fallx.WithFunc(func(_ context.Context, fc fallx.FallController) (string, error) {
			return fmt.Sprintf("fallback for: %v", fc.Error()), nil
		}),
	)
	defer func() { _ = fb.Close() }()

	val, err := fallx.Execute(fb, context.Background(),
		func(_ context.Context, _ fallx.FallController) (string, error) {
			return "", errors.New("primary failed")
		})
	fmt.Println(val, err)
	// Output:
	// fallback for: primary failed <nil>
}
