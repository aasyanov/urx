package syncx_test

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/aasyanov/urx/panix"
	"github.com/aasyanov/urx/syncx"
)

// ExampleLazy demonstrates deferred, run-once initialization with error
// handling.
func ExampleLazy() {
	var calls int
	lazy, err := syncx.NewLazy(func() (int, error) {
		calls++
		return 42, nil
	})
	if err != nil {
		panic(err)
	}

	v1, _ := lazy.Get()
	v2, _ := lazy.Get()
	fmt.Println("value:", v1, v2)
	fmt.Println("init calls:", calls)
	// Output:
	// value: 42 42
	// init calls: 1
}

// ExampleLazy_error shows that a failing init is wrapped as ErrInitFailed and
// retried on the next Get.
func ExampleLazy_error() {
	lazy, _ := syncx.NewLazy(func() (int, error) {
		return 0, errors.New("connection refused")
	})

	_, err := lazy.Get()
	fmt.Println("is ErrInitFailed:", errors.Is(err, syncx.ErrInitFailed))
	// Output:
	// is ErrInitFailed: true
}

// ExampleLazy_panic shows that a panicking init is recovered as a
// panix.PanicError and retried on the next Get.
func ExampleLazy_panic() {
	var calls int
	lazy, _ := syncx.NewLazy(func() (int, error) {
		calls++
		if calls == 1 {
			panic("not ready")
		}
		return 1, nil
	})

	_, err := lazy.Get()
	var pe *panix.PanicError
	_ = errors.As(err, &pe)
	fmt.Println("first call panicked:", err != nil)

	v, err := lazy.Get()
	fmt.Println("second call:", v, err)
	// Output:
	// first call panicked: true
	// second call: 1 <nil>
}

// ExampleGroup demonstrates running tasks concurrently with a bounded worker
// count and collecting the first error.
func ExampleGroup() {
	g, _ := syncx.NewGroup(context.Background(), syncx.WithLimit(2))

	results := make([]int, 3)
	for i := range results {
		if err := g.Go(func(context.Context) error {
			results[i] = i * i
			return nil
		}); err != nil {
			panic(err)
		}
	}

	if err := g.Wait(); err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("results:", results)
	// Output:
	// results: [0 1 4]
}

// ExampleGroup_stats shows task counters after a mixed fan-out.
func ExampleGroup_stats() {
	g, _ := syncx.NewGroup(context.Background())
	_ = g.Go(func(context.Context) error { return nil })
	_ = g.Go(func(context.Context) error { return errors.New("fail") })

	_ = g.Wait()
	st := g.Stats()
	fmt.Println("started:", st.Started)
	fmt.Println("succeeded:", st.Succeeded)
	fmt.Println("failed:", st.Failed)
	// Output:
	// started: 2
	// succeeded: 1
	// failed: 1
}

// ExampleMap demonstrates type-safe concurrent storage with an O(1) length.
func ExampleMap() {
	m := syncx.NewMap[string, int]()
	m.Store("alice", 30)
	m.Store("bob", 25)

	v, ok := m.Load("alice")
	fmt.Println("alice:", v, ok)
	fmt.Println("len:", m.Len())

	names := make([]string, 0, m.Len())
	m.Range(func(name string, _ int) bool {
		names = append(names, name)
		return true
	})
	sort.Strings(names)
	fmt.Println("names:", names)
	// Output:
	// alice: 30 true
	// len: 2
	// names: [alice bob]
}
