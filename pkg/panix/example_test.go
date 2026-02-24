package panix_test

import (
	"context"
	"fmt"

	"github.com/aasyanov/urx/pkg/errx"
	"github.com/aasyanov/urx/pkg/panix"
)

func ExampleSafe() {
	ctx := context.Background()

	_, err := panix.Safe(ctx, "risky-op", func(ctx context.Context) (string, error) {
		panic("something went wrong")
	})

	if ex, ok := errx.As(err); ok {
		fmt.Println(ex.Code, ex.Op)
	}
	// Output: PANIC risky-op
}
