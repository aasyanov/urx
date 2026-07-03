package adaptx

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "Limiter", Size: testx.Sizeof[Limiter](), Max: 320},
		{Name: "config", Size: testx.Sizeof[config](), Max: 160},
		{Name: "execution", Size: testx.Sizeof[execution](), Max: 32},
		{Name: "sample", Size: testx.Sizeof[sample](), Max: 48},
		{Name: "Stats", Size: testx.Sizeof[Stats](), Max: 160},
	})
}
