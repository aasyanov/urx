package lrux_test

import (
	"context"
	"fmt"
	"time"

	"github.com/aasyanov/urx/lrux"
)

// ExampleNew demonstrates basic cache construction and lookup.
func ExampleNew() {
	c := lrux.New[string, int](lrux.WithCapacity[string, int](100))
	defer c.Close()

	c.Set("answer", 42)
	v, ok := c.Get("answer")
	fmt.Println(v, ok)
	// Output: 42 true
}

// ExampleCache_GetOrCompute demonstrates lazy population with singleflight
// deduplication so the compute function runs at most once per key.
func ExampleCache_GetOrCompute() {
	c := lrux.New[string, int]()
	defer c.Close()

	value := c.GetOrCompute("expensive", func() int {
		return 1 + 1 // pretend this is a costly database query
	}, lrux.WithSingleflight())

	fmt.Println(value)
	// Output: 2
}

// ExampleCache_GetOrComputeCtx demonstrates context-aware computation with
// error propagation: a failing compute leaves nothing cached.
func ExampleCache_GetOrComputeCtx() {
	c := lrux.New[string, string]()
	defer c.Close()

	ctx := context.Background()
	v, err := c.GetOrComputeCtx(ctx, "user:1", func(ctx context.Context) (string, error) {
		return "Ada", nil
	})
	fmt.Println(v, err)
	// Output: Ada <nil>
}

// ExampleCache_OnEvict demonstrates reacting to evictions, for example to
// close resources when entries leave the cache.
func ExampleCache_onEvict() {
	c := lrux.New[string, int](
		lrux.WithCapacity[string, int](1),
		lrux.WithOnEvict[string, int](func(key string, _ int, reason lrux.EvictionReason) {
			if reason == lrux.EvictionCapacity {
				fmt.Printf("evicted %s (%s)\n", key, reason)
			}
		}),
	)
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2) // evicts "a" for capacity
	// Output: evicted a (capacity)
}

// ExampleNewSharded demonstrates a sharded cache for high-concurrency access.
func ExampleNewSharded() {
	c := lrux.NewSharded[string, int](
		lrux.WithShardCount[string, int](32),
		lrux.WithShardCapacity[string, int](1000),
		lrux.WithShardTTL[string, int](time.Hour),
	)
	defer c.Close()

	c.Set("session:abc", 1)
	v, ok := c.Get("session:abc")
	fmt.Println(v, ok)
	// Output: 1 true
}
