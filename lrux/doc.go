// Package lrux provides a generic, thread-safe LRU cache with TTL expiration,
// eviction callbacks, singleflight compute, and an optional sharded variant
// for high-concurrency workloads.
//
// The cache stores entries in an intrusive doubly-linked list keyed by a map,
// giving O(1) lookup, insertion, promotion, and eviction with a single heap
// allocation per entry. Statistics use atomic counters for lock-free reads,
// and eviction callbacks run outside the lock with panic recovery.
//
// # Quick Start
//
//	c := lrux.New[string, int](
//	    lrux.WithCapacity[string, int](1000),
//	    lrux.WithTTL[string, int](time.Hour),
//	)
//	defer c.Close()
//
//	c.Set("answer", 42)
//	v, ok := c.Get("answer")
//
//	v = c.GetOrCompute("answer", func() int { return expensive() },
//	    lrux.WithSingleflight(),
//	)
//
// # Sharding
//
// For more than a handful of concurrent goroutines hitting many distinct keys,
// use [NewSharded] to spread lock contention across independent shards:
//
//	sc := lrux.NewSharded[string, []byte](
//	    lrux.WithShardCount[string, []byte](32),
//	    lrux.WithShardCapacity[string, []byte](1000),
//	)
//	defer sc.Close()
//
// # Zero Dependencies
//
// lrux depends only on the Go standard library and golang.org/x/sync for
// singleflight deduplication.
package lrux
