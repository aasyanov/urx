package testx

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
)

func TestAssertFootprint(t *testing.T) {
	AssertFootprint(t, []SizeEntry{
		{"Simulator", unsafe.Sizeof(Simulator{}), 120},
		{"LatencySim", unsafe.Sizeof(LatencySim{}), 40},
		{"simConfig", unsafe.Sizeof(simConfig{}), 80},
	})
}

func TestAssertSize(t *testing.T) {
	type tiny struct{ A int8 }
	AssertSize(t, "tiny", unsafe.Sizeof(tiny{}), 1)
}

func TestSizeof(t *testing.T) {
	type pair struct{ A, B int64 }
	got := Sizeof[pair]()
	assert.Equal(t, uintptr(16), got)
}
