package lrux

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- Constructor ---

func TestNew_Defaults(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	if c.capacity != 0 {
		t.Fatalf("expected unlimited capacity, got %d", c.capacity)
	}
	if c.ttl != 0 {
		t.Fatalf("expected zero TTL, got %v", c.ttl)
	}
}

func TestNew_NegativeCapacity(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](-5))
	defer c.Close()

	if c.capacity != 0 {
		t.Fatalf("expected 0, got %d", c.capacity)
	}
}

func TestNew_NegativeTTL(t *testing.T) {
	c := New[string, int](WithTTL[string, int](-time.Second))
	defer c.Close()

	if c.ttl != 0 {
		t.Fatalf("expected 0, got %v", c.ttl)
	}
}

// --- Set / Get ---

func TestSetGet_Basic(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	v, ok := c.Get("a")
	if !ok || v != 1 {
		t.Fatalf("expected (1, true), got (%d, %v)", v, ok)
	}
}

func TestGet_Miss(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	v, ok := c.Get("missing")
	if ok || v != 0 {
		t.Fatalf("expected (0, false), got (%d, %v)", v, ok)
	}
}

func TestSet_Overwrite(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	c.Set("a", 2)
	v, ok := c.Get("a")
	if !ok || v != 2 {
		t.Fatalf("expected (2, true), got (%d, %v)", v, ok)
	}
	if c.Len() != 1 {
		t.Fatalf("expected len 1, got %d", c.Len())
	}
}

func TestSetWithTTL(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.SetWithTTL("a", 1, 50*time.Millisecond)
	v, ok := c.Get("a")
	if !ok || v != 1 {
		t.Fatalf("expected (1, true), got (%d, %v)", v, ok)
	}

	time.Sleep(60 * time.Millisecond)
	v, ok = c.Get("a")
	if ok {
		t.Fatalf("expected expired, got (%d, %v)", v, ok)
	}
}

func TestSet_OnClosed(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	c.Close()
	c.Set("a", 1)
	if c.Len() != 0 {
		t.Fatalf("expected 0, got %d", c.Len())
	}
}

// --- Eviction ---

func TestEviction_Capacity(t *testing.T) {
	var capacityEvicted []string
	c := New[string, int](
		WithCapacity[string, int](3),
		WithOnEvict[string, int](func(key string, _ int, reason EvictionReason) {
			if reason == Capacity {
				capacityEvicted = append(capacityEvicted, key)
			}
		}),
	)
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	c.Set("d", 4) // evicts "a"

	if c.Len() != 3 {
		t.Fatalf("expected 3, got %d", c.Len())
	}
	if len(capacityEvicted) != 1 || capacityEvicted[0] != "a" {
		t.Fatalf("expected [a] evicted, got %v", capacityEvicted)
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected a to be evicted")
	}
}

func TestEviction_LRUOrder(t *testing.T) {
	var evicted []string
	c := New[string, int](
		WithCapacity[string, int](3),
		WithOnEvict[string, int](func(key string, _ int, reason EvictionReason) {
			evicted = append(evicted, key)
		}),
	)
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	c.Get("a") // promote "a"
	c.Set("d", 4) // evicts "b" (least recently used)

	if len(evicted) != 1 || evicted[0] != "b" {
		t.Fatalf("expected [b] evicted, got %v", evicted)
	}
}

func TestEviction_Replaced(t *testing.T) {
	var reasons []EvictionReason
	c := New[string, int](
		WithCapacity[string, int](10),
		WithOnEvict[string, int](func(_ string, _ int, reason EvictionReason) {
			reasons = append(reasons, reason)
		}),
	)
	defer c.Close()

	c.Set("a", 1)
	c.Set("a", 2)
	if len(reasons) != 1 || reasons[0] != Replaced {
		t.Fatalf("expected [Replaced], got %v", reasons)
	}
}

// --- TTL ---

func TestTTL_GlobalExpiry(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	time.Sleep(60 * time.Millisecond)

	if _, ok := c.Get("a"); ok {
		t.Fatal("expected expired")
	}
}

func TestTTL_PerEntryOverridesGlobal(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](time.Hour),
	)
	defer c.Close()

	c.SetWithTTL("short", 1, 50*time.Millisecond)
	c.Set("long", 2)

	time.Sleep(60 * time.Millisecond)

	if _, ok := c.Get("short"); ok {
		t.Fatal("expected short to be expired")
	}
	if _, ok := c.Get("long"); !ok {
		t.Fatal("expected long to be alive")
	}
}

func TestTTL_NoExpiry(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	d := c.TTL("a")
	if d != -1 {
		t.Fatalf("expected -1, got %v", d)
	}
}

func TestTTL_Missing(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	if d := c.TTL("missing"); d != 0 {
		t.Fatalf("expected 0, got %v", d)
	}
}

func TestTTL_Expired(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	time.Sleep(60 * time.Millisecond)

	if d := c.TTL("a"); d != 0 {
		t.Fatalf("expected 0, got %v", d)
	}
}

func TestTTL_Remaining(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](time.Hour),
	)
	defer c.Close()

	c.Set("a", 1)
	d := c.TTL("a")
	if d <= 0 || d > time.Hour {
		t.Fatalf("expected positive TTL <= 1h, got %v", d)
	}
}

// --- Peek ---

func TestPeek_DoesNotPromote(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](3))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)

	v, ok := c.Peek("a")
	if !ok || v != 1 {
		t.Fatalf("expected (1, true), got (%d, %v)", v, ok)
	}

	c.Set("d", 4) // should evict "a" since Peek didn't promote it
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected a to be evicted (Peek should not promote)")
	}
}

