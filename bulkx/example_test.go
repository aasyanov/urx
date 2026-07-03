package bulkx_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aasyanov/urx/bulkx"
)

// ExampleExecute demonstrates running an operation within the bulkhead.
func ExampleExecute() {
	bh := bulkx.New(bulkx.WithMaxConcurrent(10))
	defer func() { _ = bh.Close() }()

	got, err := bulkx.Execute(bh, context.Background(),
		func(context.Context, bulkx.BulkController) (int, error) {
			return 21 * 2, nil
		})
	fmt.Println(got, err)
	// Output: 42 <nil>
}

// ExampleExecute_adapt shows a callback that adapts to occupancy: under heavy
// load it returns a lighter response instead of the full one.
func ExampleExecute_adapt() {
	bh := bulkx.New(bulkx.WithMaxConcurrent(10))
	defer func() { _ = bh.Close() }()

	resp, _ := bulkx.Execute(bh, context.Background(),
		func(_ context.Context, bc bulkx.BulkController) (string, error) {
			if bc.Load() > 0.8 {
				return "lightweight", nil
			}
			return "full", nil
		})
	fmt.Println(resp)
	// Output: full
}

// ExampleTryExecute shows the non-blocking variant rejecting a request when the
// single slot is already occupied.
func ExampleTryExecute() {
	bh := bulkx.New(bulkx.WithMaxConcurrent(1))
	defer func() { _ = bh.Close() }()

	// Occupy the only slot.
	tok, _ := bh.Acquire(context.Background())
	defer tok.Release()

	ok, _, err := bulkx.TryExecute(bh, context.Background(),
		func(context.Context, bulkx.BulkController) (int, error) {
			return 1, nil
		})
	fmt.Println(ok, err)
	// Output: false <nil>
}

// ExampleBulkhead_Acquire demonstrates manual slot management with a Token for
// code that cannot use the callback form.
func ExampleBulkhead_Acquire() {
	bh := bulkx.New(bulkx.WithMaxConcurrent(10), bulkx.WithTimeout(time.Second))
	defer func() { _ = bh.Close() }()

	tok, err := bh.Acquire(context.Background())
	if err != nil {
		fmt.Println("rejected:", err)
		return
	}
	defer tok.Release()

	fmt.Println("acquired, active:", bh.Active())
	// Output: acquired, active: 1
}

// ExampleExecute_timeout shows Execute returning ErrTimeout when no slot frees
// up within the configured wait.
func ExampleExecute_timeout() {
	bh := bulkx.New(bulkx.WithMaxConcurrent(1), bulkx.WithTimeout(10*time.Millisecond))
	defer func() { _ = bh.Close() }()

	tok, _ := bh.Acquire(context.Background())
	defer tok.Release()

	_, err := bulkx.Execute(bh, context.Background(),
		func(context.Context, bulkx.BulkController) (int, error) {
			return 1, nil
		})
	fmt.Println(errors.Is(err, bulkx.ErrTimeout))
	// Output: true
}

// ExampleBulkController_Load shows a callback adapting its response based on
// the occupancy reported by the BulkController.
func ExampleBulkController_Load() {
	bh := bulkx.New(bulkx.WithMaxConcurrent(10))
	defer func() { _ = bh.Close() }()

	val, err := bulkx.Execute(bh, context.Background(),
		func(_ context.Context, bc bulkx.BulkController) (string, error) {
			if bc.Load() > 0.8 {
				return "degraded", nil
			}
			return "full", nil
		})
	fmt.Println(val, err)
	// Output:
	// full <nil>
}
