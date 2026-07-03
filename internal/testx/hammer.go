package testx

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Concurrency stress helpers
// ---------------------------------------------------------------------------

// Hammer runs fn concurrently in n goroutines, each calling fn iters times.
// It waits for all goroutines to complete and returns the collected errors
// (only non-nil errors are included).
//
// Use with -race to detect data races in concurrent code.
//
//	errs := testx.Hammer(100, 1000, func() error {
//	    _, err := limiter.Acquire(ctx)
//	    return err
//	})
//	assert.Empty(t, errs)
func Hammer(n, iters int, fn func() error) []error {
	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			for range iters {
				if err := fn(); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	return errs
}

// HammerNoError runs fn in n goroutines (iters each) and asserts no errors.
func HammerNoError(t *testing.T, n, iters int, fn func() error) {
	t.Helper()
	errs := Hammer(n, iters, fn)
	assert.Empty(t, errs, "expected 0 errors from %d goroutines × %d iters, got %d",
		n, iters, len(errs))
}

// HammerVoid runs fn in n goroutines (iters each) without capturing results.
// Use when fn communicates via side effects (channels, atomics) and you only
// need the race detector to verify safety.
func HammerVoid(n, iters int, fn func()) {
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			for range iters {
				fn()
			}
		}()
	}
	wg.Wait()
}

// HammerIndexed runs fn in n goroutines, passing the goroutine index (0..n-1).
// Each goroutine calls fn iters times. Useful when different goroutines should
// exercise different code paths.
func HammerIndexed(n, iters int, fn func(goroutineIdx int) error) []error {
	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)
	wg.Add(n)
	for i := range n {
		go func(idx int) {
			defer wg.Done()
			for range iters {
				if err := fn(idx); err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			}
		}(i)
	}
	wg.Wait()
	return errs
}
