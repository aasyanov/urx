package bulkx

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "config", Size: testx.Sizeof[config](), Max: 32},
		{Name: "Bulkhead", Size: testx.Sizeof[Bulkhead](), Max: 80},
		{Name: "Token", Size: testx.Sizeof[Token](), Max: 16},
		{Name: "execution", Size: testx.Sizeof[execution](), Max: 24},
		{Name: "Stats", Size: testx.Sizeof[Stats](), Max: 40},
	})
}