func TestPeek_Miss(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	if _, ok := c.Peek("missing"); ok {
		t.Fatal("expected miss")
	}
}

func TestPeek_Expired(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	time.Sleep(60 * time.Millisecond)

	if _, ok := c.Peek("a"); ok {
		t.Fatal("expected expired entry to be invisible to Peek")
	}
}

func TestPeek_OnClosed(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	c.Set("a", 1)
	c.Close()

	if _, ok := c.Peek("a"); ok {
		t.Fatal("expected false on closed cache")
	}
}

// --- Has ---

func TestHas(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	if !c.Has("a") {
		t.Fatal("expected true")
	}
	if c.Has("missing") {
		t.Fatal("expected false")
	}
}

func TestHas_Expired(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	time.Sleep(60 * time.Millisecond)

	if c.Has("a") {
		t.Fatal("expected false for expired")
	}
}

func TestHas_OnClosed(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	c.Set("a", 1)
	c.Close()

	if c.Has("a") {
		t.Fatal("expected false on closed cache")
	}
}

// --- Delete ---

func TestDelete(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	if !c.Delete("a") {
		t.Fatal("expected true")
	}
	if c.Len() != 0 {
		t.Fatalf("expected 0, got %d", c.Len())
	}
}

func TestDelete_Missing(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	if c.Delete("missing") {
		t.Fatal("expected false")
	}
}

func TestDelete_EvictionCallback(t *testing.T) {
	var reason EvictionReason
	c := New[string, int](
		WithCapacity[string, int](10),
		WithOnEvict[string, int](func(_ string, _ int, r EvictionReason) { reason = r }),
	)
	defer c.Close()

	c.Set("a", 1)
	c.Delete("a")
	if reason != Deleted {
		t.Fatalf("expected Deleted, got %v", reason)
	}
}

func TestDelete_OnClosed(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	c.Set("a", 1)
	c.Close()

	if c.Delete("a") {
		t.Fatal("expected false on closed cache")
	}
}

// --- Clear ---

func TestClear(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Clear()

	if c.Len() != 0 {
		t.Fatalf("expected 0, got %d", c.Len())
	}
}

func TestClear_EvictionCallbacks(t *testing.T) {
	var count int
	c := New[string, int](
		WithCapacity[string, int](10),
		WithOnEvict[string, int](func(_ string, _ int, r EvictionReason) {
			if r != Cleared {
				panic("expected Cleared")
			}
			count++
		}),
	)
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Clear()

	if count != 2 {
		t.Fatalf("expected 2 callbacks, got %d", count)
	}
}

func TestClear_OnClosed(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	c.Close()
	c.Clear() // should not panic
}

// --- GetOrCompute ---

func TestGetOrCompute_CacheHit(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	v := c.GetOrCompute("a", func() int { return 99 })
	if v != 1 {
		t.Fatalf("expected 1, got %d", v)
	}
}

func TestGetOrCompute_CacheMiss(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	v := c.GetOrCompute("a", func() int { return 42 })
	if v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}

	v2, ok := c.Get("a")
	if !ok || v2 != 42 {
		t.Fatalf("expected (42, true), got (%d, %v)", v2, ok)
	}
}

func TestGetOrCompute_WithTTL(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.GetOrCompute("a", func() int { return 1 }, WithComputeTTL(50*time.Millisecond))
	time.Sleep(60 * time.Millisecond)

	if _, ok := c.Get("a"); ok {
		t.Fatal("expected expired")
	}
}

func TestGetOrCompute_WithSingleflight(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	var calls atomic.Int32
	compute := func() int {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return 42
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := c.GetOrCompute("a", compute, WithSingleflight())
			if v != 42 {
				t.Errorf("expected 42, got %d", v)
			}
		}()
	}
	wg.Wait()

	if n := calls.Load(); n != 1 {
		t.Fatalf("expected 1 compute call, got %d", n)
	}
}

func TestGetOrCompute_ExpiredEntry(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	time.Sleep(60 * time.Millisecond)

	v := c.GetOrCompute("a", func() int { return 99 })
	if v != 99 {
		t.Fatalf("expected 99, got %d", v)
	}
}

func TestGetOrCompute_OnClosed(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	c.Close()

	v := c.GetOrCompute("a", func() int { return 42 })
	if v != 0 {
		t.Fatalf("expected 0, got %d", v)
	}
}

func TestGetOrCompute_ConcurrentRace(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			c.GetOrCompute("key", func() int { return n })
		}(i)
	}
	wg.Wait()

	if _, ok := c.Get("key"); !ok {
		t.Fatal("expected key to exist")
	}
}

// --- Batch operations ---

func TestSetMulti(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](100))
	defer c.Close()

	c.SetMulti(map[string]int{"a": 1, "b": 2, "c": 3})
	if c.Len() != 3 {
		t.Fatalf("expected 3, got %d", c.Len())
	}
}

func TestSetMulti_Empty(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.SetMulti(nil)
	c.SetMulti(map[string]int{})
	if c.Len() != 0 {
		t.Fatalf("expected 0, got %d", c.Len())
	}
}

func TestSetMulti_OnClosed(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	c.Close()
	c.SetMulti(map[string]int{"a": 1})
	if c.Len() != 0 {
		t.Fatalf("expected 0, got %d", c.Len())
	}
}

func TestGetMulti(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](100))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)

	result := c.GetMulti([]string{"a", "b", "c"})
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if result["a"] != 1 || result["b"] != 2 {
		t.Fatalf("unexpected values: %v", result)
	}
}

