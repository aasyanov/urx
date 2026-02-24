package adaptx_test

import (
	"context"
	"fmt"

	"github.com/aasyanov/urx/pkg/adaptx"
)

func ExampleDo() {
	l := adaptx.New(adaptx.WithAlgorithm(adaptx.AIMD), adaptx.WithInitialLimit(10))
	ctx := context.Background()

	result, err := adaptx.Do(l, ctx, func(ctx context.Context, ac adaptx.AdaptController) (string, error) {
		return fmt.Sprintf("limit=%d", ac.Limit()), nil
	})

	fmt.Println(result, err)
	// Output: limit=10 <nil>
}
