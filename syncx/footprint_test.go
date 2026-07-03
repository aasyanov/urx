package syncx

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "Group", Size: testx.Sizeof[Group](), Max: 120},
		{Name: "GroupStats", Size: testx.Sizeof[GroupStats](), Max: 32},
		{Name: "groupConfig", Size: testx.Sizeof[groupConfig](), Max: 8},
		{Name: "Lazy[int]", Size: testx.Sizeof[Lazy[int]](), Max: 48},
		{Name: "Map[int,int]", Size: testx.Sizeof[Map[int, int]](), Max: 64},
	})
}