func TestDeleteMulti(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](100))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)

	n := c.DeleteMulti([]string{"a", "c", "missing"})
	if n != 2 {
		t.Fatalf("expected 2, got %d", n)
	}
	if c.Len() != 1 {
		t.Fatalf("expected 1, got %d", c.Len())
	}
}

func TestDeleteMulti_Empty(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	if n := c.DeleteMulti(nil); n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
}

func TestDeleteMulti_OnClosed(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	c.Set("a", 1)
	c.Close()

	if n := c.DeleteMulti([]string{"a"}); n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
}

// --- Iteration ---

func TestKeys(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)

	keys := c.Keys()
	if len(keys) != 3 {
		t.Fatalf("expected 3, got %d", len(keys))
	}
	// Most recent first
	if keys[0] != "c" {
		t.Fatalf("expected c first, got %s", keys[0])
	}
}

func TestKeys_OnClosed(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	c.Close()

	if keys := c.Keys(); keys != nil {
		t.Fatalf("expected nil, got %v", keys)
	}
}

func TestKeys_RemovesExpired(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	c.SetWithTTL("b", 2, time.Hour)
	time.Sleep(60 * time.Millisecond)

	keys := c.Keys()
	if len(keys) != 1 || keys[0] != "b" {
		t.Fatalf("expected [b], got %v", keys)
	}
}

func TestValues(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)

	values := c.Values()
	if len(values) != 2 {
		t.Fatalf("expected 2, got %d", len(values))
	}
}

func TestValues_OnClosed(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	c.Close()

	if values := c.Values(); values != nil {
		t.Fatalf("expected nil, got %v", values)
	}
}

func TestValues_RemovesExpired(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	c.SetWithTTL("b", 2, time.Hour)
	time.Sleep(60 * time.Millisecond)

	values := c.Values()
	if len(values) != 1 || values[0] != 2 {
		t.Fatalf("expected [2], got %v", values)
	}
}

func TestRange(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)

	var collected []string
	c.Range(func(key string, _ int) bool {
		collected = append(collected, key)
		return true
	})
	if len(collected) != 3 {
		t.Fatalf("expected 3, got %d", len(collected))
	}
}

func TestRange_EarlyStop(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)

	var count int
	c.Range(func(_ string, _ int) bool {
		count++
		return false
	})
	if count != 1 {
		t.Fatalf("expected 1, got %d", count)
	}
}

func TestRange_RemovesExpired(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	c.SetWithTTL("b", 2, time.Hour)
	time.Sleep(60 * time.Millisecond)

	var keys []string
	c.Range(func(key string, _ int) bool {
		keys = append(keys, key)
		return true
	})
	if len(keys) != 1 || keys[0] != "b" {
		t.Fatalf("expected [b], got %v", keys)
	}
}

func TestRange_OnClosed(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	c.Close()

	called := false
	c.Range(func(_ string, _ int) bool {
		called = true
		return true
	})
	if called {
		t.Fatal("expected Range to be no-op on closed cache")
	}
}

// --- ExpireOld ---

func TestExpireOld(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.SetWithTTL("c", 3, time.Hour)
	time.Sleep(60 * time.Millisecond)

	n := c.ExpireOld()
	if n != 2 {
		t.Fatalf("expected 2, got %d", n)
	}
	if c.Len() != 1 {
		t.Fatalf("expected 1, got %d", c.Len())
	}
}

func TestExpireOld_OnClosed(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	c.Close()

	if n := c.ExpireOld(); n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
}

// --- Statistics ---

func TestStats(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	c.Get("a")       // hit
	c.Get("missing") // miss

	s := c.Stats()
	if s.Hits != 1 {
		t.Fatalf("expected 1 hit, got %d", s.Hits)
	}
	if s.Misses != 1 {
		t.Fatalf("expected 1 miss, got %d", s.Misses)
	}
	if s.Size != 1 {
		t.Fatalf("expected size 1, got %d", s.Size)
	}
	if s.Capacity != 10 {
		t.Fatalf("expected capacity 10, got %d", s.Capacity)
	}
	if s.HitRate != 0.5 {
		t.Fatalf("expected 0.5 hit rate, got %f", s.HitRate)
	}
}

func TestStats_NoRequests(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	s := c.Stats()
	if s.HitRate != 0 {
		t.Fatalf("expected 0 hit rate, got %f", s.HitRate)
	}
}

func TestResetStats(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	c.Get("a")
	c.Get("missing")
	c.ResetStats()

	s := c.Stats()
	if s.Hits != 0 || s.Misses != 0 || s.Evictions != 0 {
		t.Fatalf("expected all zeros, got %+v", s)
	}
}

func TestStats_Evictions(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](2))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3) // evicts a

	s := c.Stats()
	if s.Evictions != 1 {
		t.Fatalf("expected 1 eviction, got %d", s.Evictions)
	}
}

// --- Close / IsClosed ---

func TestClose_Idempotent(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	c.Close()
	c.Close() // should not panic
	if !c.IsClosed() {
		t.Fatal("expected closed")
	}
}

func TestClose_FiresCallbacks(t *testing.T) {
	var count int
	c := New[string, int](
		WithCapacity[string, int](10),
		WithOnEvict[string, int](func(_ string, _ int, r EvictionReason) {
			if r != Cleared {
				panic("expected Cleared")
			}
			count++
		}),
	)

	c.Set("a", 1)
	c.Set("b", 2)
	c.Close()

	if count != 2 {
		t.Fatalf("expected 2, got %d", count)
	}
}

