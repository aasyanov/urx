package ctxx_test

import (
	"context"
	"fmt"

	"github.com/aasyanov/urx/pkg/ctxx"
)

func ExampleWithTrace() {
	ctx := ctxx.WithTrace(context.Background())
	ctx = ctxx.WithSpan(ctx)

	traceID, spanID := ctxx.TraceFromContext(ctx)
	fmt.Println(traceID != "", spanID != "")
	// Output: true true
}
