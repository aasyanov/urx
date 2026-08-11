package circuitx

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "State", Size: testx.Sizeof[State](), Max: 1},
		{Name: "config", Size: testx.Sizeof[config](), Max: 56},
		{Name: "execution", Size: testx.Sizeof[execution](), Max: 32},
		{Name: "Stats", Size: testx.Sizeof[Stats](), Max: 56},
		{Name: "Breaker", Size: testx.Sizeof[Breaker](), Max: 128},
	})
}