func TestClose_StopsCleanupTicker(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithCleanupInterval[string, int](10*time.Millisecond),
	)
	c.Set("a", 1)
	c.Close()
	// Verify no panic from goroutine after close
	time.Sleep(30 * time.Millisecond)
}

// --- Cleanup ticker ---

func TestCleanupTicker(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
		WithCleanupInterval[string, int](30*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	time.Sleep(100 * time.Millisecond)

	if c.Len() != 0 {
		t.Fatalf("expected 0 after cleanup, got %d", c.Len())
	}
}

// --- Unlimited capacity ---

func TestUnlimitedCapacity(t *testing.T) {
	c := New[string, int]()
	defer c.Close()

	for i := 0; i < 1000; i++ {
		c.Set(strconv.Itoa(i), i)
	}
	if c.Len() != 1000 {
		t.Fatalf("expected 1000, got %d", c.Len())
	}
}

// --- Thread safety ---

func TestConcurrent_SetGet(t *testing.T) {
	c := New[int, int](WithCapacity[int, int](100))
	defer c.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			c.Set(n, n)
		}(i)
		go func(n int) {
			defer wg.Done()
			c.Get(n)
		}(i)
	}
	wg.Wait()
}

func TestConcurrent_Mixed(t *testing.T) {
	c := New[int, int](
		WithCapacity[int, int](50),
		WithTTL[int, int](100*time.Millisecond),
	)
	defer c.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(5)
		go func(n int) { defer wg.Done(); c.Set(n, n) }(i)
		go func(n int) { defer wg.Done(); c.Get(n) }(i)
		go func(n int) { defer wg.Done(); c.Peek(n) }(i)
		go func(n int) { defer wg.Done(); c.Delete(n) }(i)
		go func(n int) { defer wg.Done(); c.Has(n) }(i)
	}
	wg.Wait()
}

// --- Intrusive list invariants ---

func TestList_SingleElement(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	c.mu.RLock()
	if c.head != c.tail {
		t.Fatal("head should equal tail for single element")
	}
	if c.head.prev != nil || c.head.next != nil {
		t.Fatal("single element should have nil prev and next")
	}
	c.mu.RUnlock()
}

func TestList_OrderAfterAccess(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	c.Get("a") // promote a to front

	c.mu.RLock()
	if c.head.key != "a" {
		t.Fatalf("expected head=a, got %s", c.head.key)
	}
	if c.tail.key != "b" {
		t.Fatalf("expected tail=b, got %s", c.tail.key)
	}
	c.mu.RUnlock()
}

// --- EvictionReason.String ---

