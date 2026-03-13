package syncx

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aasyanov/urx/pkg/errx"
)

func TestNewLazy_NilInit_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for nil init")
		}
	}()
	NewLazy[int](nil)
}

// ============================================================
// Lazy
// ============================================================

func TestLazy_InitOnce(t *testing.T) {
	var calls atomic.Int32
	l := NewLazy(func() (int, error) {
		calls.Add(1)
		return 42, nil
	})

	v1, err := l.Get()
	if err != nil || v1 != 42 {
		t.Fatalf("expected (42, nil), got (%d, %v)", v1, err)
	}
	v2, _ := l.Get()
	if v2 != 42 {
		t.Fatalf("expected 42, got %d", v2)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", calls.Load())
	}
}

func TestLazy_Error(t *testing.T) {
	sentinel := errors.New("init failed")
	l := NewLazy(func() (string, error) {
		return "", sentinel
	})

	_, err := l.Get()
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel, got %v", err)
	}
}

func TestLazy_Reset(t *testing.T) {
	var calls atomic.Int32
	l := NewLazy(func() (int, error) {
		return int(calls.Add(1)), nil
	})

	v1, _ := l.Get()
	if v1 != 1 {
		t.Fatalf("expected 1, got %d", v1)
	}

	l.Reset()
	v2, _ := l.Get()
	if v2 != 2 {
		t.Fatalf("expected 2 after reset, got %d", v2)
	}
}

