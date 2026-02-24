package circuitx_test

import (
	"context"
	"fmt"

	"github.com/aasyanov/urx/pkg/circuitx"
)

func ExampleExecute() {
	cb := circuitx.New(circuitx.WithMaxFailures(3))
	ctx := context.Background()

	result, err := circuitx.Execute(cb, ctx, func(ctx context.Context, cc circuitx.CircuitController) (string, error) {
		_ = cc.State()
		return "ok", nil
	})

	fmt.Println(result, err)
	// Output: ok <nil>
}
