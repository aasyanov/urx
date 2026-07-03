package clix

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

// TestFootprint bounds the in-memory size of the core types so accidental
// field additions that bloat the structs are caught in review. Values are
// for 64-bit platforms.
func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "Parser", Size: testx.Sizeof[Parser](), Max: 48},
		{Name: "Context", Size: testx.Sizeof[Context](), Max: 16},
		{Name: "flagMeta", Size: testx.Sizeof[flagMeta](), Max: 112},
	})
}
