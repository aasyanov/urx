package warmupx

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "config", Size: testx.Sizeof[config](), Max: 96},
		{Name: "execution", Size: testx.Sizeof[execution](), Max: 32},
		{Name: "Stats", Size: testx.Sizeof[Stats](), Max: 112},
	})
}
