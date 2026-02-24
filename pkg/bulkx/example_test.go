package bulkx_test

import (
	"context"
	"fmt"

	"github.com/aasyanov/urx/pkg/bulkx"
)

func ExampleExecute() {
	bh := bulkx.New(bulkx.WithMaxConcurrent(10))
	ctx := context.Background()

	result, err := bulkx.Execute(bh, ctx, func(ctx context.Context, bc bulkx.BulkController) (int, error) {
		return bc.MaxConcurrent(), nil
	})

	fmt.Println(result, err)
	// Output: 10 <nil>
}
