package retryx

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

// TestFootprint guards the size of key value types against accidental bloat.
func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "config", Size: testx.Sizeof[config](), Max: 96},
		{Name: "attempt", Size: testx.Sizeof[attempt](), Max: 56},
		{Name: "Option", Size: testx.Sizeof[Option](), Max: 8},
	})
}
