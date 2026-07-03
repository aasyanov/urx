package retryx

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "config", Size: testx.Sizeof[config](), Max: 80},
		{Name: "attempt", Size: testx.Sizeof[attempt](), Max: 56},
	})
}
