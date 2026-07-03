package ratex

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "config", Size: testx.Sizeof[config](), Max: 16},
		{Name: "Limiter", Size: testx.Sizeof[Limiter](), Max: 80},
		{Name: "execution", Size: testx.Sizeof[execution](), Max: 40},
		{Name: "Stats", Size: testx.Sizeof[Stats](), Max: 48},
	})
}
