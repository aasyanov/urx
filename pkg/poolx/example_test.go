package poolx_test

import (
	"context"
	"fmt"

	"github.com/aasyanov/urx/pkg/poolx"
)

func ExampleNewWorkerPool() {
	wp := poolx.NewWorkerPool(poolx.WithWorkers(4), poolx.WithQueueSize(16))
	defer wp.Close()

	err := wp.Submit(context.Background(), func(ctx context.Context) error {
		return nil
	})

	fmt.Println(err)
	// Output: <nil>
}
