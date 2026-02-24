// Example: Worker pool with adaptive concurrency and load shedding.
//
// Demonstrates: poolx workers processing tasks through shedx (load
// shedding by priority) and adaptx (auto-tuning concurrency limit).
//
// Run:
//
//	go run ./examples/worker
package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/aasyanov/urx/pkg/adaptx"
	"github.com/aasyanov/urx/pkg/errx"
	"github.com/aasyanov/urx/pkg/shedx"
)

func main() {
	shed := shedx.New(shedx.WithCapacity(50), shedx.WithThreshold(0.7))
	defer shed.Close()

	limiter := adaptx.New(
		adaptx.WithAlgorithm(adaptx.AIMD),
		adaptx.WithInitialLimit(10),
		adaptx.WithMinLimit(2),
		adaptx.WithMaxLimit(30),
		adaptx.WithOnLimitChange(func(old, new int) {
			fmt.Printf("  [adaptx] limit changed: %d → %d\n", old, new)
		}),
	)
	defer limiter.Close()

	ctx := context.Background()
	var wg sync.WaitGroup

	for i := range 40 {
		wg.Add(1)
		go func(taskID int) {
			defer wg.Done()

			priority := shedx.PriorityNormal
			if taskID%5 == 0 {
				priority = shedx.PriorityCritical
			} else if taskID%3 == 0 {
				priority = shedx.PriorityLow
			}

			result, err := shedx.Execute(shed, ctx, priority, func(ctx context.Context, sc shedx.ShedController) (string, error) {
				return adaptx.Do(limiter, ctx, func(ctx context.Context, ac adaptx.AdaptController) (string, error) {
					latency := time.Duration(20+rand.IntN(80)) * time.Millisecond
					time.Sleep(latency)

					if rand.Float64() < 0.1 {
						return "", errx.New("WORKER", "TASK_FAILED", "random failure")
					}

					return fmt.Sprintf("task-%d done (load=%.0f%%, limit=%d, latency=%v)",
						taskID, sc.Load()*100, ac.Limit(), latency), nil
				})
			})

			if err != nil {
				if ex, ok := errx.As(err); ok {
					fmt.Printf("  [task-%02d] SHED %s: %s\n", taskID, ex.Code, ex.Message)
				}
				return
			}
			fmt.Printf("  [task-%02d] %s\n", taskID, result)
		}(i)
	}

	wg.Wait()
	fmt.Println("\nAll tasks completed.")
}
