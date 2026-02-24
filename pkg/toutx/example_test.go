package toutx_test

import (
	"context"
	"fmt"
	"time"

	"github.com/aasyanov/urx/pkg/toutx"
)

func ExampleExecute() {
	ctx := context.Background()

	result, err := toutx.Execute(ctx, time.Second, func(ctx context.Context) (string, error) {
		return "fast", nil
	})

	fmt.Println(result, err)
	// Output: fast <nil>
}
