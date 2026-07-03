package hedgex

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "config", Size: testx.Sizeof[config](), Max: 64},
		{Name: "Hedger", Size: testx.Sizeof[Hedger](), Max: 96},
		{Name: "execution", Size: testx.Sizeof[execution](), Max: 48},
		{Name: "Stats", Size: testx.Sizeof[Stats](), Max: 32},
	})
}
