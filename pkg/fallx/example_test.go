package fallx_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/aasyanov/urx/pkg/fallx"
)

func ExampleFallback_Do() {
	fb := fallx.New(fallx.WithStatic("default-value"))
	ctx := context.Background()

	result, err := fb.Do(ctx, func(ctx context.Context) (string, error) {
		return "", errors.New("primary failed")
	})

	fmt.Println(result, err)
	// Output: default-value <nil>
}
