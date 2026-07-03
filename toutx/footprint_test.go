package toutx

import (
	"testing"

	"github.com/aasyanov/urx/internal/testx"
)

func TestFootprint(t *testing.T) {
	testx.AssertFootprint(t, []testx.SizeEntry{
		{Name: "config", Size: testx.Sizeof[config](), Max: 32},
		{Name: "Timer", Size: testx.Sizeof[Timer](), Max: 32},
		{Name: "execution", Size: testx.Sizeof[execution](), Max: 48},
	})
}
