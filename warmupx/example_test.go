package warmupx_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aasyanov/urx/warmupx"
)

// ExampleWarmer demonstrates gating admission while an instance warms up.
func ExampleWarmer() {
	w := warmupx.New(
		warmupx.WithMinCapacity(1),
		warmupx.WithMaxCapacity(1),
	)
	w.Start()
	defer w.Stop()

	if w.Allow() {
		fmt.Println("request admitted")
	}
	// Output:
	// request admitted
}

// ExampleExecute shows running work only when the warmer admits it, scaling
// the work to the capacity reported by the controller.
func ExampleExecute() {
	w := warmupx.New(
		warmupx.WithMinCapacity(1),
		warmupx.WithMaxCapacity(1),
	)

	out, err := warmupx.Execute(w, context.Background(),
		func(_ context.Context, wc warmupx.WarmupController) (int, error) {
			batch := wc.Capacity() * 100
			return int(batch), nil
		})

	fmt.Println(out, err)
	// Output:
	// 100 <nil>
}

// ExampleExecute_rejected shows a request rejected before warmup admits traffic.
func ExampleExecute_rejected() {
	w := warmupx.New(
		warmupx.WithMinCapacity(0),
		warmupx.WithMaxCapacity(1),
	)

	_, err := warmupx.Execute(w, context.Background(),
		func(_ context.Context, _ warmupx.WarmupController) (int, error) {
			return 1, nil
		})

	fmt.Println("rejected:", errors.Is(err, warmupx.ErrRejected))
	// Output:
	// rejected: true
}

// ExampleWarmer_MaxRequests shows scaling a concurrency limit to current
// readiness.
func ExampleWarmer_MaxRequests() {
	w := warmupx.New(
		warmupx.WithMinCapacity(0.25),
		warmupx.WithMaxCapacity(1),
	)
	fmt.Println("permitted:", w.MaxRequests(200))
	// Output:
	// permitted: 50
}

// ExampleWarmupController_Reject shows a callback using the WarmupController
// to reject an admitted call when the instance is not ready for heavy work.
func ExampleWarmupController_Reject() {
	w := warmupx.New(
		warmupx.WithDuration(time.Hour),
		warmupx.WithMinCapacity(0.0),
	)
	w.Start()
	defer w.Stop()

	_, err := warmupx.Execute(w, context.Background(),
		func(_ context.Context, wc warmupx.WarmupController) (int, error) {
			if wc.Capacity() < 0.5 {
				wc.Reject()
				return 0, nil
			}
			return 1, nil
		})
	fmt.Println("rejected:", errors.Is(err, warmupx.ErrRejected))
	// Output:
	// rejected: true
}
