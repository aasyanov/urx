package poolx_test

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/aasyanov/urx/poolx"
)

// ExampleWorkerPool demonstrates submitting work and waiting for results.
func ExampleWorkerPool() {
	wp := poolx.NewWorkerPool(poolx.WithWorkers(4), poolx.WithQueueSize(16))
	defer wp.Close()

	err := wp.SubmitWait(context.Background(), func(context.Context) error {
		fmt.Println("task ran")
		return nil
	})
	fmt.Println("err:", err)
	// Output:
	// task ran
	// err: <nil>
}

// ExampleObjectPool demonstrates reusing buffers with a reset hook.
func ExampleObjectPool() {
	pool := poolx.NewObjectPool(
		func() *bytes.Buffer { return new(bytes.Buffer) },
		poolx.WithReset(func(b *bytes.Buffer) { b.Reset() }),
	)

	buf := pool.Get()
	buf.WriteString("hello")
	fmt.Println(buf.String())
	pool.Put(buf) // reset clears it

	fmt.Println("len after put:", buf.Len())
	// Output:
	// hello
	// len after put: 0
}

// ExampleBatch demonstrates buffering items and flushing them in batches.
func ExampleBatch() {
	var (
		mu      sync.Mutex
		flushed []int
	)
	b := poolx.NewBatch(func(_ context.Context, items []int) error {
		mu.Lock()
		flushed = append(flushed, items...)
		mu.Unlock()
		return nil
	}, poolx.WithBatchSize(3))

	for i := 1; i <= 3; i++ {
		_ = b.Add(i)
	}
	_ = b.Close()

	mu.Lock()
	sort.Ints(flushed)
	mu.Unlock()
	fmt.Println(flushed)
	// Output:
	// [1 2 3]
}
