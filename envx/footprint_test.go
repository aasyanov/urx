package envx

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

// TestFootprint bounds the in-memory size of the core types so accidental
// field additions that bloat them are caught in review. Values are for
// 64-bit platforms. Var is generic; Var[int] is the representative
// instantiation.
func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "Env", Size: testx.Sizeof[Env](), Max: 48},
		{Name: "Var[int]", Size: testx.Sizeof[Var[int]](), Max: 72},
		{Name: "config", Size: testx.Sizeof[config](), Max: 24},
	})
}
