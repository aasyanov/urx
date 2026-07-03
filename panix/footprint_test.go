package panix

import (
	"testing"
	"unsafe"

	"github.com/stretchr/testify/assert"
)

func TestFootprint(t *testing.T) {
	tests := []struct {
		name string
		size uintptr
		max  uintptr
	}{
		{"PanicError", unsafe.Sizeof(PanicError{}), 56},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.LessOrEqual(t, tt.size, tt.max,
				"sizeof(%s) = %d bytes, exceeds limit %d", tt.name, tt.size, tt.max)
			t.Logf("sizeof(%s) = %d bytes (limit %d)", tt.name, tt.size, tt.max)
		})
	}
}
