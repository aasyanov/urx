package cfgx

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

// TestFootprint bounds the in-memory size of the core types so accidental
// field additions that bloat them are caught in review. Values are for
// 64-bit platforms.
func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "config", Size: testx.Sizeof[config](), Max: 32},
		{Name: "Format", Size: testx.Sizeof[Format](), Max: 1},
	})
}
