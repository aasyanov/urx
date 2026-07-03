package healthx_test

import (
	"context"
	"errors"
	"fmt"

	"github.com/aasyanov/urx/healthx"
)

// ExampleChecker demonstrates registering checks and aggregating readiness.
func ExampleChecker() {
	hc := healthx.New()
	hc.Register("cache", func(context.Context) error { return nil })
	hc.Register("database", func(context.Context) error {
		return errors.New("connection refused")
	})

	rep := hc.Readiness(context.Background())
	fmt.Println("overall:", rep.Status)
	fmt.Println("cache:", rep.Components["cache"].Status)
	fmt.Println("database:", rep.Components["database"].Status)
	// Output:
	// overall: down
	// cache: up
	// database: down
}

// ExampleChecker_markDown demonstrates failing readiness during shutdown
// while liveness stays meaningful.
func ExampleChecker_markDown() {
	hc := healthx.New()
	hc.Register("api", func(context.Context) error { return nil })

	fmt.Println("ready before:", hc.Readiness(context.Background()).Status)

	hc.MarkDown() // e.g. on SIGTERM, before draining
	fmt.Println("ready during shutdown:", hc.Readiness(context.Background()).Status)
	// Output:
	// ready before: up
	// ready during shutdown: down
}
