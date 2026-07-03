package panix

import (
	"sync"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
)

// panix cannot import github.com/aasyanov/urx/internal/testx: testx
// imports panix for RequirePanicError, which would create a test import cycle.
// Local helpers mirror testx patterns without the dependency.

type footprintEntry struct {
	name string
	size uintptr
	max  uintptr
}

func assertFootprint(t *testing.T, entries []footprintEntry) {
	t.Helper()
	for _, e := range entries {
		t.Run(e.name, func(t *testing.T) {
			assert.LessOrEqual(t, e.size, e.max,
				"sizeof(%s) = %d bytes, exceeds limit %d", e.name, e.size, e.max)
			t.Logf("sizeof(%s) = %d bytes (limit %d)", e.name, e.size, e.max)
		})
	}
}

func hammerIndexed(n, iters int, fn func(goroutineIdx int) error) []error {
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

func sizeofPanicError() uintptr {
	return unsafe.Sizeof(PanicError{})
}
