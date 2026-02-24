package shedx_test

import (
	"context"
	"fmt"

	"github.com/aasyanov/urx/pkg/shedx"
)

func ExampleExecute() {
	s := shedx.New(shedx.WithCapacity(100))
	ctx := context.Background()

	result, err := shedx.Execute(s, ctx, shedx.PriorityNormal, func(ctx context.Context, sc shedx.ShedController) (string, error) {
		return fmt.Sprintf("load=%.1f", sc.Load()), nil
	})

	fmt.Println(result, err)
	// Output: load=0.0 <nil>
}
