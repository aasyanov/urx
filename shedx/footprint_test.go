package shedx

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "config", Size: testx.Sizeof[config](), Max: 32},
		{Name: "Shedder", Size: testx.Sizeof[Shedder](), Max: 80},
		{Name: "Token", Size: testx.Sizeof[Token](), Max: 16},
		{Name: "execution", Size: testx.Sizeof[execution](), Max: 48},
		{Name: "Stats", Size: testx.Sizeof[Stats](), Max: 56},
		{Name: "Priority", Size: testx.Sizeof[Priority](), Max: 1},
	})
}
