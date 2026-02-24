package hedgex_test

import (
	"context"
	"fmt"
	"time"

	"github.com/aasyanov/urx/pkg/hedgex"
)

func ExampleHedger_Do() {
	h := hedgex.New[string](hedgex.WithDelay(50 * time.Millisecond))
	ctx := context.Background()

	result, err := h.Do(ctx, func(ctx context.Context, hc hedgex.HedgeController) (string, error) {
		return fmt.Sprintf("hedge=%v", hc.IsHedge()), nil
	})

	fmt.Println(result, err)
	// Output: hedge=false <nil>
}
