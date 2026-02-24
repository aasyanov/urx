package cronx_test

import (
	"context"
	"fmt"
	"time"

	"github.com/aasyanov/urx/pkg/cronx"
)

func ExampleAddJob() {
	s := cronx.New()

	_ = cronx.AddJob(s, "once", 0, func(ctx context.Context, jc cronx.JobController) (string, error) {
		return "ok", nil
	})

	_ = s.Start(context.Background())
	_ = s.Stop(time.Second)

	fmt.Println("done")
	// Output: done
}