func TestEvictionReason_String(t *testing.T) {
	tests := []struct {
		r    EvictionReason
		want string
	}{
		{Capacity, "capacity"},
		{Expired, "expired"},
		{Deleted, "deleted"},
		{Cleared, "cleared"},
		{Replaced, "replaced"},
		{EvictionReason(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.r.String(); got != tt.want {
			t.Errorf("EvictionReason(%d).String() = %q, want %q", tt.r, got, tt.want)
		}
	}
}

// --- keyToString ---

func TestKeyToString(t *testing.T) {
	tests := []struct {
		name string
		key  any
		want string
	}{
		{"string", "hello", "hello"},
		{"int", 42, "42"},
		{"int64", int64(100), "100"},
		{"int32", int32(50), "50"},
		{"uint", uint(7), "7"},
		{"uint64", uint64(999), "999"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			switch k := tt.key.(type) {
			case string:
				got = keyToString(k)
			case int:
				got = keyToString(k)
			case int64:
				got = keyToString(k)
			case int32:
				got = keyToString(k)
			case uint:
				got = keyToString(k)
			case uint64:
				got = keyToString(k)
			}
			if got != tt.want {
				t.Errorf("keyToString(%v) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

func TestKeyToString_Fallback(t *testing.T) {
	type custom struct{ x int }
	got := keyToString(custom{42})
	if got != "{42}" {
		t.Fatalf("expected {42}, got %s", got)
	}
}

// --- GetOrCompute double-check paths ---

func TestGetOrCompute_DoubleCheck_HitAfterLock(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	// Simulate: initial Get misses (expired), but by the time we lock,
	// another goroutine has stored a value.
	// We test this by pre-populating and calling getOrComputeDirect directly.
	v := c.getOrComputeDirect("a", func() int { return 99 }, 0)
	if v != 1 {
		t.Fatalf("expected 1 (double-check hit), got %d", v)
	}
}

func TestGetOrCompute_Singleflight_CacheHit(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	// Force singleflight path
	v := c.getOrComputeSingle("a", func() int { return 99 }, 0)
	if v != 1 {
		t.Fatalf("expected 1, got %d", v)
	}
}

// --- SetMulti with eviction ---

func TestSetMulti_Eviction(t *testing.T) {
	var evictions int
	c := New[string, int](
		WithCapacity[string, int](3),
		WithOnEvict[string, int](func(_ string, _ int, r EvictionReason) {
			if r == Capacity {
				evictions++
			}
		}),
	)
	defer c.Close()

	c.SetMulti(map[string]int{"a": 1, "b": 2, "c": 3, "d": 4, "e": 5})
	if c.Len() != 3 {
		t.Fatalf("expected 3, got %d", c.Len())
	}
	if evictions != 2 {
		t.Fatalf("expected 2 evictions, got %d", evictions)
	}
}

func TestSetMulti_Overwrite(t *testing.T) {
	var replaced int
	c := New[string, int](
		WithCapacity[string, int](10),
		WithOnEvict[string, int](func(_ string, _ int, r EvictionReason) {
			if r == Replaced {
				replaced++
			}
		}),
	)
	defer c.Close()

	c.Set("a", 1)
	c.SetMulti(map[string]int{"a": 99})
	if replaced != 1 {
		t.Fatalf("expected 1 replaced, got %d", replaced)
	}
	v, _ := c.Get("a")
	if v != 99 {
		t.Fatalf("expected 99, got %d", v)
	}
}

// --- Get removes expired entry ---

func TestGet_RemovesExpiredEntry(t *testing.T) {
	var evicted bool
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
		WithOnEvict[string, int](func(_ string, _ int, r EvictionReason) {
			if r == Expired {
				evicted = true
			}
		}),
	)
	defer c.Close()

	c.Set("a", 1)
	time.Sleep(60 * time.Millisecond)
	c.Get("a") // triggers lazy removal

	if !evicted {
		t.Fatal("expected eviction callback for expired entry")
	}
	if c.Len() != 0 {
		t.Fatalf("expected 0, got %d", c.Len())
	}
}

// ============================================================
// ShardedLRU tests
// ============================================================

func TestSharded_Basic(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	c.Set("a", 1)
	v, ok := c.Get("a")
	if !ok || v != 1 {
		t.Fatalf("expected (1, true), got (%d, %v)", v, ok)
	}
}

func TestSharded_DefaultShardCount(t *testing.T) {
	c := NewSharded[string, int](WithShardCapacity[string, int](10))
	defer c.Close()

	if len(c.shards) != 16 {
		t.Fatalf("expected 16 shards, got %d", len(c.shards))
	}
}

func TestSharded_PowerOfTwo(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](5), WithShardCapacity[string, int](10))
	defer c.Close()

	if len(c.shards) != 8 {
		t.Fatalf("expected 8 (next power of 2 from 5), got %d", len(c.shards))
	}
}

func TestSharded_SetWithTTL(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	c.SetWithTTL("a", 1, 50*time.Millisecond)
	time.Sleep(60 * time.Millisecond)

	if _, ok := c.Get("a"); ok {
		t.Fatal("expected expired")
	}
}

func TestSharded_Peek(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	c.Set("a", 1)
	v, ok := c.Peek("a")
	if !ok || v != 1 {
		t.Fatalf("expected (1, true), got (%d, %v)", v, ok)
	}
}

func TestSharded_Has(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	c.Set("a", 1)
	if !c.Has("a") {
		t.Fatal("expected true")
	}
	if c.Has("missing") {
		t.Fatal("expected false")
	}
}

func TestSharded_Delete(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	c.Set("a", 1)
	if !c.Delete("a") {
		t.Fatal("expected true")
	}
	if c.Has("a") {
		t.Fatal("expected deleted")
	}
}

func TestSharded_TTL(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100), WithShardTTL[string, int](time.Hour))
	defer c.Close()

	c.Set("a", 1)
	d := c.TTL("a")
	if d <= 0 {
		t.Fatalf("expected positive TTL, got %v", d)
	}
}

func TestSharded_GetOrCompute(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	v := c.GetOrCompute("a", func() int { return 42 })
	if v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}
}

func TestSharded_Len(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	for i := 0; i < 20; i++ {
		c.Set(strconv.Itoa(i), i)
	}
	if n := c.Len(); n != 20 {
		t.Fatalf("expected 20, got %d", n)
	}
}

func TestSharded_Clear(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	for i := 0; i < 20; i++ {
		c.Set(strconv.Itoa(i), i)
	}
	c.Clear()
	if n := c.Len(); n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
}

func TestSharded_ExpireOld(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100), WithShardTTL[string, int](50*time.Millisecond))
	defer c.Close()

	for i := 0; i < 10; i++ {
		c.Set(strconv.Itoa(i), i)
	}
	time.Sleep(60 * time.Millisecond)

	n := c.ExpireOld()
	if n != 10 {
		t.Fatalf("expected 10, got %d", n)
	}
}

func TestSharded_Close(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	c.Close()
	if !c.IsClosed() {
		t.Fatal("expected closed")
	}
}

func TestSharded_Stats(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	c.Set("a", 1)
	c.Get("a")
	c.Get("missing")

	s := c.Stats()
	if s.Hits != 1 || s.Misses != 1 {
		t.Fatalf("expected 1 hit, 1 miss, got %+v", s)
	}
	if s.Capacity != 400 {
		t.Fatalf("expected total capacity 400, got %d", s.Capacity)
	}
}

func TestSharded_ResetStats(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	c.Set("a", 1)
	c.Get("a")
	c.ResetStats()

	s := c.Stats()
	if s.Hits != 0 || s.Misses != 0 {
		t.Fatalf("expected zeros, got %+v", s)
	}
}

func TestSharded_Keys(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)

	keys := c.Keys()
	if len(keys) != 2 {
		t.Fatalf("expected 2, got %d", len(keys))
	}
}

func TestSharded_Values(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)

	values := c.Values()
	if len(values) != 2 {
		t.Fatalf("expected 2, got %d", len(values))
	}
}

func TestSharded_Range(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	for i := 0; i < 10; i++ {
		c.Set(strconv.Itoa(i), i)
	}

	var count int
	c.Range(func(_ string, _ int) bool {
		count++
		return true
	})
	if count != 10 {
		t.Fatalf("expected 10, got %d", count)
	}
}

func TestSharded_Range_EarlyStop(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	for i := 0; i < 100; i++ {
		c.Set(strconv.Itoa(i), i)
	}

	var count int
	c.Range(func(_ string, _ int) bool {
		count++
		return count < 5
	})
	if count != 5 {
		t.Fatalf("expected 5, got %d", count)
	}
}

