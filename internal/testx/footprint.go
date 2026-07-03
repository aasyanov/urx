package testx

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Footprint helpers
// ---------------------------------------------------------------------------

// SizeEntry describes one struct's expected memory footprint.
type SizeEntry struct {
	Name string
	Size uintptr
	Max  uintptr
}

// AssertFootprint verifies that each struct's unsafe.Sizeof does not
// exceed the declared maximum. Use in footprint_test.go to catch
// accidental struct bloat.
//
//	testx.AssertFootprint(t, []testx.SizeEntry{
//	    {"Breaker", unsafe.Sizeof(Breaker{}), 120},
//	    {"config",  unsafe.Sizeof(config{}),  64},
//	})
func AssertFootprint(t *testing.T, entries []SizeEntry) {
	t.Helper()
	for _, e := range entries {
		t.Run(e.Name, func(t *testing.T) {
			assert.LessOrEqual(t, e.Size, e.Max,
				"sizeof(%s) = %d bytes, exceeds limit %d", e.Name, e.Size, e.Max)
			t.Logf("sizeof(%s) = %d bytes (limit %d)", e.Name, e.Size, e.Max)
		})
	}
}

// AssertSize verifies that a struct's size equals the expected value
// exactly. Use when the size is a hard contract (e.g., cache-line aligned).
func AssertSize(t *testing.T, name string, got, want uintptr) {
	t.Helper()
	assert.Equal(t, want, got,
		"sizeof(%s) = %d bytes, want exactly %d", name, got, want)
	t.Logf("sizeof(%s) = %d bytes", name, got)
}

// Sizeof is a generic wrapper around unsafe.Sizeof for use in test tables.
// It avoids importing unsafe in every test file.
//
//	size := testx.Sizeof[MyStruct]()
func Sizeof[T any]() uintptr {
	var zero T
	return unsafe.Sizeof(zero)
}
