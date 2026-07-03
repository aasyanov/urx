package hedgex_test

import (
	"context"
	"fmt"
	"time"

	"github.com/aasyanov/urx/hedgex"
)

// ExampleExecute demonstrates the common case: one function hedged across
// staggered copies. The primary returns first, so no hedge is launched.
func ExampleExecute() {
	h := hedgex.New(
		hedgex.WithDelay(50*time.Millisecond),
		hedgex.WithMaxParallel(3),
	)

	got, err := hedgex.Execute(h, context.Background(),
		func(ctx context.Context, hc hedgex.HedgeController) (string, error) {
			if hc.IsHedge() {
				return "replica", nil // copy 2+ reads a replica
			}
			return "primary", nil
		})

	fmt.Println(got, err)
	// Output: primary <nil>
}

// ExampleExecute_hedgeWins shows a stalled primary being rescued by a hedge.
func ExampleExecute_hedgeWins() {
	h := hedgex.New(
		hedgex.WithDelay(20*time.Millisecond),
		hedgex.WithMaxParallel(2),
	)

	got, _ := hedgex.Execute(h, context.Background(),
		func(ctx context.Context, hc hedgex.HedgeController) (string, error) {
			if hc.IsHedge() {
				return "fast-replica", nil
			}
			<-ctx.Done() // primary stalls until a winner cancels it
			return "", ctx.Err()
		})

	fmt.Println(got)
	// Output: fast-replica
}

// ExampleExecuteMulti hedges across heterogeneous backends: a primary and a
// cache, each a distinct function.
func ExampleExecuteMulti() {
	h := hedgex.New(hedgex.WithDelay(10 * time.Millisecond))

	fromCache := func(ctx context.Context, _ hedgex.HedgeController) (string, error) {
		return "cached-value", nil
	}
	fromPrimary := func(ctx context.Context, _ hedgex.HedgeController) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}

	got, _ := hedgex.ExecuteMulti(h, context.Background(),
		[]hedgex.HedgeFunc[string]{fromPrimary, fromCache})

	fmt.Println(got)
	// Output: cached-value
}

// ExampleHedgeController_Cancel shows a copy withdrawing from the race when it
// learns it cannot win, leaving the slot for a sibling.
func ExampleHedgeController_Cancel() {
	h := hedgex.New(
		hedgex.WithDelay(15*time.Millisecond),
		hedgex.WithMaxParallel(2),
	)

	got, _ := hedgex.Execute(h, context.Background(),
		func(ctx context.Context, hc hedgex.HedgeController) (string, error) {
			if !hc.IsHedge() {
				hc.Cancel() // primary backend is known-unreachable
				return "", nil
			}
			return "hedge", nil
		})

	fmt.Println(got)
	// Output: hedge
}