// --- Sharded batch operations ---

func TestSharded_SetMulti(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	items := make(map[string]int, 20)
	for i := 0; i < 20; i++ {
		items[strconv.Itoa(i)] = i
	}
	c.SetMulti(items)
	if n := c.Len(); n != 20 {
		t.Fatalf("expected 20, got %d", n)
	}
}

func TestSharded_SetMulti_Empty(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	c.SetMulti(nil)
	c.SetMulti(map[string]int{})
}

func TestSharded_SetMulti_Parallel(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](1000))
	defer c.Close()

	items := make(map[string]int, 100)
	for i := 0; i < 100; i++ {
		items[strconv.Itoa(i)] = i
	}
	c.SetMulti(items)
	if n := c.Len(); n != 100 {
		t.Fatalf("expected 100, got %d", n)
	}
}

func TestSharded_GetMulti(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)

	result := c.GetMulti([]string{"a", "b", "missing"})
	if len(result) != 2 {
		t.Fatalf("expected 2, got %d", len(result))
	}
}

func TestSharded_GetMulti_Empty(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	result := c.GetMulti(nil)
	if len(result) != 0 {
		t.Fatalf("expected 0, got %d", len(result))
	}
}

func TestSharded_GetMulti_Parallel(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](1000))
	defer c.Close()

	for i := 0; i < 100; i++ {
		c.Set(strconv.Itoa(i), i)
	}

	keys := make([]string, 100)
	for i := range keys {
		keys[i] = strconv.Itoa(i)
	}
	result := c.GetMulti(keys)
	if len(result) != 100 {
		t.Fatalf("expected 100, got %d", len(result))
	}
}

func TestSharded_DeleteMulti(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)

	n := c.DeleteMulti([]string{"a", "c"})
	if n != 2 {
		t.Fatalf("expected 2, got %d", n)
	}
}

func TestSharded_DeleteMulti_Empty(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](100))
	defer c.Close()

	if n := c.DeleteMulti(nil); n != 0 {
		t.Fatalf("expected 0, got %d", n)
	}
}

func TestSharded_DeleteMulti_Parallel(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](1000))
	defer c.Close()

	for i := 0; i < 100; i++ {
		c.Set(strconv.Itoa(i), i)
	}

	keys := make([]string, 100)
	for i := range keys {
		keys[i] = strconv.Itoa(i)
	}
	n := c.DeleteMulti(keys)
	if n != 100 {
		t.Fatalf("expected 100, got %d", n)
	}
}

// --- Sharded concurrent ---

func TestSharded_Concurrent(t *testing.T) {
	c := NewSharded[int, int](WithShardCount[int, int](8), WithShardCapacity[int, int](100), WithShardTTL[int, int](time.Second))
	defer c.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func(n int) { defer wg.Done(); c.Set(n, n) }(i)
		go func(n int) { defer wg.Done(); c.Get(n) }(i)
		go func(n int) { defer wg.Done(); c.Delete(n) }(i)
	}
	wg.Wait()
}

// --- IsClosed for empty shards ---

func TestSharded_IsClosed_EmptyShards(t *testing.T) {
	s := &ShardedLRU[string, int]{shards: nil}
	if !s.IsClosed() {
		t.Fatal("expected true for nil shards")
	}
}

// --- Hasher coverage ---

func TestHasher_Types(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		h := newHasher[string]()
		a, b := h("hello"), h("world")
		if a == b {
			t.Fatal("expected different hashes")
		}
	})
	t.Run("int", func(t *testing.T) {
		h := newHasher[int]()
		_ = h(42)
	})
	t.Run("int64", func(t *testing.T) {
		h := newHasher[int64]()
		_ = h(42)
	})
	t.Run("int32", func(t *testing.T) {
		h := newHasher[int32]()
		_ = h(42)
	})
	t.Run("uint", func(t *testing.T) {
		h := newHasher[uint]()
		_ = h(42)
	})
	t.Run("uint64", func(t *testing.T) {
		h := newHasher[uint64]()
		_ = h(42)
	})
	t.Run("uint32", func(t *testing.T) {
		h := newHasher[uint32]()
		_ = h(42)
	})
	t.Run("float64", func(t *testing.T) {
		h := newHasher[float64]()
		_ = h(3.14)
	})
	t.Run("float32", func(t *testing.T) {
		h := newHasher[float32]()
		_ = h(3.14)
	})
	t.Run("bool", func(t *testing.T) {
		h := newHasher[bool]()
		a, b := h(true), h(false)
		if a == b {
			t.Fatal("expected different hashes")
		}
	})
	t.Run("struct", func(t *testing.T) {
		type key struct{ x int }
		h := newHasher[key]()
		_ = h(key{42})
	})
}

// --- GetOrCompute with expired entry in double-check path ---

func TestGetOrCompute_ExpiredInDoubleCheck(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	time.Sleep(60 * time.Millisecond)

	// The first Get in GetOrCompute will miss (expired).
	// The double-check in getOrComputeDirect will also find it expired.
	v := c.GetOrCompute("a", func() int { return 99 })
	if v != 99 {
		t.Fatalf("expected 99, got %d", v)
	}
}

// --- GetOrCompute singleflight with TTL ---

func TestGetOrCompute_SingleflightWithTTL(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	v := c.GetOrCompute("a", func() int { return 42 },
		WithSingleflight(), WithComputeTTL(50*time.Millisecond))
	if v != 42 {
		t.Fatalf("expected 42, got %d", v)
	}

	time.Sleep(60 * time.Millisecond)
	if _, ok := c.Get("a"); ok {
		t.Fatal("expected expired")
	}
}

