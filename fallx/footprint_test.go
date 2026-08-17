package fallx

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

// TestFootprint guards the size of the key value types against accidental
// bloat. Generic types are measured at the concrete int instantiation.
func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "Strategy", Size: testx.Sizeof[Strategy](), Max: 1},
		{Name: "execution", Size: testx.Sizeof[execution](), Max: 48},
		{Name: "Stats", Size: testx.Sizeof[Stats](), Max: 80},
		{Name: "cacheEntry[int]", Size: testx.Sizeof[cacheEntry[int]](), Max: 80},
		{Name: "config[int]", Size: testx.Sizeof[config[int]](), Max: 128},
		{Name: "Fallback[int] static", Size: testx.Sizeof[Fallback[int]](), Max: 248},
		{Name: "cacheShard[int]", Size: testx.Sizeof[cacheShard[int]](), Max: 56},
	})
}
