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

	ctx := context.Background()
	value, err := c.GetOrCompute(ctx, "expensive", func(ctx context.Context) (int, error) {
		return 1 + 1, nil // pretend this is a costly database query
	}, lrux.WithSingleflight())
	fmt.Println(value, err)
	// Output: 2 <nil>
}

// ExampleCache_GetOrCompute_errNotFound demonstrates that a compute function
// may return [lrux.ErrNotFound] to signal a missing backing record; nothing is
// cached and the error propagates to the caller.
func ExampleCache_GetOrCompute_errNotFound() {
	c := lrux.New[string, string]()
	defer c.Close()

	ctx := context.Background()
	_, err := c.GetOrCompute(ctx, "user:missing", func(ctx context.Context) (string, error) {
		return "", lrux.ErrNotFound
	})
	fmt.Println(err)
	// Output: lrux: key not found
}

// ExampleCache_onEvict demonstrates reacting to evictions, for example to
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