// --- Context-related: ensure no context dependency ---

func TestGetOrCompute_NoContextRequired(t *testing.T) {
	_ = context.Background() // prove context is importable but not needed
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	v := c.GetOrCompute("a", func() int { return 1 })
	if v != 1 {
		t.Fatalf("expected 1, got %d", v)
	}
}

// --- Eviction callback with no callback set ---

func TestEviction_NoCallback(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](2))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3) // evicts a, no callback, should not panic
}

// --- SetWithTTL overwrite updates expiry ---

func TestSetWithTTL_OverwriteUpdatesExpiry(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.SetWithTTL("a", 1, 50*time.Millisecond)
	c.SetWithTTL("a", 2, time.Hour)

	time.Sleep(60 * time.Millisecond)
	v, ok := c.Get("a")
	if !ok || v != 2 {
		t.Fatalf("expected (2, true), got (%d, %v)", v, ok)
	}
}

// --- Sharded with OnEvict and CleanupInterval ---

func TestSharded_WithEvictAndCleanup(t *testing.T) {
	var evictions atomic.Int32
	c := NewSharded[string, int](
		WithShardCount[string, int](4),
		WithShardCapacity[string, int](100),
		WithShardTTL[string, int](50*time.Millisecond),
		WithShardOnEvict[string, int](func(_ string, _ int, _ EvictionReason) { evictions.Add(1) }),
		WithShardCleanupInterval[string, int](30*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	time.Sleep(100 * time.Millisecond)

	if n := evictions.Load(); n < 1 {
		t.Fatalf("expected at least 1 eviction, got %d", n)
	}
}

// --- removeTailLocked with empty list ---

func TestRemoveTailLocked_EmptyList(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.mu.Lock()
	ev := c.removeTailLocked()
	c.mu.Unlock()

	if ev != nil {
		t.Fatal("expected nil for empty list")
	}
}

// --- Sharded negative shard count ---

func TestSharded_NegativeShardCount(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](-1), WithShardCapacity[string, int](10))
	defer c.Close()

	if len(c.shards) != 16 {
		t.Fatalf("expected 16 (default), got %d", len(c.shards))
	}
}

// --- Large batch operations to trigger parallel path ---

func TestSharded_SetMulti_LargeBatch(t *testing.T) {
	c := NewSharded[int, int](WithShardCount[int, int](4), WithShardCapacity[int, int](10000))
	defer c.Close()

	items := make(map[int]int, 200)
	for i := 0; i < 200; i++ {
		items[i] = i
	}
	c.SetMulti(items)
	if n := c.Len(); n != 200 {
		t.Fatalf("expected 200, got %d", n)
	}
}

func TestSharded_GetMulti_LargeBatch(t *testing.T) {
	c := NewSharded[int, int](WithShardCount[int, int](4), WithShardCapacity[int, int](10000))
	defer c.Close()

	for i := 0; i < 200; i++ {
		c.Set(i, i)
	}
	keys := make([]int, 200)
	for i := range keys {
		keys[i] = i
	}
	result := c.GetMulti(keys)
	if len(result) != 200 {
		t.Fatalf("expected 200, got %d", len(result))
	}
}

func TestSharded_DeleteMulti_LargeBatch(t *testing.T) {
	c := NewSharded[int, int](WithShardCount[int, int](4), WithShardCapacity[int, int](10000))
	defer c.Close()

	for i := 0; i < 200; i++ {
		c.Set(i, i)
	}
	keys := make([]int, 200)
	for i := range keys {
		keys[i] = i
	}
	n := c.DeleteMulti(keys)
	if n != 200 {
		t.Fatalf("expected 200, got %d", n)
	}
}

// --- GetOrCompute concurrent race with expired entry ---

func TestGetOrCompute_ConcurrentExpired(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	time.Sleep(60 * time.Millisecond)

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := c.GetOrCompute("a", func() int { return 99 })
			if v != 99 {
				t.Errorf("expected 99, got %d", v)
			}
		}()
	}
	wg.Wait()
}

// --- Compute option coverage ---

func TestComputeOptions(t *testing.T) {
	var cfg computeConfig
	WithComputeTTL(5 * time.Minute)(&cfg)
	if cfg.ttl != 5*time.Minute {
		t.Fatalf("expected 5m, got %v", cfg.ttl)
	}
	WithSingleflight()(&cfg)
	if !cfg.singleflight {
		t.Fatal("expected singleflight=true")
	}
}

// --- cleanupTicker.Stop idempotent ---

func TestCleanupTicker_StopIdempotent(t *testing.T) {
	ct := &cleanupTicker{
		ticker: time.NewTicker(time.Hour),
		stop:   make(chan struct{}),
	}
	ct.Stop()
	ct.Stop() // should not panic
}

// --- Hasher distribution ---

func TestHasher_Distribution(t *testing.T) {
	h := newHasher[int]()
	buckets := make(map[uint64]int)
	mask := uint64(15)
	for i := 0; i < 1000; i++ {
		buckets[h(i)&mask]++
	}
	for i := uint64(0); i <= mask; i++ {
		if buckets[i] == 0 {
			t.Errorf("bucket %d is empty, poor distribution", i)
		}
	}
}

// --- expireTime helper ---