func TestLazy_ConcurrentGet(t *testing.T) {
	var calls atomic.Int32
	l := NewLazy(func() (int, error) {
		calls.Add(1)
		return 99, nil
	})

	done := make(chan struct{})
	for i := 0; i < 100; i++ {
		go func() {
			v, err := l.Get()
			if err != nil || v != 99 {
				t.Errorf("unexpected (%d, %v)", v, err)
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 100; i++ {
		<-done
	}
	if calls.Load() != 1 {
		t.Fatalf("expected 1 call, got %d", calls.Load())
	}
}

// ============================================================
// Group
// ============================================================

func TestGroup_Success(t *testing.T) {
	g, _ := NewGroup(context.Background())
	var sum atomic.Int64
	for i := 0; i < 10; i++ {
		g.Go(func(ctx context.Context) error {
			sum.Add(1)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if sum.Load() != 10 {
		t.Fatalf("expected 10, got %d", sum.Load())
	}
}

func TestGroup_FirstError(t *testing.T) {
	g, _ := NewGroup(context.Background())
	sentinel := errors.New("fail")
	g.Go(func(ctx context.Context) error { return sentinel })
	g.Go(func(ctx context.Context) error { return nil })

	err := g.Wait()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGroup_PanicRecovery(t *testing.T) {
	g, _ := NewGroup(context.Background())
	g.Go(func(ctx context.Context) error {
		panic("boom")
	})

	err := g.Wait()
	if err == nil {
		t.Fatal("expected error from panic")
	}
	var xe *errx.Error
	if !errors.As(err, &xe) {
		t.Fatalf("expected *errx.Error, got %T", err)
	}
	if !xe.IsPanic() {
		t.Fatal("expected panic error")
	}
}

func TestGroup_WithLimit(t *testing.T) {
	g, _ := NewGroup(context.Background(), WithLimit(2))
	var peak atomic.Int32
	var current atomic.Int32

	for i := 0; i < 20; i++ {
		g.Go(func(ctx context.Context) error {
			cur := current.Add(1)
			for {
				old := peak.Load()
				if cur <= old || peak.CompareAndSwap(old, cur) {
					break
				}
			}
			current.Add(-1)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if peak.Load() > 2 {
		t.Fatalf("peak concurrency %d exceeded limit 2", peak.Load())
	}
}

func TestGroup_ContextCancelled(t *testing.T) {
	g, ctx := NewGroup(context.Background())
	g.Go(func(ctx context.Context) error {
		return errors.New("fail")
	})
	_ = g.Wait()
	if ctx.Err() == nil {
		t.Fatal("expected context to be cancelled after error")
	}
}

// ============================================================
// Map
// ============================================================

func TestMap_StoreLoad(t *testing.T) {
	m := NewMap[string, int]()
	m.Store("a", 1)
	m.Store("b", 2)

	v, ok := m.Load("a")
	if !ok || v != 1 {
		t.Fatalf("expected (1, true), got (%d, %v)", v, ok)
	}
	_, ok = m.Load("c")
	if ok {
		t.Fatal("expected false for missing key")
	}
}

func TestMap_Delete(t *testing.T) {
	m := NewMap[string, int]()
	m.Store("a", 1)
	m.Delete("a")
	_, ok := m.Load("a")
	if ok {
		t.Fatal("expected key to be deleted")
	}
	if m.Len() != 0 {
		t.Fatalf("expected len 0, got %d", m.Len())
	}
}

func TestMap_DeleteMissing(t *testing.T) {
	m := NewMap[string, int]()
	m.Delete("nonexistent")
	if m.Len() != 0 {
		t.Fatalf("expected len 0, got %d", m.Len())
	}
}

func TestMap_LoadOrStore(t *testing.T) {
	m := NewMap[string, int]()
	v, loaded := m.LoadOrStore("a", 1)
	if loaded || v != 1 {
		t.Fatalf("expected (1, false), got (%d, %v)", v, loaded)
	}
	v, loaded = m.LoadOrStore("a", 2)
	if !loaded || v != 1 {
		t.Fatalf("expected (1, true), got (%d, %v)", v, loaded)
	}
	if m.Len() != 1 {
		t.Fatalf("expected len 1, got %d", m.Len())
	}
}

func TestMap_Range(t *testing.T) {
	m := NewMap[string, int]()
	m.Store("a", 1)
	m.Store("b", 2)
	m.Store("c", 3)

	sum := 0
	m.Range(func(k string, v int) bool {
		sum += v
		return true
	})
	if sum != 6 {
		t.Fatalf("expected sum 6, got %d", sum)
	}
}

func TestMap_Len(t *testing.T) {
	m := NewMap[int, string]()
	if m.Len() != 0 {
		t.Fatalf("expected 0, got %d", m.Len())
	}
	m.Store(1, "a")
	m.Store(2, "b")
	if m.Len() != 2 {
		t.Fatalf("expected 2, got %d", m.Len())
	}
	m.Store(1, "updated")
	if m.Len() != 2 {
		t.Fatalf("expected 2 after update, got %d", m.Len())
	}
}

func TestMap_Concurrent(t *testing.T) {
	m := NewMap[int, int]()
	done := make(chan struct{})
	for i := 0; i < 100; i++ {
		i := i
		go func() {
			m.Store(i, i*10)
			m.Load(i)
			done <- struct{}{}
		}()
	}
	for i := 0; i < 100; i++ {
		<-done
	}
	if m.Len() != 100 {
		t.Fatalf("expected 100, got %d", m.Len())
	}
}

// ============================================================
// Error constructors
// ============================================================

func TestErrInitFailed(t *testing.T) {
	cause := errors.New("db down")
	e := errInitFailed(cause)
	if e.Domain != DomainSync || e.Code != CodeInitFailed {
		t.Fatalf("expected SYNC/INIT_FAILED, got %s/%s", e.Domain, e.Code)
	}
	if !errors.Is(e, cause) {
		t.Fatal("expected to wrap cause")
	}
}

func TestLazy_ConcurrentGetAndReset(t *testing.T) {
	var calls atomic.Int32
	l := NewLazy(func() (int, error) {
		return int(calls.Add(1)), nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			v, err := l.Get()
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if v < 1 {
				t.Errorf("expected v >= 1 (init must run), got %d", v)
			}
		}()
		go func() {
			defer wg.Done()
			l.Reset()
		}()
	}
	wg.Wait()
}