func TestExpireTime(t *testing.T) {
	c := New[string, int](WithTTL[string, int](time.Hour))
	defer c.Close()

	now := time.Now()

	// Per-entry TTL takes precedence
	exp := c.expireTime(now, 5*time.Minute)
	if d := exp.Sub(now); d < 4*time.Minute || d > 6*time.Minute {
		t.Fatalf("expected ~5m, got %v", d)
	}

	// Falls back to global TTL
	exp = c.expireTime(now, 0)
	if d := exp.Sub(now); d < 59*time.Minute || d > 61*time.Minute {
		t.Fatalf("expected ~1h, got %v", d)
	}

	// No TTL at all
	c2 := New[string, int]()
	defer c2.Close()
	exp = c2.expireTime(now, 0)
	if !exp.IsZero() {
		t.Fatalf("expected zero time, got %v", exp)
	}
}

// --- Sharded IsClosed delegates to first shard ---

func TestSharded_IsClosed_NotClosed(t *testing.T) {
	c := NewSharded[string, int](WithShardCount[string, int](4), WithShardCapacity[string, int](10))
	defer c.Close()

	if c.IsClosed() {
		t.Fatal("expected not closed")
	}
}

// --- getOrComputeDirect: re-check after compute finds live entry ---

func TestGetOrCompute_RecheckFindsLiveEntry(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	started := make(chan struct{})
	v := c.GetOrCompute("a", func() int {
		close(started)
		// While we compute, another goroutine stores a value.
		// We simulate by storing directly.
		c.Set("a", 77)
		return 42
	})
	<-started
	// The re-check path (line 342) should find the value stored by Set.
	if v != 77 {
		t.Fatalf("expected 77 (re-check hit), got %d", v)
	}
}

// --- getOrComputeDirect: re-check after compute finds expired entry ---

func TestGetOrCompute_RecheckFindsExpiredEntry(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithOnEvict[string, int](func(_ string, _ int, _ EvictionReason) {}),
	)
	defer c.Close()

	v := c.GetOrCompute("a", func() int {
		// Store an entry that will be expired by the time we re-check
		c.SetWithTTL("a", 1, time.Nanosecond)
		time.Sleep(2 * time.Millisecond)
		return 99
	})
	if v != 99 {
		t.Fatalf("expected 99, got %d", v)
	}
}

// --- getOrComputeDirect: eviction during compute store ---

func TestGetOrCompute_EvictionDuringStore(t *testing.T) {
	var evictions int
	c := New[string, int](
		WithCapacity[string, int](2),
		WithOnEvict[string, int](func(_ string, _ int, r EvictionReason) {
			if r == Capacity {
				evictions++
			}
		}),
	)
	defer c.Close()

	c.Set("x", 1)
	c.Set("y", 2)
	// Cache is full. GetOrCompute will store "z" and evict the tail.
	v := c.GetOrCompute("z", func() int { return 99 })
	if v != 99 {
		t.Fatalf("expected 99, got %d", v)
	}
	if evictions < 1 {
		t.Fatalf("expected at least 1 eviction, got %d", evictions)
	}
}

// --- Keys/Values/Range eviction callback paths (no callback) ---

func TestKeys_ExpiredNoCallback(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	time.Sleep(60 * time.Millisecond)
	keys := c.Keys()
	if len(keys) != 0 {
		t.Fatalf("expected 0, got %d", len(keys))
	}
}

func TestValues_ExpiredNoCallback(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	time.Sleep(60 * time.Millisecond)
	values := c.Values()
	if len(values) != 0 {
		t.Fatalf("expected 0, got %d", len(values))
	}
}

func TestRange_ExpiredNoCallback(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	time.Sleep(60 * time.Millisecond)
	var count int
	c.Range(func(_ string, _ int) bool {
		count++
		return true
	})
	if count != 0 {
		t.Fatalf("expected 0, got %d", count)
	}
}

// --- DeleteMulti with no callback ---

func TestDeleteMulti_NoCallback(t *testing.T) {
	c := New[string, int](WithCapacity[string, int](10))
	defer c.Close()

	c.Set("a", 1)
	c.Set("b", 2)
	n := c.DeleteMulti([]string{"a", "b"})
	if n != 2 {
		t.Fatalf("expected 2, got %d", n)
	}
}

// --- Get expired with no callback ---

func TestGet_ExpiredNoCallback(t *testing.T) {
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
	)
	defer c.Close()

	c.Set("a", 1)
	time.Sleep(60 * time.Millisecond)
	_, ok := c.Get("a")
	if ok {
		t.Fatal("expected miss")
	}
}

// --- getOrComputeDirect: expired removal in first check with callback ---

func TestGetOrCompute_ExpiredRemovalWithCallback(t *testing.T) {
	var expiredKeys []string
	c := New[string, int](
		WithCapacity[string, int](10),
		WithTTL[string, int](50*time.Millisecond),
		WithOnEvict[string, int](func(key string, _ int, r EvictionReason) {
			if r == Expired {
				expiredKeys = append(expiredKeys, key)
			}
		}),
	)
	defer c.Close()

	c.Set("a", 1)
	time.Sleep(60 * time.Millisecond)

	v := c.getOrComputeDirect("a", func() int { return 99 }, 0)
	if v != 99 {
		t.Fatalf("expected 99, got %d", v)
	}
	if len(expiredKeys) < 1 {
		t.Fatal("expected at least 1 expired callback")
	}
}

// --- fmt import coverage for keyToString fallback ---

func TestKeyToString_FmtFallback(t *testing.T) {
	type myKey struct {
		A int
		B string
	}
	got := keyToString(myKey{1, "x"})
	want := fmt.Sprint(myKey{1, "x"})
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
